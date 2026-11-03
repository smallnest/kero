package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"

	"kero/internal/proto"
	"kero/internal/settings"
)

// A turn shows in a chat by the user's own message (feishu-cards): a
// plain card while the agent works, and a short line once it ends, which
// a reaction on the message marks as done, or failed. The agent's answer
// is a card that follows the message.

// turnCard is a turn's card: the user's message, as it shows while the
// turn runs.
type turnCard struct {
	key     string
	chatID  string
	msg     string // the user's message, which the card shows
	lang    string // the language of the words the card shows
	state   string // working, done or error
	text    string // the card's body, as Feishu took it
	sent    bool   // Feishu took a card for the message
	dropped bool   // the card went, as the answer came: its updates are none
}

// newTurnCard makes a turn's card in a chat, which is the message the
// turn answers.
func (b *Bot) newTurnCard(chatID, msg, lang string) *turnCard {
	return &turnCard{key: chatKey(chatID, ""), chatID: chatID, msg: msg, lang: lang, state: "working"}
}

// card tells the turn's card, as it is now: sent once, as the message's
// card. Feishu has no way to remove the card from a message again: the
// answer never shows in it, so a turn that is done, and has an answer,
// closes with the card gone (drop) as the answer's card follows.
func (b *Bot) card(c *turnCard, state, text, note string) {
	if c == nil || !cards() || c.msg == "" || c.dropped {
		return
	}
	body := html.UnescapeString(plainBody(text))
	if note != "" {
		body += "\n\n" + note
	}
	shown, err := stateCard(body, cardState[state])
	if err != nil {
		b.logf("feishu: a card: %v", err)
		return
	}
	// An update takes the card's body alone: the header that sent it
	// stays as it was made.
	err2 := b.msgs.UpdateCard(c.msg, bodyOf(shown))
	if err2 != nil {
		b.logf("feishu: updating a card: %v", err2)
		return
	}
	c.state, c.text, c.sent = state, body, true
}

// drop shows the turn's card as a line, so that it takes no room in the
// thread below the answer, and stops its updates.
func (b *Bot) drop(c *turnCard) {
	if c == nil || c.dropped || !c.sent {
		return
	}
	c.dropped = true
	if s, err := stateCard("*"+word(c.lang, "earlier")+"*", "grey"); err == nil {
		b.msgs.UpdateCard(c.msg, bodyOf(s))
	}
}

// stateCard is a message's card JSON, in its body alone: the turn's
// text, and its state's color, which the card's header takes.
func stateCard(body, color string) (string, error) {
	if color == "" {
		color = "blue"
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "wide_screen_mode": true},
		"header": map[string]any{"template": color},
		"body": map[string]any{"direction": "vertical", "elements": []any{
			map[string]any{"tag": "markdown", "content": body},
		}},
	}
	data, err := json.Marshal(card)
	return string(data), err
}

// watchCard follows a turn of the pane from base on until it ends: the
// agent idle once seen working, or the pane waiting for the user. The
// state, body and failed are the turn's end.
func (b *Bot) watchCard(c *turnCard, id string, base int, stop time.Time) (state, body string, failed, seen bool) {
	timeout := time.Duration(b.cfg.Timeout)
	if timeout <= 0 {
		timeout = settings.FeishuDefaultTimeout
	}
	if stop.IsZero() {
		stop = time.Now().Add(timeout)
	}
	var p proto.Pane
	var last, still time.Time
	for time.Now().Before(stop) {
		time.Sleep(waits.poll)
		if err := b.api.Call("wait", proto.WaitParams{ID: id, Timeout: int64(waits.poll / time.Millisecond)}, &p); err != nil && !timedOut(err) {
			return "error", "", true, !last.IsZero()
		}
		now := time.Now()
		if p.Status == proto.Working {
			last, still = now, now
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
	if v, ok := e.Event.Action.Value["kero"]; ok && fmt.Sprint(v) == "choice" {
		return b.onChoiceAction(e)
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

// markOn is the reaction a turn's message takes as the turn starts, which
// the turn takes back as it ends.
const markOn = "OnIt"

// marked is a message that takes the turn's marks.
type marked struct{ msg string }

// markOn marks the message of a turn as it starts, as the agent works.
func (b *Bot) markOn(msg string) *marked {
	if msg == "" || !cards() {
		return nil
	}
	if err := b.msgs.React(msg, markOn); err != nil {
		b.logf("feishu: marking a message: %v", err)
		return nil
	}
	return &marked{msg}
}

// off takes the mark back, as the answer comes.
func (b *Bot) off(m *marked) {
	if m == nil {
		return
	}
	if err := b.msgs.Unreact(m.msg, markOn); err != nil {
		b.logf("feishu: taking a mark back: %v", err)
	}
}
