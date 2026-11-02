package daemon

import (
	"strings"
	"testing"
)

// A launchd job's PATH keeps its own places first, and gets the
// account's, each once, after them.
func TestWithHomePaths(t *testing.T) {
	got := withHomePaths("/usr/bin:/bin")
	if !strings.HasPrefix(got, "/usr/bin:/bin:") {
		t.Fatalf("the PATH %q", got)
	}
	// The places it adds each once, after its own.
	own := strings.Split("/usr/bin:/bin", ":")
	seen := map[string]bool{}
	for _, p := range own {
		seen[p] = true
	}
	for _, p := range strings.Split(strings.TrimPrefix(got, "/usr/bin:/bin:"), ":") {
		if seen[p] {
			t.Errorf("a place twice: %q", got)
			break
		}
		seen[p] = true
	}
	for _, p := range homePaths() {
		if !seen[p] {
			t.Errorf("no %s: %q", p, got)
		}
	}
	again := withHomePaths(got)
	if again != got {
		t.Errorf("a second time: %q", again)
	}
}

// The versions sort as numbers, not as words.
func TestByVersion(t *testing.T) {
	got := byVersion([]string{"n/v9.1.0/bin", "n/v20.0.0/bin", "n/v2.11.1/bin"})
	want := "n/v2.11.1/bin n/v9.1.0/bin n/v20.0.0/bin"
	if strings.Join(got, " ") != want {
		t.Errorf("the order %q", got)
	}
}

// The bot's commands run in the app's environment, as a pane's: the
// variables the app starts from launchd without, as the agent's provider
// settings, are the app's.
func TestBotEnvFromApp(t *testing.T) {
	d := &Daemon{}
	d.appEnv = []string{"PATH=/usr/bin", "KERO_TEST_PROVIDER=yes", "KERO_TEST_KEY=app's"}
	env := strings.Join(d.botEnv(), "\n")
	if !strings.Contains(env, "KERO_TEST_PROVIDER=yes") || !strings.Contains(env, "KERO_TEST_KEY=app's") {
		t.Errorf("the bot's environment is %s", env)
	}
}
