package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/proto"

	"kero/locales"
)

// The Chat view is a session's conversation as a chat: the prompts the
// session got, the tools its agent ran, and the answers it gave, which
// the agent keeps in its transcript, with the Feishu chat the session is
// with, whose turns the chat got. A prompt sent here goes to that chat as
// the user's message, which the bot gives the session. The session's host
// reads it (feishu.feed) as the view opens and as the chat's turns go on.

// chatRefresh is how soon the view reads a chat window again: a chat's
// turn ends with the host's state, as it goes idle.
const chatRefresh = time.Second

// chatShow is how many of a conversation's lines the view shows when it
// is long: the end of it.
const chatShow = 200

// chatState is the Chat view's, by session.
type chatState struct {
	feeds map[string]*chatFeed // the sessions' chat windows
}

// chatFeed is a session's chat window, as the host in it read it.
type chatFeed struct {
	h     *host     // the host of the read
	at    time.Time // when the last read answered
	busy  bool      // a read is under way
	got   bool      // a read answered: the turns of the chat are as it says
	err   string    // why the last read failed
	feed  proto.FeishuFeed
	pick  string       // the chat the user chose, "" for the session's window
	draft string       // the field's text, which a send clears
	list  ui.ListState // the conversation's place, which follows its end
}

// chatOf is a session's chat window.
func (a *App) chatOf(id string) *chatFeed {
	if a.side.chat.feeds == nil {
		a.side.chat.feeds = map[string]*chatFeed{}
	}
	f := a.side.chat.feeds[id]
	if f == nil {
		f = &chatFeed{}
		a.side.chat.feeds[id] = f
		f.got = true // a read at once
	}
	return f
}

// chatChanged tells the view that a host shows a new state: the hosts'
// chat windows read their turns again, as a chat's turn ends.
func (a *App) chatChanged(h *host) {
	for id, f := range a.side.chat.feeds {
		if a.hostOf(id) == h {
			f.got = false
		}
	}
}

// readChat reads a session's chat window, as the view opens and after a
// chat's turn, and as the host's state changes.
func (a *App) readChat(s *proto.Session) {
	f := a.chatOf(s.ID)
	h := a.hostOf(s.ID)
	if f.busy || f.got && time.Since(f.at) < chatRefresh {
		return
	}
	if h.cl == nil {
		// A host that connects reads it as it does.
		return
	}
	f.busy, f.got, f.h = true, true, h
	cl := h.cl
	params := proto.FeishuFeedParams{Session: s.ID, Chat: f.pick}
	// It reads from a goroutine of its own: a call from the frame's would
	// wait on the frame.
	go func() {
		var out proto.FeishuFeed
		err := cl.Call("feishu.feed", params, &out)
		a.main(func() {
			if a.side.chat.feeds[s.ID] != f {
				return
			}
			f.busy, f.at = false, time.Now()
			if err != nil {
				f.err = err.Error()
				return
			}
			f.err, f.feed = "", out
		})
	}()
}

// chatPick tells a session's window a chat to show.
func (a *App) chatPick(session, chat string) {
	f := a.chatOf(session)
	f.got, f.pick, f.feed.Chat = true, chat, chat
	if s := a.session(session); s != nil {
		a.readChat(s)
	}
}

// sendChat sends the field's text to the session's chat.
func (a *App) sendChat(s *proto.Session) {
	f := a.chatOf(s.ID)
	text := strings.TrimSpace(f.draft)
	if text == "" || f.feed.Chat == "" {
		return
	}
	h := a.hostOf(s.ID)
	if h.cl == nil {
		a.toast(locales.ChatHostOffline())
		return
	}
	if err := h.cl.Call("feishu.send", proto.FeishuSend{Chat: f.feed.Chat, Text: text}, nil); err != nil {
		a.toast(err.Error())
		return
	}
	f.draft = ""
	// The turn reads from the host as its state changes.
	f.got = false
}

// chatBody is the view: the chat at its top, the conversation, and the
// field under it.
func (a *App) chatBody(c *ui.Context, pal *palette, s *proto.Session) {
	f := a.chatOf(s.ID)
	a.readChat(s)
	a.chatHead(c, pal, s, f)
	ui.Column(c.Key("lines")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.chatLines(c, pal, f)
	})
	a.chatField(c, pal, s, f)
}

