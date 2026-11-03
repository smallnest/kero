package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"kero/internal/detect"
	"kero/internal/proto"
	"kero/internal/settings"
)

const (
	// replyTail is the rows of the pane's screen a reply ends with: the
	// end of the agent's answer, under its prompt.
	replyTail = 30
	// maxText is a text message's: Feishu takes 150 KB, and 30 for a
	// card.
	maxText = 120 << 10
	// seenCap is how many event IDs the bot remembers.
	seenCap = 512
	// queue is how many prompts a chat has waiting.
	queue = 1
)

// onMessage takes a message from Feishu, and answers it as it asks. A
// group's message it is not mentioned in is no message to it: the group
// may have several bots.
func (b *Bot) onMessage(clientCtx context.Context, e *larkim.P2MessageReceiveV1) error {
	// Its turns keep kerod at its work, as a connection that stays does.
	if _, err := b.api.State(); err == nil {
		b.api.Call("watch", map[string]bool{"stay": true}, nil)
	}
	if e != nil && e.EventV2Base != nil && e.EventV2Base.Header != nil {
	}
	if e == nil || e.Event == nil || e.Event.Message == nil {
		return nil
	}
	m := e.Event.Message
	if m.ChatId == nil {
		return nil
	}
	if e.EventV2Base != nil && e.EventV2Base.Header != nil {
		if h := e.EventV2Base.Header.AppID; h != "" && h != b.cfg.App {
			return nil // another app's, as a shared event subscription sends
		}
	}
	chatID := *m.ChatId
	if e.EventV2Base != nil && e.EventV2Base.Header != nil {
		if id := e.EventV2Base.Header.EventID; id != "" && b.seenEvent(id) {
			return nil
		}
	}
	if !b.answers(chatID) {
		return nil
	}
	if leave, ok := b.windowEvent(m); leave && ok {
		return nil
	}
	group := deref(m.ChatType) == "group"
	if group && !b.mentioned(m) {
		// A reply to the bot in a group: its answer names the event, and
		// this message comes to it alone.
		if !b.replyToBot(m) {
			return nil
		}
	}
	openID, userID := "", ""
	if e.Event.Sender != nil && e.Event.Sender.SenderId != nil {
		openID = deref(e.Event.Sender.SenderId.OpenId)
		userID = deref(e.Event.Sender.SenderId.UserId)
	}
	if !b.allowed(openID, userID) {
		b.reply(chatID, "You are not allowed to use Kero on this machine. Add your open_id ("+
			firstNotEmpty(openID, userID)+") to feishu-allow in ~/.config/kero/config.")
		return nil
	}
	// A group's messages go to the session of the chat, and each reply
	// to the message it answers: the thread's root is the chat's topic.
	// A group's mentions read alike, a message's and a thread's. In
	// shared mode, every host with the bot leaves a message that follows
	// another host's answer to that host.
	if p := parentOf(m); p != "" {
		ev, leave := b.claim(p, chatID)
		if leave {
			return nil
		}
		if ev != "" {
			return nil
		}
	}
	key := chatKey(chatID, "")
	if group {
		key = chatKey(chatID, deref(m.RootId))
	}
	text := b.textOf(parentOf(m), m)
	if text == "" {
		return nil
	}
	// One message a chat at a time: the others wait their turn, one deep.
	if !b.enter(key) {
		b.reply(chatID, "Kero is still working on the last message there. Wait for it, or send /stop.")
		return nil
	}
	response := deref(m.MessageId)
	asked := parentOf(m) // the answer a follow-up answers
	if b.shared() && len(asked) > 0 {
		response = asked
	}
	t := &turnInfo{start: time.Now(), chat: chatID, text: text, event: response, asked: asked}
	b.mu.Lock()
	c0 := b.chatLocked(key)
	c0.chat, c0.event, c0.key = chatID, response, key
	if !group {
		c0.peer = openID
	}
	b.mu.Unlock()
	b.beginTurn(key, t)
	c := b.chat(key)
	b.goTurn(func() {
		defer b.leave(key)
		defer b.dropTurn(key, t)
		if takeOverOn && b.takeOver(c) {
			return
		}
		b.turn(key, chatID, response, text)
	})
	return nil
}

