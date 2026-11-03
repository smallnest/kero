package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
