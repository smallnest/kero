package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kero/internal/proto"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// TestPermissionAsk asks a permission of a background turn in its chat,
// and gives its answer to the command: allow once, and allow always for
// the session.
func TestPermissionAsk(t *testing.T) {
	b, luna, _ := newBot(t)
	key, chatID, session := "c1", "c1", "s1"
	_, askFile, _ := proto.AnswerPair(b.outFile(session))
	os.MkdirAll(filepath.Dir(askFile), 0o700)
	os.WriteFile(askFile, nil, 0o600)
	ended := make(chan struct{})
	defer close(ended)
	go b.answerAsks(key, chatID, session, time.Now().Add(time.Minute), ended)
	ask := func(id string) {
		line := `{"type":"control_request","request_id":"` + id + `","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`
		f, _ := os.OpenFile(askFile, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString(line + "\n")
		f.Close()
	}
	waitFor := func(what string, ok func() bool) {
		for i := 0; i < 100; i++ {
			if ok() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: nothing", what)
	}
	ask("r1")
	var a *permissionAsk
	waitFor("the first ask", func() bool {
		asks.mu.Lock()
		defer asks.mu.Unlock()
		for _, x := range asks.all {
			if x.tool == "Bash" && x.session == session {
				a = x
			}
		}
		return a != nil
	})
	if _, err := askCard(a, ""); err != nil || !strings.Contains(askText(a.tool, a.input), "ls") {
		t.Fatalf("the card: %v", err)
	}
	b.onAskAction(action(a.id, "once"))
	waitFor("the answer", func() bool {
		luna.mu <- struct{}{}
		n := len(luna.input)
		<-luna.mu
		return n == 1
	})
	if in := luna.inputs(); len(in) != 1 || !strings.Contains(in[0], `"request_id":"r1"`) || !strings.Contains(in[0], `"behavior":"allow"`) {
		t.Fatalf("sent %v", in)
	}
	ask("r2")
	waitFor("the second ask", func() bool {
		asks.mu.Lock()
		defer asks.mu.Unlock()
		return len(asks.all) == 1
	})
	asks.mu.Lock()
	for _, x := range asks.all {
		a = x
	}
	asks.mu.Unlock()
	b.onAskAction(action(a.id, "always"))
	waitFor("the second answer", func() bool { return len(luna.inputs()) == 2 })
	ask("r3")
	// The chat asked the session's next turns no permission of the kind.
	waitFor("the third ask", func() bool {
		asks.mu.Lock()
		defer asks.mu.Unlock()
		return len(asks.all) == 0
	})
	asks.mu.Lock()
	asks.all = nil
	asks.mu.Unlock()
	if _, ok := b.alwaysOf(key, "Bash"); !ok {
		t.Fatal("not allowed for the session")
	}
}

// inputs is what the command's input took.
func (f *fakeLuna) inputs() []string {
	f.mu <- struct{}{}
	defer func() { <-f.mu }()
	return append([]string{}, f.input...)
}

// action is a click of a permission card's button.
func action(id, do string) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]any{"kero": "perm", "ask": id, "do": do}},
	}}
}
