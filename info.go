package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"kero/internal/client"

	"kero/internal/proto"

	"kero/locales"
)

// The Info view is the side panel's first: where the pane in the session
// works, the processes of its terminal and the ports they listen on.

const (
	// infoIdle is the time between the reads of a pane's Info view whose
	// shell ran nothing yet.
	infoIdle = 3 * time.Second

	// infoTick is the time between the reads of a pane's working
	// directory; infoProcsTick the time between two listings of its
	// processes, which carry their ports, from as slow a tool as lsof.
	// infoCached bounds the views kept.
	infoTick      = time.Second
	infoProcsTick = 2 * time.Second
	infoCached    = 32
)

// paneInfo is what the Info view shows of a pane, kept by the app across
// the builds and read again as the view shows.
type paneInfo struct {
	readAt   time.Time
	busy     bool   // a read is under way
	shell    int    // the pane's shell, as it was read of
	dir      string // the working directory, as the header shows it
	procs    []proto.Process
	procsAt  bool      // the processes were listed
	listedAt time.Time // when they were
	ports    int
	listErr  string // why the processes did not list
}

// paneInfoOf returns a pane's Info view, made for the pane as it is now.
func (a *App) paneInfoOf(p *proto.Pane) *paneInfo {
	st := a.info[p.ID]
	if st != nil && st.shell != p.PID {
		st = nil
	}
	if st != nil && st.shell <= 0 && p.PID > 0 {
		// The shell starts as the view first shows: the reads look again.
		st.shell, st.readAt, st.procsAt = p.PID, time.Time{}, false
	}
	if st == nil {
		if a.info == nil {
			a.info = map[string]*paneInfo{}
		}
		if len(a.info) >= infoCached {
			for id := range a.info {
				if id != p.ID {
					delete(a.info, id)
				}
			}
		}
		st = &paneInfo{shell: p.PID}
		a.info[p.ID] = st
	}
	return st
}

// infoTick starts a pane's reads as they fall due.
func (a *App) infoTick(c *ui.Context, p *proto.Pane) {
	st := a.paneInfoOf(p)
	now := time.Now()
	if now.Sub(st.readAt) >= infoTick {
		a.readInfo(p, st)
	}
	if !st.procsAt || now.Sub(st.listedAt) >= infoProcsTick {
		a.readProcs(p, st)
	}
	c.After(infoProcsTick / 2)
}

// readInfo reads a pane's working directory from this machine's files:
// another machine's hold their own.
func (a *App) readInfo(p *proto.Pane, st *paneInfo) {
	cl := a.hostOf(p.ID).cl
	if cl == nil || st.busy {
		return
	}
	st.readAt, st.busy = time.Now(), true
	dir, remote := a.workDir(p.ID), a.hostOf(p.ID).remote()
	go func() {
		abs := ""
		if !remote {
			abs = readDir(cl, dir)
		}
		if abs == "" {
			abs = dir
		}
		a.update(func() {
			if a.info[p.ID] != st {
				return
			}
			st.readAt, st.busy, st.dir = time.Now(), false, abs
		})
	}()
}

// readDir returns the working directory a host lists, as it is: a
// directory gone is "".
func readDir(cl *client.Client, dir string) string {
	if dir == "" {
		return ""
	}
	var l proto.FSList
	if cl.Call("fs.list", proto.FSListParams{Dir: dir}, &l) != nil {
		return ""
	}
	return l.Dir
}

// readProcs lists a pane's processes, and the ports they hold.
func (a *App) readProcs(p *proto.Pane, st *paneInfo) {
	cl := a.hostOf(p.ID).cl
	if cl == nil || p.PID <= 0 {
		return
	}
	go func() {
		var procs []proto.Process
		err := cl.Call("pane.processes", proto.ProcessesParams{PID: p.PID}, &procs)
		a.update(func() {
			if a.info[p.ID] != st {
				return
			}
			if err != nil {
				st.procsAt, st.listedAt, st.listErr = true, time.Now(), err.Error()
				return
			}
			st.procsAt, st.listedAt, st.procs, st.listErr, st.ports = true, time.Now(), procs, "", 0
			for _, pr := range procs {
				st.ports += len(pr.Ports)
			}
		})
	}()
}

// --- the views ---

