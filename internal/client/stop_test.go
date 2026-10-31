//go:build !windows

package client

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"kero/internal/proto"
)

// daemon is a kerod of a test's: it listens on kerod's socket, holds its
// lock, and names a process in it, which it runs.
type daemon struct {
	cmd  *exec.Cmd
	path string
	ln   net.Listener
	wg   sync.WaitGroup // its connections

	mu          sync.Mutex
	conns       []net.Conn
	interrupted bool
}

// fakeDaemon starts command as a process named in kerod's lock, as
// kerod's sessions are, and listens on kerod's socket: it answers as the
// test has it, or not at all.
func fakeDaemon(t *testing.T, command string) *daemon {
	t.Helper()
	t.Setenv("KERO_HOME", t.TempDir())
	t.Setenv("KERO_SOCKET", filepath.Join(t.TempDir(), "s"))
	quiet, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{path: filepath.Join(proto.Dir(), "kerod.lock")}
	// A process named as kerod is, so that a stop that does not get an
	// answer kills it.
	d.cmd = exec.Command("/bin/bash", "-c", "exec -a 'kero "+kerodCmd+"' /bin/sh -c '"+command+"'")
	d.cmd.Stdin, d.cmd.Stdout, d.cmd.Stderr = quiet, quiet, quiet
	if err := d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if d.ln, err = net.Listen("unix", proto.SocketPath()); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockFile(f); err != nil {
		t.Fatal(err)
	}
	f.WriteString(strconv.Itoa(d.cmd.Process.Pid))
	go d.accept()
	t.Cleanup(d.stop)
	return d
}

func (d *daemon) accept() {
	for {
		c, err := d.ln.Accept()
		if err != nil {
			return
		}
		d.mu.Lock()
		answer := !d.interrupted
		d.conns = append(d.conns, c)
		d.mu.Unlock()
		if !answer {
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer c.Close()
			welcome := `{"version":3,"pid":` + strconv.Itoa(d.cmd.Process.Pid) + `,"started":1}` + "\n"
			c.Write([]byte(welcome))
		}()
	}
}

// interrupt answers as kerod does as it stops: to the connections after
// it, not at all. The process goes, as it saves its sessions.
func (d *daemon) interrupt(t *testing.T) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.interrupted = true
	for _, c := range d.conns {
		c.Close()
	}
	d.conns = nil
	if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Errorf("interrupting: %v", err)
	}
}

// stuck answers no more, as one stopped does: it holds its connections.
func (d *daemon) stuck(t *testing.T) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.interrupted = true
	if err := d.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Errorf("freezing: %v", err)
	}
}

func (d *daemon) stop() {
	d.ln.Close()
	d.wg.Wait()
	d.mu.Lock()
	for _, c := range d.conns {
		c.Close()
	}
	d.mu.Unlock()
	d.cmd.Process.Kill()
	d.cmd.Wait()
	ClearDaemon()
}

// gone reports whether the daemon's process exited.
func (d *daemon) gone() bool {
	done := make(chan struct{})
	go func() { d.cmd.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

func TestLockedPID(t *testing.T) {
	d := fakeDaemon(t, "sleep 60")
	if pid, err := LockedPID(); err != nil || pid != d.cmd.Process.Pid {
		t.Fatalf("LockedPID = %d, %v; want %d", pid, err, d.cmd.Process.Pid)
	}
}

// One that answers stops itself, as it saves its sessions.
func TestStop(t *testing.T) {
	d := fakeDaemon(t, "sleep 600")
	go d.interrupt(t)
	if err := Stop(false); err != nil {
		t.Fatal(err)
	}
	if !d.gone() {
		t.Fatal("the daemon runs")
	}
}

// One that does not answer is killed, and what it leaves taken away.
func TestStopKillsAStuckDaemon(t *testing.T) {
	d := fakeDaemon(t, "sleep 600")
	d.stuck(t)
	start := time.Now()
	if err := Stop(false); err != nil {
		t.Fatal(err)
	}
	if !d.gone() {
		t.Fatal("the daemon runs")
	}
	if took := time.Since(start); took > 2*StopTimeout() {
		t.Errorf("stopping took %v", took)
	}
	if _, err := os.Stat(proto.SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket left: %v", err)
	}
	if b, _ := os.ReadFile(d.path); len(strings.TrimSpace(string(b))) != 0 {
		t.Errorf("lock left: %q", b)
	}
}

// Force kills one that answers, too: it saves nothing.
func TestStopForce(t *testing.T) {
	d := fakeDaemon(t, "sleep 600")
	if err := Stop(true); err != nil {
		t.Fatal(err)
	}
	if !d.gone() {
		t.Fatal("the daemon runs")
	}
}

// TestSettle checks that a kerod of an earlier version than the client's is
// one of an earlier Kero, and that one of this version is not, or one that
// another program runs.
func TestSettle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kerod is named by exec -a")
	}
	d := fakeDaemon(t, "sleep 60")
	defer d.stop()
	w := proto.Welcome{Version: proto.Version - 1, PID: d.cmd.Process.Pid}
	if !Settle(w) {
		t.Error("an earlier kerod is not settled")
	}
	w.Version = proto.Version
	if Settle(w) {
		t.Error("a kerod of this version is settled")
	}
	w = proto.Welcome{Version: proto.Version - 1, PID: os.Getpid()}
	if Settle(w) {
		t.Error("a process that is not kerod is settled")
	}
}

// TestSettleStop checks that a settle waits for the kerod to go: its
// sessions are saved as it stops.
func TestSettleStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kerod is named by exec -a")
	}
	d := fakeDaemon(t, "trap 'exit 0' TERM; while :; do sleep 1; done")
	defer d.stop()
	pid := d.cmd.Process.Pid
	if err := SettleStop(pid); err != nil {
		t.Fatal(err)
	}
	if !d.gone() {
		t.Error("the fake kerod does not go as it is settled")
	}
}
