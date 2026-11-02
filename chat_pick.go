package main

import (
	"github.com/egoist/mygo/ui"

	"kero/internal/proto"

	"kero/locales"
)

// chatsView is the palette over the Feishu app's chats: the one a choice
// shows the chat window of the session it opened for.
func (a *App) chatsView(c *ui.Context, pal *palette) {
	if a.pal.chats == nil {
		return
	}
	chats, session := a.pal.chats.Chats, a.pal.chatFor
	var shown []proto.FeishuChat
	for _, ch := range chats {
		if fuzzy(a.pal.query, ch.Name+" "+ch.ID) >= 0 {
			shown = append(shown, ch)
		}
	}
	a.pal.index = min(max(a.pal.index, 0), max(len(shown)-1, 0))
	var chosen *proto.FeishuChat
	ui.DialogBase(c.Key("chat-pick"), &a.pal.open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal.overlay).Justify(ui.Start).Padding(84, 0, 0, 0)
		floating(panel.Width(600).MaxHeight(460).Radius(14).Background(pal.popover), pal).Clip().Label(locales.ChatChooseTitle())
		ui.Row(c).Height(50).Shrink(0).Padding(0, 16).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, chatIcon).Size(16, 16).TextColor(pal.textMuted)
			in := ui.TextInputBase(c.Key("q"), &a.pal.query).Grow(1).FontSize(15).Placeholder(locales.ChatChoosePlaceholder()).AutoFocus()
			if in.Changed() {
				a.pal.index = 0
			}
			if c.Shortcut(0, ui.KeyDown) {
				a.pal.index = min(a.pal.index+1, len(shown)-1)
			}
			if c.Shortcut(0, ui.KeyUp) {
				a.pal.index = max(a.pal.index-1, 0)
			}
			if (in.Submitted() || c.Shortcut(0, ui.KeyEnter)) && len(shown) > 0 {
				chosen = &shown[a.pal.index]
			}
		})
		ui.Scroll(c.Key("results")).Grow(1).MinHeight(0).Padding(6).Children(func() {
			if len(shown) == 0 {
				ui.Text(c, locales.ChatNoChats()).FontSize(13).TextColor(pal.textFaint).Padding(12, 12).LineHeight(1.4)
			}
			for i, ch := range shown {
				row := ui.Row(c.Key(i)).Height(38).Padding(0, 10).Gap(10).AlignItems(ui.Center).Radius(8)
				if i == a.pal.index {
					row.Background(pal.accent.Alpha(0.12)).ScrollIntoView()
				} else if row.Hovered() {
					row.Background(pal.rowHover)
				}
				if row.Clicked() {
					chosen = &shown[i]
				}
				name := ch.Name
				if name == "" {
					name = ch.ID
				}
				row.Children(func() {
					ui.Text(c, name).FontSize(13.5).TextColor(pal.text).SingleLine().Ellipsis("…").Shrink(0).MaxWidth(300)
					ui.Text(c, ch.ID).FontSize(12.5).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					if ch.Session != "" {
						ui.Text(c, ch.Session).FontSize(12).TextColor(pal.textMuted).SingleLine().Shrink(1).MaxWidth(180)
					}
				})
			}
		})
	})
	if chosen != nil {
		a.pal.open, a.pal.chats = false, nil
		a.chatPick(session, chosen.ID)
	}
	if !a.pal.open {
		a.pal.chats = nil
	}
}
