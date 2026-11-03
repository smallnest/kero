package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kero/internal/proto"
	"kero/internal/settings"
)

// The bot's API is a client's: what it sends reaches the pane, its wait
// ends, and what it reads comes back.
func TestBotLuna(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	d := testDaemon(t)
	s, err := d.create(proto.CreateParams{Command: []string{"sh", "-c", "cat"}})
	if err != nil {
		t.Fatal(err)
	}
	id := s.Panes[0].ID
	luna := &botLuna{d: d}
	if err := luna.Call("send", proto.SendParams{ID: id, Data: "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := luna.Call("send", proto.SendParams{ID: id, Data: "\t"}, nil); err != nil {
		t.Fatal(err)
	}
	var pane proto.Pane
	if err := luna.Call("wait", proto.WaitParams{ID: id, Timeout: 5000}, &pane); err != nil {
		t.Fatal(err)
	}
	var rd proto.ReadResult
	if err := luna.Call("read", proto.ReadParams{ID: id}, &rd); err != nil || !strings.Contains(rd.Text, "hi") {
		t.Errorf("read %q, %v", rd.Text, err)
	}
	if err := luna.Call("send", proto.SendParams{ID: "nosuch", Data: "x"}, nil); err == nil {
		t.Error("a send to no pane went")
	}
}

// A wait that times out still brings the pane back: a bot that polls in
// timeouts takes a turn's status from it, which no answer after the
// timeout would give it.
func TestWaitTimeoutBringsThePane(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	d := testDaemon(t)
	s, err := d.create(proto.CreateParams{})
	if err != nil {
		t.Fatal(err)
	}
	p := d.findPane(s.Panes[0].ID)
	p.mu.Lock()
	p.setStatus(proto.Working)
	p.mu.Unlock()
	var pane proto.Pane
	err = (&botLuna{d: d}).Call("wait", proto.WaitParams{ID: p.info.ID, Timeout: 100}, &pane)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a wait on a working pane ended %v", err)
	}
	if pane.ID != p.info.ID || pane.Status != proto.Working {
		t.Errorf("the timed out wait brought %+v, want %s working", pane, p.info.ID)
	}
}

