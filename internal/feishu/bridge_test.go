package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"

	"kero/internal/proto"
	"kero/internal/settings"
)

// fakeLuna is kerod: sessions, panes, and what the bot sends.
type fakeLuna struct {
	host     string   // a host.info's hostname
	order    []string // the methods that act on a session
	sessions []proto.Session
	sent     []proto.SendParams
	created  []proto.CreateParams
	screen   string
	waits    int
	waitEnd  proto.Status // what the wait ends with, working while there are waits left
	closed   []string
	busy     bool     // the pane keeps working
	files    []string // what each agent.output gives: "2" then ""
	ran      []proto.BackgroundParams
	// script is what a pane shows, and its status, each time it is read:
	// a turn of the agent whose status lags behind its screen, as
	// Claude Code's for a moment.
	script []struct {
		screen string
		status proto.Status
	}
	reads    []proto.ReadParams     // what was read
	msgs     []*larkim.EventMessage // the messages sent or heard, by ID
	kept     map[string]string      // the responses kerod keeps, by their events
	keeps    []string               // the events kerod keeps a response of
	took     map[string]string      // the host that took an event, by its message
	keptChat map[string]string      // the chat of a response kerod keeps, by its event
	byID     map[string]*string     // a sent message's chat, whose message Feishu gives by ID alone
	answers  []string               // the answer directories a pane was given
	onRead   func(string)           // what the first read does, as an agent's hook waking
	calls    map[string]func()      // what a method does as it is called
	hold     bool                   // the screen stays as set, not the script's last
}

func (f *fakeLuna) State() (proto.State, error) {

	out := proto.State{}
	for _, s := range f.sessions {
		// As kerod does: the session's fields are its lead pane's,
		// which the state gives anew.
		s.Pane = s.Panes[0]
		out.Sessions = append(out.Sessions, s)
	}
	return out, nil
}

