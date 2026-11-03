package feishu

import (
	"context"
	"fmt"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"

	"kero/internal/proto"
	"kero/internal/settings"
)

// A turn shows in a chat as a card (feishu-cards): grey while the agent
// works, its text as it comes, green once done, yellow when it asks, red
// when it failed; then the next turn replaces it with its own.

// stream is how often a card shows a turn's new text: feishu-stream.
func (b *Bot) stream() time.Duration {
	d := time.Duration(b.cfg.Stream) * time.Second
	if d <= 0 {
		d = time.Duration(settings.FeishuDefaultStream) * time.Second
	}
	return d
}

// turnCard is a turn's card, after an earlier one of its chat, which it
// replaces where it can.
type turnCard struct {
	key     string
	chatID  string
	session string
	turn    string
	lang    string
	prev    string // the message of the chat's last card, replaced
	id      string // the card's message, once sent
	text    string // the body the card shows
	state   string // working, done, asks or error
	rich    bool   // the card's body is Markdown
	meta    string // the turn's summary, under its text
	title   bool   // the card's header, in JSON 2.0 as a sent card takes
}

// newTurnCard makes a turn's card in a chat, which replaces prev, the
// chat's last card.
func (b *Bot) newTurnCard(chatID, session, title, prev string) *turnCard {
	return &turnCard{key: chatKey(chatID, ""), chatID: chatID, session: session, turn: title, prev: prev,
		lang: langOf(title), state: "working"}
}

// card tells the turn's card, as it is now: sent once, then updated. A
// card is sent as JSON 2.0; an update takes a card's body alone, and the
// header's color stays.
func (b *Bot) card(c *turnCard, state, text, note string) {
	if c == nil || !cards() {
		return
	}
	body := plainBody(text)
	if note != "" {
		body += "\n\n" + note
	}
	if body == c.text && state == c.state && c.id != "" {
		return
	}
	c.state = state
	shown, err := cardJSON(Card{Title: c.turn, Text: body, State: state, Session: c.session, Meta: c.meta}, c.lang)
	if err != nil {
		b.logf("feishu: card: %v", err)
		return
	}
	shown = titled(shown, c)
	if c.id == "" {
		// The card's content is the card's JSON, as Feishu takes it for
		// the app's first. feishu-card-body: true sends its body's alone.
		sent := shown
		if settings.Load().FeishuCardBody {
			sent = bodyOf(shown)
		}
		id, err := b.msgs.SendCard("chat_id", c.chatID, sent)
		if err != nil {
			// No card: the reply goes as text.
			b.logf("feishu: sending a card: %v", err)
			return
		}
		c.id, c.text = id, body
		b.remember(id, record{Chat: c.key, Session: c.session, Turn: c.turn})
		// The chat's card, for its next turn's, which a restart keeps.
		cm := cardMsg{id: id, turn: c.turn, text: shown, seen: state == "working"}
		b.setCard(c.key, cm)
		b.saveCard(c.key, cm, c.lang, body, state)
		// The earlier card goes, once this one is there.
		if c.prev != "" && c.prev != id {
			if err := b.msgs.UpdateCard(c.prev, closedCard(c.lang)); err == nil {
				b.forget(c.prev)
			}
		}
		return
	}
	// An update takes the card's body, whose text is Markdown's.
	update := bodyOf(shown)
	if err := b.msgs.UpdateCard(c.id, update); err != nil {
		b.logf("feishu: updating a card: %v", err)
		return
	}
	c.text = body
}

// closedCard is a replaced card, as it stays: a line, so that the thread
// keeps its place without its text twice.
func closedCard(lang string) string {
	s, _ := cardJSON(Card{Title: word(lang, "earlier"), Text: "*" + word(lang, "earlierNote") + "*", State: "done"}, lang)
	return s
}