// takeOverOn tells whether a turn takes on a turn before it, which it
// finds running: a test that runs several turns of a chat at once, as
// its hosts hear them, turns it off.
var takeOverOn = true

// hostNote tells a chat that Feishu does not let the bot write there,
// as a group it was not added to.
const hostNote = "Feishu does not let Kero write here. Add the bot to the group, and let feishu-host name the hosts that share the app; the chat's host goes among them."

// isCommand tells a message's text that is a command.
func isCommand(content string) bool {
	cmd, _ := splitCommand(messageText(content))
	return cmd != ""
}

// parentOf is a message's parent, the message it answers.
func parentOf(m *larkim.EventMessage) string { return deref(m.ParentId) }

// replyToBot tells a group message sent in a reply to one the bot sent,
// as the bot's card, which this event alone says of: the next event of
// a reply names no event.
func (b *Bot) replyToBot(m *larkim.EventMessage) bool {
	parent := deref(m.ParentId)
	if parent == "" {
		return false
	}
	b.mu.Lock()
	_, ok := b.notes[parent]
	b.mu.Unlock()
	return ok
}

// textOf is a message's text to the session: a text's, a file's or
// image's saved path, a rich text's; "" when it has none.
func (b *Bot) textOf(chatID string, m *larkim.EventMessage) string {
	content := deref(m.Content)
	switch deref(m.MessageType) {
	case "text":
		return withoutMentions(m)
	case "file", "image":
		return strings.TrimSpace(withoutMentionsText(m) + b.attachment(chatID, deref(m.MessageType), content))
	case "post":
		return strings.TrimSpace(postText(content) + b.attachment(chatID, "post", content))
	}
	b.reply(chatID, "Kero takes text, files, images and rich text. Send one of them.")
	return ""
}

// turn runs a message: a command, or the prompt that says it. event is
// the event the message answers, which its answer names.
func (b *Bot) turn(key, chatID, event, text string) {
	cmd, rest := splitCommand(text)
	t := turnInfo{start: time.Now(), chat: chatID, text: text, event: event}
	if cmd == "" {
		if b.cfg.Run == settings.FeishuRunCommand {
			b.background(key, chatID, event, rest, t)
		} else {
			b.prompt(key, chatID, event, rest, t)
		}
		return
	}
	switch cmd {
	case "new":
		b.newSession(chatID, rest)
	case "use":
		b.use(chatID, strings.TrimSpace(rest))
	case "ls":
		b.list(chatID)
	case "status":
		b.status(chatID)
	case "stop":
		b.stop(chatID)
	case "answer":
		b.answer(chatID, strings.TrimSpace(rest))
	case "close":
		b.close(chatID)
	case "host":
		b.hostCommand(chatID, rest)
	case "help":
		b.reply(chatID, helpText)
	default:
		b.reply(chatID, fmt.Sprintf("No command /%s. Write /help for the commands.", cmd))
	}
}

// mentioned tells whether the message @-mentions the bot.
func (b *Bot) mentioned(m *larkim.EventMessage) bool {
	for _, mt := range m.Mentions {
		if mt == nil {
			continue
		}
		if deref(mt.MentionedType) == "bot" {
			return true
		}
	}
	return false
}

// withoutMentions is a text message's text without its @-mentions, as
// the bot is told them apart from what the user writes.
func withoutMentions(m *larkim.EventMessage) string { return withoutMentionsText(m) }

// withoutMentionsText is a message's text, as a text message has it,
// without its @-mentions.
func withoutMentionsText(m *larkim.EventMessage) string {
	text := messageText(deref(m.Content))
	for _, mt := range m.Mentions {
		if mt != nil && deref(mt.Key) != "" {
			text = strings.ReplaceAll(text, deref(mt.Key), "")
		}
	}
	return strings.TrimSpace(text)
}

