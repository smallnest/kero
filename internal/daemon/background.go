package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// stdin is the command's input, while it runs, which an answer to
	// what it asks goes to; input the input of a command whose asks go
	// to its stream: the answer's writes, and the asks' lines.
	stdin io.WriteCloser
	input *stdinWriter
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
	killGroup(cmd)
}

// killGroup ends the command and what it started: its process group,
// which startGroup makes, or the command alone.
func killGroup(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.Process.Kill()
		return
	}
	if syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) != nil {
		cmd.Process.Kill()
	}
}

// startGroup makes the command start a process group of its own, which
// killGroup ends.
func startGroup(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = groupAttr()
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
	// A command of a chat's runs no agent of the pane's before it: the
	// turn ended with the last command.
	pn.mu.Lock()
	pn.info.AgentSession, pn.info.AgentSessionID = nil, ""
	pn.mu.Unlock()
	if as, ok := conversationOf(p.Command); ok {
		// The conversation the command runs is the pane's, which a chat
		// window shows, though the command has no terminal to tell it.
		pn.mu.Lock()
		pn.info.AgentSession, pn.info.AgentSessionID = &as, as.ID
		pn.mu.Unlock()
		d.saveState()
	}
	cmd.Env = d.environment(p.ID, pn.info.ID, shell, p.Env)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, out, nil
	// A command whose messages ask its runner for what it runs (a turn's
	// permission) reads its messages as lines: the asks go to the file
	// beside the output's, which the bot answers, and the others to the
	// output, with its errors.
	var input io.WriteCloser
	var in *stdinWriter
	var piped, lock *os.File
	asks := io.Writer(io.Discard)
	if _, file, err := proto.AnswerPair(p.Out); err == nil && true {
		r, w, err := os.Pipe()
		if err != nil {
			out.Close()
			bg.fail(err)
			pn.bgDone()
			return err
		}
		af, err := os.Create(file)
		if err != nil {
			r.Close()
			w.Close()
			out.Close()
			bg.fail(fmt.Errorf("opening the asks: %w", err))
			pn.bgDone()
			return err
		}
		asks, piped, lock = af, r, w
		// As a file, its reads by os/exec alone, not its io.Copy, which
		// writes nothing, and ends as the pipe's writes close: the
		// command's own reads then find the asks at once.
		cmd.Stdout, cmd.Stderr, cmd.Stdin = w, w, r
		in = &stdinWriter{r, w}
		input = in
	}
	startGroup(cmd)
	if err := cmd.Start(); err != nil {
		out.Close()
		if piped != nil {
			piped.Close()
			lock.Close()
			input.Close()
			asks.(*os.File).Close()
		}
		bg.fail(fmt.Errorf("starting %s: %w", p.Command[0], err))
		pn.bgDone()
		return err
	}
	bg.mu.Lock()
	bg.cmd, bg.out, bg.stdin, bg.input = cmd, p.Out, input, in
	bg.ended, bg.err, bg.code = false, "", 0
	bg.mu.Unlock()
	pn.setBackground(cmd)
	// The command's end is read here, not by Wait, which would wait on an
	// input that the command's children keep open.
	done := make(chan error, 1)
	if piped != nil {
		go func() {
			defer asks.(*os.File).Close()
			lock.Close() // no child of the command keeps the pipe's writes open
			done <- copyLines(piped, out, asks, isAsk, func(line string) { in.add(line) })
		}()
	}
	go func() {
		state, err := cmd.Process.Wait()
		var copyErr error
		if piped != nil {
			// The lines of the command's children, which the command
			// leaves, go still.
			copyErr = <-done
		}
		if err == nil {
			err = copyErr
		}
		if input != nil {
			input.Close()
		}
		out.Close()
		bg.mu.Lock()
		bg.ended = true
		bg.stdin, bg.input = nil, nil
		bg.code = state.ExitCode()
		if err != nil && bg.err == "" {
			bg.err = err.Error()
		}
		bg.mu.Unlock()
		pn.bgDone()
	}()
	return nil
}

// stdinWriter is the command's input, as the bot writes an answer to an
// ask, and readStdin its asks. The command reads the pipe's other end,
// as a file: os/exec reads a file's input alone, and an input that its
// io.Copy reads waits for a read that ends, which Claude Code gives up
// on after 3s, and warns that it got none of.
type stdinWriter struct{ r, w *os.File }

func (x *stdinWriter) add(line string) error {
	_, err := io.WriteString(x.w, line)
	return err
}

func (x *stdinWriter) Write(p []byte) (int, error) { return x.w.Write(p) }

// Close ends the input once the command ends, whose own end waits on no
// read of it.
func (x *stdinWriter) Close() error {
	x.r.Close()
	return x.w.Close()
}

// streamAsks makes a command's messages, and its input, go through a
// pipe, whose text is lines: the lines that ask for a permission go to
// asks, and the others to out, with the errors. It returns the command's
// input, which the command's end closes.
func streamAsks(out io.Writer, asks string) (*os.File, error) {
	w, err := os.Create(asks)
	if err != nil {
		return nil, fmt.Errorf("opening the asks: %w", err)
	}
	r, stdin, err := os.Pipe()
	if err != nil {
		w.Close()
		return nil, err
	}
	go func() {
		defer r.Close()
		defer w.Close()
		copyLines(r, out, w, isAsk, func(string) {})
	}()
	return stdin, nil
}

// copyLines copies a command's messages, as lines, to out, and the lines
// that ask its runner for a permission to asks, as they come, which ask
// also takes.
func copyLines(r io.Reader, out, asks io.Writer, isAsk func([]byte) bool, ask func(string)) error {
	return copyLinesTo(r, out, isAsk, func(line string) {
		io.WriteString(asks, line)
		ask(line)
	})
}

// copyLinesTo is copyLines, with where an ask goes.
func copyLinesTo(r io.Reader, out io.Writer, isAsk func([]byte) bool, ask func(string)) error {
	rd := bufio.NewReader(r)
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			if isAsk([]byte(line)) {
				ask(line)
			} else if _, werr := io.WriteString(out, line); werr != nil && err == nil {
				err = werr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// isAsk tells a line of a command's messages that asks for a permission.
func isAsk(line []byte) bool {
	var m struct {
		Type    string `json:"type"`
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	return json.Unmarshal(line, &m) == nil && m.Type == "control_request" && m.Request.Subtype == "can_use_tool"
}

// backgroundWrite writes text to the command a session runs, which a
// reply to what it asks is.
func (d *Daemon) backgroundWrite(p proto.InputParams, raw json.RawMessage) error {
	bg, _ := d.backgroundOf(p.ID)
	if bg == nil {
		return fmt.Errorf("no session %s runs commands", p.ID)
	}
	bg.mu.Lock()
	w := bg.stdin
	if w == nil {
		bg.mu.Unlock()
		return errors.New("the command takes no input")
	}
	defer bg.mu.Unlock()
	var in struct {
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &in)
	if x := bg.input; x != nil {
		_, err := io.WriteString(x.w, in.Text)
		return err
	}
	_, err := io.WriteString(w, in.Text)
	return err
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
