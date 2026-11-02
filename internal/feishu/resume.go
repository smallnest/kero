package feishu

import (
	"fmt"
	"sync"
	"time"

	"kero/internal/proto"
	"kero/internal/settings"
)

// A turn that runs while kerod restarts leaves its card grey. The chat's
// next message takes it on: the bot sees its session at work, a command
// in the background or an agent in its pane, and follows it to its end,
// card and answer alike. A turn that ended well within its timeout, and
// has no card that shows it working, follows again.

// follow follows the turns taken on, one at a time: following runs in
// the message's own goroutine.
var follow sync.Mutex

// takeOver follows the chat's turn to its end, and tells whether it does:
// the turn from before a restart, which the chat's last message after it
// took on. A turn from a message just taken is not one.
func (b *Bot) takeOver(c *chat) bool {
	b.mu.Lock()
	started, text, event := c.running.start, c.running.text, c.running.event
	b.mu.Unlock()
	// A turn of this bot, as sync runs it, is not one of a restart.
	if started.IsZero() || started.After(b.began) || text == "" {
		return false
	}
	if c.id == "" || time.Since(started) >= b.timeout() {
		return false
	}
	follow.Lock()
	defer follow.Unlock()
	if out, pane := b.backgroundRun(c.id); pane != "" {
		if out.Ended {
			return false
		}
		b.followCommand(c, out, started, event, text)
		return true
	}
	// The session's pane, as kerod's state has it, shows its state.
	var p proto.Pane
	if !b.paneStatus(c.id, &p) || p.Status != proto.Working {
		return false
	}
	b.followPane(c, started, event, text)
	return true
}

// backgroundRun is the chat's session running a command: its output, and
// the pane that runs it, "" for a pane of the terminal.
func (b *Bot) backgroundRun(session string) (out proto.Output, pane string) {
	// A session that runs no command says so, or waits for one forever.
	if has, err := b.hasBackground(session); err != nil || !has {
		return proto.Output{}, ""
	}
	if err := b.api.Call("agent.output", proto.OutputParams{ID: session}, &out); err != nil {
		return proto.Output{}, ""
	}
	var st proto.State
	if err := b.api.Call("state", nil, &st); err == nil {
		if s := sessionIn(st.Sessions, session); s != nil {
			for i := range s.Panes {
				if s.Panes[i].Shown() {
					pane = s.Panes[i].ID
				}
			}
		}
	}
	return out, firstNotEmpty(pane, session)
}

// cardOf is the card of the chat's turn, when its start kept it.
func (b *Bot) cardOf(c *chat, id, prev, session string) *turnCard {
	card := b.newTurnCard(id, session, b.wantTurn(id), "")
	if k := b.kardStateValue(id); k.id != "" {
		// The card the turn sent, before the restart.
		card.id, card.prev, card.text = k.id, "", k.body
		card.state = k.state
	}
	return card
}

// followCommand follows a command in the background, in pane, to its
// end.
func (b *Bot) followCommand(c *chat, out proto.Output, started time.Time, event, text string) {
	key := c.key
	card := b.cardOf(c, key, "", c.id)
	if card.id == "" {
		card = nil
	}
	agent, dir, sid := c.agent, c.dir, c.sid
	from, answer := out.From, out.Text
	for !out.Ended {
		if time.Since(started) >= b.timeout() {
			if card != nil {
				b.card(card, "asks", plainBody(answer), "")
			}
			b.reply(c.chat, fmt.Sprintf("Still working after %s. /stop ends it.", b.timeout()))
			return
		}
		time.Sleep(time.Second)
		var next proto.Output
		if err := b.api.Call("agent.output", proto.OutputParams{ID: c.id, From: from}, &next); err != nil {
			if card != nil {
				b.card(card, "error", err.Error(), "")
			}
			b.reply(c.chat, err.Error())
			return
		}
		out = next
		from, answer = out.From, answer+out.Text
		if card != nil {
			b.card(card, "working", commandText(agent, answer), "")
		}
	}
	b.finished(key, c.id, event, agent, dir, sid, text, out, answer, card)
}

// followPane follows an agent in the pane, with its card.
func (b *Bot) followPane(c *chat, started time.Time, event, text string) {
	key := c.key
	card := b.cardOf(c, key, "", c.id)
	lead := leadPaneOf(b, c.id)
	_, body, _, _ := b.watchCard(card, lead, 0, started.Add(b.timeout()))
	var p proto.Pane
	b.api.Call("read", proto.ReadParams{ID: lead}, &p)
	reply := b.replyWith(&run{status: p, text: body})
	b.replyAt(key, event, c.chat, reply)
}

// paneStatus is the session's lead pane, as kerod's state has it: a read
// of its ID alone reads no pane.
func (b *Bot) paneStatus(id string, p *proto.Pane) bool {
	st, err := b.api.State()
	if err != nil {
		return false
	}
	s := sessionIn(st.Sessions, id)
	if s == nil {
		return false
	}
	*p = leadPane(s)
	return true
}

// leadPaneOf is a session's lead pane, or its first.
func leadPaneOf(b *Bot, id string) string {
	var st proto.State
	if b.api.Call("state", nil, &st) != nil {
		return id
	}
	if s := sessionIn(st.Sessions, id); s != nil {
		if p := leadPane(s).ID; p != "" {
			return p
		}
	}
	return id
}

// timeout is how long a turn may run: the configuration's, in seconds.
func (b *Bot) timeout() time.Duration {
	d := time.Duration(b.cfg.Timeout)
	if d <= 0 {
		return settings.FeishuDefaultTimeout
	}
	return d
}

// sessionIn finds a session by id, in a state's list.
func sessionIn(l []proto.Session, id string) *proto.Session {
	for i := range l {
		if l[i].ID == id {
			return &l[i]
		}
	}
	return nil
}

// wantTurn is the message of the chat's turn that runs, "" for none.
func (b *Bot) wantTurn(chatID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.chatLocked(chatKey(chatID, "")).running
	if t.pane != "" && t.start.IsZero() {
		// An answer ended the turn, which its prompt began.
		return ""
	}
	return t.text
}

// beginTurn notes the turn that begins, as the chat's turn until it ends,
// which the messages that come meanwhile leave to the chat's host.
func (b *Bot) beginTurn(key string, t *turnInfo) {
	b.mu.Lock()
	b.chatLocked(key).running = *t
	b.mu.Unlock()
}

// dropTurn forgets it.
func (b *Bot) dropTurn(key string, t *turnInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.chatLocked(key)
	if c.running.start == t.start {
		c.running = turnInfo{}
	}
}

// runningTurn is the turn the chat runs, as its message left it.
func (b *Bot) runningTurn(chatID string) turnInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.chatLocked(chatKey(chatID, "")).running
}
