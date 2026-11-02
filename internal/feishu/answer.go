package feishu

import (
	"os"
	"path/filepath"
	"strings"

	"kero/internal/proto"
	"kero/internal/settings"
)

// The answer file is the agent's Stop hook's (kero answer): the prompt
// gives the pane a file of its own, which the hook writes the turn's
// answer to as the turn ends, and the bot replies with its words, where
// the terminal's text holds the TUI's frames. M4 of the plan.

// hookAgents are the agents whose Stop hook writes the answer file, for a
// chat that drives the agent: the agents `kero skill setup` hooks, but
// Codex, whose Stop hook tells no turn's end, nor its answer.
var hookAgents = map[string]bool{"claude": true, "codex": true, "pi": true, "opencode": true}

// answersDir is where the answer files go: kerod's directory, as the
// hook, which runs outside the app, finds it.
func answersDir() string { return filepath.Join(proto.Dir(), "feishu-answers") }

// answerDir is the directory of the agent's answer files in the pane: one
// by the agent's name, and cut from one to the next, as kero answer
// checks, and as cli.answers names its root.
func answerDir(agent, pane string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, agent+"-"+pane)
	return filepath.Join(answersDir(), "feishu-answers-"+name)
}

// prepare gives the pane the directory of the turn's answer files, and
// removes those of the turns before: the turn's file, when the agent's
// hook writes it, is the one there.
func (b *Bot) prepare(pane, agent string) string {
	if !hookAgents[agent] {
		return ""
	}
	dir := answerDir(agent, pane)
	b.clear(dir)
	return dir
}

// clear removes the answer files of the turns before.
func (b *Bot) clear(dir string) {
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// answerTo gives the pane the directory of its agent's answer files.
func (b *Bot) answerTo(pane, dir string) {
	var info proto.Pane
	b.api.Call("pane.answer", proto.AnswerParams{Pane: pane, Dir: dir}, &info)
}

// answerFile is the turn's answer, as the agent's hook wrote it, the
// conversation it continued, and whether it is there: false while it is
// not. The hook writes the answer as <id>.json, the conversation's ID,
// and the bot removes the answers of the turns before as a turn starts:
// the file is the turn's.
func (b *Bot) answerFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return "", "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		return "", "", false
	}
	return strings.TrimSpace(string(data)), strings.TrimSuffix(entries[0].Name(), ".json"), true
}

// replyWithAnswer is the reply to a turn whose agent's hook wrote its
// answer: the answer, and the conversation it continued.
func (b *Bot) replyWithAnswer(r *run, head, answer string) string {
	if a := strings.TrimSpace(answer); a != "" {
		return head + a
	}
	if r.status.Status == proto.Waiting {
		return head + "The agent asks: " + orDash(r.status.Message)
	}
	return head + "The turn ended without a final message."
}

// conversationAgent is the agent of a chat's session, as a prompt takes
// it: what tells whether the agent's hook writes the answer file. What
// runs in the pane goes first: fallback, the agent of a session just
// started, then the pane's agent, then the app's agent, then the
// settings'. A prompt's agent that is none of them runs no hook.
func (b *Bot) conversationAgent(c *chat, fallback, session string) string {
	if c.agent != "" {
		return c.agent
	}
	if fallback != "" {
		return fallback
	}
	// The pane's agent, while it runs, which the chat may not know yet.
	if st, err := b.api.State(); err == nil {
		for _, s := range st.Sessions {
			if s.ID != session {
				continue
			}
			for _, p := range s.Panes {
				if p.Agent != "" {
					return p.Agent
				}
			}
		}
	}
	if b.cfg.Agent != "" && b.cfg.Agent != "default" {
		return b.cfg.Agent
	}
	if f := settings.Load().Feishu; f != nil && f.Agent != "" && f.Agent != "default" {
		return f.Agent
	}
	return ""
}