func (f *fakeLuna) Call(method string, params, out any) error {
	b, _ := json.Marshal(params)
	// Each method's params, by name: SendParams' ID is a pane's, and
	// WaitParams' too, which a struct of embedded types would not tell.
	var p struct {
		ID, Data, Project, Dir, Text, Parent string
		Event, Host, Chat                    string
		Command                              []string
		Scrollback                           bool
		Lines                                int
		Status                               []proto.Status
		Exit                                 bool
		Timeout                              int64
		Out                                  string
		From                                 int64
		Pane                                 string
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	send := proto.SendParams{ID: p.ID, Data: p.Data}
	id := proto.IDParams{ID: p.ID}
	create := proto.CreateParams{Project: p.Project, Dir: p.Dir, Command: p.Command}
	decode := func(v any) { x, _ := json.Marshal(v); json.Unmarshal(x, out) }
	switch method {
	case "state":
		st, _ := f.State()
		decode(st)
	case "pane":
		var p proto.Pane
		if len(f.sessions) > 0 {
			p = f.sessions[0].Panes[0]
		}
		decode(p)
	case "send":
		if f.calls["send"] != nil {
			f.calls["send"]()
		}
		f.sent = append(f.sent, send)
	case "read":
		if f.calls["read"] != nil {
			f.calls["read"]()
		}
		text := f.screen
		if f.onRead != nil {
			f.onRead(id.ID)
		}
		if len(f.script) > 0 {
			text = f.script[0].screen
			f.script = f.script[1:]
			if !f.hold {
				f.screen = text
			}
		}
		decode(proto.ReadResult{Text: text})
	case "feishu.reply":
		// A message the bot sent, and then its answers, as kerod keeps
		// them for Feishu.
		f.reads = append(f.reads, proto.ReadParams{ID: p.ID})
		if out != nil {
			decode("om_x")
		}
	case "host.info":
		host := firstNotEmpty(f.host, "mac")
		decode(proto.HostInfo{Hostname: host})
	case "select":
		f.order = append(f.order, "select")
	case "wait":
		f.waits++
		if f.busy {
			f.sessions[0].Panes[0].Status = proto.Working
		} else if f.working() {
			// A turn seen working: the pane stays so until its screen
			// says the turn is over.
			f.sessions[0].Panes[0].Status = proto.Working
		} else {
			f.sessions[0].Panes[0].Status = proto.Idle
		}
		decode(f.sessions[0].Panes[0])
		st := f.waitEnd
		if f.waits > 1 && f.waitEnd == proto.Working {
			st = proto.Idle
		}
		f.sessions[0].Panes[0].Status = st
		decode(f.sessions[0].Panes[0])
	case "agent.background":
		f.ran = append(f.ran, proto.BackgroundParams{ID: p.ID, Out: p.Out, Dir: p.Dir, Command: p.Command})
		if out != nil {
			decode(true)
		}
	case "pane.answer":
		f.answers = append(f.answers, p.Dir)
		if out != nil {
			decode(proto.Pane{ID: p.Pane, FeishuAnswer: p.Dir})
		}
	case "agent.output":
		// What the command printed, then its end.
		text := f.files[0]
		f.files = f.files[1:]
		res := proto.Output{Text: text, From: int64(len(text)), Ended: text == ""}
		decode(res)
	case "session.hasBackground":
		decode(false)
	case "session.setBackground":
		if out != nil {
			decode(true)
		}
	case "create":
		f.created = append(f.created, create)
		s := proto.Session{ID: "s-new", Name: "review"}
		s.Panes = []proto.Pane{{ID: "p-new", Status: proto.Idle}}
		s.Pane = s.Panes[0]
		f.sessions = append(f.sessions, s)
		if r, ok := out.(*proto.Session); ok {
			*r = s // a value beside the pointer: kerod's client fills both
		}
	case "rename":
		if out != nil {
			decode(true)
		}
	case "close":
		f.closed = append(f.closed, id.ID)
		if out != nil {
			decode(true)
		}
	case "feishu.feed":
		if r, ok := out.(*proto.FeishuFeed); ok {
			r.ID, r.Name = "s1", "api"
			for i := 0; i < 5; i++ {
				r.Lines = append(r.Lines, proto.FeishuLine{Kind: "agent", Text: "line"})
			}
		}
	case "feishu.message":
		// A message Feishu has: the one f.msgs has, which the hosts
		// hear.
		if r, ok := out.(*proto.FeishuMessage); ok {
			if m, err := f.Kept(p.ID); err == nil {
				// As kerod's, which Feishu's JSON fills.
				x, _ := json.Marshal(m)
				json.Unmarshal(x, r)
			}
		}
	case "feishu.keep":
		// A turn's response, which every host answers.
		f.kept[p.Event] = p.Text
		f.keptChat[p.Event] = p.Chat
		f.keeps = append(f.keeps, p.Event)
		if out != nil {
			decode(true)
		}
	case "feishu.kept":
		// Kerod keeps a turn's response.
		out := &proto.FeishuKept{Turns: []proto.FeishuTurn{}}
		for i := len(f.keeps) - 1; i >= 0; i-- {
			if ev := f.keeps[i]; f.kept[ev] != "" {
				out.Turns = append(out.Turns, proto.FeishuTurn{Event: ev, Text: f.kept[ev]})
				break
			}
		}
		decode(out)
	case "feishu.take":
		// The host that took an event: the one kerod keeps, or the one
		// that asks, which it keeps then.
		taken := f.took[p.Event]
		if taken == "" {
			taken = p.Host
			f.took[p.Event] = taken
		}
		decode(proto.FeishuTaken{Host: taken})
	}
	return nil
}

// working tells a turn seen working, whose screen says the turn is over.
func (f *fakeLuna) working() bool {
	if len(f.script) == 0 {
		return false
	}
	return f.script[0].status == proto.Working
}

// Kept is a message Feishu has: the one f.msgs has, which the hosts
// hear.
func (f *fakeLuna) Kept(id string) (proto.FeishuMessage, error) {
	for _, m := range f.msgs {
		if m != nil && deref(m.MessageId) == id {
			c := *m
			if chat := f.byID[id]; chat != nil {
				c = proto.FeishuMessage{ChatId: chat}
			}
			return c, nil
		}
	}
	return proto.FeishuMessage{}, errors.New("Feishu has no such message")
}

type fakeMessages struct {
	sent    []string
	cards   []string // each card's JSON, sent or updated
	replies []string // each reply to a message
	reacts  []string
	fail    bool      // sending a card fails, as an app without the card
	keep    *fakeLuna // what kerod keeps of what is sent
	targets []string  // each message's receive ID, "kind id"
}

func (m *fakeMessages) Send(kind, id, text string) (string, error) {
	m.sent = append(m.sent, text)
	m.targets = append(m.targets, kind+" "+id)
	// What kerod keeps of what is sent, which another host hears: Feishu,
	// as the message that every host finds.
	id = fmt.Sprintf("om_sent_%d", len(m.sent))
	m.keep.msgs = append(m.keep.msgs, &larkim.EventMessage{MessageId: str(id), ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("text"), Content: str(text)})
	m.keep.kept[id] = text
	m.keep.byID[id] = str("c1")
	return id, nil
}

func (m *fakeMessages) SendCard(kind, id, cardJSON string) (string, error) {
	if m.fail {
		return "", fmt.Errorf("no card")
	}
	m.cards = append(m.cards, cardJSON)
	return fmt.Sprintf("om_card_%d", len(m.cards)), nil
}

func (m *fakeMessages) UpdateCard(id, cardJSON string) error {
	m.cards = append(m.cards, cardJSON)
	return nil
}

func (m *fakeMessages) SendReply(id, text string) (string, error) {
	m.sent = append(m.sent, text)
	m.replies = append(m.replies, text)
	return m.keptReply(id, text), nil
}

// keptReply is a reply Feishu has: which every host hears, as the
// message of its ID.
func (m *fakeMessages) keptReply(parent, text string) string {
	if m.keep == nil {
		return fmt.Sprintf("om_reply_%d", len(m.replies))
	}
	id := fmt.Sprintf("om_reply_%d", len(m.replies))
	m.keep.msgs = append(m.keep.msgs, &larkim.EventMessage{MessageId: str(id), ParentId: str(parent), ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("text"), Content: str(text)})
	m.keep.kept[id] = text
	m.keep.byID[id] = str("c1")
	return id
}

func (m *fakeMessages) Get(id string) (feishuMessage, error) {
	return feishuMessage{MessageId: str(id), ChatId: str("c1")}, nil
}

func (m *fakeMessages) React(id, emoji string) error {
	m.reacts = append(m.reacts, id+":"+emoji)
	return nil
}

func (m *fakeMessages) Download(key string, image bool) ([]byte, error) {
	return []byte("\x89PNG\r\n\x1a\n0000"), nil
}

// lastCard is the last card shown, as a map.
func (m *fakeMessages) lastCard(t *testing.T) map[string]any {
	t.Helper()
	if len(m.cards) == 0 {
		t.Fatal("no card")
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(m.cards[len(m.cards)-1]), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// cardText is a card's text.
func cardText(c map[string]any) string {
	body, _ := c["body"].(map[string]any)
	elems, _ := body["elements"].([]any)
	text := ""
	for _, e := range elems {
		m, _ := e.(map[string]any)
		// The state's line comes first, its text after it.
		if m["tag"] == "markdown" || m["tag"] == "div" {
			s, _ := m["content"].(string)
			if t, ok := m["text"].(map[string]any); ok {
				s, _ = t["content"].(string)
			}
			text += s + "\n"
		}
	}
	return text
}

func newBot(t *testing.T) (*Bot, *fakeLuna, *fakeMessages) {
	t.Helper()
	kerod = nil
	kerod = nil
	t.Cleanup(func() { kerod = nil })
	t.Setenv("KERO_HOME", t.TempDir())
	t.Setenv("KERO_CONFIG", filepath.Join(t.TempDir(), "config"))
	settings.Reload()
	defer settings.Reload()
	luna := &fakeLuna{sessions: []proto.Session{session()}}
	msgs := &fakeMessages{}
	luna.calls = map[string]func(){}
	luna.kept = map[string]string{}
	luna.took = map[string]string{}
	luna.byID = map[string]*string{}
	luna.keptChat = map[string]string{}
	luna.msgs = append(luna.msgs, &larkim.EventMessage{MessageId: str("om_user"), ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("text"), Content: jsonText("hi")})
	b := New(Config{
		Settings: settings.Feishu{App: "cli_a", Allow: []string{"*"}, Timeout: int64(settings.FeishuDefaultTimeout), Agent: "default"},
		Secret:   settings.FeishuSecret{AppID: "cli_a", AppSecret: "x", Me: "ou_me"},
		Luna:     luna,
		Log:      t.Logf,
	})
	b.load()
	msgs.keep = luna
	kerod = luna
	b.msgs = msgs
	b.files = msgs
	b.sync = true
	return b, luna, msgs
}

func session() proto.Session {
	s := proto.Session{ID: "s1", Name: "api"}
	s.Panes = []proto.Pane{{ID: "p1", Status: proto.Idle, Dir: "/tmp/api"}}
	s.Pane = s.Panes[0]
	return s
}

func str(s string) *string { return &s }

// nextEvent numbers the events, which Feishu never repeats.
var nextEvent int

func textMsg(chat, from, text string) *larkim.P2MessageReceiveV1 {
	content, _ := json.Marshal(map[string]string{"text": text})
	nextEvent++
	return &larkim.P2MessageReceiveV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{AppID: "cli_a", EventID: fmt.Sprintf("ev_%d_%d", nextEvent, len(text))}},
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: str(from)}},
			Message: &larkim.EventMessage{ChatId: str(chat), ChatType: str("p2p"), MessageType: str("text"), Content: str(string(content))},
		},
	}
}

