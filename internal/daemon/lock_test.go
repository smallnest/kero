package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"kero/internal/proto"
)

// kerod empties a lock it leaves as it exits, or before the kerod that
// takes over from it starts, and writes its pid in it at that start:
// kero daemon stop kills the kerod a lock names when it does not answer.
func TestClearLock(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	lock := filepath.Join(proto.Dir(), "kerod.lock")
	if err := os.WriteFile(lock, []byte("99999"), 0o600); err != nil {
		t.Fatal(err)
	}
	ClearLock()
	if b, err := os.ReadFile(lock); err != nil || len(b) != 0 {
		t.Errorf("lock after ClearLock: %q, %v", b, err)
	}
	// One that runs names it as it starts.
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("4000000"), 0)
	if b, _ := os.ReadFile(lock); string(b) != "4000000" {
		t.Errorf("lock after a kerod's write: %q", b)
	}
}