// enter takes a chat's turn if no other runs.
func (b *Bot) enter(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.chatLocked(key)
	if c.busy {
		return false
	}
	c.busy = true
	return true
}

func (b *Bot) leave(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.chatLocked(key).busy = false
}

// goTurn runs a turn, unless the tests take the turns in their own hands
// (sync).
func (b *Bot) goTurn(f func()) {
	if b.sync {
		f()
		return
	}
	go f()
}

// splitCommand cuts a slash command from a message: /new, /use, /help, or
// the ones /foo@bot-name in a group writes.
func splitCommand(text string) (cmd, rest string) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") || strings.HasPrefix(t, "//") {
		return "", t
	}
	word, rest, _ := strings.Cut(t[1:], " ")
	if i := strings.IndexAny(word, "@"); i >= 0 {
		word = word[:i]
	}
	return strings.ToLower(word), rest
}

const helpText = `Kero

/new [project] [agent]   start a session, and bind this chat to it
/use <session>           bind this chat to an open session
/ls                      list the open sessions
/status                  the bound session's state, branch and changes
/stop                    interrupt the agent's current turn
/answer <text>           answer the question the agent asks
/close                   end the bound session

Other text goes to the bound session as a prompt.`

// newSession starts a session running the configured agent, and binds
// the chat to it.
func (b *Bot) newSession(chatID, rest string) {
	project, agent := "", ""
	for _, w := range strings.Fields(rest) {
		if w == "" {
			continue
		}
		if project == "" {
			project = w
			continue
		}
		agent = w
	}
	if agent == "" {
		agent = b.cfg.Agent
	}
	if agent != "" && agent != "default" && !knownAgent(agent) {
		b.reply(chatID, fmt.Sprintf("Kero has no agent %q.", agent))
		return
	}
	params := proto.CreateParams{Project: project, Dir: b.cfg.Dir}
	if agent != "default" {
		params.Command = agentCommand(b.cfg, agent)
	}
	var s proto.Session
	if err := b.api.Call("create", params, &s); err != nil {
		b.reply(chatID, "Kero could not start a session: "+err.Error())
		return
	}
	b.bind(chatID, s)
	// Its pane's process, if there is one: where it works.
	where := ""
	if p := s.Pane; p.Git != nil {
		where = fmt.Sprintf(" in %s%s", p.Git.Branch, changes(p.Git))
	}
	b.reply(chatID, fmt.Sprintf("%q is ready%s. Write a prompt, and the agent gets it.", b.nameSession(s), where))
}

