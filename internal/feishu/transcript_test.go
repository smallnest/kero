package feishu

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"kero/internal/proto"
)

// claudeLines is a Claude Code transcript: a prompt, a tool call and its
// result, then the turn's answer.
var claudeLines = []string{
	`{"type":"user","message":{"role":"user","content":"Fix the build"}}`,
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will look."},{"type":"tool_use","name":"Bash","input":{"command":"go build"}}]}}`,
	`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"The build passes."}]}}`,
}

func TestClaudeTranscript(t *testing.T) {
	l := claudeTranscript([]byte(strings.Join(claudeLines, "\n") + "\n"))
	var kinds []string
	for _, x := range l {
		kinds = append(kinds, x.Kind)
	}
	want := []string{"note", "agent", "tool", "tool", "agent"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds %v, want %v: %+v", kinds, want, l)
	}
	call := l[2]
	if call.Title != "go build" || !strings.Contains(call.Detail, `"command":"go build"`) {
		t.Errorf("the tool's call is %+v", call)
	}
	last := l[len(l)-1]
	if !last.Answer || last.Text != "The build passes." {
		t.Errorf("the turn's answer is %+v", last)
	}
	if l[0].Text != "Fix the build" {
		t.Errorf("the prompt is %q", l[0].Text)
	}
	if l[3].Text != "Result" || l[3].Detail != "ok" {
		t.Errorf("the tool's result is %q", l[3].Text)
	}
	if claudeTranscript([]byte("not json\n{}\n")) != nil {
		t.Errorf("a file that is no transcript has lines")
	}
}

func TestAnswersTranscript(t *testing.T) {
	data := `{"conversation":"c1","answer":"Hi"}
{"conversation":"c1","last_message":"Bye"}
`
	l := answersTranscript([]byte(data))
	if len(l) != 2 || !l[0].Answer || l[0].Text != "Hi" || l[1].Answer || l[1].Text != "Bye" {
		t.Fatalf("answers %+v", l)
	}
	if answersTranscript([]byte(`{"answer":"Hi"}`)) != nil {
		t.Errorf("an answer without a conversation is a transcript")
	}
}

func TestNewestTranscript(t *testing.T) {
	root := t.TempDir()
	dir := encodeDir("/Users/me/src/app")
	if dir != "-Users-me-src-app" {
		t.Fatalf("encoded %q", dir)
	}
	os.MkdirAll(filepath.Join(root, dir), 0o700)
	old := filepath.Join(root, dir, "old.jsonl")
	cur := filepath.Join(root, dir, "cur.jsonl")
	os.WriteFile(old, []byte("{}\n"), 0o600)
	os.WriteFile(cur, []byte("{}\n"), 0o600)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if got := newestTranscript(root, dir); got != cur {
		t.Errorf("newest is %q, want %q", got, cur)
	}
	if got := newestTranscript(root, "-nowhere"); got != "" {
		t.Errorf("newest of no directory is %q", got)
	}
}

func TestWindowEvent(t *testing.T) {
	b, luna, _ := newBot(t)
	b.hostname = ""
	m := textMsg("oc_1", "ou_me", feedRef+" oc_1\nFix it")
	// The bot hears its own prompt back, with its ID.
	heard := *m.Event.Message
	heard.MessageId = str("om_feed")
	luna.msgs = append(luna.msgs, &heard)
	luna.took = map[string]string{}
	if leave, ok := b.windowEvent(&heard); !ok || leave {
		t.Errorf("the window's event is leave %v ok %v", leave, ok)
	}
	// Another host took the event first: this one leaves it.
	heard.Content = str(feedRef + " om_feed\nFix it")
	luna.took["om_feed"] = "other"
	if luna.took["om_feed"] != "other" || b.host() != "mac" {
		t.Fatalf("took %v host %q", luna.took, b.host())
	}
	if leave, ok := b.windowEvent(&heard); !ok || !leave {
		t.Errorf("a taken event is leave %v ok %v", leave, ok)
	}
}

