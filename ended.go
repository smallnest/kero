package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/cli"
	"kero/internal/proto"
	"kero/locales"
)

const endedShown = 5

// hostEnded is a conversation of a host, for the sidebar.
type hostEnded struct {
	h *host
	c proto.Conversation
}

// loadHostConversations asks a host's kerod for the conversations it
// remembers, for the sidebar, as of the host's count of the changes to
// them gen: a later answer, or a turn of Session history off, drops it.
func (a *App) loadHostConversations(h *host, gen uint64) {
	h.convAsked = true
	cl := h.cl
	if cl == nil {
		h.convAsked = false
		return
	}
	go func() {
		var list []proto.Conversation
		err := cl.Call("conversations", nil, &list)
		a.update(func() {
			if h.convGen != gen || !h.convAsked {
				return
			}
			h.convAsked = false
			if err != nil {
				return
			}
			h.convs, h.convKnown = list, true
		})
	}()
}

// syncEnded keeps the sidebar's ended conversations asking for those of
// every host, while Session history is on.
func (a *App) syncEnded() {
	for _, h := range a.hosts {
		if h.cl == nil || h.convAsked || h.convKnown {
			continue
		}
		a.loadHostConversations(h, h.convGen)
	}
}

func (a *App) endedSections(c *ui.Context, pal *palette) {
	var ended, archived []hostEnded
	for _, h := range a.hosts {
		for _, cv := range h.convs {
			// A conversation a pane runs is in the sidebar's sessions.
			if cv.Session != "" {
				continue
			}
			if cv.Archived {
				archived = append(archived, hostEnded{h, cv})
			} else {
				ended = append(ended, hostEnded{h, cv})
			}
		}
	}
	newest := func(x, y hostEnded) int { return cmp.Compare(y.c.Updated, x.c.Updated) }
	slices.SortStableFunc(ended, newest)
	slices.SortStableFunc(archived, newest)
	if len(ended)+len(archived) == 0 {
		a.skillHint(c, pal)
		return
	}
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
		a.endedRow(c, pal, he.h, he.c, now)
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

func (a *App) endedRow(c *ui.Context, pal *palette, h *host, cv proto.Conversation, now time.Time) {
	key := "e-" + h.name + "-" + cv.Agent + "-" + cv.ID
	row := ui.Row(c.Key(key)).Height(44).Padding(0, 6, 0, rowInset).Gap(10).Radius(8).AlignItems(ui.Center)
	pointerTooltip(c, row, "tip-"+key, h.shortDir(cv.Dir))
	hovered := row.Hovered()
	if hovered {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		a.resumeConversation(h, cv)
	}
	row.ContextMenu(func(m *ui.Menu) { a.endedMenu(m, h, cv) })
	row.Children(func() {
		i := programIcons[cv.Agent]
		if i == nil {
			i = terminalIcon
		}
		ui.Icon(c, i).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cv.Label()).FontSize(13).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if h.remote() {
					a.hostTag(c, pal, h)
				}
			})
			ui.Text(c, proto.DirName(cv.Dir)).FontSize(12).TextColor(pal.textFaint).SingleLine().Ellipsis("…")
		})
		if hovered && !cv.Archived {
			iconButton(c, "archive", archiveIcon, locales.EndedArchive(), pal, func() {
				a.conversationCall(h, "conversation.archive", cv, nil)
			})
		} else {
			ui.Text(c, ago(cv.Updated, now)).FontSize(12).TextColor(pal.textFaint).SingleLine().Shrink(0)
		}
	})
}

// conversationCall asks a host to forget or archive a conversation, as
// the sidebar's menus do.
func (a *App) conversationCall(h *host, method string, cv proto.Conversation, run func()) {
	a.callOn(h, method, proto.ConversationParams{Agent: cv.Agent, ID: cv.ID, Archived: method == "conversation.archive"}, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		if run != nil {
			run()
		}
	})
}

func (a *App) endedMenu(m *ui.Menu, h *host, cv proto.Conversation) {
	if m.Item(locales.EndedResume()).Chosen() {
		a.resumeConversation(h, cv)
	}
	if cv.Archived {
		if m.Item(locales.EndedRestore()).Chosen() {
			a.conversationCall(h, "conversation.unarchive", cv, nil)
		}
	} else if m.Item(locales.EndedArchive()).Chosen() {
		a.conversationCall(h, "conversation.archive", cv, nil)
	}
	m.Separator()
	if m.Item(locales.SessionCopyId()).Chosen() {
		a.copyText = cv.ID
	}
	if m.Item(locales.SessionCopyDirectory()).Disabled(cv.Dir == "").Chosen() {
		a.copyText = cv.Dir
	}
	if m.Item(locales.SessionRevealIn(fileManager())).Disabled(cv.Dir == "" || h.remote()).Chosen() {
		openPath(cv.Dir)
	}
	if !cv.Archived {
		m.Separator()
		if m.Item(locales.EndedDelete()).Chosen() {
			a.conversationCall(h, "conversation.forget", cv, nil)
		}
	}
}

func (a *App) endedSectionMenu(m *ui.Menu, key string, list []hostEnded) {
	each := func(method string) {
		for _, he := range list {
			a.conversationCall(he.h, method, he.c, nil)
		}
	}
	if key == "ended" {
		if m.Item(locales.EndedArchiveAll()).Chosen() {
			each("conversation.archive")
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
			run:     func() { each("conversation.forget") },
		}
	}
}

// skillHint tells, while nothing has ended, that ended sessions need
// Kero's skill, which tells kerod each conversation's id.
func (a *App) skillHint(c *ui.Context, pal *palette) {
	if now := c.Now(); now.Sub(a.skillsAt) > 5*time.Second {
		a.skillsAt = now
		a.skillMissing = slices.ContainsFunc(cli.SkillStates(), func(st cli.SkillState) bool { return st.State == "missing" })
	}
	if !a.skillMissing {
		return
	}
	hint := ui.Row(c.Key("ended-hint")).Margin(18, 0, 3, 0).Padding(6, 6, 6, rowInset).Radius(6)
	if hint.Hovered() {
		hint.Background(pal.rowHover)
	}
	if hint.Clicked() {
		a.openSettings("agents")
	}
	hint.Children(func() {
		ui.Text(c, locales.EndedSetUpSkill()).FontSize(12).TextColor(pal.textFaint).Grow(1).MinWidth(0)
	})
}
