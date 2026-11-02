package feishu

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"kero/internal/proto"
	"kero/internal/settings"
)

// background runs the prompt with the agent's own command, in the
// session's background, which tells the reply and no terminal shows: as
// claude -p, whose text is the answer. response is the event the answer
// replies to, which t has; a restart's take on the turn has none.
func (b *Bot) background(key, chatID, response, text string, t turnInfo) {
	base := b.chat(key)
	agent := base.agent
	if agent == "" {
		agent = b.cfg.Agent
	}
	if agent == "" || agent == "default" || !knownAgent(agent) {
		b.reply(chatID, "Which agent? /new <project> <agent> starts one, as /new api claude.")
		return
	}
	dir := base.dir
	if dir == "" {
		dir = b.cfg.Dir
	}
	if dir == "" {
		dir = settings.UserHome()
	}
	if t.event == "" {
		t.event = response
	}
	session, err := b.backgroundSession(chatID, base.id, agent, dir)
	if err != nil {
		b.reply(chatID, err.Error())
		return
	}
	sid := base.sid
	if sid == "" {
		sid = newUUID()
	}
	cmd := b.backgroundCommand(agent, sid, base.sid, text)
	if err := b.api.Call("agent.background", proto.BackgroundParams{ID: session, Command: cmd, Dir: dir, Out: b.outFile(session)}, nil); err != nil {
		b.reply(chatID, err.Error())
		return
	}
	var card *turnCard
	prev := ""
	if cards() && !terminal(agent) {
		prev, _ = b.lastCard(key, session)
		card = b.newTurnCard(chatID, session, text, prev)
		b.card(card, "working", "", "")
	}
	from := int64(0)
	answer := ""
	stop := time.Now().Add(b.timeout())
	shown := ""
	textAt := time.Time{}
	for {
		var out proto.Output
		if err := b.api.Call("agent.output", proto.OutputParams{ID: session, From: from}, &out); err != nil {
			b.card(card, "error", err.Error(), "")
			b.reply(chatID, err.Error())
			return
		}
		from, answer = out.From, answer+out.Text
		if text := commandText(agent, answer); text != shown && time.Since(textAt) >= b.stream() {
			shown, textAt = text, time.Now()
			b.card(card, "working", shown, "")
		}
		if out.Ended {
			b.finished(key, chatID, t.event, agent, dir, sid, text, out, answer, card)
			return
		}
		if time.Now().After(stop) {
			b.mu.Lock()
			base.agent, base.dir, base.cmd, base.sid = agent, dir, "", sid
			b.mu.Unlock()
			b.save()
			b.card(card, "asks", shown, "")
			b.reply(chatID, fmt.Sprintf("Still working after %s. /stop ends it.", b.timeout()))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// finished ends a background turn: the chat's conversation continues,
// the card turns green or red, and the answer goes to the chat in a
// reply to the event.
func (b *Bot) finished(key, chatID, response, agent, dir, sid, text string, out proto.Output, answer string, card *turnCard) {
	out.Text = answer
	if sid = conversation(agent, answer, sid); sid == "" {
		sid = b.chat(key).sid
	}
	b.mu.Lock()
	c := b.chatLocked(key)
	c.agent, c.dir, c.cmd, c.sid = agent, dir, "", sid
	c.card = cardMsg{}
	b.mu.Unlock()
	b.save()
	state := "done"
	if out.Exited != 0 || out.Error != "" {
		state = "error"
	}
	reply := b.backgroundReply(agent, out)
	if !cards() && terminal(agent) {
		// Feishu may not show a command's rows as a card's: the answer
		// goes as a message, and the card says so.
		b.card(card, state, "", word(langOf(text), "inTerminal"))
	} else {
		b.card(card, state, reply, "")
	}
	b.replyAt(key, response, chatID, reply)
}

// backgroundSession makes the chat's session a background one, with the
// agent's name, or returns it if it is one already.
func (b *Bot) backgroundSession(chatID, id, agent, dir string) (string, error) {
	if id != "" {
		var has bool
		if err := b.api.Call("session.hasBackground", proto.SessionParams{ID: id}, &has); err == nil && has {
			return id, nil
		}
	}
	project := ""
	if st, err := b.api.State(); err == nil {
		for _, s := range st.Sessions {
			if s.ID == id {
				project = s.Project
			}
		}
	}
	name := agent + " · " + filepath.Base(dir)
	if base := filepath.Base(dir); base == "" || base == "/" || base == "~" {
		name = agent
	}
	if project == "" {
		if st, err := b.api.State(); err == nil && len(st.Projects) > 0 {
			project = st.Projects[0].ID
		}
	}
	var s proto.Session
	if err := b.api.Call("create", proto.CreateParams{Project: project, Dir: dir, Name: name}, &s); err != nil {
		return "", fmt.Errorf("Kero could not start a session: %w", err)
	}
	if err := b.api.Call("session.setBackground", proto.SessionParams{ID: s.ID}, nil); err != nil {
		return "", err
	}
	b.bind(chatID, s)
	return s.ID, nil
}

// backgroundCommand is the agent's command with the prompt, and the
// options that tell the answer and continue the conversation: the first
// turn starts it with sid, and the others continue it with resume, which
// is the conversation's ID the first turn gave.
func (b *Bot) backgroundCommand(agent, sid, resume, text string) []string {
	cmd := append([]string{}, agentCommand(b.cfg, agent)...)
	again := resume != ""
	switch agent {
	case "claude":
		if again {
			cmd = append(cmd, "-p", "--output-format", "text", "--resume", resume)
		} else {
			cmd = append(cmd, "-p", "--output-format", "text", "--session-id", sid)
		}
	case "codex":
		cmd = append(cmd, "exec")
		if again {
			cmd = append(cmd, "resume", resume, "--skip-git-repo-check")
		}
	case "opencode":
		cmd = append(cmd, "run")
		if again {
			cmd = append(cmd, "--session", resume)
		}
	case "gemini":
		cmd = append(cmd, "-p")
		if again {
			cmd = append(cmd, "--resume", resume)
		} else {
			cmd = append(cmd, "--session-id", sid)
		}
	case "pi":
		if again {
			cmd = append(cmd, "-p", "--session-id", resume)
		} else {
			cmd = append(cmd, "-p", "--session-id", sid)
		}
	default:
		if !again && len(cmd) == 1 {
			return nil // no way told to run one prompt and end
		}
	}
	return append(cmd, text)
}

// conversation is the agent's session that a turn's output names, for
// the next turn to continue: the one the first turn started, or another
// its output says, as one the agent chose.
func conversation(agent, raw string, sid string) string {
	found := ""
	for _, line := range strings.Split(raw, "\n") {
		// OpenCode names its session sessionID, as Claude Code does
		// session_id, and Codex thread_id.
		var m struct {
			Snake  string `json:"session_id"`
			Camel  string `json:"sessionID"`
			Thread string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		switch {
		case m.Camel != "" && agent == "opencode":
			found = m.Camel
		case m.Thread != "":
			found = m.Thread
		case m.Snake != "" && (agent == "claude" || agent == "gemini"):
			found = m.Snake
		}
	}
	if found == "" && raw != "" && (agent == "claude" || agent == "pi") {
		return sid // the CLI keeps the one it was given
	}
	return found
}

// backgroundReply is the answer a background turn gives: its text, or
// what ended it.
func (b *Bot) backgroundReply(agent string, out proto.Output) string {
	if out.Error != "" && out.Text == "" {
		return "The agent failed: " + out.Error
	}
	text := commandText(agent, out.Text)
	if strings.TrimSpace(text) == "" {
		if out.Exited != 0 {
			return fmt.Sprintf("The agent ended with code %d, and no output.", out.Exited)
		}
		return "The agent answered nothing."
	}
	if out.Exited != 0 {
		text += fmt.Sprintf("\n\nThe agent ended with code %d.", out.Exited)
	}
	return text
}

// commandText takes the answer from the agent's output: the text of its
// strings, or its last message, or the stream's, and the output itself
// when it is none of these.
func commandText(agent, raw string) string {
	switch agent {
	case "claude":
		var text strings.Builder
		for _, line := range strings.Split(raw, "\n") {
			var m struct {
				Type    string `json:"type"`
				Message *struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
				Result string `json:"result"`
			}
			if json.Unmarshal([]byte(line), &m) != nil {
				t := strings.TrimSpace(line)
				// Claude Code's own reports to the terminal, such as a
				// model the gateway does not know, are no part of the
				// answer.
				if strings.HasPrefix(t, "[claude-code:") {
					continue
				}
				if t != "" {
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(t)
				}
				continue
			}
			switch m.Type {
			case "assistant":
				if m.Message != nil {
					for _, c := range m.Message.Content {
						if c.Type == "text" && c.Text != "" {
							if text.Len() > 0 {
								text.WriteString("\n\n")
							}
							text.WriteString(c.Text)
						}
					}
				}
			case "result":
				if m.Result != "" && strings.TrimSpace(text.String()) == "" {
					return m.Result
				}
			}
		}
		return strings.TrimSpace(text.String())
	case "opencode":
		var text strings.Builder
		for _, line := range strings.Split(raw, "\n") {
			var e struct {
				Type string `json:"type"`
				Part *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"part"`
			}
			if json.Unmarshal([]byte(line), &e) == nil && e.Type == "text" && e.Part != nil && e.Part.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(e.Part.Text)
			}
		}
		if text.Len() > 0 {
			return text.String()
		}
	}
	return strings.TrimSpace(raw)
}

// hasBackground tells a session's that runs its agent's commands without
// a terminal.
func (b *Bot) hasBackground(id string) (bool, error) {
	var has bool
	err := b.api.Call("session.hasBackground", proto.SessionParams{ID: id}, &has)
	return has, err
}

func (b *Bot) outFile(session string) string {
	return filepath.Join(proto.Dir(), "feishu-run", session+".out")
}

// newUUID is a version 4 UUID, as the agents take a session's ID.
func newUUID() string {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
