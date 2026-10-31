package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// listeningPorts tells the ports of the pids it asks of alone, a service
// Kero shares excluded, and each port once.
func TestListeningPorts(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "lsof")
	script := `#!/bin/sh
printf '%s\n' p100 f3 n'*:7000' f4 n'*:*' p200 f5 n127.0.0.1:3000 f6 n127.0.0.1:3000 f7 n'*:8080' f8 n10.0.0.1:443-\>1.2.3.4:55
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LSOF", fake)
	got := listeningPorts([]int{200})
	if len(got[100]) != 0 || len(got[200]) != 2 || got[200][0] != 3000 || got[200][1] != 8080 {
		t.Errorf("listeningPorts = %v", got)
	}
}