// textMsgFrom is a text message with its ID, as a turn names it.
func textMsgFrom(chat, id, text string) *larkim.P2MessageReceiveV1 {
	e := textMsg(chat, "ou_me", text)
	e.Event.Message.MessageId = str(id)
	return e
}

func last(msgs *fakeMessages) string {
	if len(msgs.sent) == 0 {
		return ""
	}
	return msgs.sent[len(msgs.sent)-1]
}

// jsonText is a message's content as Feishu sends a text message's: JSON.
func jsonText(text string) *string {
	b, _ := json.Marshal(map[string]string{"text": text})
	return str(string(b))
}

// /new starts a session, and a prompt goes to its pane, then Enter, and
// the reply holds what the turn printed.
func TestPrompt(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.onMessage(nil, textMsg("c1", "ou_me", "/new"))
	if len(luna.created) != 1 || !strings.Contains(last(msgs), `"review" is ready`) {
		t.Fatalf("created %+v, replied %q", luna.created, last(msgs))
	}
	if got := b.chat("c1").id; got != "s-new" {
		t.Fatalf("the chat is bound to %q", got)
	}

	luna.screen = "old\n$ "
	luna.waitEnd = proto.Working
	defer shortWaits()()
	b.onMessage(nil, textMsg("c1", "ou_me", "fix the tests"))
	sent := luna.sent[len(luna.sent)-2:]
	if sent[0].Data != "fix the tests" || sent[1].Data != "\r" || sent[0].ID != "p-new" {
		t.Fatalf("sent %+v", luna.sent)
	}
	if luna.waits < 2 {
		t.Errorf("the bot waits %d times, as the agent works and then stops", luna.waits)
	}
	luna.screen = "old\n$ \n● Fixed 2 tests.\n"
	b.onMessage(nil, textMsg("c1", "ou_me", "/ls"))
	if len(luna.sent) == 0 {
		t.Fatal("no prompt went")
	}
}

