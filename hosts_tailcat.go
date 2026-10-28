package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/remote"
	"kero/internal/settings"

	"kero/locales"
)

// Tailcat is the third way Kero reaches a host, beside the listener on a
// tailnet and SSH: a relay with a pair code, which needs no Tailscale
// (internal/remote/tailcat.go). The tailcat switch in Settings › Hosts
// turns it on and off: off, the app lists no host of it, and kerod
// starts no listener of it.
//
// The settings are one group, after the switch: the hosts it adds, which
// this machine reaches, and this machine as a host, which others reach.

// tailcatField is a field of the Tailcat group that failed: which, as its
// key, and why.
type tailcatField struct {
	key, err string
}

// tailcatDraft is the Tailcat group's form, which adds a host, and the
// fields it edits: the port of the hosts' listeners, the relay, and the
// port of this machine's listener.
type tailcatDraft struct {
	name, addr  string
	listenPort  string // this machine's listener's, while a host
	port, relay string
	err         tailcatField
	code        string    // this machine's pair code, as kerod's key makes it
	codeErr     string    // why there is none, as a failure
	at          time.Time // when the key was last read, or rotated
	copied      time.Time
	looking     bool
}

// tailcatGroup is Settings › Hosts › Tailcat: the switch, and with it on,
// the hosts Kero adds and this machine as a host.
func (a *App) tailcatGroup(c *ui.Context, pal *palette) {
	on := a.set.Tailcat
	settingsGroup(c, pal, "", "", func() {
		groupTitle(c, pal, locales.SetTailcatTitle())
		settingRow(c, pal, locales.SetTailcat(), locales.SetTailcatDetail(), func() {
			toggle(c.Key("tailcat"), &on, pal).OnClick(func() {
				s := a.set.Clone()
				s.Tailcat = on
				a.set = s
				a.resetTailcat()
			})
		})
		if !on {
			return
		}
		d := &a.ss.tailcat
		if d.port == "" {
			a.resetTailcat()
		}
		if groupRows++; groupRows > 1 {
			ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
		}
		a.tailcatHosts(c, pal, d)
		if groupRows++; groupRows > 1 {
			ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
		}
		a.tailcatListen(c, pal, d)
	})
	if !on {
		settingsGroup(c, pal, "", locales.SetTailcatOffNote(), nil)
	}
}

// resetTailcat reads the Tailcat group's fields again from the settings,
// and this machine's pair code with them.
func (a *App) resetTailcat() {
	s := a.set
	d := &a.ss.tailcat
	*d = tailcatDraft{
		port:       s.TailcatPort,
		relay:      s.TailcatRelay,
		listenPort: settings.TailcatListenPortDefault(),
	}
	if d.port == "" {
		d.port = settings.TailcatPortDefault()
	}
	if _, port, ok := s.TailcatListen(); ok {
		d.listenPort = port
	}
	if s.Tailcat {
		a.tailcatCode()
	}
}

// tailcatHosts is the hosts Kero adds over Tailcat, and how it reaches
// them: the head of the Tailcat group.
func (a *App) tailcatHosts(c *ui.Context, pal *palette, d *tailcatDraft) {
	ui.Column(c).Padding(11, 0).Gap(10).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, locales.SetTailcatAdd()).FontSize(13.5).TextColor(pal.text)
			ui.Text(c, locales.SetTailcatAddDetail()).FontSize(12).TextColor(pal.textMuted).LineHeight(1.35)
		})
		add := func() { a.addTailcatHost() }
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				textField(c, "tailcat-addr", &d.addr, locales.SetTailcatPlaceholder(), 0, pal).Font("monospace").OnSubmit(add)
			})
			ui.Box(c).Width(130).Shrink(0).Children(func() {
				textField(c, "tailcat-name", &d.name, defaultTailcatName(d.addr), 0, pal).OnSubmit(add)
			})
			if fieldButton(c, "tailcat-add", locales.CommonAdd(), true, pal) {
				add()
			}
		})
		tailcatFieldErr(c, pal, d, "tailcat-addr")
		tailcatSettingRow(c, pal, locales.SetTailcatPort(), locales.SetTailcatPortDetail(), func() {
			textField(c, "tailcat-port", &d.port, settings.TailcatPortDefault(), 70, pal).OnSubmit(func() { d.save(a, "tailcat-port") })
		})
		tailcatFieldErr(c, pal, d, "tailcat-port")
		// A relay's URL is long: a field of its own, under the label.
		ui.Column(c).Gap(6).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, locales.SetTailcatRelay()).FontSize(13.5).TextColor(pal.text)
				ui.Text(c, locales.SetTailcatRelayDetail()).FontSize(12).TextColor(pal.textMuted).LineHeight(1.35)
			})
			textField(c, "tailcat-relay", &d.relay, remote.DefaultDERPMapURL, 0, pal).Font("monospace").OnSubmit(func() { d.save(a, "tailcat-relay") })
		})
		tailcatFieldErr(c, pal, d, "tailcat-relay")
	})
}

