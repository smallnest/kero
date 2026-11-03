package feishu

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// A list of the agent's that asks the chat to choose is a question, with
// its options as the buttons answer them.
func TestQuestionOf(t *testing.T) {
	for _, tc := range []struct {
		text, question string
		options        []string
	}{
		{"I can fix it two ways.\n\nWhich color:\n1. Red\n2. Blue", "Which color:", []string{"Red", "Blue"}},
		{"Pick one?\nA) vm\nB) **Container**", "Pick one?", []string{"vm", "Container"}},
		{"Pick:\n（Red）\n（Blue）", "Pick:", []string{"Red", "Blue"}},
		{"Pick:\n1. 红\n2、蓝\n3、绿", "Pick:", []string{"红", "蓝", "绿"}},
		// No question asks to choose, and a single option is no list.
		{"Steps:\n1. Install\n2. Run\n\nDone.", "", nil},
		{"Pick:\n1. Only", "", nil},
	} {
		q, options := questionOf(tc.text)
		if q != tc.question || !slices.Equal(options, tc.options) {
			t.Errorf("%q is %q %q", tc.text, q, options)
		}
		if want := tc.question != ""; endsAsk(q) != want {
			t.Errorf("%q asks %v", tc.text, !want)
		}
	}
}

// A click of a question's card answers the agent with its option, and
// the card shows the answer.
func TestChoiceAction(t *testing.T) {
	b, luna, msgs := newBot(t)
	// A turn's answer is the question's card, which takes its options.
	// The agent's question, in the chat's own words, shows as a card
	// whose options are buttons.
	which := "Which color?\n1. Red\n2. Blue"
	b.sync = true
	b.chats["c1"] = &chat{id: "s1", event: "ev_q"}
	b.askOptions(newChoice("ev_q", "c1", "om_card_1", which))
	shown, err := optionsCard(&questionAsk{id: "a1", lang: "en_us", question: "Which color?", options: []string{"Red", "Blue"}}, "")
	var card map[string]any
	if err != nil || json.Unmarshal([]byte(shown), &card) != nil || len(choiceButtons(card)) != 3 || !strings.Contains(shown, `"tag":"input"`) ||
		!strings.Contains(shown, `"content":"Which color?"`) {
		t.Fatalf("the card is %s, %v", shown, err)
	}
	// The card Feishu took the question as shows its options.
	if len(msgs.updates) != 1 || !strings.HasPrefix(msgs.updates[0], "om_card_") || !strings.Contains(msgs.updates[0], `"ask":"`) {
		t.Fatalf("the question's card %v", msgs.updates)
	}
	click := func(opt string) *callback.CardActionTriggerResponse {
		id := msgs.updates[0]
		id = id[strings.Index(id, `"ask":"`)+7:]
		id = id[:strings.Index(id, `"`)]
		res, err := b.onCardAction(nil, &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
			Action:  &callback.CallBackAction{Value: map[string]any{"kero": "choice", "ask": id, "opt": opt}},
			Context: &callback.Context{OpenMessageID: "om_card_1"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := click("1"); res == nil || res.Toast.Content != "Sent." {
		t.Fatalf("the click answers %+v", res)
	}
	time.Sleep(300 * time.Millisecond) // the answer goes to the pane, which takes a moment to type it
	// The question's words go before the option, which the agent gets
	// once: its list of the options is no part of them.
	answered := false
	for _, s := range luna.sent {
		answered = answered || s.Data == "Which color?\nBlue"
	}
	if !answered {
		t.Errorf("the pane gets %+v", luna.sent)
	}
	// The card shows the question, and the answer under it.
	u := msgs.updates[1]
	last := parseCard(t, u[strings.Index(u, ": ")+2:strings.LastIndex(u, " [marks")])
	if !strings.Contains(cardText(last), "**Blue**") || strings.Contains(cardText(last), "Red\n") {
		t.Errorf("the card shows %q", cardText(last))
	}
	// The question is answered once.
	if res := click("0"); res == nil || res.Toast.Content != "This question is answered already." {
		t.Errorf("the second click answers %+v", res)
	}
}

// choiceButtons are a card's buttons that answer a question.
func choiceButtons(card map[string]any) []map[string]any {
	body, _ := card["body"].(map[string]any)
	elems, _ := body["elements"].([]any)
	var buttons []map[string]any
	for _, e := range elems {
		m, _ := e.(map[string]any)
		cols, _ := m["columns"].([]any)
		if m["tag"] == "column_set" {
			m = map[string]any{"elements": cols}
		}
		inner, _ := m["elements"].([]any)
		for _, e := range inner {
			b, _ := e.(map[string]any)
			if v, _ := b["value"].(map[string]any); v["kero"] == "choice" {
				buttons = append(buttons, b)
			}
		}
		if len(cols) == 0 {
			if v, _ := m["value"].(map[string]any); v["kero"] == "choice" {
				buttons = append(buttons, m)
			}
		}
	}
	return buttons
}
