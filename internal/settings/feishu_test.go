package settings

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The feishu-* keys read and write the configuration, with defaults
// filled in, and the App Secret stays out of it.
func TestFeishuKeys(t *testing.T) {
	file := `# mine
feishu-app = cli_a1b2c3d4
feishu-allow = alice@example.com
feishu-allow = *
feishu-timeout = 1h30m
feishu-run = command
feishu-agent = pi
feishu-agent-command = claude=claude --permission-mode acceptEdits
feishu-agent-command = pi=~/bin/pi --model fast
`
	s, probs := parse(file)
	if len(probs) != 0 {
		t.Fatalf("problems %q", probs)
	}
	if !FeishuSet(s.Feishu) || s.Feishu.App != "cli_a1b2c3d4" {
		t.Fatalf("app %+v", s.Feishu)
	}
	if s.Feishu.Run != FeishuRunCommand {
		t.Errorf("run %q", s.Feishu.Run)
	}
	if s.Feishu.Timeout != int64(90*time.Minute) || s.Feishu.Stream != int64(FeishuDefaultStream) {
		t.Errorf("timeouts %v %v", time.Duration(s.Feishu.Timeout), time.Duration(s.Feishu.Stream))
	}
	if got := s.Feishu.AgentCommand("pi"); len(got) != 3 || got[1] != "--model" || !strings.HasSuffix(got[0], "/bin/pi") {
		t.Errorf("pi runs %q", got)
	}
	if !s.Feishu.Allowed("ou_1", "", "") {
		t.Error("* doesn't allow everyone")
	}
	out := format(s, file)
	for _, want := range []string{"# mine", "feishu-app = cli_a1b2c3d4", "feishu-timeout = 1h30m",
		"feishu-agent-command = claude=claude --permission-mode acceptEdits", "feishu-allow = *"} {
		if !strings.Contains(out, want) {
			t.Errorf("the file lacks %q:\n%s", want, out)
		}
	}

	// Nobody allowed but who set it up.
	s.Feishu.Allow = []string{"ou_me"}
	if s.Feishu.Allowed("ou_other", "on_1", "bob@example.com") {
		t.Error("an open_id allows another person")
	}
	if !s.Feishu.Allowed("", "ou_me", "") {
		t.Error("an allowed open_id isn't allowed")
	}

	// Undoing it leaves the rest.
	s.Feishu.App = ""
	s.normalize()
	if FeishuSet(s.Feishu) {
		t.Error("no app is on")
	}
}

// A bad agent, duration or app is a problem in the file.
func TestFeishuKeysRejectBadValues(t *testing.T) {
	for _, line := range []string{"feishu-agent = nosuchagent", "feishu-timeout = soon", "feishu-app = two words",
		"feishu-agent-command = nosuchagent=x", "feishu-stream = 0s", "feishu-run = pane"} {
		if _, probs := parse(line + "\n"); len(probs) != 1 {
			t.Errorf("%q: problems %q", line, probs)
		}
	}
}

// The App Secret goes in kero's directory, readable by this account only,
// and the configuration never holds it.
func TestFeishuSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KERO_HOME", dir)
	t.Setenv("KERO_CONFIG", filepath.Join(dir, "config"))
	if _, err := LoadFeishuSecret(); err != ErrFeishuNotConfigured {
		t.Fatalf("no secret reads %v", err)
	}
	want := FeishuSecret{AppID: "cli_a", AppSecret: "s3cret", Me: "ou_me"}
	if err := SaveFeishuSecret(want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(FeishuSecretPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the secret is %v", st.Mode().Perm())
	}
	got, err := LoadFeishuSecret()
	if err != nil || got != want {
		t.Fatalf("read %+v, %v", got, err)
	}
	if err := SetFeishu(Feishu{App: "cli_a"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if strings.Contains(string(b), "s3cret") {
		t.Error("the configuration holds the secret")
	}
	if !slices.ContainsFunc(strings.Split(string(b), "\n"), func(l string) bool { return l == "feishu-app = cli_a" }) {
		t.Errorf("the configuration lacks feishu-app:\n%s", b)
	}
}
