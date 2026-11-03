package feishu

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"kero/internal/proto"
	"kero/internal/settings"
)

// waits are how long the driving waits: the tests make them short.
var waits = struct {
	// start is how long a prompt waits for its agent to start working,
	// or to answer: one that starts after it is already working when the
	// bot finds it so, or done.
	start time.Duration
	// settle is how long the screen must stay the same, the agent idle,
	// for a turn to end that was never seen working.
	settle time.Duration
	// quiet is how long a pane seen working stays idle for the turn to
	// end: an idle that joins the next, as a tool's end and an agent's
	// thinking, is no end.
	quiet time.Duration
	// grace ends a turn that was never seen working after it, once the
	// agent is idle: a program that takes the prompt, and never says
	// that it works.
	grace time.Duration
	// poll is how often the bot looks at the run's screen.
	poll time.Duration
}{
	start:  1500 * time.Millisecond,
	settle: 800 * time.Millisecond,
	quiet:  1200 * time.Millisecond,
	grace:  4 * time.Second,
	poll:   250 * time.Millisecond,
}

// stuckAfter is how long a pane may work before the bot takes the status
// for one that is not the turn's: a program's report, or the daemon's,
// kept by the pane, which the screen's rules do not change.
var stuckAfter = 5 * time.Second

// run is a prompt's: the rows its screen began with, a turn's text
// since, and what the pane last said.
type run struct {
	start  int
	status proto.Pane
	text   string
	// doubted tells a turn whose text the bot could not tell from an
	// earlier one.
	doubted bool
	// answer is the turn's, as the agent's hook wrote it (answer.go), and
	// conversation the agent's session it continued, "" while there is
	// none.
	answer       string
	conversation string
	// hook tells the answer the agent's hook wrote, which the bot gave
	// the pane's directory for, and which is the turn's text.
	hook bool
	// answerStart is when the bot last looked for the answer.
	answerStart time.Time
}

// prompt sends the message to the bound session, as a prompt, and
// replies with what the turn printed once the agent stops working, or
// with the answer the agent's hook writes.
func (b *Bot) prompt(key, chatID, response, text string, t turnInfo) {
	c := b.chat(key)
	if c.id == "" {
		b.reply(chatID, "This chat has no session yet. /new starts one, or /use an open one.")
		return
	}
	if _, err := b.api.State(); err != nil {
		b.reply(chatID, "Kero is unreachable: "+err.Error())
		return
	}
	s, err := b.session(c.id)
	if err != nil {
		// The session, closed from the app or another host, binds no
		// longer.
		b.unbind(key)
		b.reply(chatID, "The session this chat had is gone. /new starts one, or /use an open one.")
		return
	}
	var card *turnCard
	if cards() {
		card = b.newTurnCard(chatID, t.event, langOf(text))
		b.card(card, "working", "", "")
	}
	agent := b.conversationAgent(c, "", c.id)
	r, err := b.drive(s, text, card, t, agent, "")
	if err != nil {
		b.replyAt(key, response, chatID, err.Error())
		return
	}
	// A terminal's turn shows as the reply's card: the answer's card, or
	// the hook's answer.
	if r.hook {
		// The agent's hook wrote the turn's answer, which the terminal's
		// text would bury in its frames: the answer is the turn's text,
		// and the conversation is the hook's.
		b.card(card, "done", r.answer, "")
		b.drop(card)
		b.mu.Lock()
		c := b.chatLocked(key)
		c.sid, c.card = r.conversation, cardMsg{}
		b.mu.Unlock()
		b.save()
	}
	reply := b.replyWith(r)
	state := "done"
	if r.status.Status == proto.Waiting {
		// The agent asks, and its options go in the card, as buttons.
		state = "asks"
		if q, ok := question(reply); ok {
			reply = q
		}
	}
	b.replyTurn(key, response, chatID, Card{
		Title:    titleOf(reply),
		Text:     reply,
		State:    state,
		Duration: time.Since(t.start),
	}, "Sent. /status shows the turn.")
}

