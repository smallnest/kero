package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRestoreMark checks that a stop that no client asked for settles the
// restore: no line, and a mark from now. And that a stop a client asked for
// after a settle marks once, however many sessions come back.
func TestRestoreMark(t *testing.T) {
	d := &Daemon{dir: t.TempDir()}
	os.MkdirAll(filepath.Join(d.dir, dirSessions), 0o700)
	var markFile string
	read := func() int64 {
		b, _ := os.ReadFile(markFile)
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	sess := func(n int) *session {
		s := &session{}
		for i := 0; i < n; i++ {
			s.panes = append(s.panes, testPane(t))
		}
		return s
	}
	dir := t.TempDir()
	mkdirAll := func(path string) error { return os.MkdirAll(filepath.Join(dir, path), 0o700) }
	if err := mkdirAll(dirSessions); err != nil {
		t.Fatal(err)
	}
	d.dir = dir
	markFile = filepath.Join(dir, dirSessions, restoreMark)
	before := time.Now().UnixMilli()
	d.restoreN, d.lastStopped = 10, 20
	d.noteRestore(sess(2), "then")
	if n := read(); n < before {
		t.Errorf("a stop no client asked for marked %d, not now", n)
	}
	d.noteRestore(sess(1), "then")
	if n := read(); n != before && n < before {
		t.Errorf("a settled restore did not mark again: %d", n)
	}
	// The daemon read the mark file: the stop that comes is asked for.
	d.restoreN, d.lastStopped, d.restoredAt = read(), 0, 0
	d.noteRestore(sess(2), "Mon")
	want := d.markWhen
	d.noteRestore(sess(1), "Mon")
	d.markDone()
	if n := read(); n <= before {
		t.Errorf("a stop a client asked for did not mark: %d", n)
	}
	// Marked now, though a restore right after another.
	d.restoreN, d.lastStopped, d.restoredAt = read(), 0, 0
	d.noteRestore(sess(1), "Mon")
	d.markDone()
	if n := read(); n <= before {
		t.Errorf("a mark right after another does not overgo the last: %d", n)
	}
	if want != "Mon" {
		t.Errorf("the mark does not name the restore: %q", want)
	}
}

// TestLeftover checks that a parent that is not Kero is no Kero that runs
// while this one does.
func TestLeftover(t *testing.T) {
	if leftover(os.Getpid()) || leftover(1) || leftover(0) || leftover(-1) {
		t.Error("leftover reports this test, init or nothing as a Kero")
	}
	if !leftover(os.Getppid()) {
		t.Log("the parent is not a Kero, as a test runner's")
	}
}

// TestMarkDrawn checks that a mark since restore loaded its own is drawn,
// and one from before is not.
func TestMarkDrawn(t *testing.T) {
	d := &Daemon{dir: t.TempDir()}
	os.MkdirAll(filepath.Join(d.dir, dirSessions), 0o700)
	if d.markDrawn() {
		t.Error("no mark, yet one is drawn")
	}
	d.restoreN = time.Now().UnixMilli()
	d.markSettled()
	if d.markDrawn() {
		t.Error("a mark of now is drawn, as the restore's")
	}
	d.restoreN -= 1000
	if !d.markDrawn() {
		t.Error("a mark after the restore's is not drawn")
	}
}
