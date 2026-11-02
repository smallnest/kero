package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"kero/internal/proto"
)

// background is a command a session runs without a terminal: the agent a
// Feishu prompt runs, as claude -p, whose output goes to a file the bot
// reads, and whose end sets the session idle.
type background struct {
	mu    sync.Mutex
	id    string
	out   string
	cmd   *exec.Cmd
	ended bool
	err   string // why it did not start, or what ended it
	code  int
}

// newBackground is the command a pane may run.
func newBackground(out string) *background { return &background{out: out} }

func (b *background) fail(err error) {
	b.mu.Lock()
	b.ended, b.err = true, err.Error()
	b.mu.Unlock()
}

// stop ends the command, and what it started.
func (b *background) stop() {
	b.mu.Lock()
	cmd := b.cmd
	b.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		cmd.Process.Kill()
		return
	}
	if syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) != nil {
		cmd.Process.Kill()
	}
}

// backgroundOf is a session's command, and the pane that runs it: none in
// a session no command runs in.
func (d *Daemon) backgroundOf(id string) (*background, *pane) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.findSessionLocked(id)
	if s == nil {
		return nil, nil
	}
	for i := len(s.panes) - 1; i >= 0; i-- {
		if s.panes[i].bg != nil {
			return s.panes[i].bg, s.panes[i]
		}
	}
	return nil, nil
}

// runBackground runs a command in a session, without a terminal to show
// it: its output to Out, in the shell the session's pane runs, with the
// daemon's environment and the session's directory, in a session of its
// own, which /stop ends.
func (d *Daemon) runBackground(p proto.BackgroundParams) error {
	bg, pn := d.backgroundOf(p.ID)
	if bg == nil {
		return fmt.Errorf("no session %s runs commands", p.ID)
	}
	// A command that ended gives way to the next prompt's: the sessions
	// a chat runs commands in take one command after another.
	bg.mu.Lock()
	running := bg.cmd != nil && !bg.ended
	bg.mu.Unlock()
	if running {
		return errors.New("this session runs its command still")
	}
	if p.Out == "" || len(p.Command) == 0 {
		return errors.New("background: no output file or command")
	}
	pn.mu.Lock()
	dir := pn.info.Dir
	pn.mu.Unlock()
	if p.Dir != "" {
		dir = p.Dir
	}
	shell := userShell()
	if err := os.MkdirAll(filepath.Dir(p.Out), 0o700); err != nil {
		bg.fail(err)
		pn.bgDone()
		return err
	}
	out, err := os.Create(p.Out)
	if err != nil {
		bg.fail(fmt.Errorf("opening the output: %w", err))
		pn.bgDone()
		return err
	}
	// The command in its words, run by the shell itself: no quote to go
	// wrong. The children go without stdin, which carries nothing.
	cmd := exec.Command(p.Command[0], p.Command[1:]...)
	cmd.Dir = dir
	if as, ok := conversationOf(p.Command); ok {
		// The conversation the command runs is the pane's, which a chat
		// window shows, though the command has no terminal to tell it.
		pn.mu.Lock()
		pn.conv = &as
		pn.info.AgentSessionID = as.ID
		pn.mu.Unlock()
	}
	cmd.Env = d.environment(p.ID, pn.info.ID, shell, p.Env)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, out, nil
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		out.Close()
		bg.fail(fmt.Errorf("starting %s: %w", p.Command[0], err))
		pn.bgDone()
		return err
	}
	bg.mu.Lock()
	bg.cmd, bg.out = cmd, p.Out
	bg.ended, bg.err, bg.code = false, "", 0
	bg.mu.Unlock()
	pn.setBackground(cmd)
	go func() {
		err := cmd.Wait()
		out.Close()
		bg.mu.Lock()
		bg.ended = true
		bg.code = cmd.ProcessState.ExitCode()
		if err != nil && bg.err == "" {
			bg.err = err.Error()
		}
		bg.mu.Unlock()
		pn.bgDone()
	}()
	return nil
}

// backgroundOutput is a command's output from From on, and whether it
// ended.
func (d *Daemon) backgroundOutput(p proto.OutputParams) (proto.Output, error) {
	bg, _ := d.backgroundOf(p.ID)
	if bg == nil {
		return proto.Output{}, fmt.Errorf("no session %s runs commands", p.ID)
	}
	bg.mu.Lock()
	out, ended, err, code := bg.out, bg.ended, bg.err, bg.code
	bg.mu.Unlock()
	res := proto.Output{From: p.From, Ended: ended, Error: err, Exited: code}
	data, rerr := os.ReadFile(out)
	if rerr != nil && !os.IsNotExist(rerr) {
		return res, rerr
	}
	if p.From < 0 || p.From > int64(len(data)) {
		p.From = 0 // started again, with a new file
	}
	data = data[p.From:]
	if len(data) > proto.MaxOutput {
		data = data[len(data)-proto.MaxOutput:]
	}
	res.Text, res.From = string(data), p.From+int64(len(data))
	return res, nil
}

// setBackground makes a session's lead pane a place commands run in the
// background instead of a terminal: none opens until one does, and the
// command runs in the pane's directory.
func (d *Daemon) setBackground(id string) error {
	d.mu.Lock()
	s := d.findSessionLocked(id)
	var p *pane
	if s != nil && len(s.panes) > 0 {
		p = s.panes[0]
	}
	d.mu.Unlock()
	if p == nil {
		return fmt.Errorf("no session %s", id)
	}
	p.mu.Lock()
	bg, proc := p.bg, p.proc
	p.mu.Unlock()
	if bg != nil {
		return nil
	}
	if proc == nil {
		return nil
	}
	// Its shell goes, with the terminal it would draw in: what is left
	// runs the commands.
	p.close()
	p.mu.Lock()
	p.closed = false
	p.changed = make(chan struct{})
	p.bg = newBackground("")
	p.info.Status, p.info.Message = proto.Idle, ""
	p.mu.Unlock()
	go d.changed()
	return nil
}

// stopBackground ends the command a session runs, which /stop asks for.
func (d *Daemon) stopBackground(id string) error {
	bg, _ := d.backgroundOf(id)
	if bg == nil {
		return fmt.Errorf("no session %s runs commands", id)
	}
	bg.mu.Lock()
	running := bg.cmd != nil && !bg.ended
	bg.mu.Unlock()
	if !running {
		return errors.New("nothing runs there")
	}
	bg.stop()
	return nil
}