// The status says what the keys and the secret make of Feishu.
func TestFeishuStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KERO_HOME", dir)
	t.Setenv("KERO_CONFIG", filepath.Join(dir, "config"))
	d := &Daemon{}
	var st proto.FeishuState
	if err := (&botLuna{d: d}).Call("feishu.status", nil, &st); err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Secret || st.Connected {
		t.Fatalf("nothing is set up: %+v", st)
	}
	if err := settings.SetFeishu(settings.Feishu{App: "cli_a"}); err != nil {
		t.Fatal(err)
	}
	if err := (&botLuna{d: d}).Call("feishu.status", nil, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Configured || st.Secret || st.Connected {
		t.Errorf("an app without its secret: %+v", st)
	}
	if err := settings.SaveFeishuSecret(settings.FeishuSecret{AppID: "cli_a", AppSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := (&botLuna{d: d}).Call("feishu.status", nil, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Secret {
		t.Errorf("the secret is kept: %+v", st)
	}
}

// A session's background command runs without a terminal, its output in
// a file, and ends the session's work; a session so made has no shell.
func TestBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	d := testDaemon(t)
	s, err := d.create(proto.CreateParams{Name: "bg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.setBackground(s.ID); err != nil {
		t.Errorf("setting it again: %v", err)
	}
	luna := &botLuna{d: d}
	// A command that still runs takes no other.
	slow := filepath.Join(t.TempDir(), "slow")
	if err := luna.Call("agent.background", proto.BackgroundParams{ID: s.ID, Out: slow, Command: []string{"sleep", "1"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := luna.Call("agent.background", proto.BackgroundParams{ID: s.ID, Out: slow, Command: []string{"true"}}, nil); err == nil {
		t.Error("a command ran over one that still runs")
	}
	var o proto.Output
	for i := 0; i < 100 && !o.Ended; i++ {
		time.Sleep(50 * time.Millisecond)
		if err := luna.Call("agent.output", proto.OutputParams{ID: s.ID, From: o.From}, &o); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "out")
	if err := luna.Call("agent.background", proto.BackgroundParams{ID: s.ID, Out: out, Command: []string{"printf", "hello 'world'"}}, nil); err != nil {
		t.Fatal(err)
	}
	o = proto.Output{}
	for i := 0; i < 100 && !o.Ended; i++ {
		time.Sleep(50 * time.Millisecond)
		if err := luna.Call("agent.output", proto.OutputParams{ID: s.ID, From: o.From}, &o); err != nil {
			t.Fatal(err)
		}
	}
	if !o.Ended || o.Text != "hello 'world'" || o.Exited != 0 {
		t.Fatalf("output %+v", o)
	}
	var p proto.Pane
	if err := luna.Call("wait", proto.WaitParams{ID: s.Panes[0].ID, Timeout: 3000}, &p); err != nil || p.Status != proto.Idle {
		t.Errorf("the session ends idle: %+v, %v", p, err)
	}
	// The command that ended gives way to the next prompt's.
	out2 := filepath.Join(t.TempDir(), "out2")
	if err := luna.Call("agent.background", proto.BackgroundParams{ID: s.ID, Out: out2, Command: []string{"printf", "second"}}, nil); err != nil {
		t.Fatalf("a second command after the first: %v", err)
	}
	o = proto.Output{}
	for i := 0; i < 100 && !o.Ended; i++ {
		time.Sleep(50 * time.Millisecond)
		if err := luna.Call("agent.output", proto.OutputParams{ID: s.ID, From: o.From}, &o); err != nil {
			t.Fatal(err)
		}
	}
	if !o.Ended || o.Text != "second" {
		t.Fatalf("the second command's output %+v", o)
	}
	// It is saved as it was: with no shell, and its background.
	sp := savedPaneDir(t, d, s.ID)
	if !sp.Background {
		t.Error("the pane is saved without its background")
	}
}

func savedPaneDir(t *testing.T, d *Daemon, id string) savedPane {
	t.Helper()
	d.saveState()
	data, err := os.ReadFile(d.statePath())
	if err != nil {
		t.Fatal(err)
	}
	var f savedState
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.Sessions {
		if s.ID == id {
			return s.Panes[0]
		}
	}
	t.Fatalf("no session %s saved", id)
	return savedPane{}
}

// The store of a bot's app gives the other hosts of the app an answer,
// its lines, and the host that took the event.
func TestFeishuShared(t *testing.T) {
	d := &Daemon{}
	luna := &botLuna{d: d}
	if _, err := luna.Kept("om_a"); err == nil {
		t.Fatal("a message before a keep")
	}
	if err := luna.Call("feishu.keep", proto.FeishuKeep{Event: "om_a", Chat: "c1", Text: "> hi\nkero-heard linux 1"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := luna.Kept("om_a"); err != nil || derefStr(got.ChatId) != "c1" {
		t.Fatalf("kept %+v, %v", got, err)
	}
	var kept proto.FeishuKept
	if err := luna.Call("feishu.kept", nil, &kept); err != nil || len(kept.Turns) != 1 || kept.Turns[0].Event != "om_a" {
		t.Fatalf("kept %+v, %v", kept, err)
	}
	var took proto.FeishuTaken
	if err := luna.Call("feishu.take", proto.FeishuTake{Event: "om_a", Host: "linux"}, &took); err != nil || took.Host != "linux" {
		t.Fatalf("first take %+v, %v", took, err)
	}
	if err := luna.Call("feishu.take", proto.FeishuTake{Event: "om_a", Host: "mac"}, &took); err != nil || took.Host != "linux" {
		t.Fatalf("second take %+v, %v", took, err)
	}
	if err := luna.Call("feishu.take", proto.FeishuTake{Event: "om_b", Host: ""}, &took); err != nil || took.Host != "" {
		t.Fatalf("a take by none %+v, %v", took, err)
	}
}

// A chat's command names the conversation it runs, which a chat window
// shows.
func TestConversationOf(t *testing.T) {
	as, ok := conversationOf([]string{"/Users/x/.local/bin/claude", "-p", "--output-format", "text", "--session-id", "abc"})
	if !ok || as.Agent != "claude" || as.ID != "abc" {
		t.Errorf("%+v %v", as, ok)
	}
	if as, ok := conversationOf([]string{"claude", "-p", "--resume", "def"}); !ok || as.ID != "def" {
		t.Errorf("%+v %v", as, ok)
	}
	if _, ok := conversationOf([]string{"codex", "exec"}); ok {
		t.Error("codex took a conversation")
	}
}

// A chat's command names the conversation it runs, which its session's
// pane tells the window.
func TestConversationOfCommands(t *testing.T) {
	for _, c := range []struct {
		cmd   []string
		agent string
		id    string
	}{
		{[]string{"opencode", "run", "--session", "ses_1"}, "opencode", "ses_1"},
		{[]string{"claude", "-p", "--session-id", "u1"}, "claude", "u1"},
		{[]string{"codex", "exec", "resume", "th-1"}, "codex", "th-1"},
	} {
		as, ok := conversationOf(c.cmd)
		if !ok || as.Agent != c.agent || as.ID != c.id {
			t.Errorf("%q runs %+v, %v", c.cmd, as, ok)
		}
	}
	if _, ok := conversationOf([]string{"sh", "-c", "true"}); ok {
		t.Error("sh names no conversation")
	}
}