// The reply is what the turn added: not the old screen, and the last
// rows only.
func TestReplyTail(t *testing.T) {
	start := "a\nb\nc\n"
	screen := start + strings.Repeat("x\n", 40) + "done\n"
	got := since(screen, rows(start), 3)
	if want := "x\nx\ndone"; got != want {
		t.Errorf("tail %q, want %q", got, want)
	}
	if got := since("nothing new\n", rows("a\nb\nc\n"), 5); got != "" {
		t.Errorf("nothing new gives %q", got)
	}
}

// A message from someone not allowed is refused, and nothing runs.
func TestNotAllowed(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.cfg.Allow = nil
	b.onMessage(nil, textMsg("c1", "ou_stranger", "/new"))
	if len(luna.created) != 0 || len(msgs.sent) != 1 || !strings.Contains(last(msgs), "not allowed") {
		t.Fatalf("created %+v, replied %q", luna.created, msgs.sent)
	}
	// Who ran setup may, and another event of the same ID is ignored.
	dup := textMsg("c2", "ou_me", "/new")
	b.onMessage(nil, dup)
	b.onMessage(nil, dup)
	if len(luna.created) != 1 {
		t.Errorf("created %d sessions, want 1: a redelivered event runs twice", len(luna.created))
	}
}

// In a group, only a message that @-mentions the bot, or answers it,
// acts.
func TestGroupMentions(t *testing.T) {
	b, luna, msgs := newBot(t)
	m := textMsg("g1", "ou_me", "/new")
	*m.Event.Message.ChatType = "group"
	b.onMessage(nil, m)
	if len(luna.created) != 0 {
		t.Fatal("a group message without a mention acted")
	}
	m = textMsg("g1", "ou_me", "/new")
	*m.Event.Message.ChatType = "group"
	m.Event.Message.Mentions = []*larkim.MentionEvent{{Key: str("@_user_1"), MentionedType: str("bot")}}
	b.onMessage(nil, m)
	if len(luna.created) != 1 || last(msgs) == "" {
		t.Fatalf("a mention didn't act: %+v", luna.created)
	}
	if got := b.chat(chatKey("g1", "")).id; got != "s-new" {
		t.Errorf("the group is bound to %q", got)
	}
}

// Commands: /ls marks the chat's session, /use binds by name, /status
// tells what the pane does, /close ends it once idle.
func TestCommands(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.onMessage(nil, textMsg("c1", "ou_me", "/use api"))
	if !strings.Contains(last(msgs), "This chat is api") {
		t.Fatalf("/use replied %q", last(msgs))
	}
	b.onMessage(nil, textMsg("c1", "ou_me", "/ls"))
	if !strings.Contains(last(msgs), "→ ○ api") {
		t.Errorf("/ls lists %q", last(msgs))
	}
	b.onMessage(nil, textMsg("c1", "ou_me", "/status"))
	if !strings.Contains(last(msgs), "idle") || !strings.Contains(last(msgs), "/tmp/api") {
		t.Errorf("/status shows %q", last(msgs))
	}
	luna.sessions[0].Panes[0].Status = proto.Working
	b.onMessage(nil, textMsg("c1", "ou_me", "/close"))
	if len(luna.closed) != 0 {
		t.Error("/close ended a session working")
	}
	luna.sessions[0].Panes[0].Status = proto.Idle
	b.onMessage(nil, textMsg("c1", "ou_me", "/close"))
	if !slices.Equal(luna.closed, []string{"s1"}) || b.chat("c1").id != "" {
		t.Errorf("closed %v, bound %q", luna.closed, b.chat("c1").id)
	}
	b.onMessage(nil, textMsg("c1", "ou_me", "/nosuch"))
	if !strings.Contains(last(msgs), "/help") {
		t.Errorf("an unknown command replies %q", last(msgs))
	}
}

// A chat keeps its session as the bot restarts, and loses the binding
// when the session is gone.
func TestBindsPersist(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.onMessage(nil, textMsg("c1", "ou_me", "/use api"))
	again := New(Config{Settings: b.cfg, Secret: settings.FeishuSecret{AppID: "cli_a", AppSecret: "x", Me: "ou_me"}, Luna: luna, Log: t.Logf})
	again.msgs = msgs
	again.sync = true
	again.load()
	if got := again.chat("c1").id; got != "s1" {
		t.Fatalf("after a restart the chat is bound to %q", got)
	}
	luna.sessions = nil
	again.onMessage(nil, textMsg("c1", "ou_me", "hello"))
	if !strings.Contains(last(msgs), "gone") || again.chat("c1").id != "" {
		t.Fatalf("a prompt to a gone session replied %q, bound %q", last(msgs), again.chat("c1").id)
	}
}