// chatHead is the view's top: the chat the session is with, the way to
// one of the app's, and the state of the read.
func (a *App) chatHead(c *ui.Context, pal *palette, s *proto.Session, f *chatFeed) {
	ui.Row(c.Key("head")).Height(headerH).Shrink(0).Padding(0, 8, 0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		name := f.feed.Name
		if name == "" {
			name = f.feed.Chat
		}
		if f.feed.Chat == "" {
			name = locales.ChatSessionNone()
		}
		ui.Icon(c, chatIcon).Size(13, 13).TextColor(pal.textMuted).Shrink(0)
		ui.Text(c, name).FontSize(12.5).FontWeight(600).TextColor(pal.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Tooltip(f.tip())
		if f.err != "" {
			ui.Text(c, f.err).FontSize(11.5).TextColor(pal.danger).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Tooltip(f.err)
		}
		ui.Box(c).Grow(1).AlignSelf(ui.Stretch).DragWindow()
		if h := a.hostOf(s.ID); h.cl != nil {
			iconButton(c, "chat-chats", searchIcon, locales.ChatChooseTitle(), pal, func() {
				a.defer_(func() { a.openChats(s.ID) })
			})
		}
	})
	infoRule(c, pal)
}

// tip is the chat's ID, and its host.
func (f *chatFeed) tip() string {
	if f.feed.Chat == "" {
		return ""
	}
	return f.feed.Chat + " · " + f.feed.Host
}

// chatLines shows the conversation: what the user wrote to the right,
// the agent's messages to the left, its tools faint, and each turn's
// answer marked.
func (a *App) chatLines(c *ui.Context, pal *palette, f *chatFeed) {
	lines := f.feed.Lines
	if len(lines) > chatShow {
		lines = lines[len(lines)-chatShow:]
	}
	if len(lines) == 0 {
		msg := locales.ChatNothing()
		switch {
		case f.busy && !f.read():
			msg = locales.ChatReadingNow()
		case f.err != "":
			msg = f.err
		case f.feed.Chat == "":
			msg = locales.ChatSessionNone()
		}
		a.sideMessage(c, pal, locales.SideChat(), msg)
		return
	}
	f.list.FollowEnd, f.list.FollowAnimation = true, &ui.Smooth
	ui.List(c.Key("chat"), &f.list, len(lines), func(i int) {
		a.chatLine(c, pal, lines[i])
	}).Grow(1).MinHeight(0).Padding(10, 12, 10, 12).Gap(6)
}

// read tells that a read of the window answered before.
func (f *chatFeed) read() bool { return f.feed.ID != "" }

// chatLine is a line of the conversation.
func (a *App) chatLine(c *ui.Context, pal *palette, l proto.FeishuLine) {
	text := strings.TrimSpace(l.Text)
	if text == "" {
		return
	}
	switch l.Kind {
	case "tool":
		ui.Text(c, text).FontSize(11.5).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Ellipsis("…").Padding(0, 2).Tooltip(text)
	case "note", "user":
		ui.Row(c).AlignSelf(ui.End).MaxWidthPercent(88).Children(func() {
			ui.Column(c).Padding(6, 10).Radius(10).Background(pal.control).Children(func() {
				ui.Text(c, text).FontSize(12.5).TextColor(pal.text).Wrap().Selectable()
			})
		})
	default:
		body := ui.Column(c).Padding(6, 10).Radius(10).Children(func() {
			ui.Text(c, text).FontSize(12.5).TextColor(pal.textMuted).Wrap().Selectable()
		})
		if l.Answer {
			// The turn's answer: the text, on the chip, as a chat shows
			// what it answered.
			body.Background(pal.chip)
		}
	}
}

// chatField is the field under the conversation: a prompt for the chat,
// which Enter sends.
func (a *App) chatField(c *ui.Context, pal *palette, s *proto.Session, f *chatFeed) {
	draft := f.draft
	ready := f.feed.Chat != "" && a.hostOf(s.ID).cl != nil
	infoRule(c, pal)
	ui.Row(c.Key("field")).Padding(8, 12, 10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		var in ui.Element
		frame := ui.Row(c.Key("frame")).Grow(1).MinWidth(0).Cursor(ui.CursorText).Children(func() {
			ph := locales.ChatPlaceholder()
			if !ready {
				ph = locales.ChatSessionNone()
			}
			in = ui.TextInputBase(c.Key("in"), &draft).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Padding(0, 8).FontSize(12.5).Placeholder(ph).Label(ph)
		})
		f.draft = draft
		field(c, frame, in.Focused(), pal).Padding(0)
		if in.Submitted() && ready {
			a.defer_(func() { a.sendChat(s) })
		}
		if fieldButton(c, "chat-send", locales.ChatSend(), true, pal) && ready {
			a.defer_(func() { a.sendChat(s) })
		}
	})
}

// openChats opens the palette on the app's chats, a choice showing the
// session's window of one.
func (a *App) openChats(session string) {
	h := a.hostOf(session)
	if h.cl == nil {
		a.toast(locales.ChatHostOffline())
		return
	}
	// The call is no frame's: it waits on a read, and a frame's would not
	// wait on it.
	cl := h.cl
	go func() {
		var chats proto.FeishuChats
		if err := cl.Call("feishu.chats", nil, &chats); err != nil {
			a.main(func() { a.toast(err.Error()) })
			return
		}
		a.main(func() {
			a.pal = paletteState{open: true, chats: &chats, chatFor: session, gen: a.pal.gen + 1}
		})
	}()
}