// drive types text into the session's pane, which Enter ends, and follows
// the turn until the agent stops; card, where given, shows the turn while
// it runs. Asked is the answer that continues the turn, which drive does
// not type. answers, where given, is the pane's directory of the hook's
// answers: one that an answer continued began with the turn's, which the
// bot gave the pane.
func (b *Bot) drive(s *proto.Session, text string, card *turnCard, t turnInfo, agent, known string) (*run, error) {
	p := paneOf(s)
	if p.ID == "" {
		return nil, errNoPane
	}
	if t.start.IsZero() {
		if work(p) {
			return nil, workingError(s, p)
		}
	}
	r := &run{start: 0, status: p}
	answers := known
	if answers == "" {
		// The pane's agent hook writes the turn's answer there, from a
		// directory of its own, which the bot gives the pane. A prompt
		// looks again as it ends, for the agent then.
		answers = b.answersTo(p.ID, agent)
	}
	var rd proto.ReadResult
	if err := b.api.Call("read", proto.ReadParams{ID: p.ID, Scrollback: true}, &rd); err != nil {
		return nil, err
	}
	r.start = rows(rd.Text)
	if text == "" {
		// A turn an answer continues began with the prompt, which Enter
		// ends, and the chat sent the answer: drive types nothing.
	} else if err := b.send(p.ID, text, true); err != nil {
		return nil, err
	}
	// Its turn keeps kerod at its work, as a connection that stays does.
	b.api.Call("watch", map[string]bool{"stay": true}, nil)
	stop := t.start.Add(b.timeout())
	if t.start.IsZero() {
		stop = time.Now().Add(b.timeout())
	}
	prompted := time.Now()
	var seen, still time.Time // when the agent was first, and last, seen to work
	before := rd.Text

	// settle reads the pane, and tells whether the turn can end: the
	// agent waits; or it was seen working, and its pane is quiet; or it
	// was never seen working, its screen is still, and its answer is
	// written, or it waited long enough; or the hook's answer is the
	// turn's.
	settle := func() (bool, error) {
		var now proto.ReadResult
		if err := b.api.Call("read", proto.ReadParams{ID: p.ID, Scrollback: true}, &now); err != nil {
			return false, err
		}
		if now.Text != before {
			before, still = now.Text, time.Now()
		}
		switch {
		case p.Status == proto.Waiting:
			return true, nil
		case !seen.IsZero():
			return time.Since(still) >= waits.quiet, nil
		}
		if time.Since(still) >= waits.settle && time.Since(prompted) >= waits.grace {
			return true, nil
		}
		return time.Since(prompted) >= waits.start && len(written(now.Text)) > 0, nil
	}
	// read takes the turn's answer, as the hook wrote it: the hook writes
	// it as the turn ends, after the agent is done, so the answer is the
	// turn's.
	read := func() {
		if answers == "" {
			return
		}
		// answerStart is when the bot looked: a hook that writes after the
		// agent is idle has the grace to write, which the bot waits.
		r.answerStart = time.Now()
		r.answer, r.conversation, r.hook = b.answerFile(answers)
		if r.hook {
			// An answer, where it is, is the turn's.
			r.doubted = false
		}
	}
	for {
		if !time.Now().Before(stop) {
			return nil, errRunning
		}
		time.Sleep(waits.poll)
		// The pane as it was, until it changes. The hook writes the turn's
		// answer as the agent starts to work, as it stops: the bot looks
		// for it then, and again as the turn ends.
		if err := b.api.Call("wait", proto.WaitParams{ID: p.ID, Timeout: int64(waits.poll / time.Millisecond)}, &p); err != nil && !timedOut(err) {
			return nil, err
		}
		now := time.Now()
		read()
		if p.Status == proto.Working {
			stuck := seen.IsZero() && now.Sub(prompted) > stuckAfter
			// A status the daemon keeps across a restart, as one a
			// program reported, stays until the program reports again:
			// no turn ends it. Its end is all the bot takes.
			if seen.IsZero() && !stuck {
				seen = now
			}
			still = now
			if stuck {
				break
			}
			continue
		}
		done, err := settle()
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
	}
	var now proto.ReadResult
	if err := b.api.Call("read", proto.ReadParams{ID: p.ID, Scrollback: true}, &now); err != nil {
		return nil, err
	}
	r.status, r.text = p, now.Text
	// A pane of a screen of its own, as a TUI's, keeps what the turn
	// printed in the rows below its prompt, not in rows the screen added.
	at := turnTail(rd.Text, now.Text, text)
	if at != "" {
		r.text, r.start = at, 0
	}
	// The hook writes as the pane's screen is drawn, after the agent
	// stops: the bot looks once more, and the hook's answer, where there
	// is one, is the turn's.
	time.Sleep(waits.poll / 2)
	if answers == "" && agent != "" {
		answers = b.answersTo(p.ID, agent)
	}
	read()
	if !r.hook {
		time.Sleep(waits.poll / 2)
		if seen.IsZero() && at == "" && !turnSeen(rd.Text, now.Text) {
			// Nothing says where the turn began: /status tells the rest.
			r.doubted = true
		} else if r.doubted {
			// A turn whose text the hook's answer now tells, after the
			// bot took it for an earlier one.
			r.doubted = false
		}
	}
	return r, nil
}

