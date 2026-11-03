//go:build darwin

package daemon

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"kero/internal/settings"
)

// A launchd job's PATH keeps its own places first, and gets the
// account's, each once, after them.
func TestWithHomePaths(t *testing.T) {
	got := withHomePaths("/usr/bin:/bin")
	if !strings.HasPrefix(got, "/usr/bin:/bin:") {
		t.Fatalf("the PATH is %q", got)
	}
	if strings.Count(got, ":/bin:") > 1 {
		t.Errorf("a place of its own comes again: %q", got)
	}
	again := withHomePaths(got)
	if again != got {
		t.Errorf("the account's places come again: %q, then %q", got, again)
	}
}

// A program a shell started that outlives it and holds its output open,
// as a profile's prompt does, does not hold the read past the shell.
func TestShellOutputOfAProgramThatOutlivesIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The command ends, its program keeps the output open.
	runShell(ctx, "/bin/sh", "-c", "sleep 4 & printf hi")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the read took %v, waiting for the shell's program", took)
	}
}

// The places the agents install to are on the PATH kerod runs its
// commands with, as a chat's background prompt runs an agent by name.
func TestHomePathsCoverTheAgents(t *testing.T) {
	places := homePaths()
	for _, want := range []string{
		filepath.Join(settings.UserHome(), ".opencode", "bin"),
		filepath.Join(settings.UserHome(), ".local", "bin"),
		"/opt/homebrew/bin",
	} {
		if !slices.Contains(places, want) {
			t.Errorf("%s is not among %q", want, places)
		}
	}
}
