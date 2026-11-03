//go:build darwin

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