func TestFeed(t *testing.T) {
	b, luna, msgs := newBot(t)
	msgs.send = true
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", encodeDir("/tmp/api"))
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "conv1.jsonl"), []byte(strings.Join(claudeLines, "\n")+"\n"), 0o600)
	b.mu.Lock()
	b.chatLocked("oc_1").agent = "claude"
	b.mu.Unlock()
	luna.sessions[0].Panes[0].Agent = "claude"
	b.bind("oc_1", luna.sessions[0])

	f, err := b.Feed(proto.FeishuFeedParams{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Chat != "oc_1" || f.Pane != "p1" || f.Agent != "claude" || len(f.Lines) != 5 {
		t.Fatalf("feed %+v", f)
	}
	if len(f.Lines) < 1 || !f.Lines[len(f.Lines)-1].Answer {
		t.Errorf("the feed's last line is not the turn's answer")
	}
	// The session's window is the chat's.
	if b.pinned("s1") != "oc_1" {
		t.Errorf("the window is pinned to %q", b.pinned("s1"))
	}

	// The window's prompt goes to the chat's person, as their message,
	// which the bot then hears and answers.
	b.chat("oc_1").peer = "ou_me"
	sent, err := b.Sends("oc_1", "Run the tests")

	if err != nil || sent.Message != "om_sent_1" {
		t.Fatalf("send: %+v %v", sent, err)
	}
	// The prompt, and the card that follows it.
	if len(msgs.targets) == 0 || msgs.targets[0] != "open_id ou_me" || msgs.sent[0] != "Run the tests" {
		t.Errorf("prompt %+v %+v", msgs.targets, msgs.sent)
	}

}

// A turn that a chat ran without a terminal names its conversation
// by its ID: the window shows that file, not the newest of a directory.
func TestTranscriptByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "-tmp-proj")
	os.MkdirAll(dir, 0o700)
	for name, text := range map[string]string{
		"chat.jsonl":   `{"type":"user","message":{"content":"hi"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"From the chat."}]}}`,
		"newest.jsonl": `{"type":"user","message":{"content":"hi"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"From the terminal."}]}}`,
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600)
	}
	newest := filepath.Join(dir, "newest.jsonl")
	os.Chtimes(newest, time.Now(), time.Now().Add(time.Minute))
	l := (&Bot{}).lines(&proto.Session{Pane: proto.Pane{Dir: "/tmp/proj"}}, proto.FeishuFeed{Agent: "claude", Conversation: "chat", Chat: "oc_1"})
	if len(l) == 0 || l[len(l)-1].Text != "From the chat." {
		t.Errorf("the window shows %+v", l)
	}
}

// An answer's card reads the answer as Markdown, as the agent wrote it,
// and the commands' notes too: any answer is a card when cards are on.
func TestReplyCard(t *testing.T) {
	b, _, msgs := newBot(t)
	b.reply("c1", "## Fixed\n\nUse `kero`:\n\n- one\n- two")
	if len(msgs.cards) != 1 || len(msgs.sent) != 0 {
		t.Fatalf("cards %d, messages %d", len(msgs.cards), len(msgs.sent))
	}
	c := msgs.lastCard(t)
	if title := c["header"].(map[string]any)["title"].(map[string]any)["content"]; title != "Fixed" {
		t.Errorf("the title is %q", title)
	}
	text := cardText(c)
	if !strings.Contains(text, "## Fixed") || strings.Contains(text, "```") {
		t.Errorf("the answer is %q", text)
	}
	b.reply("c1", "/new starts one.")
	if !strings.Contains(last(msgs), "/new starts one.") {
		t.Errorf("a command's note is %q", last(msgs))
	}
}

// A turn of a chat's is kept in the conversation its window shows, which
// an agent's own transcript does not give Kero for every agent.
func TestKeptTurn(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	b, _, _ := newBot(t)
	b.keepTurn("opencode", "ses_1", "1+1=?", "2")
	b.keepTurn("opencode", "ses_1", "and two more?", "4")
	// The file is the conversation as the turn's command names it.
	file := transcriptFile(opencodeSession("", "ses_1"), "opencode", "")
	if file == "" {
		t.Fatal("the conversation has no file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	l := transcript("opencode", data)
	var kinds, texts []string
	for _, x := range l {
		kinds, texts = append(kinds, x.Kind), append(texts, x.Text)
	}
	if !slices.Equal(kinds, []string{"user", "agent", "user", "agent"}) ||
		!slices.Equal(texts, []string{"1+1=?", "2", "and two more?", "4"}) {
		t.Fatalf("the lines %+v", l)
	}
	// A conversation no turn was kept of has no file.
	if got := transcriptFile(opencodeSession("", "ses_2"), "opencode", ""); got != "" {
		t.Errorf("another conversation has %q", got)
	}
}

// A chat window shows the turns Kero kept, and the agent the chat runs,
// which a background session's pane has no terminal to tell.
func TestFeedShowsKeptTurns(t *testing.T) {
	b, luna, _ := newBot(t)
	b.chats["c1"] = &chat{id: "s1", agent: "opencode"}
	luna.sessions[0].Panes[0].AgentSessionID = "ses_1"
	b.keepTurn("opencode", "ses_1", "1+1=?", "2")
	f, err := b.Feed(proto.FeishuFeedParams{Session: "s1", Chat: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Agent != "opencode" {
		t.Errorf("the agent is %q", f.Agent)
	}
	if len(f.Lines) != 2 || f.Lines[0].Text != "1+1=?" || f.Lines[1].Text != "2" {
		t.Fatalf("the lines are %+v", f.Lines)
	}
}
