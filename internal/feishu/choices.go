package feishu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// The agent sometimes asks the user to pick from a list, as "Which color:
//
//	1. Red
//	2. Blue
//
// and the bot shows the question, and its options as buttons, in the
// turn's card, with a field for an answer of its own. A click answers the
// agent with the option's text, as /answer does.

// questions are the questions the chat may answer by their buttons, by
// ID.
var questions struct {
	mu   sync.Mutex
	next int
	all  map[string]*questionAsk
}

// questionAsk is a question of a turn, which the chat's buttons answer.
type questionAsk struct {
	id       string
	chatID   string
	lang     string
	question string
	options  []string    // the options, as the buttons answer them
	answers  chan string // the chat's answer, once
}

// maxOptions is how many options a question's card shows: Feishu's cards
// are smaller with the buttons.
const maxOptions = 10

// optionLine is a line of the agent's question that names a choice: a
// number, a letter, a bullet or a bracket before its text, as "1. Red",
// "A) Blue" or "（Red）", and not a sublist's "  - vm".
var (
	numberedOption  = regexp.MustCompile(`^\s*(\d{1,2})[.)、．:]\s*(\S.*)$`)
	letteredOption  = regexp.MustCompile(`^\s*([A-Za-z])[.)、．:]\s*(\S.*)$`)
	bracketedOption = regexp.MustCompile(`^\s*[（(]\s*([^）)]{1,40}\S)\s*[）)]\s*$`)
)

// questionOf is the agent's question in a turn's text, and its options:
// the last run of two or more option lines, with the line before them, and
// "" with no options.
func questionOf(text string) (string, []string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	start, options := -1, []string{}
	for i, line := range lines {
		if opt, ok := optionOf(line); ok {
			if start < 0 || strings.TrimSpace(lines[i-1]) == "" {
				start = i
			}
			options = append(options, opt)
			continue
		}
		if start >= 0 && strings.TrimSpace(line) != "" {
			start, options = -1, options[:0]
		}
	}
	if start < 0 || len(options) < 2 || len(options) > maxOptions {
		return "", nil
	}
	question := strings.TrimSpace(strings.Join(lines[:start], "\n"))
	if i := strings.LastIndex(question, "\n\n"); i >= 0 {
		// The agent's words before the question are no part of it.
		question = strings.TrimSpace(question[i+2:])
	}
	return question, options
}