// shortWaits makes the waits the driving relies on short, for the tests.
func shortWaits() func() {
	old := waits
	waits.start, waits.settle, waits.quiet, waits.grace, waits.poll = 60*time.Millisecond, 60*time.Millisecond, 150*time.Millisecond, 250*time.Millisecond, 60*time.Millisecond
	return func() { waits = old }
}

// A turn whose agent's screen, drawn as a prompt again, tells nothing of
// its answer is answered from the passage its Stop hook wrote.
func TestPromptAnsweredWhileIdle(t *testing.T) {
	old := stuckAfter
	stuckAfter = 150 * time.Millisecond
	defer func() { stuckAfter = old }()
	defer shortWaits()()
	b, luna, msgs := newBot(t)
	b.chats["c1"] = &chat{id: "s1"}
	b.leave("c1")
	luna.hold = true
	// The turn: the agent takes the prompt, and keeps a status it had
	// before it, working; its answer is written as the pane shows it.
	luna.screen = "❯ 1+1=?\n\n» 2\n\n──────────────────────────────────────\n❯ "
	luna.script = []struct {
		screen string
		status proto.Status
	}{{"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}}
	b.onMessage(nil, textMsg("c1", "ou_me", "1+1=?"))
	if got := last(msgs); !strings.Contains(got, "2") || !strings.Contains(got, "terminal") {
		t.Fatalf("replied %q", got)
	}
}

// A status the daemon keeps across a restart, as one a program reported,
// does not hold a prompt forever.
func TestPromptStuckWorking(t *testing.T) {
	old := stuckAfter
	stuckAfter = 100 * time.Millisecond
	defer func() { stuckAfter = old }()
	defer shortWaits()()
	b, luna, msgs := newBot(t)
	b.chats["c1"] = &chat{id: "s1"}
	b.leave("c1")
	luna.busy = true
	luna.script = []struct {
		screen string
		status proto.Status
	}{{"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}, {"old\n", proto.Working}}
	b.onMessage(nil, textMsg("c1", "ou_me", "fix it"))
	if got := last(msgs); !strings.Contains(got, "cannot tell") && !strings.Contains(got, "text") {
		t.Fatalf("replied %q", got)
	}
}

// In the command mode, a prompt runs the agent's own command in the
// background, and its text is the reply.
func TestCommandMode(t *testing.T) {
	defer shortWaits()()
	b, luna, msgs := newBot(t)
	b.cfg.Run = settings.FeishuRunCommand
	b.cfg.Agent = "claude"
	b.chats["c1"] = &chat{id: "s1"}
	b.leave("c1")
	luna.files = []string{"2", ""}
	b.onMessage(nil, textMsg("c1", "ou_me", "1+1=?"))
	got := last(msgs)
	if got != "2" {
		t.Fatalf("replied %q", got)
	}
	if len(luna.ran) != 1 {
		t.Fatalf("ran %+v", luna.ran)
	}
	cmd := strings.Join(luna.ran[0].Command, " ")
	if !strings.HasPrefix(cmd, "claude -p --output-format text --session-id ") || !strings.HasSuffix(cmd, " 1+1=?") {
		t.Errorf("ran %q", cmd)
	}
	if luna.ran[0].Dir == "" || !filepath.IsAbs(luna.ran[0].Out) {
		t.Errorf("ran in %q, output %q", luna.ran[0].Dir, luna.ran[0].Out)
	}
}

// Each agent's command continues the chat's conversation.
func TestCommandContinues(t *testing.T) {
	b, _, _ := newBot(t)
	first := b.backgroundCommand("opencode", "sid-1", "", "hi")
	next := b.backgroundCommand("opencode", "sid-2", "ses_9", "again")
	if strings.Join(next, " ") != "opencode run --session ses_9 again" || strings.Join(first, " ") != "opencode run hi" {
		t.Errorf("%q, %q", first, next)
	}
	next = b.backgroundCommand("codex", "sid-2", "th-1", "again")
	if strings.Join(next, " ") != "codex exec resume th-1 --skip-git-repo-check again" {
		t.Errorf("codex %q", next)
	}
	if sid := conversation("opencode", `{"type":"step_start","sessionID":"ses_7"}`, "x"); sid != "ses_7" {
		t.Errorf("opencode continues %q", sid)
	}
}

// splitCommand cuts the commands, and leaves prompts, and // paths.
func TestSplitCommand(t *testing.T) {
	for _, c := range []struct{ in, cmd, rest string }{
		{"/new api pi", "new", "api pi"},
		{"/New", "new", ""},
		{"/help@kero-bot please", "help", "please"},
		{"fix /new", "", "fix /new"},
		{"//server/share", "", "//server/share"},
	} {
		if cmd, rest := splitCommand(c.in); cmd != c.cmd || strings.TrimSpace(rest) != c.rest {
			t.Errorf("%q gives %q %q", c.in, cmd, rest)
		}
	}
}

func (f *fakeLuna) hostname() string { return firstNotEmpty(f.host, "mac") }

// cardMsgOf is a message event, as Feishu sends one: its parent, and
// what it says.
func cardMsgOf(chat, id, parent, content string) *larkim.P2MessageReceiveV1 {
	nextEvent++
	return &larkim.P2MessageReceiveV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{AppID: "cli_a", EventID: fmt.Sprintf("evm_%d_%s", nextEvent, id)}},
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: str("ou_me")}},
			Message: &larkim.EventMessage{MessageId: str(id), ParentId: str(parent), ChatId: str(chat), ChatType: str("p2p"), MessageType: str("text"), Content: str(content)},
		},
	}
}