// infoView is the side panel's Info view: of the pane the user is in.
func (a *App) infoView(c *ui.Context, pal *palette, s *proto.Session) {
	p := s.PaneByID(a.focusOf(s))
	if p == nil {
		p = s.FocusPane()
	}
	if p == nil {
		a.sideMessage(c, pal, locales.SideInfo(), "")
		return
	}
	a.infoTick(c, p)
	ui.Scroll(c.Key("info")).Grow(1).MinHeight(0).TrackScroll(&a.side.info.scroll).Children(func() {
		a.infoBody(c, pal, s, p)
	})
}

// infoBody is an Info view: the working directory, the processes and the
// ports, each a card of the side panel's own look, under its header's tabs
// and a margin of the panel's on either side.
func (a *App) infoBody(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane) {
	st := a.paneInfoOf(p)
	ui.Column(c).Padding(8, 12, 12, 12).Gap(8).AlignItems(ui.Stretch).Children(func() {
		a.infoDir(c, pal, s, a.hostOf(p.ID), p, st)
		a.infoProcs(c, pal, p, st)
		a.infoPorts(c, pal, p, st)
		a.infoMagpie(c, pal)
	})
}

// infoCard is a card of the Info view: a row of a header, a tile's icon, a
// title and an aside, with a hairline under it.
func infoCard(c *ui.Context, pal *palette, svg *ui.SVG, title, aside string, body func()) {
	ui.Column(c).Radius(12).Background(pal.chip).Clip().Children(func() {
		infoHead(c, pal, func() {
			infoTile(c, pal, svg)
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(pal.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			if aside != "" {
				if strings.ContainsRune(aside, '/') {
					infoValue(c, pal, aside)
				} else {
					ui.Text(c, aside).FontSize(11.5).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().MinWidth(48).TextAlign(ui.End)
				}
			}
		})
		body()
	})
}

// infoHead is a card's header: its row, then its hairline.
func infoHead(c *ui.Context, pal *palette, fn func()) {
	ui.Column(c).Children(func() {
		ui.Row(c).Height(40).Padding(0, 12).Gap(9).AlignItems(ui.Center).Children(fn)
		infoRule(c, pal)
	})
}

// infoRows lists a card's rows, each divided from the one above.
func infoRows(c *ui.Context, fn func()) {
	ui.Column(c).Padding(0, 0, 3, 0).AlignItems(ui.Stretch).Children(fn)
}

// infoRule divides a card's rows.
func infoRule(c *ui.Context, pal *palette) {
	ui.Box(c).Height(1).Margin(0, 1, 0, 1).Background(pal.text.Alpha(0.07))
}

// infoTile is a card's icon, on a small rounded square.
func infoTile(c *ui.Context, pal *palette, svg *ui.SVG) {
	t := ui.Box(c).Size(24, 24).Radius(7).Background(pal.text.Alpha(0.07)).Center().Shrink(0)
	t.Children(func() { ui.Icon(c, svg).Size(13, 13).TextColor(pal.textMuted) })
}

// infoLabel is a row's label, on the left.
func infoLabel(c *ui.Context, pal *palette, label string) {
	ui.Text(c, label).FontSize(12.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
}

// infoValue is a card's aside, on the right of its title: what the card
// holds, in short words, cut short if need be.
func infoValue(c *ui.Context, pal *palette, value string) {
	ui.Text(c, value).FontSize(11.5).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).TextAlign(ui.End)
}

// infoDir is the Info view's working directory, with what opens it: its
// own name as the card's title, its parent after, and the ways to it,
// each a button.
func (a *App) infoDir(c *ui.Context, pal *palette, s *proto.Session, h *host, p *proto.Pane, st *paneInfo) {
	dir := st.dir
	if dir == "" {
		dir = p.WorkDir()
	}
	pid, sid := p.ID, s.ID
	// Copying carries the pane the user is in: here, no press of the card's
	// focuses the pane.
	copyDir := func() {
		if cur := a.session(sid); cur != nil {
			a.setFocus(cur, pid)
		}
		a.copyText = dir
	}
	_, name := splitDir(dir)
	infoCard(c, pal, folderIcon, name, dir, func() {
		ui.Row(c).Padding(8, 12, 12, 12).Gap(6).Wrap().Children(func() {
			a.infoAction(c, pal, "app-editor", locales.InfoOpenEditor(mygo.App.Name()), docIcon, func() {
				if file := indexFile(h, s, p, dir); file != "" {
					a.split(s, proto.CreateParams{Split: p.ID, Direction: "right", File: file})
				}
			})
			editor := editorName(h)
			if editor == "vi" {
				editor = "Vim"
			}
			a.infoAction(c, pal, "editor", locales.InfoOpenEditor(editor), editorGlyph(editor), func() {
				a.split(s, proto.CreateParams{Split: p.ID, Direction: "right", Command: editorCommand(h, dir)})
			})
			a.infoAction(c, pal, "copy-dir", locales.InfoCopyPath(), infoCopyIcon, copyDir)
			if !h.remote() {
				a.infoAction(c, pal, "reveal", locales.InfoReveal(), folderIcon, func() { revealPath(dir) })
			}
		})
	})
}

// infoAction is a button of a card: its icon and its label, on a light pill
// that deepens as the pointer is on it.
func (a *App) infoAction(c *ui.Context, pal *palette, key, label string, svg *ui.SVG, fn func()) {
	b := ui.ButtonBase(c.Key(key)).Height(26).Padding(0, 9).Gap(5).Radius(7).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Label(label)
	bg := pal.text.Alpha(0.05)
	if b.Pressed() {
		bg = pal.text.Alpha(0.13)
	} else if b.Hovered() {
		bg = pal.text.Alpha(0.09)
	}
	b.Background(bg).TextColor(pal.text)
	b.Children(func() {
		ui.Icon(c, svg).Size(11, 11).Opacity(0.65)
		ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine()
	})
	b.Tooltip(label)
	if b.Clicked() {
		fn()
	}
}

// splitDir tells a directory's parent and its own name, as /Users/kero
// and kero; of the root, the whole path is the name.
func splitDir(dir string) (parent, name string) {
	dir = strings.TrimSuffix(dir, "/")
	if i := strings.LastIndexByte(dir, '/'); i > 0 {
		return dir[:i], dir[i+1:]
	}
	if dir == "" {
		return "", ""
	}
	return dir, ""
}

// editorGlyph is the logo of the editor the editor button opens, as vim's
// for vi and nvim.
func editorGlyph(name string) *ui.SVG {
	if g := programIcons[name]; g != nil {
		return g
	}
	if name == "vi" {
		return programIcons["vim"]
	}
	return linkIcon
}

// indexFile is the file a folder's file pane opens: its README, or, for a
// host without one to ask, the folder itself, whose pane lists it.
func indexFile(h *host, s *proto.Session, p *proto.Pane, dir string) string {
	if dir == "" {
		return ""
	}
	if h.remote() {
		return dir
	}
	for _, name := range []string{"README.md", "README", "readme.md"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			return filepath.Join(dir, name)
		}
	}
	return dir
}