// tailcatSettingRow is settingRow, which is no row of its group's, here.
func tailcatSettingRow(c *ui.Context, pal *palette, label, detail string, control func()) {
	n := groupRows
	settingRow(c, pal, label, detail, control)
	groupRows = n
}

// tailcatListen is this machine as a host over Tailcat: the switch of
// kerod's tailcat listener here, its port, and the pair code Kero on
// other machines adds with.
func (a *App) tailcatListen(c *ui.Context, pal *palette, d *tailcatDraft) {
	on := hasTailcatListener(&a.set)
	detail := locales.SetTailcatListenDetail()
	if on {
		detail = locales.SetTailcatOn()
	}
	ui.Column(c).Padding(11, 0).Gap(8).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, locales.SetTailcatListen()).FontSize(13.5).TextColor(pal.text)
				ui.Text(c, detail).FontSize(12).TextColor(pal.textMuted).LineHeight(1.35)
			})
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Children(func() {
				toggle(c.Key("tailcat-listen"), &on, pal).OnClick(func() {
					s := a.set.Clone()
					if on {
						port := strings.TrimSpace(d.listenPort)
						if !settings.ValidTailcatPort(port) {
							port = settings.TailcatListenPortDefault()
						}
						s.SetTailcatListen(port)
					} else {
						s.SetTailcatListen("")
					}
					a.set = s
					a.resetTailcat()
				})
			})
		})
		if !on {
			return
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, locales.SetTailcatListenPort()).FontSize(13.5).TextColor(pal.text)
				ui.Text(c, locales.SetTailcatListenPortDetail()).FontSize(12).TextColor(pal.textMuted).LineHeight(1.35)
			})
			if in := textField(c, "tailcat-listen-port", &d.listenPort, settings.TailcatListenPortDefault(), 70, pal); in.Submitted() {
				d.save(a, "tailcat-listen-port")
			}
		})
		tailcatFieldErr(c, pal, d, "tailcat-listen-port")
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			code, col := d.code, pal.text
			switch {
			case d.codeErr != "":
				code, col = d.codeErr, pal.danger
			case code == "":
				code, col = locales.SetTailcatStarting(), pal.textMuted
			}
			ui.Text(c, code).Font("monospace").FontSize(12.5).TextColor(col).LineHeight(1.35).Selectable().MinWidth(0)
			ui.Row(c).Grow(1)
			if code == "" || d.codeErr != "" {
				return
			}
			icon, label := copyIcon, locales.HostCopyAddress()
			if time.Since(d.copied) < 1500*time.Millisecond {
				icon, label = checkIcon, locales.ToastCopied()
			}
			iconButton(c, "copy-tailcat", icon, label, pal, func() {
				c.WriteClipboard(code)
				d.copied = time.Now()
				go func() {
					time.Sleep(1500 * time.Millisecond)
					a.update(func() {})
				}()
			})
			iconButton(c, "change-tailcat", reloadIcon, locales.SetTailcatChange(), pal, func() {
				a.ss.confirm = &confirmation{
					open:    true,
					title:   locales.SetTailcatChange(),
					action:  locales.SetTailcatChange(),
					destroy: true,
					run:     func() { a.rotateTailcatCode() },
				}
			})
		})
	})
}

// tailcatFieldErr is the reason a field of the group failed, under it.
func tailcatFieldErr(c *ui.Context, pal *palette, d *tailcatDraft, key string) {
	if d.err.key == key {
		ui.Text(c, d.err.err).FontSize(12).TextColor(pal.danger)
	}
}

