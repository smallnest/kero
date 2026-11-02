package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"kero/internal/client"
	"kero/internal/proto"
	"kero/internal/settings"
	"kero/locales"
)

// fakeKerod is a kerod that answers a chat window's methods, on a socket
// of its own, where none runs.
type fakeKerod struct {
	mu    sync.Mutex
	feed  proto.FeishuFeed
	chats proto.FeishuChats
	sent  []proto.FeishuSend
	seen  int
	a     *App  // whose sessions the state answers
	prog  *pipe // the pane's program
}

// pipe is a program's side of a pane's terminal: what it gets, as typed.
type pipe struct {
	mu  sync.Mutex
	got strings.Builder
}

func (p *pipe) Read(b []byte) (int, error) { select {} }

func (p *pipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.got.Write(b)
	p.mu.Unlock()
	return len(b), nil
}

func (p *pipe) Close() error { return nil }

// typed is what the program got.
func (p *pipe) typed() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.got.String()
}

func (f *fakeKerod) answer(req proto.Request) proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var res any
	switch req.Method {
	case "feishu.feed":
		res = f.feed
	case "feishu.chats":
		res = f.chats
	case "feishu.send":
		var p proto.FeishuSend
		b, _ := json.Marshal(req.Params)
		json.Unmarshal(b, &p)
		f.sent = append(f.sent, p)
		res = proto.FeishuSent{Message: "om_sent"}
	case "state":
		st := proto.State{}
		if f.a != nil {
			for _, s := range f.a.st.Sessions {
				st.Sessions = append(st.Sessions, *f.a.session(s.ID))
			}
			st.Projects = f.a.st.Projects
		}
		// A state event's, as the app merges it.
		a := f.a
		if a != nil {
			go a.main(func() { a.setState(a.local(), st) })
		}
		return proto.Message{ID: req.ID}
	case "host.info", "view":
		return proto.Message{ID: req.ID}
	default:
		return proto.Message{ID: req.ID}
	}
	return proto.Message{ID: req.ID, Result: res}
}

// serve answers the calls of connections to it.
func (f *fakeKerod) serve(t *testing.T, ln net.Listener) {
	t.Cleanup(func() { ln.Close() })
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go f.conn(t, c)
	}
}

func (f *fakeKerod) conn(t *testing.T, c net.Conn) {
	{
		defer c.Close()
		dec, enc := json.NewDecoder(c), json.NewEncoder(c)
		// The connection's greeting, as kerod answers it.
		var hello proto.Hello
		if err := dec.Decode(&hello); err != nil {
			return
		}
		if err := enc.Encode(proto.Welcome{Version: proto.Version}); err != nil {
			return
		}
		for {
			var req proto.Request
			if err := dec.Decode(&req); err != nil {
				if err != io.EOF {
					return
				}
				return
			}
			if err := enc.Encode(f.answer(req)); err != nil {
				return
			}
		}
	}
}

// chatApp is the app with a Feishu app, and a fake kerod of this machine
// that answers the chat window's methods.
func chatApp(t *testing.T) (*App, *fakeKerod) {
	t.Helper()
	a := testApp(t)
	home := t.TempDir()
	path := filepath.Join(home, "config")
	if err := os.WriteFile(path, []byte("feishu-app = cli_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERO_CONFIG", path)
	set := settings.Load()
	a.set.Feishu = set.Feishu
	f := &fakeKerod{a: a}
	f.feed = proto.FeishuFeed{ID: "s1", Chat: "oc_1", Name: "Kero bot", Host: "mac", Pane: "a", Agent: "claude", Lines: []proto.FeishuLine{
		{Kind: "note", Text: "Fix the build"},
		{Kind: "think", Text: "The build broke."},
		{Kind: "tool", Text: "Tool Bash", Title: "go build", Detail: `{"command":"go build","description":"Build it"}`},
		{Kind: "tool", Text: "Result", Detail: "# kero\n./main.go:3:1: syntax error"},
		{Kind: "agent", Text: "I will look."},
		{Kind: "agent", Text: "Here: **fixed** the `main.go` syntax:\n\n```go\nfunc main() {}\n```\n\n- one\n- two"},
		{Kind: "agent", Text: "The build passes.", Answer: true},
	}}
	f.chats = proto.FeishuChats{Host: "mac", Chats: []proto.FeishuChat{{ID: "oc_1", Name: "Kero bot", Type: "p2p", Session: "s1"}, {ID: "oc_2", Name: "Team", Type: "group"}}}
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "kerod.sock"))
	if err != nil {
		t.Fatal(err)
	}
	go f.serve(t, ln)
	cl, err := client.ConnectVia(dialer(ln.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	h := a.local()
	h.cl = cl
	h.conn = connReady
	// The pane's terminal, whose program is a pipe here, takes the chat's
	// prompts, as the user would type them.
	f.prog = &pipe{}
	o := a.termOptions("a", nil)
	o.Conn = f.prog
	trm, err := terminal.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trm.Close() })
	a.terms["a"] = &termEntry{term: trm, started: time.Now()}
	return a, f
}

// dialer is a host's connection to a fake kerod.
type dialer string

func (d dialer) Dial() (net.Conn, error) { return net.Dial("unix", string(d)) }

// chatSettle shows the view until the reads answer.
func chatSettle(tt *ui.Tester, done func() bool) {
	for i := 0; i < 50; i++ {
		tt.Frame()
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The Chat view shows the session's conversation in the focused pane's
// terminal's place, and a prompt written in it goes to the session's chat.
func TestChatView(t *testing.T) {
	a, f := chatApp(t)
	// It is set as a click on the header's chat button, which the window
	// draws, does.
	a.showPaneChat(a.session("s1"), "a")
	tt := ui.NewTester(a.view, 1600, 800)
	tt.Frame()
	chatSettle(tt, func() bool { return tt.HasText("Fix the build") })
	if !a.paneChat["a"] {
		t.Fatal("the pane shows no chat")
	}
	// The agent's Markdown reads as it is written: no ** or ` about.
	for _, want := range []string{"Fix the build", "Bash", "go build", locales.ChatOutput(), "The build passes.", "Here: fixed the main.go syntax:", "func main() {}", "one"} {
		if !tt.HasText(want) {
			t.Errorf("the conversation lacks %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("**fixed**") {
		t.Errorf("the Markdown shows unread: %v", tt.Texts())
	}
	// The window only shows: it has no field to write a prompt in.
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prog.typed() != "" {
		t.Errorf("the pane's program got %q", f.prog.typed())
	}
}

// The Chat view lets the window be narrow: the conversation shrinks with
// it, as a chat app's does.
func TestChatNarrow(t *testing.T) {
	a, _ := chatApp(t)
	a.showPaneChat(a.session("s1"), "a")
	tt := ui.NewTester(a.view, 1600, 800)
	tt.Frame()
	chatSettle(tt, func() bool { return tt.HasText("Fix the build") })
	shot(t, tt, "chat-wide")
	tt.SetSize(900, 600)
	chatSettle(tt, func() bool { return tt.HasText("The build passes.") })
	shot(t, tt, "chat-narrow")
	if !tt.HasText("The build passes.") {
		t.Errorf("the narrow view lacks the answer: %v", tt.Texts())
	}
	// A tool's line opens its details.
	if err := tt.Click("Bash"); err != nil {
		t.Fatal(err)
	}
	chatSettle(tt, func() bool { return tt.HasText(`{"command":"go build","description":"Build it"}`) })
	if !tt.HasText(`{"command":"go build","description":"Build it"}`) {
		t.Errorf("the tool's details stay folded: %v", tt.Texts())
	}
}
