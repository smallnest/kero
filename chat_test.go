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
		{Kind: "tool", Text: "Tool Bash go build"},
		{Kind: "agent", Text: "I will look."},
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
	for _, want := range []string{"Kero bot", "Fix the build", "Tool Bash go build", "The build passes."} {
		if !tt.HasText(want) {
			t.Errorf("the conversation lacks %q: %v", want, tt.Texts())
		}
	}
	// The field takes the focus, which settles in a frame or two, and
	// typing comes after.
	if err := tt.Click(locales.ChatPlaceholder()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	tt.Type("Run the tests")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	chatSettle(tt, func() bool { return strings.Contains(f.prog.typed(), "Run the tests") })
	if got := f.prog.typed(); !strings.Contains(got, "Run the tests") || !strings.HasSuffix(got, "\r") {
		t.Fatalf("the pane's program got %q", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 0 {
		t.Errorf("the prompt went to Feishu too: %+v", f.sent)
	}
	if d := a.chatOf("s1").draft; d != "" {
		t.Errorf("the field keeps %q", d)
	}
}

// The chats of the app, a choice showing the window of one.
func TestChatChoose(t *testing.T) {
	a, _ := chatApp(t)
	a.showPaneChat(a.session("s1"), "a")
	tt := ui.NewTester(a.view, 1600, 800)
	tt.Frame()
	chatSettle(tt, func() bool { return tt.HasText("Fix the build") })
	// The call answers in a goroutine, so it runs as a frame's would.
	a.later = append(a.later, func() { a.openChats("s1") })
	for i := 0; i < 50 && !a.pal.open; i++ {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	tt.Frame()
	if !tt.HasText("Team") || !tt.HasText("oc_2") {
		t.Fatalf("the chats: %v", tt.Texts())
	}
	if err := tt.Click("Team"); err != nil {
		t.Fatal(err)
	}
	chatSettle(tt, func() bool { return a.chatOf("s1").feed.Chat == "oc_2" })
	if got := a.chatOf("s1").feed.Chat; got != "oc_2" {
		t.Errorf("the window shows %q", got)
	}
}

// The header's chat button turns a pane to its chat, and back to the
// terminal, which the chat's card shows alone.
func TestChatToggle(t *testing.T) {
	a, _ := chatApp(t)
	tt := ui.NewTester(a.view, 1600, 800)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	// The controls show while the pointer is over the pane, or the pane
	// has the focus, as it does here.
	if err := tt.Click(locales.SideChat()); err != nil {
		t.Fatalf("the chat button: %v", err)
	}
	tt.Frame()
	if !a.paneChat["a"] {
		t.Fatal("the pane shows no chat")
	}
	chatSettle(tt, func() bool { return tt.HasText("Fix the build") })
	if !tt.HasText("Fix the build") {
		t.Errorf("the chat: %v", tt.Texts())
	}
	if err := tt.Click(locales.ActionShowTerminal()); err != nil {
		t.Fatalf("the terminal button: %v", err)
	}
	tt.Frame()
	if a.paneChat["a"] {
		t.Error("the pane shows the chat still")
	}
	// The other pane's chat shows it zoomed, and the terminal its card
	// again.
	cur := a.session("s1")
	cur.Zoomed = ""
	a.showPaneChat(cur, "b")
	chatSettle(tt, func() bool { return a.session("s1").Zoomed == "b" })
	if z := a.session("s1").Zoomed; z != "b" {
		t.Fatalf("the other pane is not zoomed: %q", z)
	}
	a.hidePaneChat(a.session("s1"), "b")
	tt.Frame()
	if len(a.paneChat) != 0 || a.session("s1").Zoomed != "" {
		t.Errorf("the panes after the terminal: %v, zoomed %q", a.paneChat, a.session("s1").Zoomed)
	}
	if tt.HasText(locales.ActionShowTerminal()) {
		t.Error("the terminal's pane shows the chat's button still")
	}
}