// replyContent is a reply's text, as Feishu reads it: the text itself.
func replyContent(content string) string {
	var c struct{ Text string }
	json.Unmarshal([]byte(content), &c)
	return c.Text
}

// replyNamed is a reply's content: the event it names, and its text.
func replyNamed(event, text string) string {
	lines := ""
	if !strings.HasPrefix(text, "kero-") {
		lines = hopText(event)
	}
	content, _ := json.Marshal(map[string]string{"text": lines + text})
	return replyContent(string(content))
}

// Two hosts hear every message: linux answers a message that names
// nothing, and mac, the chat's host, answers linux's answer with the
// prompt. For /host mac, linux answers mac's message before it, and
// leaves the prompt that comes with it to mac.
func TestHostHandoff(t *testing.T) {
	defer shortWaits()()
	b, luna, msgs := newBot(t)
	b.cfg.Host = []string{"mac", "linux"}
	luna.host, b.hostname = "linux", "linux"
	// A message Feishu has: what it says, and the message it answers,
	// whose text Feishu gives the other hosts by its ID and chat alone.
	mac := func() { b.hostname, luna.host = "mac", "mac" }
	linux := func() { b.hostname, luna.host = "linux", "linux" }
	// Feishu has the message the first one answers, and the answer mac's
	// prompt replies to: the first host that hears a message takes it.
	luna.msgs = append(luna.msgs, &larkim.EventMessage{MessageId: str("om_user"), ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("text"), Content: jsonText("ask")})
	luna.msgs = append(luna.msgs, &larkim.EventMessage{MessageId: str("om_sent_1"), ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("text"), Content: jsonText("hi")})
	b.chats["c1"] = &chat{id: "s1", agent: "claude", dir: "/tmp"}
	b.sync = true
	// The turn's text, after what the pane had.
	luna.script = append(luna.script, struct {
		screen string
		status proto.Status
	}{screen: "> "}, struct {
		screen string
		status proto.Status
	}{screen: "> hi\nI fixed the tests.\n> "})
	luna.screen = "> hi\nI fixed the tests.\n> "
	// A message Feishu's JSON fills: its text comes from it, not from
	// the struct a test set.
	linux()
	m := &larkim.P2MessageReceiveV1{}
	json.Unmarshal([]byte(`{"event":{"sender":{"sender_id":{"open_id":"ou_me"}},"message":{"message_id":"om_a","parent_id":"om_user","chat_id":"c1","chat_type":"p2p","message_type":"text","content":"{\"text\":\"hi\"}"}}}`), m)
	b.onMessage(nil, m)

	// The message answers one that names nothing, so linux, which answers
	// prompts, answers it, with its screen. Its text comes from Feishu's
	// JSON, not from the struct a test set, which says "not text".
	if n := len(luna.sent); n != 2 || luna.sent[0].Data != "hi" || luna.sent[1].Data == "hi" {
		t.Fatalf("a message's text: %+v", luna.sent)
	}

	// mac answers linux's answer: a prompt in a reply that names the
	// event, and the chat's host, "" until /host names one.
	mac()
	luna.sent = nil
	b.onMessage(nil, cardMsgOf("c1", "om_b", "om_sent_1", "hi"))
	// The prompt, which Enter ends.
	if !slices.Contains(luna.sent, proto.SendParams{ID: "p1", Data: "hi"}) {
		t.Fatalf("mac sent %+v", luna.sent)
	}
	b.chats["c1"].busy = false
	b.mu.Lock()
	b.chatLocked("c1").host = ""
	b.mu.Unlock()

	// /host mac: the chat's host, which mac is, answers the prompt, and the
	// next message answers its answer.
	mac()
	replies := len(msgs.replies)
	b.onMessage(nil, textMsg("c1", "ou_me", "/host mac"))
	if n := len(msgs.replies); n != replies+1 || !strings.Contains(msgs.replies[n-1], "Messages here go to mac.") {
		t.Fatalf("mac sent %+v", luna.sent)
	}
	if b.chat("c1").host != "mac" {
		t.Fatalf("the chat's host is %q", b.chat("c1").host)
	}
	// linux leaves the message, which answers mac's answer.
	linux()
	n := len(luna.sent)
	b.onMessage(nil, cardMsgOf("c1", "om_b2", "om_sent_2", "run it"))
	if len(luna.sent) != n || luna.created != nil {
		t.Fatalf("linux, not mac, ran it: %+v, created %+v", luna.sent, luna.created)
	}

	// The prompt replies to the host's answer, which names mac: mac runs
	// it, and linux leaves it.
	luna.sent = nil
	luna.created = nil
	b.onMessage(nil, cardMsgOf("c1", "om_linux", "om_sent_3", "/new"))
	if len(luna.sent) != 0 || len(luna.created) != 0 {
		t.Fatalf("linux, not the host, acted: %+v, created %+v", luna.sent, luna.created)
	}
	// mac hears a new event for it: one Feishu sends each host alone, whose
	// ID linux did not see.
	mac()
	ev := textMsg("c1", "ou_me", "/new")
	ev.EventV2Base.Header.EventID = "ev_new"
	luna.created = nil
	b.onMessage(nil, ev)
	if len(luna.created) != 1 {
		t.Fatalf("mac created %+v, sent %+v", luna.created, luna.sent)
	}
}