// answersTo gives a pane the directory of its agent's answer files, and
// returns it: "" for no agent, whose hook writes no answer.
func (b *Bot) answersTo(pane, agent string) string {
	if agent == "" {
		return ""
	}
	dir := answerDir(agent, pane)
	b.answerTo(pane, dir)
	return dir
}

// turnSeen tells a turn in the pane's text: the prompt sent, or the
// agent's answer, under the line "»".
func turnSeen(before, after string) bool {
	return after != before && (strings.Count(after, "\n") > strings.Count(before, "\n") || written(after) != "")
}

// turnTail is a turn's text in a screen that draws its viewport anew and
// keeps no scrollback, as a TUI does: the rows below the prompt it echoed,
// up to the rows of its input box. "" when the screen grew from the start
// of the turn, which since tells, or when the prompt is not in it.
func turnTail(before, after, prompt string) string {
	q := key(prompt)
	if q == "" || rows(after) > rows(before) {
		return ""
	}
	ls := strings.Split(strings.TrimRight(after, "\n"), "\n")
	at := -1
	for i := len(ls) - 1; i > 0; i-- {
		// The prompt in its box: the row above it is the box's border.
		if strings.Contains(key(ls[i]), q) && borderOnly(ls[i-1]) {
			at = i
			break
		}
	}
	if at < 0 {
		return ""
	}
	out := ls[at+1:]
	for i := 1; i < len(out); i++ {
		// The input box begins after a blank row: the turn ends there.
		if borderOnly(out[i]) && strings.TrimSpace(out[i-1]) == "" {
			out = out[:i]
			break
		}
	}
	for len(out) > 0 && turnMeta(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// key is a row as a prompt reads in it: without its escapes, the border of
// its box, and its spacing, at most keyRun of it.
func key(s string) string {
	s = strings.Join(strings.Fields(ansiRe.ReplaceAllString(s, "")), " ")
	s = strings.TrimSpace(strings.TrimLeft(s, "│|┃»> "))
	if r := []rune(s); len(r) > keyRun {
		s = string(r[:keyRun])
	}
	return s
}

// keyRun is how much of a prompt tells it in the pane.
const keyRun = 24

// borderOnly tells a row of a box's border, and nothing else.
func borderOnly(l string) bool {
	t := strings.TrimSpace(l)
	return t != "" && strings.Trim(t, "─━═╌╍┄┈╭╮╰╯│┃┌┐└┘├┤┬┴┼╔╗╚╝║") == ""
}

// turnMeta tells a row of a turn's own, which is no part of its text: a
// program's name and model, its count of tokens, and the bar of its
// screen.
func turnMeta(l string) bool {
	t := strings.TrimSpace(strings.Trim(l, "│|┃"))
	return t == "" || borderOnly(l) || strings.HasPrefix(t, "Build ·") ||
		strings.HasPrefix(t, "+ Tokens:") || strings.HasPrefix(t, "Build auto") ||
		strings.HasPrefix(t, "╹") || strings.HasPrefix(t, "╺")
}

// replyWith is the reply to a prompt: what the turn printed, and the
// answer an agent's hook wrote, where the terminal shows it.
func (b *Bot) replyWith(r *run) string {
	if r.doubted {
		return "Kero cannot tell the turn's text from what came before. /status shows the pane."
	}
	head := ""
	if r.status.Status == proto.Waiting {
		head = "The agent asks. /answer <text> answers it.\n\n"
	}
	if r.hook {
		// The hook's answer is the turn's text, which the pane's frames
		// do not hold; the terminal's rows are no reply to it.
		return head + r.answer
	}
	if a := written(r.text); a != "" {
		// The agent's answer, as its hook wrote it; the terminal's text
		// under it, for what its answer leaves out.
		if t := clean(since(r.text, r.start, tuiTail)); t != "" && strings.Count(t, "\n") > 0 {
			return head + a + "\n\n" + t
		}
		return head + a
	}
	if tail := clean(since(r.text, r.start, replyTail)); tail != "" {
		return textNote + head + tail
	}
	if r.status.Status == proto.Waiting {
		return head + "The agent asks: " + orDash(r.status.Message)
	}
	return fmt.Sprintf("The turn printed nothing new. The pane is %s.", r.status.Status)
}

// tuiTail is the rows of the terminal's text a reply keeps under the
// agent's answer.
const tuiTail = 12

const textNote = "The text below is the terminal's, not the agent's final words.\n\n"

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?:<=>]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// clean is a turn's text without the escapes a terminal keeps, and the
// lines that are borders only.
func clean(t string) string {
	t = ansiRe.ReplaceAllString(t, "")
	ls := strings.Split(t, "\n")
	out := ls[:0]
	for _, l := range ls {
		if s := strings.TrimSpace(l); s != "" && strings.Trim(s, "─━═╌╍┄┈╭╮╰╯│┃┌┐└┘├┤┬┴┼╔╗╚╝║•·-—") == "" {
			continue
		}
		out = append(out, strings.TrimRight(l, " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// written is the passage between the last row of a horizontal rule and
// the prompt box, without its border: the answer an agent's Stop hook
// wrote there under the line "»".
func written(screen string) string {
	ls := strings.Split(screen, "\n")
	for i := len(ls) - 1; i >= 0; i-- {
		t := strings.TrimSpace(strings.Trim(ls[i], "│|"))
		if strings.Trim(t, "─━═╌╍┄┈") == "" && len([]rune(t)) >= 20 && i > 0 && strings.Contains(ls[i-1], "»") {
			var body []string
			for _, l := range ls[:i] {
				body = append(body, strings.TrimSpace(strings.Trim(l, "│|")))
			}
			j := -1
			for k := len(body) - 1; k >= 0; k-- {
				if strings.TrimSpace(strings.Trim(body[k], "─━═╌╍┄┈")) == "" {
					j = k
					break
				}
			}
			a := strings.TrimSpace(strings.TrimLeft(strings.Join(body[j+1:], "\n"), "» "))
			return a
		}
	}
	return ""
}

var (
	errNoPane  = errors.New("The session has no pane. /new starts another.")
	errRunning = errors.New("Still working after the time limit. /status shows it, /stop interrupts it.")
)

func workingError(s *proto.Session, p proto.Pane) error {
	return fmt.Errorf("%s is %s. /status shows it, /stop interrupts it, /answer answers its question.", s.DisplayName(settings.Load().SessionName), p.Status)
}

// timedOut tells a wait that ended on its own time, which tells nothing
// of the pane.
func timedOut(err error) bool { return strings.Contains(err.Error(), "timed out") }

// send types text into a pane, then Enter apart from it, which a program
// could take for a paste of a newline, as kero prompt does.
func (b *Bot) send(id, text string, enter bool) error {
	if err := b.api.Call("send", proto.SendParams{ID: id, Data: text}, nil); err != nil {
		return err
	}
	if !enter {
		return nil
	}
	time.Sleep(120 * time.Millisecond)
	return b.api.Call("send", proto.SendParams{ID: id, Data: "\r"}, nil)
}

// paneOf is a session's lead pane, else its first: a session no client's
// state has told kerod the lead of has none.
func paneOf(s *proto.Session) proto.Pane { return leadPane(s) }

// work tells a pane whose agent works, or waits for the user.
func work(p proto.Pane) bool { return p.Status == proto.Working || p.Status == proto.Waiting }
