package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/proto"

	"kero/locales"
)

// Magpie is a coding agents' gateway that runs on this machine, on port
// 3425 (MAGPIE_URL, else MAGPIE_PORT, asks another), and keeps a file of
// what they spent (~/.config/magpie/usage.jsonl). The Info view shows its
// usage while it runs: today's and this pane's conversation's, the plan
// windows closest to their limit, and its latest call.

const (
	// magpieTick is the time between two reads of what it keeps, and
	// magpieSniff, much longer, between two looks for it while it ran.
	magpieTick  = 15 * time.Second
	magpieSniff = time.Minute
	// magpiePages bounds the reads of its requests, which one session's
	// calls may lie deep in, and magpieWait keeps them off the redraws:
	// a read that takes that long is off the table until it answers.
	magpiePages = 16
	magpieWait  = time.Minute
)

// magpieInfo is what the Info view shows of Magpie.
type magpieInfo struct {
	readAt  time.Time
	sniffAt time.Time
	busy    bool
	ran     bool // it answered once: the file may change again
	usage   *magpieUsage
	err     string
}

// magpieUsage is Magpie's usage as the Info view shows it.
type magpieUsage struct {
	Today       magpieBucket
	Session     int  // this pane's conversation's calls, 0 if another
	SessErr     bool // its calls are past the pages read
	SessionCost float64
	Latest      magpieCall
	Windows     []magpieWindow // the plan's, closest to its limit first
}

// magpieCall is the latest call through the gateway.
type magpieCall struct {
	Status int
	Served string
	Ms     int
}

// magpieWindow is a plan's window, as left.
type magpieWindow struct {
	Provider string // the provider's name, as Magpie names it
	Name     string
	Left     float64 // percent
}

