package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"

	"kero/internal/proto"
)

// A turn run in the background (feishu-run = command) has no terminal to
// ask in: Claude Code asks the program that runs it, on the stream of its
// messages, for each tool it would run beyond its permissions. The bot
// asks the chat instead: a card names the tool and what it would run, and
// its buttons allow it once, allow it for the chat's session, or refuse
// it. A reason the chat gives with the refusal goes to the agent.

// asks are the permissions the chat asks for, by ID.
var asks struct {
	mu   sync.Mutex
	next int
	all  map[string]*permissionAsk
}

// permissionAsk is a permission of a background turn, which the chat
// answers.
type permissionAsk struct {
	id      string
	key     string
	chatID  string
	session string
	lang    string
	tool    string
	input   any
	// asked is the command's ID of what it asked.
	asked  string
	at     time.Time
	answer chan permissionAnswer
}

// permissionAnswer is the chat's answer: allow or deny, and the words it
// gives.
type permissionAnswer struct {
	Allow bool
	Words string
}

// newAsk puts a permission of a turn in the chat that runs it, which
// returns as its buttons answer, as the turn times out, or a refusal.
func (b *Bot) newAsk(key, chatID, session, lang, tool string, input any) *permissionAsk {
	asks.mu.Lock()
	if asks.all == nil {
		asks.all = map[string]*permissionAsk{}
	}
	asks.next++
	// The days, so that one never repeats.
	a := &permissionAsk{id: fmt.Sprintf("%d-%d", time.Now().UnixNano(), asks.next), key: key, chatID: chatID, session: session,
		lang: lang, tool: tool, input: input, at: time.Now(), answer: make(chan permissionAnswer, 1)}
	asks.all[a.id] = a
	asks.mu.Unlock()
	if _, ok := b.alwaysOf(key, tool); ok {
		b.answerPermission(a.id, permissionAnswer{Allow: true})
	}
	return a
}

// ask sends the card of a permission, the first time: a turn's polling
// reads a permission again with each of its output, and the chat asks
// once.
func (b *Bot) ask(a *permissionAsk) {
	shown, err := askCard(a, "")
	if err != nil {
		b.logf("feishu: a permission's card: %v", err)
		return
	}
	if _, err := b.msgs.SendCard("chat_id", a.chatID, shown); err != nil {
		b.logf("feishu: sending a permission's card: %v", err)
	}
}

// alwaysOf is the tools a chat allowed for its session, as two
// booleans: whether the chat is one, and whether it allows the tool.
func (b *Bot) alwaysOf(key, tool string) (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.chats[key]
	if c == nil {
		return false, false
	}
	return true, c.allowed[tool]
}

// allowAlways allows a tool for the chat's session, from now on.
func (b *Bot) allowAlways(key, tool string) {
	b.mu.Lock()
	c := b.chats[key]
	if c == nil {
		c = &chat{}
		b.chats[key] = c
	}
	if c.allowed == nil {
		c.allowed = map[string]bool{}
	}
	c.allowed[tool] = true
	b.mu.Unlock()
	b.save()
}

// answerPermission answers a permission from the chat, once.
func (b *Bot) answerPermission(id string, ans permissionAnswer) bool {
	asks.mu.Lock()
	a := asks.all[id]
	asks.mu.Unlock()
	if a == nil {
		return false
	}
	select {
	case a.answer <- ans:
		return true
	default:
		return false
	}
}

// dropAsk forgets a permission once it is answered: its card stays as it
// is.
func dropAsk(id string) {
	asks.mu.Lock()
	delete(asks.all, id)
	asks.mu.Unlock()
}