// A card shows a background turn until it ends, green, with its text.
func TestCardTurn(t *testing.T) {
	old := stuckAfter
	stuckAfter = 100 * time.Millisecond
	defer func() { stuckAfter = old }()
	defer shortWaits()()
	b, luna, msgs := newBot(t)
	b.cfg.Run = settings.FeishuRunCommand
	luna.files = []string{`{"type":"assistant","message":{"content":[{"type":"text","text":"Fixed 2 tests."}]}}`, "", ""}
	b.chats["c1"] = &chat{id: "s1", agent: "claude", dir: "/tmp", card: cardMsg{id: "om_old"}}
	b.onMessage(nil, cardMsgOf("c1", "om_t", "om_user", "fix the tests"))
	if len(msgs.cards) < 2 {
		t.Fatalf("cards %d", len(msgs.cards))
	}
	// The card at its end is the last update's.
	lastCardText := func() string { return cardText(msgs.lastCard(t)) }
	if !strings.Contains(lastCardText(), "Fixed 2 tests.") {
		t.Errorf("the card says %q", lastCardText())
	}
	var first map[string]any
	json.Unmarshal([]byte(msgs.cards[0]), &first)
	if first["header"].(map[string]any)["template"] != "grey" {
		t.Errorf("the first card is %v", first["header"])
	}
	if action, _ := json.Marshal(first); !strings.Contains(string(action), `"kero":"open"`) {
		t.Errorf("the card has no button: %s", action)
	}
	// The chat's last card, which the next turn replaces.
	state, _ := json.Marshal(msgs.cards)
	if !strings.Contains(string(state), `Fix the tests`) && !strings.Contains(string(state), `fix the tests`) {
		t.Errorf("cards %s", state)
	}
	if len(msgs.replies) == 0 || !strings.Contains(msgs.replies[len(msgs.replies)-1], "Fixed 2 tests.") {
		t.Errorf("replies %q", msgs.replies)
	}
}

// An image goes to the session by its saved path.
func TestImage(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	b, luna, msgs := newBot(t)
	b.chats["c1"] = &chat{id: "s1"}
	b.leave("c1")
	luna.waitEnd = proto.Working
	b.onMessage(nil, &larkim.P2MessageReceiveV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{AppID: "cli_a", EventID: "ev_img"}},
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: str("ou_me")}},
			Message: &larkim.EventMessage{ChatId: str("c1"), ChatType: str("p2p"), MessageType: str("image"), Content: str(`{"image_key":"img_v3_123"}`)},
		},
	})
	if len(luna.sent) == 0 || !strings.Contains(luna.sent[0].Data, "feishu-files") || !strings.Contains(luna.sent[0].Data, ".png") {
		t.Fatalf("sent %+v, replied %q", luna.sent, last(msgs))
	}
}

// The button of a card opens its session in Kero, its end in the app.
func TestCardButton(t *testing.T) {
	b, luna, _ := newBot(t)
	luna.msgs = []*larkim.EventMessage{{MessageId: str("om_c"), ChatId: str("c1"), MessageType: str("interactive")}}
	b.remember("om_c", record{Chat: "c1", Session: "s1"})
	res, err := b.onCardAction(nil, &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Operator: &callback.Operator{OpenID: "ou_me"},
		Action:   &callback.CallBackAction{Value: map[string]any{"kero": "open"}},
		Context:  &callback.Context{OpenMessageID: "om_c"},
	}})
	if err != nil || res == nil || res.Toast == nil || res.Toast.Type != "success" {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if !slices.Contains(luna.order, "select") {
		t.Fatalf("the click sent %v", luna.order)
	}
	// The record of a card's session stays in kerod's directory.
	b2, _, _ := newBot(t)
	b2.remember("om_c", record{Chat: "c1", Session: "s1"})
	b2.loadNotes()
	if r, ok := b2.note("om_c"); !ok || r.Session != "s1" {
		t.Fatalf("note %+v", r)
	}
}

