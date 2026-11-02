package feishu

import (
	"strings"

	"kero/internal/proto"
)

// A Feishu app may connect from several machines, each with its own
// kerod: every host hears each event, and each reply to the bot's
// answer, which Feishu gives by its ID and chat alone, for the answer's
// text belongs to the host that wrote it. The bot's answer names the
// event in its first line; the host that answers the event's message
// keeps its answer ("feishu.keep"), and the host the chat's messages go
// to answers the chat's replies with the answer's event. A chat goes to
// one host with /host, or else to the first that hears its message,
// which takes the event ("feishu.take").

// keep is what kerod keeps of an answer: the event it names, the chat
// it came to, and its lines.
type keep struct {
	Event string `json:"event"`
	Chat  string `json:"chat"`
	Text  string `json:"text"`
}

// kept is what kerod keeps: its "feishu.kept" gives it.
func kept() ([]keep, error) {
	var k struct {
		Turns []keep `json:"turns"`
	}
	err := kerod.Call("feishu.kept", nil, &k)
	return k.Turns, err
}

// keptLines is the last answer of a shared app that names an event:
// another host answers the chat's replies with it.
func keptLines() (event, lines string) {
	turns, _ := kept()
	if len(turns) == 0 {
		return "", ""
	}
	t := turns[len(turns)-1]
	return t.Event, t.Text
}

// noteTurn keeps this host's answer, which another host answers the
// chat's replies with.
func (b *Bot) noteTurn(event, chatID, text string) {
	if event == "" || text == "" {
		return
	}
	kerod.Call("feishu.keep", keep{Event: event, Chat: chatID, Text: text}, nil)
}

// chatOf is the chat every answer kerod keeps is of: a bot answers one
// chat at a time.
func chatOf() string {
	turns, _ := kept()
	if len(turns) == 0 {
		return ""
	}
	return turns[len(turns)-1].Chat
}

// tookIt is the host that took an event, as the hosts keep it: "" for
// none.
func (b *Bot) tookIt(event string) string {
	return b.takeAt(event, "")
}

// takeIt takes an event for this host, unless another took it first.
func (b *Bot) takeIt(event string) string {
	return b.takeAt(event, b.host())
}

// takeAt is "feishu.take": with a host, it takes the event unless
// another took it; it gives the host that has it.
func (b *Bot) takeAt(event, host string) string {
	var k struct{ Host string }
	r := &take{Event: event, Host: host}
	if b.api.Call("feishu.take", r, &k) == nil && k.Host != "" {
		return k.Host
	}
	r = &take{Event: event, Host: host}
	var res proto.FeishuTaken
	if kerod == nil || kerod.Call("feishu.take", r, &res) != nil {
		return ""
	}
	return res.Host
}

// take is "feishu.take"'s params: the host takes the event, which kerod
// gives to it or to the host that has it.
type take struct{ Event, Host string }

func (b *Bot) claim(parent, chatID string) (event string, leave bool) {
	if !b.shared() {
		return
	}
	// The message Feishu has, and what its lines say.
	msg, err := b.api.Kept(parent)
	if err != nil {
		// Feishu has no message of the ID, as of a card: its reply is no
		// event.
		leave = true
		return
	}
	// The answer names the event it answers: kerod keeps the answer
	// this host wrote, which Feishu gives another host by its ID alone.
	event, from, tookof := linesOf(deref(msg.ChatId), deref(msg.Content))
	if event == "" && chatID == chatOf() {
		if e, _ := keptLines(); e != "" {
			event = e
		}
	}
	if from == b.host() {
		// This host's answer, whose lines the other hosts hear.
		return "", true
	}
	if event != "" {
		// An answer names the event.
		return
	}
	// The first host that hears a message of the event's takes it: the
	// other hosts leave the message, and the chats that the message's
	// host answers go to it.
	took := b.takeAt(parent, b.host())
	if took == "" {
		leave = true
		return
	}
	// A /host'd chat answers the event's next messages with the answer,
	// which says the chat's host: no other host's message in the chat
	// answers anything, so every host answers it.
	if tookof != "" && tookof != b.host() {
		return "", false
	}
	b.notedChat(chatID, parent, took)
	return "", false
}

// linesOf reads an answer's lines: its first, the event, then the lines
// that each host that hears it writes. It gives the event, the host the
// chat's messages go to, and the host that took the event. Feishu gives
// a host another host's answer by its ID and chat alone, its lines ""
// then: those are the answer's, which kerod keeps.
func linesOf(chatID, content string) (event, from, tookof string) {
	lines := strings.Split(messageText(content), "\n")
	if len(lines) < 2 {
		if chatID == "" || chatID != chatOf() {
			return "", "", ""
		}
		_, content = keptLines()
	}
	event, from, _, tookof = hearing(content)
	return event, from, tookof
}

// notedChat notes the event the chat's answer is of, and the host it goes
// to.
func (b *Bot) notedChat(chatID, event, owner string) {
	b.mu.Lock()
	b.wants[chatKey(chatID, "")] = owner
	b.claims[chatKey(chatID, "")] = event
	b.mu.Unlock()
}