// use binds the chat to an open session.
func (b *Bot) use(chatID, ref string) {
	if ref == "" {
		b.reply(chatID, "Which session? /ls lists them.")
		b.list(chatID)
		return
	}
	st, err := b.api.State()
	if err != nil {
		b.reply(chatID, "Kero is unreachable: "+err.Error())
		return
	}
	var found []*proto.Session
	for i := range st.Sessions {
		s := &st.Sessions[i]
		if !takesPrompts(s) {
			continue
		}
		n := s.DisplayName(settings.Load().SessionName)
		if n == ref || s.Name == ref || s.ID == ref || strings.HasPrefix(s.ID, ref) || strings.EqualFold(n, ref) {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		for i := range st.Sessions {
			s := &st.Sessions[i]
			n := s.DisplayName(settings.Load().SessionName)
			if s.ID == ref || strings.HasPrefix(s.ID, ref) || s.Name == ref || strings.EqualFold(n, ref) {
				b.reply(chatID, fmt.Sprintf("%s has no terminal to take prompts. /ls lists the sessions that do.", n))
				return
			}
		}
		b.reply(chatID, fmt.Sprintf("Kero has no session %q to take prompts. /ls lists those that do.", ref))
		return
	}
	if len(found) > 1 {
		by := ""
		for _, s := range found {
			n := s.DisplayName(settings.Load().SessionName)
			if strings.EqualFold(n, ref) || n == ref {
				if by != "" {
					by = ""
					break
				}
				by = s.ID
			}
		}
		if by == "" {
			b.reply(chatID, fmt.Sprintf("Kero has %d sessions named %q. /use their ids.", len(found), ref))
			return
		}
		found = []*proto.Session{sessionOf(found, by)}
	}
	b.bind(chatID, *found[0])
	b.reply(chatID, fmt.Sprintf("This chat is %s%s now.", found[0].DisplayName(settings.Load().SessionName), where(found[0])))
}

// list shows the open sessions.
func (b *Bot) list(chatID string) {
	st, err := b.api.State()
	if err != nil {
		b.reply(chatID, "Kero is unreachable: "+err.Error())
		return
	}
	var out strings.Builder
	cur := b.chat(chatKey(chatID, "")).id
	for _, s := range st.Sessions {
		if !takesPrompts(&s) {
			continue // no terminal to send a prompt to
		}
		mark := "·"
		if s.ID == cur {
			mark = "→"
		}
		fmt.Fprintf(&out, "%s %s %s%s\n", mark, stateDot(s), s.DisplayName(settings.Load().SessionName), where(&s))
	}
	if out.Len() == 0 {
		b.reply(chatID, "No session takes prompts. /new starts one.")
		return
	}
	b.reply(chatID, strings.TrimRight(out.String(), "\n"))
}

// firstLine is a text's first line, cut short.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return line
}

// status shows the bound session's state, branch and changes.
func (b *Bot) status(chatID string) {
	s, err := b.bound(chatID)
	if err != nil {
		b.reply(chatID, "This chat has no session yet. /new starts one.")
		return
	}
	p := leadPane(s)
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s\n", s.DisplayName(settings.Load().SessionName), stateDot(*s))
	fmt.Fprintf(&out, "%s · %s\n", p.Status, orDash(p.Message))
	if g := p.Git; g != nil {
		fmt.Fprintf(&out, "%s%s\n", g.Branch, changes(g))
	}
	fmt.Fprintf(&out, "%s", orDash(p.WorkDir()))
	b.reply(chatID, out.String())
}

// stop interrupts the bound session's pane, as Ctrl-C.
func (b *Bot) stop(chatID string) {
	s, err := b.bound(chatID)
	if err != nil {
		b.reply(chatID, "This chat has no session yet. /new starts one.")
		return
	}
	if has, err := b.hasBackground(s.ID); err == nil && has {
		if turn := b.runningTurn(chatID).text; turn != "" {
			if err := b.api.Call("agent.stop", proto.SessionParams{ID: s.ID}, nil); err != nil {
				b.reply(chatID, err.Error())
				return
			}
			b.reply(chatID, "Ended: "+firstLine(turn))
			return
		}
		b.reply(chatID, "Nothing runs there.")
		return
	}
	if err := b.api.Call("send", proto.SendParams{ID: s.Pane.ID, Data: "\x03"}, nil); err != nil {
		b.reply(chatID, err.Error())
		return
	}
	b.reply(chatID, "Interrupted.")
}

