package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/client"
	"kero/internal/feishu"
	"kero/internal/proto"
	"kero/internal/settings"
	"kero/locales"
)

// The agents a new session from Feishu runs, unless the configuration
// names others.
var feishuAgents = []string{"default", "claude", "codex", "opencode", "pi", "gemini", "qwen", "kimi", "amp", "droid"}

// feishuDraft is Settings › Agents › Feishu: the form, and the bot of this
// machine. The form takes what its fields hold, which a frame's elements
// do not keep beyond it: a field, or a row's button, reads it here.
type feishuDraft struct {
	app     string
	appSet  bool
	secret  string
	allow   string
	dir     string
	hosts   string
	timeout string
	stream  string
	err     string // why the last check failed
	busy    bool   // a check with Feishu runs

	st     proto.FeishuState // the bot, as kerod last told it
	known  bool              // kerod has told it
	filled bool              // the fields hold what the settings have
	run    *string           // the way a prompt runs, as the group sets it
}

// feishuRead asks this machine's kerod for the bot's state.
func (a *App) feishuRead() {
	h := a.local()
	if h.cl == nil {
		return
	}
	// A call of its own, which decodes the state: Go reports only errors.
	h.cl.Go("feishu.status", nil, func(err error) {
		if err != nil {
			return
		}
		a.update(func() {
			if st, ok := a.feishuState(h.cl); ok {
				a.ss.feishu.st, a.ss.feishu.known = st, true
			}
		})
	})
}

// feishuState is the bot's state, as this machine's kerod tells it.
func (a *App) feishuState(cl *client.Client) (proto.FeishuState, bool) {
	var st proto.FeishuState
	if err := cl.Call("feishu.status", nil, &st); err != nil {
		return st, false
	}
	return st, true
}

