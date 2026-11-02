package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"kero/internal/detect"
	"kero/internal/proto"
)

// Feishu is the configuration's Feishu keys: the app kerod connects to,
// who may use it, and what a chat's sessions run. The App Secret is not
// among them, as the configuration file is often copied and synced: kerod
// keeps it in FeishuSecretPath.
type Feishu struct {
	App     string   `json:"app,omitempty"`     // cli_…, the app's ID
	Allow   []string `json:"allow,omitempty"`   // open_id, user_id or email, or *
	Timeout int64    `json:"timeout,omitempty"` // ms, a turn's
	Stream  int64    `json:"stream,omitempty"`  // ms, a card's updates

	// This machine's: the agent and directory new sessions take, and the
	// command each agent runs, by the name detect knows it by.
	Agent  string            `json:"agent,omitempty"` // "default" or an agent's name
	Dir    string            `json:"dir,omitempty"`
	Agents map[string]string `json:"agents,omitempty"`
	// Run tells how a prompt runs the agent: a session's pane ("session"),
	// as a user's turn, or the agent's command in the background
	// ("command"), as claude -p.
	Run string `json:"run,omitempty"`
	// Host names the machines whose bots on this app hear its events, as
	// their hostnames: only the one that answers a message does, and the
	// others leave it. Empty, or one: every message is this machine's.
	Host []string `json:"host,omitempty"`
}

// The ways a prompt runs its agent.
const (
	FeishuRunSession = "session"
	FeishuRunCommand = "command"
)

// Background is the agent and directory a prompt runs in the background:
// the run's, and its fallbacks.
func (f *Feishu) Background() (agent, dir string) {
	if f == nil {
		return "", ""
	}
	if f.Agent == "default" {
		return "", f.Dir
	}
	return f.Agent, f.Dir
}

// Feishu defaults, as Load fills them in.
const (
	FeishuDefaultTimeout = 30 * time.Minute
	FeishuDefaultStream  = 2 * time.Second
)

// FeishuSet says whether Feishu is on: an app ID is set.
func FeishuSet(f *Feishu) bool { return f != nil && f.App != "" }

// Allowed says whether a person may use the bot: anyone if Allow names *,
// else those it names by open_id, user_id or email, and nobody else.
func (f *Feishu) Allowed(openID, userID, email string) bool {
	for _, a := range f.Allow {
		if a == "*" || (a != "" && (a == openID || a == userID || a == email)) {
			return true
		}
	}
	return false
}

// AgentCommand is the command a new session runs the agent by, split into
// its words, or nil for the agent's program alone.
func (f *Feishu) AgentCommand(agent string) []string {
	var cmd []string
	for _, w := range strings.Fields(f.Agents[agent]) {
		if len(cmd) == 0 && w == "~" || strings.HasPrefix(w, "~/") {
			w = UserHome() + strings.TrimPrefix(w, "~")
		}
		cmd = append(cmd, w)
	}
	return cmd
}

// UserHome is the account's home directory, as ~ expands it.
func UserHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// FeishuSecret is what kero feishu setup asks for.
type FeishuSecret struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	// Me is the open_id of the person who ran setup, the only one allowed
	// until feishu-allow names others.
	Me string `json:"me,omitempty"`
}

// FeishuSecretPath is where the App Secret goes: next to kerod's lock and
// log, readable by this account only.
func FeishuSecretPath() string { return filepath.Join(proto.Dir(), "feishu.json") }

// LoadFeishuSecret reads the App Secret; errNotConfigured when there is
// none.
func LoadFeishuSecret() (FeishuSecret, error) {
	var s FeishuSecret
	b, err := os.ReadFile(FeishuSecretPath())
	if errors.Is(err, os.ErrNotExist) {
		return s, ErrFeishuNotConfigured
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", FeishuSecretPath(), err)
	}
	return s, nil
}

// SaveFeishuSecret writes the App Secret, readable by this account only.
func SaveFeishuSecret(s FeishuSecret) error {
	if s.AppID == "" || s.AppSecret == "" {
		return errors.New("the app's ID and secret are both needed")
	}
	if err := os.MkdirAll(filepath.Dir(FeishuSecretPath()), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := FeishuSecretPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, FeishuSecretPath())
}

// ErrFeishuNotConfigured is no App Secret kept: kero feishu setup writes it.
var ErrFeishuNotConfigured = errors.New("Feishu is not set up: run `kero feishu setup`")

// SetFeishu writes the Feishu keys of the configuration, keeping the
// others and the file's comments and order.
func SetFeishu(f Feishu) error {
	s := Load()
	s.Feishu = &f
	s.normalize()
	return Save(s)
}

