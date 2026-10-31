package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Usage is what Claude Code last reported of a session.
type Usage struct {
	// Context is the tokens of the session's last request, as its context
	// fills, and Window the tokens its context holds.
	Context, Window int
	// Model is the last request's model.
	Model string
	// Session is the tokens the session spent: what it read and wrote.
	Session int
	// Quota is the plan's limits, when the session reported them.
	Quota *Quota
}

// Quota is a plan's limits: the share of each last reported used, 0 to 1,
// and when each resets, zero when unknown.
type Quota struct {
	Session, Weekly float64
	SessionResets   time.Time
	WeeklyResets    time.Time
}

// Result is what a session's transcript tells.
type Result struct {
	Path  string // the transcript
	Usage Usage
}

// configDir is where Claude Code keeps its files, this user's unless
// CLAUDE_CONFIG_DIR names another.
func configDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// ProjectsDir is where Claude Code keeps its sessions' transcripts: one
// directory of each working directory.
func ProjectsDir() string { return filepath.Join(configDir(), "projects") }

// ProjectDir is the directory Claude Code keeps a working directory's
// sessions in, as it names it: every character but letters, digits and
// dashes a dash, as -Users-me-api.
func ProjectDir(dir string) string {
	if p, err := filepath.Abs(dir); err == nil {
		dir = p
	}
	var b strings.Builder
	for _, r := range dir {
		if r == '-' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Transcript returns the file Claude Code writes a session's transcript
// to: in the directory of dir, or, for a session that moved, in the
// newest of the directories holding one of its name. "" when it has none,
// as while its session starts.
func Transcript(session, dir string) string {
	if session == "" || strings.ContainsAny(session, `/\`) {
		return ""
	}
	newest := func(p string) (string, time.Time) {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return "", time.Time{}
		}
		return p, st.ModTime()
	}
	if p, _ := newest(filepath.Join(ProjectsDir(), ProjectDir(dir), session+".jsonl")); p != "" {
		return p
	}
	files, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", session+".jsonl"))
	best, bestAt := "", time.Time{}
	for _, p := range files {
		if p, at := newest(p); p != "" && (best == "" || at.After(bestAt)) {
			best, bestAt = p, at
		}
	}
	return best
}

// A window by a model's name, for a window its session did not report:
// Claude Code's own table. A model not here may hold more, as 1M-context
// ones do, and its share then reads too high.
const (
	window200k = 200_000
	window1m   = 1_000_000
)

func windowOf(model string) int {
	m := strings.ToLower(model)
	if strings.Contains(m, "[1m]") || strings.HasSuffix(m, "-1m") {
		return window1m
	}
	return window200k
}

// Read reads what a session's transcript tells, from as much of its end
// as it must: its last request, which carries the limits the session last
// reported.
func Read(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	const chunk = 1 << 20
	var tail []byte
	for size := int64(chunk); ; size *= 2 {
		start := st.Size() - size
		if start < 0 {
			start = 0
		}
		b := make([]byte, st.Size()-start)
		n, err := f.ReadAt(b, start)
		if err != nil && n != len(b) {
			return Result{}, err
		}
		tail = b
		if start == 0 || lastRequest(b, start > 0) != nil {
			break
		}
	}
	head := st.Size() - int64(len(tail))
	r := Result{Path: path}
	e := lastRequest(tail, head > 0)
	if e == nil {
		return r, nil
	}
	r.Usage.Context = e.usage.total()
	r.Usage.Model = e.model
	r.Usage.Session = sessionTokens(f, head, tail)
	r.Usage.Quota = e.quota
	window := e.window
	if window == 0 {
		window = windowOf(e.model)
	}
	if r.Usage.Context > window {
		window = r.Usage.Context
	}
	r.Usage.Window = window
	return r, nil
}

// use is a request's use of its model.
type use struct {
	input, cacheCreate, cacheRead, output int
}

func (u use) total() int { return u.input + u.cacheCreate + u.cacheRead + u.output }

// entry is what a line of a transcript tells: a request's use of a model,
// and the limits Claude Code keeps with it.
type entry struct {
	id, model string
	usage     use
	window    int // the context window the line reported, 0 for none
	quota     *Quota
}

// field returns the first value of a JSON field of a line, as the line
// writes it, or "" for none: a search of the flat text, which reads what
// Claude Code writes, wherever it writes it.
func field(l, name string) string {
	key := `"` + name + `"`
	i := strings.Index(l, key)
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(l[i+len(key):], " \t:")
	switch {
	case strings.HasPrefix(rest, `"`):
		j := strings.Index(rest[1:], `"`)
		if j < 0 {
			return ""
		}
		return rest[1 : 1+j]
	case rest != "":
		j := strings.IndexAny(rest, ",}")
		if j < 0 {
			j = len(rest)
		}
		return strings.TrimSpace(rest[:j])
	}
	return ""
}

func fieldInt(l, name string) int {
	n, _ := strconv.Atoi(field(l, name))
	return n
}

func fieldFloat(l, name string) (float64, bool) {
	s := field(l, name)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// parse reads a line of a transcript as Claude Code writes it.
func parse(l string) (entry, bool) {
	var e entry
	e.usage = use{
		input:       fieldInt(l, "input_tokens"),
		cacheCreate: fieldInt(l, "cache_creation_input_tokens"),
		cacheRead:   fieldInt(l, "cache_read_input_tokens"),
		output:      fieldInt(l, "output_tokens"),
	}
	if e.usage.total() == 0 {
		return e, false
	}
	e.id, e.model, e.window = field(l, "id"), field(l, "model"), fieldInt(l, "contextWindow")
	e.quota = limits(l)
	return e, true
}

// limits reads the plan's limits of a line, from its five-hour one, which
// carries its weekly one, as a request reports them, or as Claude Code
// caches them in its session's lines.
func limits(l string) *Quota {
	i := strings.Index(l, `"five_hour"`)
	if i < 0 {
		return nil
	}
	rest := l[i:]
	v, ok := fieldFloat(rest, "utilization")
	if !ok {
		return nil
	}
	q := &Quota{Session: share(v)}
	if s := field(rest, "resets_at"); s != "" {
		q.SessionResets = parseTime(s)
	}
	if j := strings.Index(rest, `"seven_day"`); j >= 0 {
		week := rest[j:]
		if v, ok := fieldFloat(week, "utilization"); ok {
			q.Weekly = share(v)
			if s := field(week, "resets_at"); s != "" {
				q.WeeklyResets = parseTime(s)
			}
		}
	}
	return q
}

// share is a use of a limit, 0 to 1: a fraction, or a percent.
func share(v float64) float64 {
	if v > 1 {
		v /= 100
	}
	return v
}

// lastRequest returns the last request of the end of a transcript, nil
// for none: the end, cut from a file, starts mid-line.
func lastRequest(b []byte, cut bool) *entry {
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if cut && i == 0 {
			break // the line the cut split
		}
		if !strings.Contains(l, `"usage"`) {
			continue
		}
		if e, ok := parse(l); ok {
			return &e
		}
	}
	return nil
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// sessionTokens sums the tokens a session spent: the requests of its
// lines, each counted once, as an answer's parts share its id. head is
// what the file holds before tail, which continues a line.
func sessionTokens(f *os.File, head int64, tail []byte) int {
	counted := map[string]bool{}
	total := 0
	count := func(lines []string) {
		for _, l := range lines {
			if !strings.Contains(l, `"usage"`) {
				continue
			}
			e, ok := parse(l)
			if !ok || e.id != "" && counted[e.id] {
				continue
			}
			counted[e.id] = true
			total += e.usage.total()
		}
	}
	if head > 0 {
		all := make([]byte, head)
		n, err := f.ReadAt(all, 0)
		if err == nil || n == len(all) {
			count(strings.Split(string(all), "\n"))
		}
	}
	count(strings.Split(string(tail), "\n"))
	return total
}

// Human returns a number of tokens as Claude Code shows it: 213k for
// 213,412, 3.6M for 3,612,004.
func Human(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	case n >= 1_000:
		return fmt.Sprintf("%.0f", float64(n)/1_000) + "k"
	}
	return strconv.Itoa(n)
}