// A rich text's text and images.
func TestPost(t *testing.T) {
	content := `{"zh_cn":{"title":"Look","content":[[{"tag":"text","text":"see "},{"tag":"img","image_key":"img_1"}]]}}`
	if got := postText(content); !strings.HasPrefix(got, "Look\nsee") {
		t.Errorf("post text %q", got)
	}
	_, images := messageFiles(content)
	if len(images) != 1 || images[0].key != "img_1" {
		t.Errorf("images %+v", images)
	}
}

// hold is a call the messages take a moment to answer, as Feishu's do:
// the bot's work is not all done before it replies.

// writeAnswerLater writes an answer, as a hook that takes a moment does.
func writeAnswerLater(dir, conversation, text string) {
	go func() { writeAnswer(dir, conversation, text) }()
}

// writeAnswer writes an answer as an agent's hook does.
func writeAnswer(dir, conversation, text string) {
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, conversation+".json"), []byte(text), 0o600)
}

// answerFile is an answer an agent's hook writes, as kero answer does:
// the conversation's first.
func answerFile(t *testing.T, dir, conversation, text string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, conversation+".json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The agent's hook writes each turn's answer to the file the prompt gives
// its pane, and the reply is that answer, not the terminal's frames.

// An answer to the agent's question ends the turn: the hook writes its
// answer there, and the reply is that answer.
func TestHookAnswerToQuestion(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.bind("c1", session())
	setup := luna.sessions[0]
	// As kerod does, the session's fields are its lead pane's too.
	setup.Agent, setup.Status, setup.Message = "claude", proto.Waiting, "Which branch?"
	setup.Panes[0] = setup.Pane
	luna.sessions[0] = setup
	defer shortWaits()()
	dir := answerDir("claude", "p1")
	defer os.RemoveAll(dir)
	luna.screen = "Which branch?\n"
	luna.waitEnd = proto.Idle
	luna.onRead = func(id string) {
		if luna.answers != nil {
			// The hook writes once the bot gave the pane the directory.
			writeAnswerLater(dir, "turn2", "On main.\n")
			luna.onRead = nil
		}
	}
	luna.calls["send"] = func() {
		time.Sleep(20 * time.Millisecond)
	}
	// The prompt that asked it is the turn the answer continues.
	b.beginTurn("c1", &turnInfo{start: time.Now(), chat: "c1", text: "Which branch?", event: "om_question"})
	defer func() {
		b.mu.Lock()
		c := b.chatLocked("c1")
		defer b.mu.Unlock()
		if c.running.text != "" {
			t.Errorf("the turn still runs: %+v", c.running)
		}
	}()

	b.onMessage(nil, textMsg("c1", "ou_me", "/answer main"))

	// The bot gives the pane the turn's answer directory, as the turn
	// began, and follows the answer under it.
	for _, d := range luna.answers {
		if d != dir {
			t.Fatalf("answered in %q, not %q", luna.answers, dir)
		}
	}
	sent := luna.sent
	// The chat typed the answer, not drive, and Enter: the pane's hook
	// names the turn's answer.
	if len(sent) != 1 || sent[0].Data != "" || len(luna.answers) != 1 {
		t.Fatalf("sent %+v", sent)
	}
	got := last(msgs)
	if !strings.Contains(got, "On main.") || strings.Contains(got, "Which branch?") && strings.Contains(got, "no final") {
		t.Fatalf("replied %q", got)
	}
	if b.chat("c1").sid != "turn2" {
		t.Errorf("the conversation is %q", b.chat("c1").sid)
	}
}

// A prompt whose agent's Stop hook writes the turn's answer is replied
// with that answer, beside the terminal's rows, and the next message
// continues the hook's conversation.
func TestHookAnswerToPrompt(t *testing.T) {
	b, luna, msgs := newBot(t)
	b.bind("c1", session())
	setup := luna.sessions[0]
	setup.Agent = "claude"
	setup.Panes[0] = setup.Pane
	luna.sessions[0] = setup
	defer shortWaits()()
	dir := answerDir("claude", "p1")
	defer os.RemoveAll(dir)
	luna.screen = "$ "
	// The hook writes as the agent stops, after the turn's rows.
	luna.waitEnd = proto.Idle
	luna.calls["read"] = func() {
		if luna.answers != nil {
			writeAnswer(dir, "turn3", "All tests pass.\n")
		}
	}

	b.onMessage(nil, textMsg("c1", "ou_me", "Run the tests"))
	if len(luna.answers) != 1 || luna.answers[0] != dir {
		t.Fatalf("answered in %q", luna.answers)
	}
	got := last(msgs)
	if !strings.Contains(got, "All tests pass.") {
		t.Fatalf("replied %q", got)
	}
	if b.chat("c1").sid != "turn3" {
		t.Errorf("the conversation is %q", b.chat("c1").sid)
	}
}
