//go:build unix

package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// A command that starts a process group of its own, which a stop ends
// with it.
func TestKillGroup(t *testing.T) {
	out := t.TempDir() + "/pid"
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo $! > "+out+"; wait")
	startGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	group, _ := syscall.Getpgid(cmd.Process.Pid)
	if group != cmd.Process.Pid {
		t.Fatalf("the group is %d, the command %d", group, cmd.Process.Pid)
	}
	var child int
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(out); err == nil {
			child, _ = strconv.Atoi(string(b[:len(b)-1]))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("no child")
	}
	killGroup(cmd)
	cmd.Wait()
	for i := 0; i < 50; i++ {
		if syscall.Kill(child, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the child %d lives", child)
}
