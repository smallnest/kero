package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kero/internal/feishu"
	"kero/internal/proto"
	"kero/internal/settings"
)

// bot is kerod's connection to its Feishu app, while feishu-app names
// one and its secret is kept (kero feishu setup): from then on its turns
// keep kerod running, as a server's do.
type bot struct {
	d      *Daemon
	cancel context.CancelFunc
	app    string
	err    error // why the last connection ended, if it did
}

// feishuBot is the bot, and nil while it connects.
func (d *Daemon) feishuBot() *feishu.Bot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.feishu
}

// startFeishu connects the bot, or stops the last one.
func (d *Daemon) startFeishu() {
	set := settings.Load()
	d.mu.Lock()
	old := d.bot
	d.bot, d.feishu = nil, nil
	d.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	if !settings.FeishuSet(set.Feishu) {
		return
	}
	secret, err := settings.LoadFeishuSecret()
	if err != nil {
		d.log.Printf("feishu: %v", err)
		return
	}
	b := &bot{d: d, app: secret.AppID}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	conn := feishu.New(feishu.Config{
		Settings: *set.Feishu,
		Secret:   secret,
		Luna:     &botLuna{d: d},
		Log:      func(f string, a ...any) { d.log.Printf(f, a...) },
		Env:      d.botEnv(),
	})
	d.mu.Lock()
	d.bot, d.feishu = b, conn
	d.mu.Unlock()
	conn.Chats = func(ctx context.Context) ([]proto.FeishuChat, error) {
		all, err := conn.ChatsList(ctx)
		return all.Chats, err
	}
	go func() {
		d.log.Printf("feishu: connecting app %s", secret.AppID)
		err := conn.Run(ctx)
		d.mu.Lock()
		if d.feishu == conn {
			b.err = err
			d.feishu = nil
		}
		d.mu.Unlock()
		d.log.Printf("feishu: connection to %s ended: %v", secret.AppID, err)
	}()
}

// feishuAct tells a bot that acts, which a client of the app runs: the
// answer it gives is the chat's host's line.
func (d *Daemon) feishuAct(action string, args map[string]string) {
	d.mu.Lock()
	conns := make([]*controlConn, 0, len(d.subs))
	for c := range d.subs {
		conns = append(conns, c)
	}
	d.mu.Unlock()
	for _, c := range conns {
		c.event(proto.Message{Event: "action", Action: action, Args: args})
	}
}

// FeishuStatus tells the bot's app and whether it connects.
func (d *Daemon) feishuStatus() proto.FeishuState {
	st := proto.FeishuState{Configured: settings.FeishuSet(settings.Load().Feishu)}
	_, err := settings.LoadFeishuSecret()
	st.Secret = err == nil
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.bot != nil {
		st.App = d.bot.app
		st.Connected = true
	} else if st.Configured {
		st.Error = "not connected"
	}
	return st
}

// feishuWindow runs the methods of a chat window (feishu.chats,
// feishu.feed, feishu.send).
func (d *Daemon) feishuWindow(method string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		if len(raw) == 0 {
			return nil
		}
		return json.Unmarshal(raw, v)
	}
	var out any
	switch method {
	case "feishu.chats":
		var p proto.FeishuChats
		if err := decode(&p); err != nil {
			return nil, err
		}
		b, err := d.chatBot()
		if err != nil {
			return nil, err
		}
		out, _ = b.ChatsList(context.Background())
	case "feishu.feed":
		var p proto.FeishuFeedParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		b := d.feishuBot()
		if b == nil {
			return nil, errors.New("the Feishu bot is not connected")
		}
		return b.Feed(p)
	case "feishu.send":
		var p proto.FeishuSend
		if err := decode(&p); err != nil {
			return nil, err
		}
		b := d.feishuBot()
		if b == nil {
			return nil, errors.New("the Feishu bot is not connected")
		}
		return b.Sends(p.Chat, p.Text)
	}
	return out, nil
}

// chatBot is the bot a chat window shows, which answers the chat.
func (d *Daemon) chatBot() (*feishu.Bot, error) {
	b := d.feishuBot()
	if b == nil {
		return nil, errors.New("the Feishu bot is not connected")
	}
	return b, nil
}

// botLuna is kerod's API to the bot: what a client's connection gives it.
type botLuna struct{ d *Daemon }

// Kept is a message as Feishu has it: an error for a message Feishu
// has none of.
func (l *botLuna) Kept(id string) (proto.FeishuMessage, error) {
	var m proto.FeishuMessage
	err := l.Call("feishu.message", proto.IDParams{ID: id}, &m)
	return m, err
}

func (l *botLuna) State() (proto.State, error) {
	var st proto.State
	return st, l.Call("state", nil, &st)
}