// feishuGroup is Settings › Agents › Feishu: the app and its secret, who
// may use the bot, how a prompt runs, and what its cards show.
func (a *App) feishuGroup(c *ui.Context, pal *palette) {
	d := &a.ss.feishu
	set := a.applied.Feishu
	setup := settings.FeishuSet(set)
	if set == nil {
		set = &settings.Feishu{}
	}
	if !d.appSet {
		d.app = set.App
	}
	d.appSet = true
	if !d.known && d.err == "" {
		// Once the first frames show, with the window's.
		time.AfterFunc(time.Second, func() { a.feishuRead() })
	}
	// The fields', as the settings have them, until they change.
	if !d.filled {
		d.allow = strings.Join(set.Allow, ", ")
		d.hosts = strings.Join(set.Host, ", ")
		d.dir = set.Dir
		d.filled = true
	}
	if d.run == nil {
		d.run = new(string)
	}
	*d.run = set.Run
	d.timeout = settings.FormatDuration(time.Duration(feishuTimeout(set)))
	d.stream = settings.FormatDuration(time.Duration(feishuStream(set)))
	note := locales.SetFeishuDetail()
	if len(a.hosts) > 1 {
		note += " " + locales.SetShellThisComputer()
	}
	settingsGroup(c, pal, locales.SetFeishu(), note, func() {
		settingRow(c, pal, locales.SetFeishuAppId(), a.feishuStatusLine(d), func() {
			textField(c, "feishu-app", &d.app, "cli_…", 200, pal)
			if d.busy {
				ui.Text(c, locales.SetFeishuConnecting()).FontSize(12.5).TextColor(pal.textMuted).SingleLine()
			} else if button(c, "feishu-connect", locales.SetFeishuCheck(), "", true, pal) {
				a.feishuConnect(d)
			}
		})
		if d.err != "" {
			ui.Text(c, d.err).FontSize(12).TextColor(pal.attention).LineHeight(1.35).Wrap().Padding(0, 0, 10, 0)
		}
	})
	settingsGroup(c, pal, "", "", func() {
		settingRow(c, pal, locales.SetFeishuSecret(), locales.SetFeishuSecretDetail(), func() {
			secret := textField(c, "feishu-secret", &d.secret, locales.SetFeishuSecretPlaceholder(), 200, pal)
			if secret.Valid() {
				secret.Password()
			}
		})
	})
	// What a bot does, with the app set: its state and switch off, who may
	// use it, how a prompt runs, and where.
	if !setup {
		return
	}
	a.feishuRow(c, pal, locales.SetFeishuOffState(), a.feishuStatusLine(d), func() {
		if button(c, "feishu-off", locales.SetFeishuDisconnect(), "", false, pal) {
			a.feishuDisconnect(d)
		}
	})
	a.feishuRow(c, pal, locales.SetFeishuAllow(), list(set.Allow, locales.SetFeishuOnlySetup()), func() {
		textField(c, "feishu-allow", &d.allow, locales.SetFeishuAllowPlaceholder(), 200, pal).OnSubmit(func() { a.feishuRows(d) })
	})
	a.feishuRunRow(c, pal)
	dirDetail := proto.ShortDir(set.Dir)
	if set.Dir == "" {
		dirDetail = locales.SetFeishuDirDetail() + " · " + locales.SetFeishuDirDefault()
	}
	a.feishuRow(c, pal, locales.SetFeishuAgent(), a.feishuAgent(set.Agent), func() {
		agents := slices.Clone(feishuAgents)
		if !slices.Contains(agents, set.Agent) {
			agents = append(agents, set.Agent)
		}
		popup(c, "feishu-agent", &a.set.Feishu.Agent, agents, func(agent string) string {
			if agent == "default" {
				return locales.SetFeishuAgentDefault()
			}
			return agent
		}, 200, pal)
	})
	a.feishuRow(c, pal, locales.SetFeishuDir(), dirDetail, func() {
		textField(c, "feishu-dir", &d.dir, locales.SetFeishuDirDefault(), 200, pal).OnSubmit(func() { a.feishuRows(d) })
	})
	a.feishuRow(c, pal, locales.SetFeishuHost(), locales.SetFeishuHostDetail(), func() {
		textField(c, "feishu-host", &d.hosts, locales.SetFeishuHostPlaceholder(), 200, pal).OnSubmit(func() { a.feishuRows(d) })
	})
	a.feishuRow(c, pal, locales.SetFeishuTimeout(), locales.SetFeishuTimeoutDetail(), func() {
		textField(c, "feishu-timeout", &d.timeout, "30m", 90, pal).OnSubmit(func() { a.feishuRows(d) })
	})
	a.feishuRow(c, pal, locales.SetFeishuStream(), locales.SetFeishuStreamDetail(), func() {
		textField(c, "feishu-stream", &d.stream, "30m", 90, pal).OnSubmit(func() { a.feishuRows(d) })
	})
}

// feishuRunRow is the row of how a prompt runs its agent.
func (a *App) feishuRunRow(c *ui.Context, pal *palette) {
	a.feishuRow(c, pal, locales.SetFeishuRun(), locales.SetFeishuRunDetail(), func() {
		segmented(c, "feishu-run", a.ss.feishu.run, []string{settings.FeishuRunSession, settings.FeishuRunCommand},
			[]string{locales.SetFeishuRunSession(), locales.SetFeishuRunCommand()})
	})
}

// feishuRow is the group of a row, its controls by build.
func (a *App) feishuRow(c *ui.Context, pal *palette, label, detail string, build func()) {
	// The title, as its row reads it.
	title := label
	settingsGroup(c, pal, label, "", func() {
		settingRow(c, pal, title, detail, build)
	})
}

// feishuRows saves what the fields hold, as one submits: a field the app
// cannot read, as a time that does not parse, stays as it was.
func (a *App) feishuRows(d *feishuDraft) {
	f := a.feishu()
	f.Allow = splitList(d.allow)
	if d.run != nil && *d.run != "" {
		f.Run = *d.run
	}
	f.Dir = strings.TrimSpace(d.dir)
	f.Host = splitList(d.hosts)
	f.Timeout = timeoutValue(d.timeout, feishuTimeout(f))
	f.Stream = timeoutValue(d.stream, feishuStream(f))
	a.feishuNormal()
}