// watchCard follows a turn of the pane from base on until it ends: the
// agent idle once seen working, or the pane waiting for the user. The
// card shows the pane's new rows every stream; state, body and failed
// are the turn's end.
func (b *Bot) watchCard(c *turnCard, id string, base int, stop time.Time) (state, body string, failed, seen bool) {
	timeout := time.Duration(b.cfg.Timeout)
	if timeout <= 0 {
		timeout = settings.FeishuDefaultTimeout
	}
	if stop.IsZero() {
		stop = time.Now().Add(timeout)
	}
	if c != nil {
		go func() {
			time.Sleep(2 * time.Second)
			b.card(c, "working", textNote+since(b.read(id), base, toolTail), "")
		}()
	}
	var p proto.Pane
	var last, still time.Time
	shown := ""
	textAt := time.Time{}
	for time.Now().Before(stop) {
		time.Sleep(waits.poll)
		if err := b.api.Call("wait", proto.WaitParams{ID: id, Timeout: int64(waits.poll / time.Millisecond)}, &p); err != nil && !timedOut(err) {
			return "error", "", true, !last.IsZero()
		}
		now := time.Now()
		if p.Status == proto.Working {
			last, still = now, now
		}
		if now.Sub(textAt) >= b.stream() {
			textAt = now
			t := since(b.read(id), base, toolTail)
			if t != shown {
				shown = t
				b.card(c, "working", textNote+t, "")
			}
		}
		switch {
		case p.Status == proto.Waiting:
			text := b.read(id)
			if seen := since(text, base, replyTail); seen != "" && written(text) == "" &&
				clean(seen) == clean(textNote+since(text, base, toolTail)) {
				// The question is in the pane, and the card shows it.
				continue
			}
			head := ""
			if !last.IsZero() {
				head = textNote
			}
			return "asks", head + since(text, base, replyTail), false, !last.IsZero()
		case p.Status == proto.Failed:
			return "error", orDash(p.Message) + "\n\n" + since(b.read(id), base, toolTail), true, !last.IsZero()
		case !last.IsZero() && !still.IsZero() && now.Sub(still) >= waits.quiet:
			return "done", "", false, true
		}
	}
	return "done", "", false, !last.IsZero()
}

// read is a pane's screen, with its scrollback.
func (b *Bot) read(id string) string {
	var rd proto.ReadResult
	if err := b.api.Call("read", proto.ReadParams{ID: id, Scrollback: true}, &rd); err != nil {
		return ""
	}
	return rd.Text
}

// toolTail is the rows of the pane's text a working card shows.
const toolTail = 16

// onCardAction answers the click of one of the bot's cards: the button
// that opens its session in Kero.
func (b *Bot) onCardAction(ctx context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	if e == nil || e.Event == nil || e.Event.Action == nil {
		return nil, nil
	}
	openID := ""
	if e.Event.Operator != nil {
		openID = e.Event.Operator.OpenID
	}
	if v, ok := e.Event.Action.Value["kero"]; ok && fmt.Sprint(v) == "perm" {
		return b.onAskAction(e)
	}
	if v, ok := e.Event.Action.Value["kero"]; !ok || fmt.Sprint(v) != "open" {
		return nil, nil
	}
	msg := ""
	status := "warning"
	if id := deref(&e.Event.Context.OpenMessageID); id != "" {
		if r, ok := b.note(id); ok {
			if r.Session != "" {
				if _, err := b.api.State(); err == nil {
					if err := b.api.Call("select", proto.FocusParams{Session: r.Session}, nil); err == nil {
						msg, status = word(langOf(r.Turn), "opened"), "success"
					} else {
						msg = err.Error()
					}
				}
			}
		}
	}
	if msg == "" && openID == "" {
		return nil, nil
	}
	if msg == "" {
		msg = word("en_us", "gone")
	}
	return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: status, Content: msg}}, nil
}

// setCard keeps the chat's last card, which its next turn replaces.
func (b *Bot) setCard(key string, c cardMsg) {
	b.mu.Lock()
	b.chatLocked(key).card = c
	b.mu.Unlock()
}

// lastCard is the chat's last card, and whether its turn was seen to
// work: the one its last turn showed, or the one the session shows now,
// which a turn from elsewhere, or from before a restart, sent. A
// restart's pane is done, and takes no turn.
func (b *Bot) lastCard(key, session string) (id string, seen bool) {
	c := b.chat(key)
	// The card the chat had is the one to replace: the last turn's, or,
	// for none, one a restart left undone.
	prev := c.card.id
	if session == "" || !cards() {
		return prev, c.card.seen
	}
	var p proto.Pane
	if err := b.api.Call("pane", proto.IDParams{ID: session}, &p); err != nil || p.Status == proto.Idle {
		return "", false
	}
	shown, err := cardJSON(Card{Title: "", Text: "*" + word("en_us", "earlierNote") + "*", State: "working", Session: session}, "en_us")
	if err != nil {
		return "", false
	}
	id, err = b.msgs.SendCard("chat_id", key, shown)
	if err != nil {
		return "", false
	}
	b.remember(id, record{Chat: key, Session: session})
	b.setCard(key, cardMsg{id: id, text: shown, seen: true})
	return id, true
}
