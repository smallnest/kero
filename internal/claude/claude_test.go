package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The last request gives the context and the plan's limits; the session
// sums its requests, each answer's parts once.
func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":100,"cache_creation_input_tokens":1000,"cache_read_input_tokens":2000,"output_tokens":50}}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":100,"cache_creation_input_tokens":1000,"cache_read_input_tokens":2000,"output_tokens":50}}}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-opus-5","usage":{"input_tokens":10,"cache_read_input_tokens":213402,"output_tokens":300},"contextWindow":1000000,"rateLimits":{"five_hour":{"utilization":53,"resets_at":"2026-10-31T02:49:00Z"},"seven_day":{"utilization":0.37,"resets_at":"2026-11-04T10:59:00Z"}}}}`,
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	r, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	u := r.Usage
	if u.Context != 213712 || u.Window != 1_000_000 || u.Model != "claude-opus-5" {
		t.Errorf("context %d of %d, model %q", u.Context, u.Window, u.Model)
	}
	if want := 100 + 1000 + 2000 + 50 + 10 + 213402 + 300; u.Session != want {
		t.Errorf("session %d, want %d", u.Session, want)
	}
	q := u.Quota
	if q == nil || q.Session != 0.53 || q.Weekly != 0.37 {
		t.Fatalf("quota %+v", q)
	}
	if !q.SessionResets.Equal(time.Date(2026, 10, 31, 2, 49, 0, 0, time.UTC)) {
		t.Errorf("session resets %v", q.SessionResets)
	}
}

// A window is a model's own, which Claude Code does not report.
func TestWindow(t *testing.T) {
	if got := windowOf("claude-sonnet-4-5[1m]"); got != window1m {
		t.Errorf("1M window %d", got)
	}
	if got := windowOf("claude-opus-5"); got != window200k {
		t.Errorf("window %d", got)
	}
}

func TestProjectDir(t *testing.T) {
	if got := ProjectDir("/Users/me/ai/ledger-x"); got != "-Users-me-ai-ledger-x" {
		t.Errorf("dir %q", got)
	}
	if got := ProjectDir("/Users/me/my.note"); got != "-Users-me-my-note" {
		t.Errorf("dir %q", got)
	}
}

func TestHuman(t *testing.T) {
	for in, want := range map[int]string{0: "0", 412: "412", 213412: "213k", 3612004: "3.6M", 2_000_000: "2M"} {
		if got := Human(in); got != want {
			t.Errorf("%d: %q, want %q", in, got, want)
		}
	}
}