// editorName names the editor the editor button opens the directory in:
// the shell's, $VISUAL or $EDITOR, by its command's name.
func editorName(h *host) string {
	if h.remote() || runtime.GOOS != "darwin" {
		return "vi"
	}
	for _, v := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if f := strings.Fields(v); len(f) > 0 && f[0] != "" {
			return filepath.Base(f[0])
		}
	}
	return "vi"
}

// infoProcs is the Info view's processes: those of the pane's terminal,
// the parents first, each with how long it ran, its CPU and the ports it
// listens on.
func (a *App) infoProcs(c *ui.Context, pal *palette, p *proto.Pane, st *paneInfo) {
	if st.shell <= 0 {
		// The shell has not started: the view looks again for it.
		c.After(infoIdle)
	}
	aside := ""
	if len(st.procs) > 0 {
		aside = itoa(len(st.procs))
	}
	infoCard(c, pal, cpuIcon, locales.InfoProcess(), aside, func() {
		switch {
		case st.listErr != "":
			ui.Text(c, st.listErr).FontSize(12).TextColor(pal.danger).LineHeight(1.35).Padding(0, 12, 8, 12).Wrap()
		case p.PID <= 0:
			infoEmpty(c, pal, locales.InfoNoShell())
		case !st.procsAt:
			ui.Box(c).Height(34).Padding(0, 12).Center().Children(func() { ui.Spinner(c).Size(12, 12) })
		default:
			infoRows(c, func() {
				for i, pr := range st.procs {
					if i > 0 {
						infoRule(c, pal)
					}
					processRow(c, pal, pr)
				}
			})
		}
	})
}

// infoEmpty is a card's word that it has nothing to list.
func infoEmpty(c *ui.Context, pal *palette, text string) {
	ui.Text(c, text).FontSize(12).TextColor(pal.textFaint).Padding(0, 12, 10, 12)
}