// answer sends text to the bound session, and Enter: an answer to the
// question the agent asks.
func (b *Bot) answer(chatID, text string) {
	s, err := b.bound(chatID)
	if err != nil {
		b.reply(chatID, "This chat has no session yet. /new starts one.")
		return
	}
	if b.cfg.Run == settings.FeishuRunCommand {
		// A background session has no prompt to answer in the pane.
		if has, err := b.hasBackground(s.ID); err == nil && has {
			b.reply(chatID, "This session runs its agent in the background: send the prompt as a message.")
			return
		}
	}
	// An answer to the agent's question ends a turn, as a prompt does: the
	// pane's hook writes its answer, and the chat's host replies with it.
	key := chatKey(chatID, "")
	if text != "" && s.Pane.Status == proto.Waiting {
		// The agent asks, and its hook names the answer: the host follows
		// the turn, and replies with it. Else the answer is text.
		b.answering(key, s, turnInfo{event: key, chat: chatID})
		return
	}
	if err := b.send(s.Pane.ID, text, true); err != nil {
		b.reply(chatID, err.Error())
		return
	}
	// Its turn keeps kerod at its work, as a connection that stays does.
	b.api.Call("watch", map[string]bool{"stay": true}, nil)
	if cards() && len(text) > 0 {
		// The turn shows in a card, which /stop ends.
		var p proto.Pane
		if err := b.api.Call("pane", proto.IDParams{ID: s.Pane.ID}, &p); err == nil {
			prev, _ := b.lastCard(chatKey(chatID, ""), s.ID)
			card := b.newTurnCard(chatID, s.ID, text, prev)
			b.goTurn(func() {
				state, body, failed, _ := b.watchCard(card, s.Pane.ID, rows(b.read(s.Pane.ID)), time.Time{})
				if failed {
					state = "error"
				}
				b.card(card, state, body, "")
			})
		}
	}
	b.reply(chatID, "Sent. /status shows the turn.")
}

// answering hands an answer to the agent's question, which ends the turn
// the chat's host follows: the pane's hook writes its answer, and the
// chat's host replies with it.
func (b *Bot) answering(key string, s *proto.Session, t turnInfo) {
	pane := leadPane(s).ID
	agent := b.conversationAgent(b.chat(key), "", s.ID)
	answers := b.prepare(pane, agent)
	if answers != "" {
		var info proto.Pane
		b.api.Call("pane.answer", proto.AnswerParams{Pane: pane, Dir: answers}, &info)
	}
	if err := b.send(pane, "", false); err != nil {
		b.reply(t.chat, err.Error())
		return
	}
	t.pane, t.start = pane, time.Now()
	if !b.shared() {
		b.goTurn(func() { b.followAnswer(key, t.chat, agent, answers, t) })
		return
	}
	// One host follows the turn, the one the chat's messages go to.
	b.goTurn(func() {
		if host, _ := b.claim("", t.chat); host != "" && host != b.host() && b.takeAt(t.event, b.host()) != b.host() {
			return
		}
		b.followAnswer(key, t.chat, agent, answers, t)
	})
}

// followAnswer follows the turn an answer ends, and replies with the
// hook's answer of it. The turn began with a prompt: t, the turn that
// prompt began, or one that starts at the answer.
func (b *Bot) followAnswer(key, chatID, agent, answers string, t turnInfo) {
	var card *turnCard
	if cards() {
		prev, _ := b.lastCard(key, "")
		card = b.newTurnCard(chatID, "", "", prev)
		b.card(card, "working", "", "")
	}
	// The answer continues the prompt's turn, which t notes: drive looks
	// for the pane and the agent no more.
	s := &proto.Session{ID: chatID, Pane: proto.Pane{ID: t.pane, Status: proto.Working}}
	r, err := b.drive(s, "", card, t, agent, answers)
	if err != nil {
		b.card(card, "error", err.Error(), "")
		b.reply(chatID, err.Error())
		return
	}
	state := "done"
	if r.status.Status == proto.Failed {
		state = "error"
	}
	if r.hook {
		// The hook's answer is the turn's text, not the terminal's, and
		// it names the conversation the answer continued.
		b.card(card, state, r.answer, "")
		b.mu.Lock()
		c := b.chatLocked(key)
		c.sid, c.card = r.conversation, cardMsg{}
		b.mu.Unlock()
		b.save()
	} else {
		b.card(card, state, "", "")
	}
	b.replyAt(key, t.event, chatID, b.replyWith(r))
}