// optionOf is a line's option, the text that answers it, and whether the
// line is one.
func optionOf(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || len([]rune(t)) > 80 {
		return "", false
	}
	if m := bracketedOption.FindStringSubmatch(t); m != nil {
		return strings.TrimSpace(m[1]), true
	}
	var text string
	if m := numberedOption.FindStringSubmatch(t); m != nil {
		text = m[2]
	} else if m := letteredOption.FindStringSubmatch(t); m != nil && strings.Count(t, " ") < 6 {
		text = m[2]
	} else {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.HasSuffix(text, ":") || strings.HasSuffix(text, "：") {
		return "", false // the number of a list whose lines are other lists
	}
	// A markdown bold's stars are no part of the choice.
	text = strings.Trim(strings.TrimSuffix(strings.TrimPrefix(text, "**"), "**"), " ")
	return text, true
}

// choice is a turn's question, whose card the chat answers. msg is the
// card that shows it.
type choice struct {
	key      string
	chatID   string
	msg      string
	question string
	options  []string
}

// newChoice makes a turn's question of the words the agent wrote, which
// the card shows: nil where they name no options.
func newChoice(key, chatID, msg, text string) *choice {
	q, options := questionOf(text)
	if len(options) < 2 {
		return nil
	}
	return &choice{key: key, chatID: chatID, msg: msg, question: q, options: options}
}

// endsAsk tells whether the agent's words ask the chat, which no answer
// can end: their last rune is a ":" or a "?".
func endsAsk(q string) bool {
	r := []rune(strings.TrimSpace(q))
	if len(r) == 0 {
		return false
	}
	last := r[len(r)-1]
	return last == ':' || last == '：' || last == '?' || last == '？'
}

// question is a turn's text as a question the chat may choose to answer,
// and the agent's words alone, which its card shows: the text has two or
// more options, and its question ends in ":" or "?".
func question(text string) (string, bool) {
	q, options := questionOf(text)
	if len(options) < 2 || !endsAsk(q) {
		return "", false
	}
	return q, true
}

// askedQuestion tells whether a card's text asks the chat, which its
// options answer.
func askedQuestion(text string) bool {
	q, options := questionOf(text)
	return len(options) >= 2 && endsAsk(q)
}

// askOptions shows a turn's question instead of its card's text: the
// question, the buttons of its options, and a field for an answer of the
// chat's own, in the language of the question's words. The question's
// text is what its card shows, which names the options the buttons take.
func (b *Bot) askOptions(c *choice) {
	if c == nil || !cards() || c.msg == "" || !endsAsk(c.question) {
		return
	}
	// The language of the question's words, whatever the chat's is.
	questions.mu.Lock()
	if questions.all == nil {
		questions.all = map[string]*questionAsk{}
	}
	questions.next++
	ask := &questionAsk{id: fmt.Sprintf("%d-%d", time.Now().UnixNano(), questions.next), chatID: c.chatID,
		lang: langOf(c.msg), question: c.question, options: c.options, answers: make(chan string, 1)}
	questions.all[ask.id] = ask
	questions.mu.Unlock()
	if b.answerOnce(ask) {
		return
	}
	shown, err := optionsCard(ask, "")
	if err != nil {
		b.logf("feishu: a question's card: %v", err)
		return
	}
	if err := b.msgs.UpdateCard(c.msg, shown); err != nil {
		b.logf("feishu: showing a question's options: %v", err)
	}
}

// answerOnce tells whether the chat answered a question already, and
// answers the agent with the answer if it did: a question answered before
// its card shows goes on at once.
func (b *Bot) answerOnce(ask *questionAsk) bool {
	select {
	case text := <-ask.answers:
		b.answer(ask.chatID, text)
		return true
	default:
		return false
	}
}

// optionsCard is the card of a question: the agent's question, the
// buttons of its options, and a field that takes an answer of the chat's
// own; answered keeps the question and shows the chat's answer alone.
func optionsCard(a *questionAsk, answered string) (string, error) {
	var elems []any
	if a.question != "" {
		elems = append(elems, map[string]any{"tag": "markdown", "content": a.question})
	}
	if answered != "" {
		elems = append(elems, map[string]any{"tag": "markdown", "content": "**" + escapeMarkdown(answered) + "**"})
	} else {
		// Each option is a button of its own.
		for i, opt := range a.options {
			elems = append(elems, optionButton(a.id, i, opt))
		}
		elems = append(elems,
			map[string]any{"tag": "input", "input_type": "text", "name": "text",
				"placeholder": map[string]any{"tag": "plain_text", "content": word(a.lang, "choice.other")},
				"max_length":  2000,
			},
			map[string]any{
				"tag":   "button",
				"type":  "primary",
				"text":  map[string]any{"tag": "plain_text", "content": word(a.lang, "choice.send")},
				"value": map[string]any{"kero": "choice", "ask": a.id, "opt": -1},
			})
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": word(a.lang, "choice.title")},
			"template": "yellow",
		},
		"body": map[string]any{"direction": "vertical", "elements": elems},
	}
	data, err := json.Marshal(card)
	return string(data), err
}

// optionButton is the button that answers a question with one of its
// options.
func optionButton(id string, i int, text string) map[string]any {
	return map[string]any{
		"tag":   "button",
		"type":  "default",
		"text":  map[string]any{"tag": "plain_text", "content": fmt.Sprintf("%d. %s", i+1, truncateRunes(text, 40))},
		"value": map[string]any{"kero": "choice", "ask": id, "opt": fmt.Sprint(i)},
	}
}

// truncateRunes cuts words to n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// onChoiceAction answers the click of a question's card: the option's
// text goes to the agent, or else the field's, and the card shows the
// answer. The question's words go before the answer, as the agent gets
// them with it.
func (b *Bot) onChoiceAction(e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	v := e.Event.Action.Value
	questions.mu.Lock()
	ask := questions.all[fmt.Sprint(v["ask"])]
	questions.mu.Unlock()
	if ask == nil {
		return toast("en_us", word("en_us", "choice.gone")), nil
	}
	lang := ask.lang
	var answer string
	var i int
	if _, err := fmt.Sscan(fmt.Sprint(v["opt"]), &i); err == nil && i >= 0 && i < len(ask.options) {
		answer = ask.options[i]
	} else {
		answer = permissionInput(e)
	}
	if answer == "" {
		return toast(lang, word(lang, "choice.empty")), nil
	}
	questions.mu.Lock()
	open := questions.all[ask.id]
	delete(questions.all, ask.id)
	questions.mu.Unlock()
	if open != ask {
		return toast(lang, word(lang, "choice.answered")), nil
	}
	// The question's words end with a list of the options, which the
	// buttons show: the agent gets them once.
	sent := answer
	if q := ask.question; q != "" {
		sent = q + "\n" + answer
	}
	if e.Event.Context != nil {
		shown, err := optionsCard(ask, answer)
		if err == nil {
			if id := deref(&e.Event.Context.OpenMessageID); id != "" {
				b.msgs.UpdateCard(id, shown)
			}
		}
	}
	select {
	case ask.answers <- sent:
		go b.answer(ask.chatID, sent)
	case <-time.After(time.Minute):
		// The question's card went unanswered: the agent gets no answer.
	}
	return toast(lang, word(lang, "choice.sent")), nil
}