// askCard is the card that asks for a permission: the tool, what it
// would run, and the buttons that answer it, whose value the bot reads.
func askCard(a *permissionAsk, state string) (string, error) {
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": word(a.lang, "perm.title")},
			"template": "yellow",
		},
	}
	if state != "" {
		card["header"].(map[string]any)["template"] = "grey"
	}
	elems := []any{}
	text := askText(a.tool, a.input)
	if text != "" {
		elems = append(elems, map[string]any{"tag": "markdown", "content": text})
	}
	if state == "" {
		button := func(id, label, kind string) map[string]any {
			return map[string]any{
				"tag":   "button",
				"type":  kind,
				"text":  map[string]any{"tag": "plain_text", "content": label},
				"value": map[string]any{"kero": "perm", "ask": a.id, "do": id},
			}
		}
		elems = append(elems, map[string]any{"tag": "action", "actions": []any{
			button("once", word(a.lang, "perm.once"), "primary"),
			button("always", word(a.lang, "perm.always"), "default"),
			button("deny", word(a.lang, "perm.deny"), "danger"),
		}})
		elems = append(elems, map[string]any{"tag": "markdown", "content": "*" + escapeMarkdown(word(a.lang, "perm.hint")) + "*"})
	} else {
		elems = append(elems, map[string]any{"tag": "markdown", "content": "*" + escapeMarkdown(state) + "*"})
	}
	card["body"] = map[string]any{"direction": "vertical", "elements": elems}
	data, err := json.Marshal(card)
	return string(data), err
}

// askText names a tool's call: its name, and the command, the file or
// the address it would take, in a code block.
func askText(tool string, input any) string {
	var s strings.Builder
	s.WriteString("**" + escapeMarkdown(tool) + "**")
	m, _ := input.(map[string]any)
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "prompt", "description"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			s.WriteString("\n\n" + codeFence(truncate(v, 2000)))
			return s.String()
		}
	}
	if input != nil {
		if b, err := json.Marshal(input); err == nil && string(b) != "{}" {
			s.WriteString("\n\n" + codeFence(truncate(string(b), 2000)))
		}
	}
	return s.String()
}

// permissionOf reads a permission request of Claude Code's stream: its
// ID, its tool, and the tool's input.
func permissionOf(msg json.RawMessage) (id, tool string, input any, ok bool) {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Tool    string `json:"tool_name"`
			Input   any    `json:"input"`
		} `json:"request"`
	}
	if json.Unmarshal(msg, &m) != nil || m.Type != "control_request" || m.Request.Subtype != "can_use_tool" {
		return "", "", nil, false
	}
	return m.RequestID, m.Request.Tool, m.Request.Input, m.RequestID != ""
}

// permissionReply is the answer to a permission request, as Claude Code
// takes it.
func permissionReply(id string, ans permissionAnswer) string {
	if ans.Allow {
		b, _ := json.Marshal(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": id,
				"response":   map[string]any{"behavior": "allow", "updatedInput": nil},
			},
		})
		return string(b)
	}
	msg := ans.Words
	if msg == "" {
		msg = "The user refused this."
	}
	b, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": id,
			"response":   map[string]any{"behavior": "deny", "message": msg},
		},
	})
	return string(b)
}

// onAskAction answers the click of a permission's card: allow it once,
// allow its tool for the session, or refuse it, with words under the
// text's heading.
func (b *Bot) onAskAction(e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	a, do, _ := askOf(e)
	lang := "en_us"
	if a != nil {
		lang = a.lang
	}
	if a == nil || do == "" {
		return nil, nil
	}
	asks.mu.Lock()
	id := a.id
	asks.mu.Unlock()
	ans := permissionAnswer{}
	switch do {
	case "once":
		ans.Allow = true
	case "always":
		ans.Allow = true
		b.allowAlways(a.key, a.tool)
	case "deny":
		ans.Words = permissionInput(e)
	}
	if !b.answerPermission(id, ans) {
		return toast(lang, word(lang, "perm.answered")), nil
	}
	dropAsk(id)
	if shown, err := askCard(a, word(lang, "perm."+do)); err == nil {
		if e.Event.Context != nil && e.Event.Context.OpenMessageID != "" {
			b.msgs.UpdateCard(e.Event.Context.OpenMessageID, bodyOf(shown))
		}
	}
	return toast(lang, word(lang, "perm."+do)), nil
}

