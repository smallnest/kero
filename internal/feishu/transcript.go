package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kero/internal/proto"
	"kero/internal/settings"
)

// A chat window in Kero shows the agent's conversation in a session:
// the prompts, the tools it ran, and the messages it wrote, as the
// agent keeps them. Claude Code keeps each message in a JSON line of its
// session's file. A Stop hook that a chat driver runs writes each turn's
// last message to the pane's answer file, and so does kero answer.

// maxFeedLines is how many lines a chat window shows: the end of a long
// conversation.
const maxFeedLines = 400

// claudeLine is a Claude Code transcript's line.
type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"message"`
}

// claudeTranscript is Claude Code's: each message's text, and each tool it
// ran.
func claudeTranscript(data []byte) []proto.FeishuLine {
	var out []proto.FeishuLine
	var e claudeLine
	for _, ls := range lines(data) {
		e = claudeLine{}
		if json.Unmarshal(ls, &e) != nil {
			continue
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
		text, prompt := e.Message.Content.(string)
		if prompt {
			// Claude Code writes the prompt as the terminal sent it.
			out = append(out, proto.FeishuLine{Kind: "note", Text: short(text, 200)})
			continue
		}
		out = append(out, contentLines(e.Message.Content)...)
	}
	// A turn's messages are the last run of assistant messages: the
	// last one is the answer the chat got.
	if len(out) == 0 {
		return nil
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Kind == "agent" {
			out[i].Answer = true
			break
		}
	}
	return out
}

// contentLines is a Claude Code message's content: its text, the tool
// calls it made, or the results it holds.
func contentLines(content any) []proto.FeishuLine {
	var out []proto.FeishuLine
	if text, ok := content.(string); ok && strings.TrimSpace(text) != "" {
		out = append(out, proto.FeishuLine{Kind: "agent", Text: text})
	}
	parts, _ := content.([]any)
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "text" {
			text, _ := m["text"].(string)
			if strings.TrimSpace(text) != "" {
				out = append(out, proto.FeishuLine{Kind: "agent", Text: text})
			}
		}
		if typ == "tool_use" {
			name, _ := m["name"].(string)
			out = append(out, proto.FeishuLine{Kind: "tool", Text: toolText(name, m["input"])})
		}
		if typ == "tool_result" {
			out = append(out, proto.FeishuLine{Kind: "tool", Text: "Result: " + short(resultText(m["content"]), 200)})
		}
	}
	return out
}

// answersTranscript is a transcript as kero answer writes it: a JSON
// object a line, with the conversation's ID and its answer, which the
// hook gives, else the agent's last message.
func answersTranscript(data []byte) []proto.FeishuLine {
	var out []proto.FeishuLine
	var turn bool
	var e struct {
		Conversation string `json:"conversation"`
		Answer       string `json:"answer"`
		Last         string `json:"last_message"`
	}
	for _, ls := range lines(data) {
		e = struct {
			Conversation string `json:"conversation"`
			Answer       string `json:"answer"`
			Last         string `json:"last_message"`
		}{}
		if json.Unmarshal(ls, &e) != nil {
			return nil
		}
		if e.Conversation == "" {
			return nil
		}
		var text string
		var answer bool
		if e.Answer != "" {
			text, answer = e.Answer, true
		} else {
			text = e.Last
		}
		turn = turn || answer
		out = append(out, proto.FeishuLine{Kind: "agent", Text: text, Answer: answer})
	}
	if !turn {
		return nil
	}
	return out
}

func toolText(name string, input any) string {
	b, _ := json.Marshal(input)
	return "Tool " + firstNotEmpty(name, "call") + " " + short(string(b), 200)
}

// resultText is a tool result's text.
func resultText(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, p := range v {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// codexLine is a Codex session file's line.
type codexLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Command []string `json:"command"`
	} `json:"payload"`
}

// codexTranscript is Codex's: each message, and each command it ran.
func codexTranscript(data []byte) []proto.FeishuLine {
	var out []proto.FeishuLine
	var e codexLine
	for _, ls := range lines(data) {
		e = codexLine{}
		if json.Unmarshal(ls, &e) != nil || e.Type != "response_item" {
			continue
		}
		switch e.Payload.Type {
		case "message":
			kind := "agent"
			if e.Payload.Role == "user" {
				kind = "user"
			}
			for _, c := range e.Payload.Content {
				if strings.TrimSpace(c.Text) != "" {
					out = append(out, proto.FeishuLine{Kind: kind, Text: c.Text})
				}
			}
		case "function_call":
			out = append(out, proto.FeishuLine{Kind: "tool", Text: "Tool " + short(strings.Join(e.Payload.Command, " "), 200)})
		}
	}
	return out
}

// lines is a file's lines, which a transcript has one event a line. The
// end is what a chat window shows, of a file that may be long.
func lines(data []byte) [][]byte {
	ls := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(ls) > 2000 {
		ls = ls[len(ls)-2000:]
	}
	return ls
}

// turnTimes is a chat's turns.
type turnTimes struct {
	Session string `json:"session,omitempty"`
	Turns   int    `json:"turns,omitempty"`
	Last    string `json:"last,omitempty"`
}

// chatsFile is the file of the chats' turns and the sessions' windows, as
// the bot keeps them with the chats' bindings.
type chatsFile struct {
	Turns map[string]turnTimes `json:"turns,omitempty"`
	Pins  map[string]string    `json:"pins,omitempty"` // chats, by session
}

// ChatsList lists the app's chats, with the sessions the bot's turns of
// them went to, for a chat window.
func (b *Bot) ChatsList(ctx context.Context) (proto.FeishuChats, error) {
	out := proto.FeishuChats{Host: b.host()}
	if b.Chats == nil {
		return out, errors.New("the bot is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	chats, err := b.Chats(ctx)
	b.mu.Lock()
	for i := range chats {
		key := chatKey(chats[i].ID, "")
		chats[i].Session = b.chatLocked(key).id
		if t, ok := b.times[key]; ok {
			chats[i].Turns, chats[i].Last = t.Turns, t.Last
		}
	}
	b.mu.Unlock()
	out.Chats = chats
	return out, err
}

// pinned is the chat a session's window shows, "" for none.
func (b *Bot) pinned(session string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pins[session]
}

// Feed is a session's conversation for its chat window: the chat it
// shows, which pins set, or the chat the session is bound to. It gives
// the session's window to the chat.
func (b *Bot) Feed(p proto.FeishuFeedParams) (proto.FeishuFeed, error) {
	s, err := b.session(p.Session)
	if err != nil {
		return proto.FeishuFeed{}, err
	}
	f := proto.FeishuFeed{ID: s.ID, Name: s.DisplayName(settings.Load().SessionName), Host: b.host()}
	if s.Status != "" {
		f.Status = string(s.Status)
	}
	if pane := paneOf(s); pane.ID != "" {
		f.Pane, f.Agent, f.Conversation = pane.ID, pane.Agent, pane.AgentSessionID
		f.Message, f.Evidence = pane.Message, pane.FeishuAnswer
	}
	f.Chat = p.Chat
	// A command of a chat's runs its agent without the pane's terminal to
	// tell it: the conversation it names is a Claude Code conversation.
	if len(f.Agent) == 0 && len(f.Conversation) > 0 {
		f.Agent = "claude"
	}
	if f.Chat == "" {
		f.Chat = b.pinned(s.ID)
	}
	if f.Chat == "" {
		f.Chat = b.boundChat(s.ID)
	}
	if f.Chat != "" {
		b.pin(s.ID, f.Chat)
		f.Lines = b.lines(s, f)
	}
	return f, nil
}

// boundChat is the chat a session's turns go to, "" for none.
func (b *Bot) boundChat(session string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, id := range b.binds {
		if id == session {
			return key
		}
	}
	return ""
}

// pin gives a session's window to a chat.
func (b *Bot) pin(session, chat string) {
	b.mu.Lock()
	b.pins[session] = chat
	b.mu.Unlock()
	b.save()
}

// lines is a session's conversation, the end of it that a window shows:
// the agent's transcript, which the answer file fills for its last turn.
func (b *Bot) lines(s *proto.Session, f proto.FeishuFeed) []proto.FeishuLine {
	var out []proto.FeishuLine
	file := transcriptFile(f.Conversation, f.Agent, sessionDir(s))
	if file != "" {
		if data, err := os.ReadFile(file); err == nil {
			out = transcript(f.Agent, data)
		}
	}
	if len(out) == 0 && len(f.Evidence) > 0 {
		if a := answersIn(f.Evidence); len(a) > 0 {
			out = a
		}
	}
	// The chat's prompts, which the transcript of an agent may not tell
	// from what its hooks wrote.
	if b.shared() {
		if turns, err := kept(); err == nil {
			for _, t := range turns {
				text := strings.TrimSpace(strings.TrimPrefix(t.Text, hopText("")))
				if t.Chat == f.Chat && len(text) > 0 {
					out = append(out, proto.FeishuLine{Kind: "note", Text: "Chat: " + short(text, 300)})
				}
			}
		}
	}
	if len(out) > maxFeedLines {
		out = out[len(out)-maxFeedLines:]
	}
	return out
}

// answersIn is the answers a pane's answer directory has, the last file
// first: a hook names them its conversation, and then its count.
func answersIn(dir string) []proto.FeishuLine {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entries[i].Name()))
		if err != nil {
			continue
		}
		if a := answersTranscript(data); len(a) > 0 {
			return a
		}
	}
	return nil
}

// transcript is an agent's transcript from its file.
func transcript(agent string, data []byte) []proto.FeishuLine {
	switch agent {
	case "claude":
		return claudeTranscript(data)
	case "codex":
		return codexTranscript(data)
	}
	return nil
}

// sessionDir is a session's directory: its pane's.
func sessionDir(s *proto.Session) string {
	if s.Pane.Dir != "" {
		return s.Pane.Dir
	}
	for _, p := range s.Panes {
		if p.Dir != "" {
			return p.Dir
		}
	}
	return ""
}

// transcriptFile finds the file of an agent's conversation: the newest
// file that Claude Code keeps for the directory, which resumes the
// directory's conversation after kerod loses the ID.
func transcriptFile(conv, agent, dir string) string {
	if agent == "claude" {
		return newestTranscript(filepath.Join(claudeHome(), "projects"), encodeDir(dir))
	}
	return ""
}

// claudeHome is Claude Code's directory, as its hook is told.
func claudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude")
}

// encodeDir is Claude Code's name of a directory in its projects: every
// character that is not a letter, a digit or "-", as "-".
func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func encodeDir(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func newestTranscript(root, dir string) string {
	found, when := "", time.Time{}
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if st, err := e.Info(); err == nil && !st.ModTime().Before(when) {
			found, when = filepath.Join(root, dir, e.Name()), st.ModTime()
		}
	}
	return found
}