// Call runs a method as a client's call does: in a connection of its
// own, which hears no events.
func (l *botLuna) Call(method string, params, out any) error {
	// Not a subscriber, and no client of d.clients: only the answer goes
	// to it.
	c := &controlConn{out: make(chan proto.Message, 1), done: make(chan struct{})}
	defer close(c.done)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := l.d.call(c, method, raw)

	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if out == nil || res == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// feishuShared runs the methods a bot of several hosts shares with the
// other hosts of its app: the messages Feishu has, the answers a host
// keeps of the events it answers, and the hosts that took the events.
func (d *Daemon) feishuShared(method string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		if len(raw) == 0 {
			return nil
		}
		return json.Unmarshal(raw, v)
	}
	var out any
	switch method {
	case "feishu.message":
		var p proto.IDParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		m, ok := d.feishuApp.get(p.ID)
		if !ok {
			return nil, fmt.Errorf("no message %s", p.ID)
		}
		out = m
	case "feishu.keep":
		var p proto.FeishuKeep
		if err := decode(&p); err != nil {
			return nil, err
		}
		d.feishuApp.add(p)
		out = true
	case "feishu.kept":
		out = proto.FeishuKept{Turns: d.feishuApp.turns()}
	case "feishu.take":
		var p proto.FeishuTake
		if err := decode(&p); err != nil {
			return nil, err
		}
		out = proto.FeishuTaken{Host: d.feishuApp.take(p.Event, p.Host)}
	}
	return out, nil
}

// sharedFeishu is what a bot keeps of its app for the other hosts:
// the messages it sends, the answers it keeps, and the hosts that took
// the events (multi.go of internal/feishu).
type sharedFeishu struct {
	mu   sync.Mutex
	sent []proto.FeishuMessage // the last messages this daemon sent, by ID
	kept []proto.FeishuKeep    // the last answers, by event
	took map[string]string     // each event's host
}

// sharedCap is how many messages and answers sharedFeishu keeps.
const sharedCap = 64

func (s *sharedFeishu) get(id string) (proto.FeishuMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.sent) - 1; i >= 0; i-- {
		if derefStr(s.sent[i].MessageId) == id {
			return s.sent[i], true
		}
	}
	return proto.FeishuMessage{}, false
}

// add keeps an answer a host gives: Feishu gives the other hosts its ID
// and chat alone, and kerod gives its lines.
func (s *sharedFeishu) add(k proto.FeishuKeep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept = append(s.kept, k)
	if len(s.kept) > sharedCap {
		s.kept = s.kept[len(s.kept)-sharedCap:]
	}
	s.sent = append(s.sent, proto.FeishuMessage{MessageId: strPtr(k.Event), ChatId: strPtr(k.Chat), MessageType: strPtr("text"), Content: strPtr(k.Text)})
	if len(s.sent) > sharedCap {
		s.sent = s.sent[len(s.sent)-sharedCap:]
	}
}

// turns is the answers a host keeps, the last first.
func (s *sharedFeishu) turns() []proto.FeishuTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proto.FeishuTurn, 0, len(s.kept))
	for i := len(s.kept) - 1; i >= 0; i-- {
		k := s.kept[i]
		out = append(out, proto.FeishuTurn{Event: k.Event, Chat: k.Chat, Text: k.Text})
	}
	return out
}

// take is an event's host: the host that took it first, or the host
// that asks, which takes it then; "" for no host.
func (s *sharedFeishu) take(event, host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.took == nil {
		s.took = map[string]string{}
	}
	if h, ok := s.took[event]; ok {
		return h
	}
	if host == "" {
		return ""
	}
	s.took[event] = host
	return host
}

func strPtr(s string) *string { return &s }

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// conversationOf returns the conversation an agent's command runs, as
// the command names it: Claude Code's --session-id, or --resume.
func conversationOf(cmd []string) (proto.AgentSession, bool) {
	if len(cmd) == 0 || !strings.HasPrefix(filepath.Base(cmd[0]), "claude") {
		return proto.AgentSession{}, false
	}
	for i, w := range cmd {
		if (w == "--session-id" || w == "--resume" || w == "--session_id" || w == "-r") && i+1 < len(cmd) && len(cmd[i+1]) > 0 {
			return proto.AgentSession{Agent: "claude", ID: cmd[i+1]}, true
		}
	}
	return proto.AgentSession{}, false
}

// botEnv is the bot's environment, which its commands take: the app's,
// which its settings keep, as the window read it, with the account's
// places on its path; else the app's that connected; else kerod's own.
func (d *Daemon) botEnv() []string {
	var app []string
	host, _ := os.Hostname()
	app, at := settings.AppEnv(host)
	if time.Since(at) > 24*time.Hour {
		// A window of a week ago may run with a token that ended.
		app = nil
	}
	if len(app) == 0 {
		d.mu.Lock()
		app = d.appEnv
		d.mu.Unlock()
	}
	if len(app) > 0 {
		return withoutPath(app)
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "PATH" {
			env = append(env, kv)
		}
	}
	return withoutPath(env)
}

// withoutPath is an environment with the account's places after its own
// on the PATH, as a launchd job's has none of them.
func withoutPath(env []string) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != "PATH" {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+withHomePaths(pathIn(env)))
}

// pathIn is an environment's PATH.
func pathIn(env []string) string {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "PATH" {
			return v
		}
	}
	return ""
}