// askOf reads a card action's permission, and its choice.
func askOf(e *callback.CardActionTriggerEvent) (*permissionAsk, string, string) {
	if e == nil || e.Event == nil || e.Event.Action == nil {
		return nil, "", ""
	}
	v := e.Event.Action.Value
	asks.mu.Lock()
	a := asks.all[fmt.Sprint(v["ask"])]
	asks.mu.Unlock()
	return a, fmt.Sprint(v["do"]), ""
}

// permissionInput is the words a card's action takes, from the card's
// own JSON, as Feishu sends them: the text of its input, which says why
// a permission is refused.
func permissionInput(e *callback.CardActionTriggerEvent) string {
	if e.EventReq == nil || e.EventReq.Body == nil {
		return ""
	}
	var in struct {
		Action struct {
			FormValue map[string]any `json:"form_value"`
			Input     string         `json:"input_value"`
		} `json:"action"`
	}
	if json.NewDecoder(bytes.NewReader(e.EventReq.Body)).Decode(&in) != nil {
		return ""
	}
	var words []string
	if in.Action.Input != "" {
		words = append(words, in.Action.Input)
	}
	for _, v := range in.Action.FormValue {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			words = append(words, s)
		}
	}
	return strings.Join(words, "\n")
}

// toast is a card's answer: a word in the chat, and no change to it.
func toast(lang, msg string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "info", Content: msg}}
}

// claudeAskFlags make Claude Code ask, on the stream of its messages,
// for each tool beyond its permissions, which a terminal would ask for:
// its messages come as JSON lines, and its answer among them.
var claudeAskFlags = []string{"--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}

// askPoll is how often a turn looks for the permissions its command asks
// for, which kerod puts in the second file of its output's pair.
const askPoll = 500 * time.Millisecond

// answerAsks asks the chat for the permissions the turn's command asks
// for, and answers them, until the command ends with done, or the turn
// times out at stop: the permissions unanswered then get a refusal, which
// the command would wait for otherwise.
func (b *Bot) answerAsks(key, chatID, session string, stop time.Time, done <-chan struct{}) {
	_, file, err := proto.AnswerPair(b.outFile(session))
	if err != nil {
		b.logf("feishu: %v", err)
		return
	}
	lang := "en_us"
	from := int64(0)
	seen := map[string]bool{}
	tick := time.NewTicker(askPoll)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-time.After(time.Until(stop)):
			return
		case <-tick.C:
		}
		data, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			b.logf("feishu: %v", err)
			continue
		}
		if from > int64(len(data)) {
			from = 0 // a turn of the same session's output, anew
		}
		part := data[from:]
		i := bytes.LastIndexByte(part, '\n')
		if i < 0 {
			continue // the line still written waits for its end
		}
		from += int64(i + 1)
		permits(part[:i], seen, func(id, tool string, in any) {
			a := b.newAsk(key, chatID, session, lang, tool, in)
			a.asked = id
			b.ask(a)
			b.answerAsk(a)
		})
	}
}

// permits gives each permission of lines of a command's stream to each,
// once.
func permits(lines []byte, seen map[string]bool, each func(id, tool string, in any)) {
	for _, line := range strings.Split(string(lines), "\n") {
		id, tool, in, ok := permissionOf(json.RawMessage(strings.TrimSpace(line)))
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		each(id, tool, in)
	}
}

// answerAsk gives a permission's answer to the command that asked, which
// the chat allows the tool for the session at once, or its click gives
// later. It is no step of the bot's polling, which reads the next
// permissions as it waits.
func (b *Bot) answerAsk(a *permissionAsk) {
	go func() {
		ans := <-a.answer
		dropAsk(a.id)
		if err := b.sendAnswer(a.session, a.asked, ans); err != nil {
			b.logf("feishu: answering a permission: %v", err)
			b.reply(a.chatID, err.Error())
		}
	}()
}

// sendAnswer gives a permission's answer to the command that asked.
func (b *Bot) sendAnswer(session, id string, ans permissionAnswer) error {
	return b.api.Call("agent.send", proto.InputParams{ID: session, Text: permissionReply(id, ans) + "\n"}, nil)
}