// processRow is a process of the list: its name over its PID and how long
// it ran, and on the right its ports, its CPU and its logo.
func processRow(c *ui.Context, pal *palette, pr proto.Process) {
	row := ui.Row(c.Key("proc-"+itoa(pr.PID))).MinHeight(42).Padding(0, 12).Gap(10).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, pr.Name).FontSize(13).TextColor(pal.text).SingleLine().Ellipsis("…")
			sub := "PID " + itoa(pr.PID)
			if age := duration(pr.Age); age != "" {
				sub += " · " + age
			}
			ui.Text(c, sub).FontSize(11).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Ellipsis("…")
		})
		if pr.CPU >= 0.5 {
			ui.Text(c, fmt.Sprintf("%.0f%%", pr.CPU)).FontSize(11.5).TextColor(pal.textMuted).FontFeatures("tnum").Shrink(0)
		}
		if g := programIcons[pr.Name]; g != nil {
			ui.Icon(c, g).Size(12, 12).TextColor(pal.textFaint).Opacity(0.8)
		}
	})
}

// infoPorts is the Info view's ports: those the pane's processes listen on,
// each opening in the browser.
func (a *App) infoPorts(c *ui.Context, pal *palette, p *proto.Pane, st *paneInfo) {
	if st.shell <= 0 {
		// The shell has not started: the ports come with its processes.
		c.After(infoIdle)
	}
	aside := ""
	if st.ports > 0 {
		aside = itoa(st.ports)
	}
	infoCard(c, pal, globeIcon, locales.InfoPorts(), aside, func() {
		switch {
		case !st.procsAt && p.PID <= 0:
			infoEmpty(c, pal, locales.InfoNoShell())
		case !st.procsAt:
			ui.Box(c).Height(34).Padding(0, 12).Center().Children(func() { ui.Spinner(c).Size(12, 12) })
		case st.ports == 0:
			infoEmpty(c, pal, locales.InfoNoPorts())
		default:
			infoRows(c, func() {
				first := true
				for _, pr := range st.procs {
					for _, port := range pr.Ports {
						if !first {
							infoRule(c, pal)
						}
						first = false
						portRow(c, pal, pr.Name, port)
					}
				}
			})
		}
	})
}

// portRow is a port a process listens on, which opens in the browser.
func portRow(c *ui.Context, pal *palette, name string, port int) {
	row := ui.Row(c.Key("port-"+itoa(port))).MinHeight(36).Padding(0, 12).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		openPath("http://localhost:" + itoa(port))
	}
	row.Children(func() {
		ui.Text(c, "localhost:"+itoa(port)).FontSize(12.5).FontWeight(600).TextColor(pal.accent).FontFeatures("tnum").SingleLine().Shrink(0)
		ui.Text(c, name).FontSize(11.5).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		ui.Icon(c, linkIcon).Size(11, 11).TextColor(pal.textFaint).Opacity(0.8)
	})
}

// duration returns how long a process ran as ps tells it, in words: 62h
// 17m, 2m 14s or 51s, "" for a time not read.
func duration(age string) string {
	if age == "" {
		return ""
	}
	days := 0
	if d, rest, ok := strings.Cut(age, "-"); ok {
		days, age = atoi(d), rest
	}
	// ps tells a time's fields with colons, as 62:17:03 for hours,
	// minutes and seconds, and a day's in hours and minutes, as 2-17:03;
	// and else with their units, as 62h17m03s.
	var f []int
	if strings.Contains(age, ":") {
		for _, v := range strings.Split(age, ":") {
			f = append(f, atoi(v))
		}
	} else {
		// ps leaves the fields it has none of out, as 5m for five minutes.
		units, num := map[string]int{}, ""
		for _, r := range age {
			switch r {
			case 'h', 'm', 's':
				units[string(r)] = atoi(num)
				num = ""
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				num += string(r)
			}
		}
		if num != "" {
			units["s"] = atoi(num)
		}
		f = []int{units["h"], units["m"], units["s"]}
	}
	var hours, minutes, seconds int
	switch len(f) {
	case 3:
		hours, minutes, seconds = f[0], f[1], f[2]
	case 2:
		minutes, seconds = f[0], f[1]
	case 1:
		seconds = f[0]
	default:
		return ""
	}
	if days > 0 || hours > 0 {
		return itoa(days*24+hours) + "h " + itoa(minutes) + "m"
	}
	if minutes > 0 {
		return itoa(minutes) + "m " + itoa(seconds) + "s"
	}
	return itoa(seconds) + "s"
}

// atoi reads a string of digits as a number.
func atoi(s string) int {
	num := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		num = num*10 + int(r-'0')
	}
	return num
}