// magpieBucket is calls' count, tokens and cost, as Magpie keeps them.
type magpieBucket struct {
	Calls      int     `json:"calls"`
	Errors     int     `json:"errors"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Reasoning  int     `json:"reasoning"`
	Cost       float64 `json:"cost"`
}

// Tokens counts the bucket's tokens: those it read with the cache, as read
// and as written, and wrote.
func (b magpieBucket) Tokens() int {
	return b.Input + b.Output + b.CacheRead + b.CacheWrite + b.Reasoning
}

// Hit is the bucket's share of its input that the cache read, in percent.
func (b magpieBucket) Hit() float64 {
	if in := b.Input + b.CacheRead + b.CacheWrite; in > 0 {
		return float64(b.CacheRead) * 100 / float64(in)
	}
	return 0
}

// magpieURL is where Magpie answers: MAGPIE_URL, else its port, else its
// own.
func magpieURL() string {
	if u := strings.TrimSpace(os.Getenv("MAGPIE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	port := "3425"
	if p := strings.TrimSpace(os.Getenv("MAGPIE_PORT")); p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port
}

// magpieOf returns Magpie's Info view, made the first time.
func (a *App) magpieOf() *magpieInfo {
	if a.magpie == nil {
		a.magpie = &magpieInfo{}
	}
	return a.magpie
}

// magpieTick reads Magpie as it falls due, and looks for it while it ran,
// as the Info view shows.
func (a *App) magpieTick(c *ui.Context) {
	st := a.magpieOf()
	due := magpieTick
	if !st.ran {
		due = magpieSniff
	}
	if !st.busy && st.usage == nil && time.Since(st.readAt) >= magpieWait {
		st.busy = false
	}
	if !st.busy && (st.usage == nil || time.Since(st.readAt) >= due) {
		st.readAt, st.busy = time.Now(), true
		a.readMagpie(st)
	}
	if st.usage == nil && time.Since(st.readAt) < magpieWait {
		c.After(magpieWait / 10)
		return
	}
	c.After(magpieTick / 2)
}

// readMagpie asks for Magpie's usage, and for how many calls the session
// of the pane the user is in made.
func (a *App) readMagpie(st *magpieInfo) {
	base := magpieURL()
	go func() {
		// The daemon answers the session while the reads wait on Magpie.
		session := a.paneSession()
		u, err := fetchMagpie(base, session)
		a.update(func() {
			if a.magpie != st {
				return
			}
			st.busy = false
			if err != nil {
				if st.usage == nil {
					st.err = locales.InfoMagpieDown()
				}
				return
			}
			st.ran, st.usage, st.err = true, u, ""
		})
	}()
}

// paneSession is the coding agent's session of the pane the user is in, as
// its hooks told kerod: "" while it told of none.
func (a *App) paneSession() string {
	s := a.session(a.selected)
	if s == nil {
		return ""
	}
	p := s.PaneByID(a.focusOf(s))
	if p == nil || p.Agent == "" {
		return ""
	}
	cl := a.hostOf(p.ID).cl
	if cl == nil {
		return ""
	}
	var r proto.AgentSessionResult
	if cl.Call("pane.agentSession", proto.AgentSessionParams{PID: p.PID, Agent: p.Agent}, &r) != nil {
		return ""
	}
	return r.Session
}

// fetchMagpie reads Magpie at base: its usage, the calls of session and
// the plan windows. The session's calls are the requests, which the
// daemon of another machine does not answer: they come from this
// machine's usage file alone.
func fetchMagpie(base, session string) (*magpieUsage, error) {
	now := time.Now()
	if !magpieRuns(base) {
		return nil, errors.New("magpie does not run at " + base)
	}
	var usage magpieUsageJSON
	if err := magpieGet(base+"/v1/magpie/usage", &usage); err != nil {
		return nil, err
	}
	u := &magpieUsage{Today: usage.magpieBucket}
	if s := usage.Series; len(s) > 0 {
		if day := s[len(s)-1].Time; day != "" {
			if d, err := time.Parse(time.RFC3339, day); err == nil && sameDay(d, now) {
				u.Today = s[len(s)-1].magpieBucket
			}
		}
	}
	// The windows come next: a gateway whose plans it does not read yet is
	// still a gateway.
	var quotas magpieQuotasJSON
	if err := magpieGet(base+"/v1/magpie/quotas", &quotas); err == nil {
		u.Windows = magpieMostLeft(quotas)
	}
	var reqs struct {
		magpieRequestsJSON
		Since string `json:"since"`
	}
	if err := magpieGet(base+"/v1/magpie/usage/requests?limit=1", &reqs); err != nil || len(reqs.Rows) == 0 {
		return u, nil
	}
	l := reqs.Rows[0]
	u.Latest = magpieCall{Status: l.Status, Served: l.Served, Ms: l.Ms}
	if session == "" {
		return u, nil
	}
	if l.Session == session && reqs.Total == 1 {
		u.Session, u.SessionCost = 1, l.Cost
		return u, nil
	}
	// The requests come newest first: a page that reaches today's start
	// holds every call of it.
	since, err := time.Parse(time.RFC3339, reqs.Since)
	if err != nil {
		since = startOfDay(now)
	}
	for page, offset := 0, 0; page < magpiePages; page, offset = page+1, offset+500 {
		var r magpieRequestsJSON
		url := base + "/v1/magpie/usage/requests?limit=500&offset=" + strconv.Itoa(offset)
		if err := magpieGet(url, &r); err != nil {
			u.SessErr = true
			return u, nil
		}
		for _, row := range r.Rows {
			if row.Session != session {
				continue
			}
			if t, err := time.Parse(time.RFC3339, row.T); err == nil && t.After(since) {
				u.Session++
				u.SessionCost += row.Cost
			}
		}
		if len(r.Rows) < 500 || offset+500 >= r.Total {
			return u, nil
		}
		if last := r.Rows[len(r.Rows)-1]; last.T != "" {
			if t, err := time.Parse(time.RFC3339, last.T); err == nil && t.Before(since) {
				return u, nil
			}
		}
	}
	u.SessErr = true
	return u, nil
}

// magpieRuns reports whether Magpie answers at base.
func magpieRuns(base string) bool {
	var root struct {
		Name string `json:"name"`
	}
	if err := magpieGet(base+"/", &root); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(root.Name), "magpie")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// magpieGet reads url's JSON into v.
func magpieGet(url string, v any) error {
	client := http.Client{Timeout: 4 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(v)
}

// magpieMostLeft picks the plan's windows closest to their limits: the
// fewest left first.
func magpieMostLeft(q magpieQuotasJSON) []magpieWindow {
	var all []magpieWindow
	for _, p := range q.Data {
		for _, w := range p.Windows {
			if w.Unlimited {
				continue
			}
			all = append(all, magpieWindow{Provider: p.Name, Name: w.Name, Left: w.Remaining})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Left < all[j].Left })
	return all
}

type magpieUsageJSON struct {
	magpieBucket
	Series []struct {
		magpieBucket
		Time string `json:"time"`
	} `json:"series"`
}

type magpieRequestsJSON struct {
	Total int `json:"total"`
	Rows  []struct {
		T       string  `json:"t"`
		Session string  `json:"session"`
		Status  int     `json:"status"`
		Served  string  `json:"served"`
		Ms      int     `json:"ms"`
		Cost    float64 `json:"cost"`
	} `json:"rows"`
}

type magpieQuotasJSON struct {
	Data []struct {
		Name    string `json:"name"`
		Windows []struct {
			Name      string  `json:"name"`
			Remaining float64 `json:"remaining"`
			Unlimited bool    `json:"unlimited"`
		} `json:"windows"`
	} `json:"data"`
}

// magpieCost is dollars in words: $1.18, $0.042, $0.0006.
func magpieCost(c float64) string {
	switch {
	case c >= 100:
		return "$" + strconv.Itoa(int(math.Round(c)))
	case c >= 10:
		return "$" + strconv.FormatFloat(c, 'f', 1, 64)
	case c >= 1:
		return "$" + strconv.FormatFloat(c, 'f', 2, 64)
	case c >= 0.01:
		return "$" + strconv.FormatFloat(c, 'f', 3, 64)
	case c > 0:
		return "$" + strconv.FormatFloat(c, 'f', 4, 64)
	}
	return "$0"
}

// magpieTokens is a count of tokens in short words: 1.2M, 34.5k, 812.
func magpieTokens(n int) string {
	switch {
	case n >= 10_000_000:
		return strconv.Itoa(n/1_000_000) + "M"
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 100_000:
		return strconv.Itoa(n/1_000) + "k"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "k"
	}
	return strconv.Itoa(n)
}

// --- the Info view's card ---

// magpieGlyph is a paper plane, the call it sends for the agent.
var magpieGlyph = icon(`<path d="M3 4.5 13 8 9.5 9.5 8 13 3 4.5Z"/><path d="M5.8 6.2c1.9.4 3.3.3 4.6-.6"/>`)

// infoMagpie draws Magpie's card last: while it answers, what it spent
// today and this conversation spent through it, the plan windows closest
// to their limits and its latest call; while it does not, why it does not,
// once it answered or as a message it installed it.
func (a *App) infoMagpie(c *ui.Context, pal *palette) {
	st := a.infoMagpieState(c)
	if st.usage == nil && st.err == "" {
		return
	}
	aside := ""
	if st.err == "" {
		aside = magpieCost(st.usage.Today.Cost)
	}
	infoCard(c, pal, magpieGlyph, locales.InfoMagpie(), aside, func() {
		switch {
		case st.usage == nil:
			infoEmpty(c, pal, st.err)
		default:
			infoMagpieUsage(c, pal, st.usage, st.err)
		}
	})
}

// infoMagpieUsage lists what Magpie spent, then its sessions before now,
// another's or the one that ended.
func infoMagpieUsage(c *ui.Context, pal *palette, u *magpieUsage, down string) {
	infoRows(c, func() {
		infoMetric(c, pal, locales.InfoMagpieCalls(), strconv.Itoa(u.Today.Calls))
		infoRule(c, pal)
		infoMetric(c, pal, locales.InfoMagpieTokens(), magpieTokens(u.Today.Tokens()))
		infoRule(c, pal)
		infoMetricSub(c, pal, locales.InfoMagpieLeft(), magpieHit(u.Today), locales.InfoMagpieCache())
		if u.Session > 0 || u.SessErr {
			infoRule(c, pal)
			infoMetric(c, pal, locales.InfoMagpieSession(), magpieSessionLine(u))
		}
		for i, w := range u.Windows {
			if i >= 4 {
				break
			}
			infoRule(c, pal)
			infoMetricSub(c, pal, w.Provider, magpieLeft(w.Left), w.Name)
		}
		if u.Latest.Served != "" {
			infoRule(c, pal)
			infoMetric(c, pal, locales.InfoMagpieLatest(), magpieCallLine(u.Latest))
		}
		if down != "" {
			infoRule(c, pal)
			infoEmpty(c, pal, down)
		}
	})
}

// infoMagpieState is Magpie's Info view, read as the card shows it.
func (a *App) infoMagpieState(c *ui.Context) *magpieInfo {
	st := a.magpieOf()
	a.magpieTick(c)
	return st
}

// infoMetric is a row of a card: a label, and a value on the right.
func infoMetric(c *ui.Context, pal *palette, label, value string) {
	ui.Row(c).MinHeight(32).Padding(0, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1).MinWidth(0)
		infoMetricValue(c, pal, value)
	})
}

// infoMetricSub is a metric of two lines: its value over a word of what it
// counts, or a detail of it.
func infoMetricSub(c *ui.Context, pal *palette, label, value, sub string) {
	ui.Row(c).MinHeight(42).Padding(6, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").Grow(1).Shrink(1).MinWidth(0)
		ui.Column(c).Gap(1).AlignItems(ui.End).Shrink(1).MinWidth(80).Children(func() {
			infoMetricValue(c, pal, value)
			ui.Text(c, sub).FontSize(11).TextColor(pal.textFaint).SingleLine()
		})
	})
}

// infoMetricValue is a metric's value, on the right.
func infoMetricValue(c *ui.Context, pal *palette, value string) {
	ui.Text(c, value).FontSize(12.5).FontWeight(500).TextColor(pal.text).FontFeatures("tnum").SingleLine().Shrink(0)
}

// magpieLeft is a window's share left in words.
func magpieLeft(left float64) string {
	return strconv.Itoa(int(math.Round(left))) + "%"
}

// magpieHit is how much of today's input the cache read.
func magpieHit(b magpieBucket) string {
	if b.Input+b.CacheRead+b.CacheWrite == 0 {
		return "—"
	}
	return strconv.Itoa(int(math.Round(b.Hit()))) + "%"
}

// magpieSessionLine is this conversation's calls, and their cost.
func magpieSessionLine(u *magpieUsage) string {
	if u.SessErr {
		return "500+"
	}
	return strconv.Itoa(u.Session) + " · " + magpieCost(u.SessionCost)
}

// magpieCallLine is the latest call: its status, what served it and how
// long it took.
func magpieCallLine(l magpieCall) string {
	line := strconv.Itoa(l.Status) + " " + l.Served
	if l.Ms > 0 {
		line += " · " + fmtTime(l.Ms)
	}
	return line
}

// fmtTime is a duration in short words: 1.4s, 840ms.
func fmtTime(ms int) string {
	if ms < 1000 {
		return strconv.Itoa(ms) + "ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
}