// save checks and keeps the group's field keyed key, which is one of its
// fields set.
func (d *tailcatDraft) save(a *App, key string) {
	var v string
	switch key {
	case "tailcat-port":
		if v = strings.TrimSpace(d.port); !settings.ValidTailcatPort(v) {
			d.err = tailcatField{key, v + ": a port is 1 to 65535"}
			return
		}
	case "tailcat-relay":
		v = strings.TrimSpace(d.relay)
		if v != "" && !settings.ValidDERPMapURL(v) {
			d.err = tailcatField{key, v + ": a relay is the https URL of a DERP map"}
			return
		}
	case "tailcat-listen-port":
		if v = strings.TrimSpace(d.listenPort); !settings.ValidTailcatPort(v) {
			d.err = tailcatField{key, v + ": a port is 1 to 65535"}
			return
		}
		s := a.set.Clone()
		s.SetTailcatListen(v)
		a.set, d.err = s, tailcatField{}
		return
	}
	s := a.set.Clone()
	switch key {
	case "tailcat-port":
		s.TailcatPort = v
	case "tailcat-relay":
		s.TailcatRelay = v
	}
	a.set, d.err = s, tailcatField{}
}

// defaultTailcatName is the name a tailcat host takes when the user
// gives none: a pair code names no name of a machine.
func defaultTailcatName(addr string) string {
	if _, err := remote.ParseAddr(strings.TrimSpace(addr)); err == nil {
		return "tailcat"
	}
	return locales.SetTailcatNamePlaceholder()
}

// addTailcatHost adds the host of the Tailcat group's form: its pair
// code, as the address, is a secret of the host's.
func (a *App) addTailcatHost() {
	d := &a.ss.tailcat
	addr := strings.TrimSpace(d.addr)
	if addr != "" && !strings.HasPrefix(addr, "tailcat://") {
		addr = "tailcat://" + addr
	}
	name := strings.TrimSpace(d.name)
	if name == "" {
		name = defaultTailcatName(addr)
	}
	if err := addTailcatHost(&a.set, name, addr); err != nil {
		d.err = tailcatField{"tailcat-addr", err.Error()}
		return
	}
	*d = tailcatDraft{port: d.port, relay: d.relay, listenPort: d.listenPort, code: d.code, codeErr: d.codeErr}
}

// addTailcatHost adds a tailcat host to s, if its code and name are new.
func addTailcatHost(s *settings.Settings, name, addr string) error {
	a, err := remote.ParseAddr(addr)
	if err != nil {
		return err
	}
	if a.Scheme != "tailcat" {
		return errNotTailcat
	}
	if s.TailcatNameTaken(name, -1) {
		return errTailcatNameTaken(name)
	}
	next := s.Clone()
	next.Tailcat = true
	next.TailcatHosts = append(next.TailcatHosts, settings.TailcatHost{Name: name, Addr: a.String(), Added: true})
	*s = next
	return nil
}

// tailcatCode asks kerod's key for this machine's pair code, in the
// background: its key makes the code, whether kerod runs or not.
func (a *App) tailcatCode() {
	d := &a.ss.tailcat
	if d.looking || !d.at.IsZero() {
		return
	}
	d.looking = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		code, err := remote.LocalTailcatAddress(ctx)
		cancel()
		a.update(func() {
			d.code, d.codeErr, d.looking, d.at = code, errorText(err), false, time.Now()
		})
	}()
}

// rotateTailcatCode gives this machine a new pair code, which the old
// one's no longer opens.
func (a *App) rotateTailcatCode() {
	k, err := remote.RotateTailcatKey(remote.TailcatKeyPath())
	if err != nil {
		a.toast(err.Error())
		return
	}
	d := &a.ss.tailcat
	*d = tailcatDraft{port: d.port, relay: d.relay, listenPort: d.listenPort}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		code, err := remote.TailcatAddress(ctx, k)
		cancel()
		a.update(func() {
			d.code, d.codeErr, d.at = errorText(err), errorText(err), time.Now()
			if err == nil {
				d.code = "tailcat://" + code
			}
		})
	}()
	a.toast(locales.SetTailcatChanged())
	// kerod starts its listener with the key again, which follow finds.
	s := a.set.Clone()
	s.TailcatRotate = !s.TailcatRotate
	a.set = s
}

var (
	errNotTailcat        = errString("the address is no tailcat pair code")
	errTailcatNotRunning = errString("kerod is not running here")
)

type errString string

func (e errString) Error() string { return string(e) }

func errTailcatNameTaken(name string) error { return errString(locales.SetHostNameTaken(name)) }

// hasTailcatListener reports whether s makes this machine a tailcat host.
func hasTailcatListener(s *settings.Settings) bool {
	_, _, ok := s.TailcatListen()
	return ok
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
