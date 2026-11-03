package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kero/internal/proto"
)

// TestStreamAsks keeps the lines that ask for a permission apart from a
// command's output.
func TestStreamAsks(t *testing.T) {
	dir := t.TempDir()
	out, _ := os.OpenFile(filepath.Join(dir, "out"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	asksFile := filepath.Join(dir, "out.asks")
	piped, err := streamAsks(out, asksFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash"}}`,
		`{"type":"control_request","request_id":"r9","request":{"subtype":"initialize"}}`,
		`{"type":"result","result":"done"}`,
	}
	piped.WriteString(strings.Join(lines, "\n") + "\n")
	piped.Close()
	// The file gives all of what the pipe took at once, as a poll may
	// read it before the split does.
	// A turn's end, which the split's last lines take a moment after.
	time.Sleep(time.Second)
	main, _ := os.ReadFile(out.Name())
	asks, _ := os.ReadFile(asksFile)
	if got := strings.Count(string(main), "\n"); got != 3 || !strings.Contains(string(main), `"request_id":"r9"`) {
		t.Fatalf("the output: %d lines out %v file %q in %v", got, out, main, piped)
	}
	if got := strings.Count(string(asks), "control_request"); got != 1 || !strings.Contains(string(asks), `"r1"`) {
		t.Fatalf("the asks: %s", asks)
	}
}

// A command asks through its input only if it says so: one that does not,
// as OpenCode's run, would take a pipe that never ends for a prompt of its
// own, and wait for it.
func TestAsksOnStdin(t *testing.T) {
	asking := [][]string{
		{"claude", "--permission-prompt-tool", "stdio", "--output-format", "stream-json", "hi"},
		{"claude", "--permission-prompt-tool=stdio", "hi"},
	}
	for _, cmd := range asking {
		if !asksOnStdin(cmd) {
			t.Errorf("%q asks not through its input", cmd)
		}
	}
	plain := [][]string{
		{"opencode", "run", "hi"},
		{"claude", "--output-format", "stream-json", "--verbose", "hi"},
		{"codex", "exec", "hi"},
	}
	for _, cmd := range plain {
		if asksOnStdin(cmd) {
			t.Errorf("%q takes an input that never ends", cmd)
		}
	}
}

// A command that reads no input ends at once, rather than wait for a pipe
// that nothing closes.
func TestBackgroundReadsNoInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no cat")
	}
	d := testDaemon(t)
	s, err := d.create(proto.CreateParams{Name: "bg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.setBackground(s.ID); err != nil {
		t.Fatal(err)
	}
	luna := &botLuna{d: d}
	out := filepath.Join(t.TempDir(), "out")
	if err := luna.Call("agent.background", proto.BackgroundParams{ID: s.ID, Out: out, Command: []string{"cat"}}, nil); err != nil {
		t.Fatal(err)
	}
	var o proto.Output
	for i := 0; i < 60 && !o.Ended; i++ {
		time.Sleep(50 * time.Millisecond)
		if err := luna.Call("agent.output", proto.OutputParams{ID: s.ID, From: o.From}, &o); err != nil {
			t.Fatal(err)
		}
	}
	if !o.Ended {
		t.Fatal("a command that reads no input never ended")
	}
}