// ValidateFeishu tells what is wrong with the feishu-* keys, for setup
// and kero config check.
func ValidateFeishu(f *Feishu) error {
	if f == nil || f.App == "" {
		return nil
	}
	var bad []string
	if strings.ContainsAny(f.App, " \t=") {
		bad = append(bad, fmt.Sprintf("feishu-app: %q is not an app ID, as cli_a1b2c3d4", f.App))
	}
	if f.Timeout != 0 && f.Timeout < int64(time.Second) {
		bad = append(bad, fmt.Sprintf("feishu-timeout: %s is shorter than a second", time.Duration(f.Timeout)))
	}
	if f.Stream != 0 && f.Stream < int64(time.Second) {
		bad = append(bad, fmt.Sprintf("feishu-stream: %s is shorter than a second", time.Duration(f.Stream)))
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// normalizeFeishu fills in the defaults and drops what no key takes.
func normalizeFeishu(f *Feishu) {
	if f == nil {
		return
	}
	if f.Timeout <= 0 {
		f.Timeout = int64(FeishuDefaultTimeout)
	}
	if f.Stream <= 0 {
		f.Stream = int64(FeishuDefaultStream)
	}
	if f.Agent == "" {
		f.Agent = "default"
	}
	if f.Run == "" {
		f.Run = FeishuRunSession
	}
	allow := slices.DeleteFunc(append([]string{}, f.Allow...), func(a string) bool { return a == "" })
	f.Allow = slices.Compact(slices.Sorted(slices.Values(allow)))
	if len(f.Agents) == 0 {
		f.Agents = nil
	}
	host := slices.DeleteFunc(append([]string{}, f.Host...), func(h string) bool { return h == "" })
	f.Host = slices.Compact(slices.Sorted(slices.Values(host)))
}

// ParseDuration reads a duration as the feishu-timeout key writes it:
// 30m, 1h30m, 90s.
func ParseDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration, as 30m or 1h", v)
	}
	if d < time.Second {
		return 0, fmt.Errorf("%q is shorter than a second", v)
	}
	return d, nil
}

// FormatDuration writes a duration as the configuration writes it: 30m,
// 1h30m, 90s.
func FormatDuration(d time.Duration) string { return d.Round(time.Second).String() }

// feishuCardKeys are the keys of a Feishu turn's card, after the
// feishu-* keys.
func feishuCardKeys() []key {
	return []key{
		boolKey("feishu-cards", "show a turn from Feishu as a card that updates while the agent works, in the state's color, rather than as text messages when it ends", func(s *Settings) *bool { return &s.FeishuCards }),
		boolKey("feishu-markdown", "the cards' text as Markdown", func(s *Settings) *bool { return &s.FeishuMarkdown }),
		{name: "feishu-host", doc: "the machines whose Kero bots, on this app, hear its messages: only one answers each, and the others leave it (comma separated hostnames); empty for every message to be this machine's",
			get: func(s *Settings) []string {
				if s.Feishu == nil {
					return []string{""}
				}
				return []string{strings.Join(s.Feishu.Host, ",")}
			},
			set: func(s *Settings, v []string) error {
				var hosts []string
				for _, h := range strings.Split(v[len(v)-1], ",") {
					if h = strings.TrimSpace(h); h != "" {
						if strings.ContainsAny(h, " \t=") {
							return fmt.Errorf("%q is not a hostname", h)
						}
						hosts = append(hosts, h)
					}
				}
				if s.Feishu == nil {
					s.Feishu = &Feishu{Timeout: int64(FeishuDefaultTimeout), Stream: int64(FeishuDefaultStream), Agent: "default", Run: FeishuRunSession}
				}
				s.Feishu.Host = hosts
				return nil
			}},
		boolKey("feishu-open-button", "a card's button opens its session in Kero", func(s *Settings) *bool { return &s.FeishuOpenButton }),
		boolKey("feishu-card-body", "send a card as its body's JSON, as an update takes it (try it if Feishu shows no card, or does not update one)", func(s *Settings) *bool { return &s.FeishuCardBody }),
	}
}

