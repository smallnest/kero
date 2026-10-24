package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/proto"
	"kero/locales"
)

const endedShown = 5

type hostEnded struct {
	h *host
	e proto.EndedSession
}

func (a *App) endedSections(c *ui.Context, pal *palette) {
	var ended, archived []hostEnded
	for _, h := range a.hosts {
		for _, e := range h.ended {
			if e.Archived {
				archived = append(archived, hostEnded{h, e})
			} else {
				ended = append(ended, hostEnded{h, e})
			}
		}
	}
	newest := func(x, y hostEnded) int { return cmp.Compare(y.e.EndedAt, x.e.EndedAt) }
	slices.SortStableFunc(ended, newest)
	slices.SortStableFunc(archived, newest)
	a.endedSection(c, pal, "ended", locales.EndedTitle(), ended, &a.prefs.EndedOpen)
	a.endedSection(c, pal, "archived", locales.EndedArchived(), archived, &a.prefs.ArchivedOpen)
}

func (a *App) endedSection(c *ui.Context, pal *palette, key, title string, list []hostEnded, open *bool) {
	if len(list) == 0 {
		return
	}
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	hdr := ui.Row(c.Key("section-"+key)).Height(28).Margin(18, 0, 3, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Radius(6)
	if hdr.Clicked() {
		*open = !*open
		a.savePrefs()
	}
	hdr.ContextMenu(func(m *ui.Menu) { a.endedSectionMenu(m, key, list) })
	hdr.Children(func() {
		ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(pal.textMuted).SingleLine()
		ui.Textf(c, "%d", len(list)).FontSize(12.5).TextColor(pal.textFaint).FontFeatures("tnum")
		chev := ui.Icon(c, chevronIcon).Size(11, 11).TextColor(pal.textFaint)
		chev.Rotate(chev.AnimateTo("turn", 90*b2f(*open), ui.Snappy))
	})
	if !*open {
		return
	}
	shown := list
	if key == "ended" && !a.endedAll && len(list) > endedShown {
		shown = list[:endedShown]
	}
	now := c.Now()
	for _, he := range shown {
		a.endedRow(c, pal, he.h, he.e, now)
	}
	if len(shown) < len(list) {
		more := ui.Row(c.Key("show-all")).Height(26).Padding(0, 6, 0, inset).AlignItems(ui.Center).Radius(6)
		if more.Hovered() {
			more.Background(pal.rowHover)
		}
		if more.Clicked() {
			a.endedAll = true
		}
		more.Children(func() {
			ui.Text(c, locales.EndedShowAll(len(list))).FontSize(12.5).TextColor(pal.textFaint).SingleLine()
		})
	}
}

func (a *App) endedRow(c *ui.Context, pal *palette, h *host, e proto.EndedSession, now time.Time) {
	key := "e-" + h.name + "-" + e.Agent + "-" + e.ID
	row := ui.Row(c.Key(key)).Height(44).Padding(0, 6, 0, rowInset).Gap(10).Radius(8).AlignItems(ui.Center)
	pointerTooltip(c, row, "tip-"+key, h.shortDir(e.Dir))
	hovered := row.Hovered()
	if hovered {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		a.resumeEnded(h, e)
	}
	row.ContextMenu(func(m *ui.Menu) { a.endedMenu(m, h, e) })
	row.Children(func() {
		i := programIcons[e.Agent]
		if i == nil {
			i = terminalIcon
		}
		ui.Icon(c, i).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
		name := e.Name
		if name == "" {
			name = e.Agent
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(13).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if h.remote() {
					a.hostTag(c, pal, h)
				}
			})
			ui.Text(c, proto.DirName(e.Dir)).FontSize(12).TextColor(pal.textFaint).SingleLine().Ellipsis("…")
		})
		if hovered && !e.Archived {
			iconButton(c, "archive", archiveIcon, locales.EndedArchive(), pal, func() {
				a.callOn(h, "ended.archive", proto.IDParams{ID: e.ID}, nil)
			})
		} else {
			ui.Text(c, ago(e.EndedAt, now)).FontSize(12).TextColor(pal.textFaint).SingleLine().Shrink(0)
		}
	})
}

func (a *App) endedMenu(m *ui.Menu, h *host, e proto.EndedSession) {
	if m.Item(locales.EndedResume()).Chosen() {
		a.resumeEnded(h, e)
	}
	if e.Archived {
		if m.Item(locales.EndedRestore()).Chosen() {
			a.callOn(h, "ended.unarchive", proto.IDParams{ID: e.ID}, nil)
		}
	} else if m.Item(locales.EndedArchive()).Chosen() {
		a.callOn(h, "ended.archive", proto.IDParams{ID: e.ID}, nil)
	}
	m.Separator()
	if m.Item(locales.SessionCopyId()).Chosen() {
		a.copyText = e.ID
	}
	if m.Item(locales.SessionCopyDirectory()).Disabled(e.Dir == "").Chosen() {
		a.copyText = e.Dir
	}
	if m.Item(locales.SessionRevealIn(fileManager())).Disabled(e.Dir == "" || h.remote()).Chosen() {
		openPath(e.Dir)
	}
	m.Separator()
	if m.Item(locales.EndedDelete()).Chosen() {
		a.callOn(h, "ended.delete", proto.IDParams{ID: e.ID}, nil)
	}
}

func (a *App) endedSectionMenu(m *ui.Menu, key string, list []hostEnded) {
	each := func(method string) {
		for _, he := range list {
			a.callOn(he.h, method, proto.IDParams{ID: he.e.ID}, nil)
		}
	}
	if key == "ended" {
		if m.Item(locales.EndedArchiveAll()).Chosen() {
			each("ended.archive")
		}
		return
	}
	if m.Item(locales.EndedDeleteAll()).Chosen() {
		a.confirm = &confirmation{
			open:    true,
			title:   locales.EndedDeleteAllTitle(len(list)),
			detail:  locales.EndedDeleteAllDetail(),
			action:  locales.EndedDelete(),
			destroy: true,
			run:     func() { each("ended.delete") },
		}
	}
}

func (a *App) resumeEnded(h *host, e proto.EndedSession) {
	cl := h.cl
	if cl == nil {
		a.toast(locales.HostNotConnected(h.label()))
		return
	}
	go func() {
		var s proto.Session
		err := cl.Call("ended.resume", proto.IDParams{ID: e.ID}, &s)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.addPending(h, s)
			a.selectSession(s.ID)
		})
	}()
}