// feishuNormal fills in what a Setup of the configuration fills in: the
// times, and the agent and the way a prompt runs, which have each a
// default.
func (a *App) feishuNormal() {
	f := a.feishu()
	f.Timeout = feishuTimeout(f)
	f.Stream = feishuStream(f)
	if f.Agent == "" {
		f.Agent = "default"
	}
	if f.Run == "" {
		f.Run = settings.FeishuRunSession
	}
}

// feishuTimeout and feishuStream are a turn's time limit, and a card's
// updates, as the configuration has them or the default.
func feishuTimeout(f *settings.Feishu) int64 {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return int64(settings.FeishuDefaultTimeout)
}

func feishuStream(f *settings.Feishu) int64 {
	if f.Stream > 0 {
		return f.Stream
	}
	return int64(settings.FeishuDefaultStream)
}

// feishu is the Feishu settings the group edits, and applies.
func (a *App) feishu() *settings.Feishu {
	if a.set.Feishu == nil {
		a.set.Feishu = &settings.Feishu{}
	}
	return a.set.Feishu
}

// timeoutValue reads a time as a field types it, 30m or 1h30m: in
// milliseconds, or was when it cannot be read.
func timeoutValue(v string, was int64) int64 {
	d, err := settings.ParseDuration(v)
	if err != nil {
		return was
	}
	return int64(d)
}

// splitList reads a list as the user types it: by commas, without spaces.
func splitList(v string) []string {
	out := []string{}
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// list tells a list, as a row says it, by commas.
func list(l []string, empty string) string {
	if len(l) == 0 {
		return empty
	}
	return strings.Join(l, ", ")
}

// feishuAgent tells the agent a new session runs.
func (a *App) feishuAgent(agent string) string {
	if agent == "default" || agent == "" {
		return locales.SetFeishuAgentDefault()
	}
	return agent
}

// feishuStatusLine tells the bot of this machine: connected to which
// app, off, or why not.
func (a *App) feishuStatusLine(d *feishuDraft) string {
	if !d.known {
		return ""
	}
	switch {
	case d.st.Connected:
		return locales.SetFeishuConnected(d.st.App)
	case d.st.Configured && !d.st.Secret:
		return locales.SetFeishuSecretMissing()
	case d.st.Error != "":
		return d.st.Error
	}
	return locales.SetFeishuNotConnected()
}

// feishuConnect checks the app with Feishu, keeps its secret and its ID,
// and connects the bot of this machine. The secret is needed when the ID
// is another app's, or none is kept.
func (a *App) feishuConnect(d *feishuDraft) {
	app := strings.TrimSpace(d.app)
	if app == "" {
		d.err = locales.SetFeishuSecretNeeded()
		return
	}
	kept, _ := settings.LoadFeishuSecret()
	secret := strings.TrimSpace(d.secret)
	if secret == "" && app == kept.AppID {
		secret = kept.AppSecret
	}
	if secret == "" {
		d.err = locales.SetFeishuSecretNeeded()
		return
	}
	d.err, d.busy = "", true
	// Feishu first, over the network: never on the window's thread.
	go func() {
		err := feishu.Probe(app, secret)
		a.update(func() {
			d.busy = false
			if err != nil {
				d.err = err.Error()
				return
			}
			a.feishuKeep(d, app, secret, kept.Me)
		})
	}()
}

// feishuKeep saves the app's secret, keeping who set it up, writes the
// app to the configuration, and connects the bot.
func (a *App) feishuKeep(d *feishuDraft, app, secret, me string) {
	if err := settings.SaveFeishuSecret(settings.FeishuSecret{AppID: app, AppSecret: secret, Me: me}); err != nil {
		d.err = err.Error()
		return
	}
	d.secret = ""
	f := a.feishu()
	f.App = app
	if err := settings.ValidateFeishu(f); err != nil {
		d.err = err.Error()
		return
	}
	a.feishuNormal()
	a.callOn(a.local(), "feishu.restart", nil, nil)
	a.feishuRead()
}

// feishuDisconnect takes the app off the configuration, and stops the
// bot.
func (a *App) feishuDisconnect(d *feishuDraft) {
	f := a.feishu()
	f.App = ""
	a.feishuNormal()
	a.callOn(a.local(), "feishu.restart", nil, nil)
	a.feishuRead()
}