// feishuKeys are the configuration's Feishu keys, after host's and
// tailcat's.
func feishuKeys() []key {
	ensure := func(s *Settings) *Feishu {
		if s.Feishu == nil {
			s.Feishu = &Feishu{Timeout: int64(FeishuDefaultTimeout), Stream: int64(FeishuDefaultStream), Agent: "default", Run: FeishuRunSession}
		}
		return s.Feishu
	}
	duration := func(name, doc string, f func(*Feishu) *int64) key {
		return key{name: name, doc: doc,
			get: func(s *Settings) []string { return []string{FormatDuration(time.Duration(*f(ensure(s))))} },
			set: func(s *Settings, v []string) error {
				d, err := ParseDuration(v[len(v)-1])
				if err != nil {
					return err
				}
				*f(ensure(s)) = int64(d)
				return nil
			}}
	}
	return []key{
		{name: "feishu-app", doc: "the ID of the Feishu app the sessions take prompts from, cli_a1b2c3d4: empty for off; its secret is not here, but in kero's directory, as kero feishu setup writes it",
			get: func(s *Settings) []string {
				if s.Feishu == nil {
					return []string{""}
				}
				return []string{s.Feishu.App}
			},
			set: func(s *Settings, v []string) error {
				app := strings.TrimSpace(v[len(v)-1])
				if strings.ContainsAny(app, " \t=") {
					return fmt.Errorf("%q is not an app ID, as cli_a1b2c3d4", app)
				}
				if app != "" {
					ensure(s).App = app
					return nil
				}
				if s.Feishu != nil {
					f := *s.Feishu
					f.App = ""
					s.Feishu = &f
				}
				return nil
			}},
		{name: "feishu-allow", doc: "who may send prompts from Feishu: an open_id, user_id or email, or * for anyone who reaches the bot; repeat it, and leave it empty for only the person who ran kero feishu setup", list: true,
			get: func(s *Settings) []string {
				if s.Feishu == nil || len(s.Feishu.Allow) == 0 {
					return []string{""}
				}
				return slices.Clone(s.Feishu.Allow)
			},
			set: func(s *Settings, v []string) error {
				f := ensure(s)
				f.Allow = slices.DeleteFunc(append([]string{}, v...), func(a string) bool { return strings.TrimSpace(a) == "" })
				for i := range f.Allow {
					f.Allow[i] = strings.TrimSpace(f.Allow[i])
				}
				return nil
			}},
		duration("feishu-timeout", "the longest a turn from Feishu waits before its reply says it timed out", func(f *Feishu) *int64 { return &f.Timeout }),
		duration("feishu-stream", "how often the reply card of a working turn takes the new text", func(f *Feishu) *int64 { return &f.Stream }),
		{name: "feishu-agent", doc: "the agent a new session from Feishu runs: default for the new session's program, or an agent's name, as claude, codex, opencode or pi",
			get: func(s *Settings) []string { return []string{ensure(s).Agent} },
			set: func(s *Settings, v []string) error {
				a := strings.TrimSpace(v[len(v)-1])
				if a != "default" && !detect.Known(a) {
					return fmt.Errorf("%q is not default or an agent Kero knows", a)
				}
				ensure(s).Agent = a
				return nil
			}},
		{name: "feishu-run", doc: "how a prompt runs its agent: session, in the session's pane as a user's turn; or command, as the agent's own command in the background (claude -p), which tells the reply and no terminal shows",
			get: func(s *Settings) []string { return []string{ensure(s).Run} },
			set: func(s *Settings, v []string) error {
				r := strings.TrimSpace(v[len(v)-1])
				if r != FeishuRunSession && r != FeishuRunCommand {
					return fmt.Errorf("%q is not session or command", r)
				}
				ensure(s).Run = r
				return nil
			}},
		{name: "feishu-dir", doc: "where a new session from Feishu starts when the message names no project; empty for new-session-directory",
			get: func(s *Settings) []string { return []string{ensure(s).Dir} },
			set: func(s *Settings, v []string) error {
				ensure(s).Dir = strings.TrimSpace(v[len(v)-1])
				return nil
			}},
		{name: "feishu-agent-command", doc: "the command a new session from Feishu runs an agent by, as name=claude --permission-mode acceptEdits; repeat it, and leave an agent out to run its program with no arguments", list: true,
			get: func(s *Settings) []string {
				if s.Feishu == nil || len(s.Feishu.Agents) == 0 {
					return []string{""}
				}
				out := make([]string, 0, len(s.Feishu.Agents))
				for _, a := range slices.Sorted(maps.Keys(s.Feishu.Agents)) {
					out = append(out, a+"="+s.Feishu.Agents[a])
				}
				return out
			},
			set: func(s *Settings, v []string) error {
				agents := map[string]string{}
				for _, line := range v {
					if strings.TrimSpace(line) == "" {
						continue
					}
					name, cmd, ok := strings.Cut(line, "=")
					name = strings.TrimSpace(name)
					if !ok || !detect.Known(name) {
						return fmt.Errorf("%q is not name=command, with an agent Kero knows", line)
					}
					if strings.TrimSpace(cmd) == "" {
						return fmt.Errorf("%s= names no command", name)
					}
					agents[name] = strings.TrimSpace(cmd)
				}
				f := ensure(s)
				if len(agents) == 0 {
					agents = nil
				}
				f.Agents = agents
				return nil
			}},
	}
}