// close ends the bound session, once no turn runs.
func (b *Bot) close(chatID string) {
	s, err := b.bound(chatID)
	if err != nil {
		b.reply(chatID, "This chat has no session yet. /new starts one.")
		return
	}
	if s.Pane.Status == proto.Working {
		b.reply(chatID, "The agent is working. /stop interrupts it, then /close.")
		return
	}
	if err := b.api.Call("close", proto.IDParams{ID: s.ID}, nil); err != nil {
		b.reply(chatID, err.Error())
		return
	}
	b.unbind(chatKey(chatID, ""))
	b.reply(chatID, "Closed. /new starts another.")
}

// bound is the chat's session, now.
func (b *Bot) bound(chatID string) (*proto.Session, error) {
	id := b.chat(chatKey(chatID, "")).id
	if id == "" {
		return nil, errors.New("no session")
	}
	s, err := b.session(id)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// nameSession names an unnamed session, which a shell would name by its
// folder, as its agent has yet to start.
func (b *Bot) nameSession(s proto.Session) string {
	name := plainName(s.DisplayName(settings.Load().SessionName))
	if s.Name == "" && strings.TrimSpace(name) != "" {
		b.api.Call("rename", proto.RenameParams{ID: s.ID, Name: name}, nil)
	}
	return name
}

// leadPane is a session's lead pane, else its first: a session no
// client's state has told kerod the lead of has none.
func leadPane(s *proto.Session) proto.Pane {
	if s.Pane.ID != "" {
		return s.Pane
	}
	if len(s.Panes) > 0 {
		return s.Panes[0]
	}
	return proto.Pane{}
}

// session finds an open session by id.
func (b *Bot) session(id string) (*proto.Session, error) {
	st, err := b.api.State()
	if err != nil {
		return nil, err
	}
	for i := range st.Sessions {
		if st.Sessions[i].ID == id {
			return &st.Sessions[i], nil
		}
	}
	return nil, errors.New("the session is gone")
}

// plainName takes the quotes off a name, which a title may hold.
func plainName(s string) string {
	return strings.Trim(s, `"'`)
}

func (b *Bot) bind(chatID string, s proto.Session) {
	key := chatKey(chatID, "")
	b.mu.Lock()
	b.chatLocked(key).id = s.ID
	b.binds[key] = s.ID
	b.mu.Unlock()
	b.save()
}

// unbind takes the chat's session, which is gone or closed, off the
// chat.
func (b *Bot) unbind(key string) {
	b.mu.Lock()
	b.chatLocked(key).id = ""
	had := b.binds[key] != ""
	delete(b.binds, key)
	b.mu.Unlock()
	if had {
		b.save()
	}
}

// takesPrompts tells a session a prompt can go to: one with a terminal.
func takesPrompts(s *proto.Session) bool {
	for i := range s.Panes {
		if !s.Panes[i].Shown() {
			return true
		}
	}
	return false
}

func sessionOf(l []*proto.Session, id string) *proto.Session {
	for _, s := range l {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// bindFile is where the chats' bindings go: kerod's directory, kept with
// its sessions, so that a chat finds its session again after kerod
// restarts.
func bindFile() string { return filepath.Join(proto.Dir(), "feishu-chats.json") }

// save writes the bindings.
func (b *Bot) save() {
	b.mu.Lock()
	w := chatsFile{Turns: maps.Clone(b.times), Pins: maps.Clone(b.pins)}
	allowed := map[string][]string{}
	for key, c := range b.chats {
		for tool := range c.allowed {
			allowed[key] = append(allowed[key], tool)
		}
	}
	b.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(bindFile()), 0o700); err != nil {
		return
	}
	data, _ := json.Marshal(struct {
		chatsFile
		Binds   map[string]string   `json:"binds"`
		Allowed map[string][]string `json:"allowed,omitempty"`
	}{w, maps.Clone(b.binds), allowed})
	tmp := bindFile() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil && os.Rename(tmp, bindFile()) != nil {
		os.Remove(tmp)
	}
}

// load reads the bindings.
func (b *Bot) load() {
	data, err := os.ReadFile(bindFile())
	if err != nil {
		return
	}
	// The chats' turns, with their keys, or the keys alone, as the file
	// was before the chats' turns.
	var w struct {
		chatsFile
		Binds   map[string]string   `json:"binds"`
		Allowed map[string][]string `json:"allowed"`
	}
	if json.Unmarshal(data, &w) != nil {
		return
	}
	if w.Binds == nil {
		json.Unmarshal(data, &w.Binds)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, id := range w.Binds {
		if _, ok := b.chats[k]; !ok {
			b.chats[k] = &chat{id: id}
		}
		b.binds[k] = id
	}
	for k, tools := range w.Allowed {
		c := b.chats[k]
		if c == nil {
			continue
		}
		c.allowed = map[string]bool{}
		for _, tool := range tools {
			c.allowed[tool] = true
		}
	}
	for c, t := range w.Turns {
		b.times[c] = t
	}
	for s, c := range w.Pins {
		b.pins[s] = c
	}
}

func (b *Bot) reply(chatID, text string) {
	key := chatKey(chatID, "")
	b.mu.Lock()
	event := b.claims[key]
	b.mu.Unlock()
	b.replyAt(key, event, chatID, text)
}

// replyAt answers a message in a chat as a reply to it, which the hosts
// that share the app hear and the chat's answers follow: event is the
// event the message answers, "" for a message of its own.
func (b *Bot) replyAt(key, event, chatID, text string) { b.replyTurn(key, event, chatID, "", text) }

// replyTurn ends a turn in a chat: text says what the turn did, as
// "Sent." does, and card is the turn's answer, which its card shows.
// A card is the turn's answer, and text a note about it.
func (b *Bot) replyTurn(key, event, chatID, card, text string) {
	text = truncate(strings.TrimSpace(text), maxText)
	if text == "" {
		return
	}
	if b.shared() {
		// An answer names the event, which every host hears, and the host
		// the chat's messages go to, which /host set. Kerod keeps the
		// response, which the other hosts answer the event's next
		// messages with: Feishu does not give a host another host's
		// messages.
		b.mu.Lock()
		want := b.wants[key]
		b.mu.Unlock()
		text = hopText(event) + fromText(want) + text
		b.noteTurn(event, chatID, text)
	}
	// Every answer shows as a card (feishu-cards), as the wrapper the
	// agent runs in shows its turns: no message without one. An app that
	// several hosts share keeps every answer, which the hosts answer the
	// next messages with: the card goes beside it.
	answer := strings.TrimSpace(card)
	if answer == "" {
		answer = text
	}
	if cards() {
		if err := b.replyCard(chatID, answer, text); err == nil && !b.shared() {
			return
		}
	}
	text = strings.Join([]string{answer, strings.TrimSpace(text)}, "\n\n")
	var err error
	if event != "" {
		_, err = sendReply(b.msgs, event, text)
		if err == nil {
			return
		}
	}
	if _, err = b.msgs.Send("chat_id", chatID, text); err != nil {
		b.logf("feishu: reply to %s: %v", chatID, err)
	}
}

// titleLen is how long a card's title is, about: a prompt's first line,
// or its words, cut where they end.
const titleLen = 60

// titleOf is the title of the card that answers a turn: the first line
// of its text, all of it when it is short, and its words cut otherwise.
func titleOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>*-• "))
	if r := []rune(line); len(r) > titleLen {
		cut := titleLen
		for i := titleLen; i > titleLen/2; i-- {
			if r[i-1] == ' ' {
				cut = i - 1
				break
			}
		}
		line = strings.TrimSpace(string(r[:cut]))
	}
	if line == "" {
		return text
	}
	return line
}

// replyCard answers a turn in a chat with a card: its title the answer's
// first words, its text the answer, in the done state's color, and note
// what the turn did under it.
func (b *Bot) replyCard(chatID, text, note string) error {
	if strings.TrimSpace(note) == strings.TrimSpace(text) {
		note = ""
	}
	shown, err := cardJSON(Card{Title: titleOf(text), Text: strings.TrimSpace(text), State: "done", Note: note}, langOf(text))
	if err != nil {
		return err
	}
	_, err = b.msgs.SendCard("chat_id", chatID, shown)
	if err != nil {
		b.logf("feishu: sending a card: %v", err)
	}
	return err
}

// shared tells an app that several hosts connect from.
func (b *Bot) shared() bool { return len(b.cfg.Host) > 1 }

// keyOf is a chat's key, for the bot its key is not the chat's ID, as
// the owner's.
func (b *Bot) keyOf(chatID string) string { return chatKey(chatID, "") }

// chatKey is a chat's: a group's thread is a chat of its own.
func chatKey(chatID, thread string) string {
	if thread == "" {
		return chatID
	}
	return chatID + "\x00" + thread
}

func (b *Bot) chat(key string) *chat {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.chatLocked(key)
}

func (b *Bot) chatLocked(key string) *chat {
	c := b.chats[key]
	if c == nil {
		c = &chat{}
		b.chats[key] = c
	}
	return c
}

func (b *Bot) seenEvent(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen[id] {
		return true
	}
	b.seen[id] = true
	b.order = append(b.order, id)
	if len(b.order) > seenCap {
		delete(b.seen, b.order[0])
		b.order = b.order[1:]
	}
	return false
}

func (b *Bot) allowed(openID, userID string) bool {
	f := &b.cfg
	for _, a := range f.Allow {
		if a == "*" || a != "" && (a == openID || a == userID) {
			return true
		}
	}
	// Only who ran kero feishu setup, until feishu-allow names others.
	return b.me != "" && (openID == b.me || userID == b.me)
}

// messageText is the text of a text message's content: {"text":"…"}.
func messageText(content string) string {
	var c struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &c); err != nil {
		// Only the text's JSON: "\n" and the like.
		var s string
		if json.Unmarshal([]byte(content), &s) == nil {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(c.Text)
}

// rows counts the pane's lines.
func rows(text string) int { return strings.Count(text, "\n") }

// since is the end of a pane's text, rows after start on: what a turn
// added, at most tail of it.
func since(text string, start, tail int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if start < len(lines) {
		lines = lines[start:]
	} else {
		lines = nil
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.Index(cut, "\n"); i >= 0 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

func stateDot(s proto.Session) string {
	switch {
	case s.Pane.Status == proto.Working:
		return "●"
	case s.Pane.Status == proto.Waiting, s.Pane.Status == proto.Failed:
		return "◐"
	case s.Pane.Status == proto.Exited:
		return "✕"
	}
	return "○"
}

func where(s *proto.Session) string {
	if s == nil {
		return ""
	}
	if p := s.Pane; p.Git != nil {
		return fmt.Sprintf(" — %s%s", p.Git.Branch, changes(p.Git))
	}
	if d := s.Pane.WorkDir(); d != "" {
		return " — " + d
	}
	return ""
}

func changes(g *proto.GitInfo) string {
	if g == nil || (g.Added == 0 && g.Removed == 0 && g.Files == 0) {
		return ""
	}
	return fmt.Sprintf(" +%d −%d", g.Added, g.Removed)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNotEmpty(l ...string) string {
	for _, s := range l {
		if s != "" {
			return s
		}
	}
	return ""
}

// agentCommand is the command a new session runs the agent by: the
// feishu-agent-command line's words, or else the agent's program.
func agentCommand(f settings.Feishu, agent string) []string {
	if cmd := f.AgentCommand(agent); len(cmd) > 0 {
		return cmd
	}
	return []string{agent}
}

func knownAgent(agent string) bool { return detect.Known(agent) }

func sendReply(m Messages, id, text string) (string, error) {
	return m.SendReply(id, text)
}
