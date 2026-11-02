package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"kero/internal/client"
	"kero/internal/proto"
)

// answerCmd is what an agent runs as each turn ends, by the Stop hook
// `kero skill setup` installs: it tells the Feishu bot the turn's answer,
// for a chat that drives the agent, where the terminal's text hides it.
// The bot gives it a file for the answer with the prompt; without one, it
// does nothing, as it does outside Kero, and on any trouble: it never
// fails the agent.
func answerCmd(args []string) error {
	agent := ""
	if len(args) > 0 {
		agent = args[0]
	}
	pane, sid := os.Getenv("KERO_PANE"), os.Getenv("KERO_SESSION")
	var in struct {
		Event   string `json:"hook_event_name"`
		AgentID string `json:"agent_id"`   // set for Claude Code's subagents
		Session string `json:"session_id"` // the agent's own
		Last    string `json:"last_assistant_message"`
		Reason  string `json:"reason"` // "stop" ends a turn, others ask
	}
	json.NewDecoder(io.LimitReader(os.Stdin, 16<<20)).Decode(&in)
	if in.Event == "" {
		in.Event = "Stop" // a hook that names no event, as pi's and opencode's do
	}
	if pane == "" || in.Event != "Stop" || in.AgentID != "" || in.Reason != "" && in.Reason != "stop" {
		return nil
	}
	c, err := client.Connect(true)
	if err != nil {
		return nil
	}
	defer c.Close()
	if in.Session != "" {
		// The pane may be another's, as for the hook at start: kerod tells
		// the pane that runs the agent's session, while one does.
		var r proto.AgentSessionResult
		if c.Call("pane.agentSession", proto.AgentSessionParams{PID: os.Getpid(), Agent: agent, Session: in.Session}, &r) != nil {
			return nil
		}
		pane, sid = r.Pane, r.Session
	}
	var st proto.State
	if c.Call("state", nil, &st) != nil {
		return nil
	}
	var file string
	for _, s := range st.Sessions {
		if s.ID != sid {
			continue
		}
		for _, p := range s.Panes {
			if p.ID == pane {
				file = p.FeishuAnswer
			}
		}
	}
	if file == "" {
		return nil
	}
	text := in.Last
	if text == "" {
		text = transcriptAnswer(agent, in.Session)
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	file, err = answerPath(file)
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil
	}
	if err := writeFileAtomic(file, []byte(strings.TrimSpace(text)+"\n")); err != nil {
		fmt.Fprintln(os.Stderr, "kero hook answer:", err)
	}
	return nil
}

// answers is where the bot keeps the panes' answer directories, kerod's
// feishu-answers.
var answers = filepath.Join(proto.Dir(), "feishu-answers")

// answerPath is the answer file the bot gives: a file of its own, in
// kerod's directory, and not one the bot gives another agent.
func answerPath(file string) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	root := answers
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if filepath.Dir(dir) != root || !strings.HasPrefix(filepath.Base(dir), "feishu-answers-") {
		return "", fmt.Errorf("%s: not a file the bot gives", file)
	}
	return abs, nil
}

// transcriptAnswer is a turn's answer from the agent's transcript, the
// last message the agent wrote, for an agent whose hook gives none.
func transcriptAnswer(agent, session string) string {
	dir := ""
	if a, ok := answerTranscript[agent]; ok {
		dir = a(session)
	}
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(dir)
	if err != nil {
		return ""
	}
	var last []string
	for _, line := range bytes.Split(b, []byte("\n")) {
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Message.Role != "assistant" {
			continue
		}
		if e.Type != "" && e.Type != "assistant" {
			continue
		}
		var text []string
		switch content := e.Message.Content.(type) {
		case string:
			text = append(text, content)
		case []any:
			for _, part := range content {
				if p, ok := part.(map[string]any); ok {
					if s, ok := p["text"].(string); ok && p["type"] == "text" {
						text = append(text, s)
					}
				}
			}
		}
		last = text
	}
	return strings.Join(last, "\n")
}

// answerTranscript tells where an agent keeps the transcript of session.
var answerTranscript = map[string]func(session string) string{
	"claude": func(session string) string {
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(proto.Home(), ".claude")
		}
		return findTranscript(filepath.Join(dir, "projects"), session)
	},
	"codex": func(session string) string {
		dir := os.Getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(proto.Home(), ".codex")
		}
		return findTranscript(filepath.Join(dir, "sessions"), session)
	},
}

// findTranscript finds the file of an agent's session under dir, whose
// path has the session's ID in it.
func findTranscript(dir, session string) string {
	if session == "" {
		return ""
	}
	var found string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		if strings.Contains(filepath.Base(path), session) && strings.HasSuffix(path, ".jsonl") {
			found = path
		}
		return nil
	})
	return found
}
