package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"kero/internal/proto"

	"kero/locales"
)

// The Chat view is a session's conversation as a chat: the prompts the
// session got, the tools its agent ran, and the answers it gave, which
// the agent keeps in its transcript, with the Feishu chat the session is
// with, whose turns the chat got. A prompt sent here goes to the
// session's pane, as a prompt for its agent. The session's host
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
	// tools are the tools that show their details, by session and the
	// tool's line.
	tools map[string]map[string]bool
}

// toolOpen tells that a conversation's tool, at a line, shows its
// details: a click on its line folds or opens them.
func (a *App) toolOpen(session string, at int, open bool) bool {
	if a.side.chat.tools == nil {
		a.side.chat.tools = map[string]map[string]bool{}
	}
	t := a.side.chat.tools[session]
	if t == nil {
		t = map[string]bool{}
		a.side.chat.tools[session] = t
	}
	key := strconv.Itoa(at)
	if open {
		t[key] = !t[key]
	}
	return t[key]
}

// chatFeed is a session's chat window, as the host in it read it.
type chatFeed struct {
	h    *host     // the host of the read
	at   time.Time // when the last read answered
	busy bool      // a read is under way
	got  bool      // a read answered: the turns of the chat are as it says
	err  string    // why the last read failed
	feed proto.FeishuFeed
	pick string       // the chat the user chose, "" for the session's window
	list ui.ListState // the conversation's place, which follows its end
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
	if f.busy || f.got && len(f.feed.Lines) > 0 && time.Since(f.at) < chatRefresh {
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

// chatBody is the view: the conversation, in a column of a comfortable
// width. It shows the chat's turns, which Feishu sends: a prompt is not
// written here.
func (a *App) chatBody(c *ui.Context, pal *palette, s *proto.Session) {
	f := a.chatOf(s.ID)
	a.readChat(s)
	ui.Column(c.Key("lines")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.chatLines(c, pal, s, f)
	})
}

// chatWidth is how wide a conversation's column is, its bubbles inside
// it: as a chat app's, a long line reads best at a width.
const chatWidth = 720

// chatBubble is how wide a bubble's text may be: most of the column, as
// a chat app's.
const chatBubble = chatWidth * 0.88

// chatLines shows the conversation: what the user wrote to the right,
// the agent's messages to the left, its tools faint, and each turn's
// answer marked.
func (a *App) chatLines(c *ui.Context, pal *palette, s *proto.Session, f *chatFeed) {
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
		a.chatLine(c, pal, f, s.ID, i, lines[i])
	}).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(18, 16, 12, 8).Gap(10)
}

// read tells that a read of the window answered before.
func (f *chatFeed) read() bool { return f.feed.ID != "" }

// chatLine is a line of the conversation.
func (a *App) chatLine(c *ui.Context, pal *palette, f *chatFeed, session string, i int, l proto.FeishuLine) {
	text := strings.TrimSpace(l.Text)
	if text == "" && l.Detail == "" && l.Title == "" {
		return
	}
	// The column's: a line of the conversation, in the middle of the
	// pane at a fixed width, as wide as it, its own text inside a
	// bubble, the agent's or the user's.
	ui.Column(c).AlignSelf(ui.Center).WidthPercent(100).MaxWidth(chatWidth).AlignItems(ui.Stretch).Children(func() {
		switch l.Kind {
		case "tool":
			a.chatTool(c, pal, session, i, l)
			return
		case "think":
			ui.Column(c).MaxWidth(chatWidth*0.9).Padding(6, 2).Gap(2).Children(func() {
				ui.Text(c, locales.ChatThought()).FontSize(11).FontWeight(600).TextColor(pal.textFaint)
				a.chatMarkdown(c, pal, text, pal.textMuted, pal.textMuted.Alpha(0.08))
			})
			return
		case "note", "user":
			// The user's prompt: a Feishu message's, or one typed in
			// the terminal, its bubble as wide as the column, which a
			// long prompt takes.
			ui.Row(c).AlignSelf(ui.Stretch).Justify(ui.End).Children(func() {
				ui.Box(c).Grow(1)
				ui.Column(c).Padding(8, 13).Radius(14).Background(pal.accent).Children(func() {
					a.chatMarkdown(c, pal, text, chatOnAccent, ui.RGBA(255, 255, 255, 0.16))
				})
			})
			return
		}
		// The agent's: its text on a soft bubble, the turn's answer a
		// shade stronger.
		bg := pal.text.Alpha(0.05)
		if l.Answer {
			bg = pal.text.Alpha(0.085)
		}
		ui.Row(c).AlignSelf(ui.Stretch).MaxWidth(chatBubble).Children(func() {
			ui.Column(c).Padding(8, 13).Radius(14).Background(bg).Children(func() {
				a.chatMarkdown(c, pal, text, pal.text, pal.text.Alpha(0.07))
			})
		})
	})
}

// chatThemes are the Markdown themes of the chat windows, by day and
// by night: a message's colors, which differ, its own.
var chatThemes = map[string]markdown.Theme{}

// chatOnAccent is the color of the text on the user's prompts.
var chatOnAccent = ui.Hex("#ffffff")

// chatMarkdown shows a message's text as Markdown, as a chat app does:
// headings, lists, quotes, tables, code blocks, and links, which open in
// the browser. fg is the color of its text and code the fill of code
// spans, which differ with the message's surface.
func (a *App) chatMarkdown(c *ui.Context, pal *palette, text string, fg, code ui.Color) {
	key := "light"
	if pal.dark {
		key = "dark"
	}
	th, ok := chatThemes[key]
	if !ok || th.Text != fg || th.CodeBackground != code {
		th = markdown.Theme{
			FontSize:            13,
			LineHeight:          1.5,
			Text:                fg,
			Muted:               fg.Alpha(0.68),
			Strong:              fg,
			Code:                fg,
			CodeBackground:      code,
			BlockSpacing:        0.55,
			ItemSpacing:         0.2,
			QuoteBar:            code.Mix(fg, 0.3),
			QuoteText:           fg.Alpha(0.75),
			Rule:                code,
			CodeBlockBackground: code,
			CodeBlockBorder:     code,
			TableBorder:         code,
			TableHeader:         code,
		}
		chatThemes[key] = th
	}
	markdown.View(c, text, &markdown.Options{
		Theme:    &th,
		WrapCode: true,
		OnLink:   func(url string) { c.OpenURL(url) },
	})
}

// chatTool is a tool's line: its icon and its name, and what it ran,
// with the output under it, which a click folds or opens.
func (a *App) chatTool(c *ui.Context, pal *palette, session string, at int, l proto.FeishuLine) {
	g, name := gearIcon, strings.TrimPrefix(l.Text, "Tool ")
	if l.Text == "Result" {
		g, name = returnIcon, locales.ChatOutput()
	}
	// A tool's name is its line, and what it ran, after it: a line
	// without a name, as an older host's, says no more than it holds.
	arg := l.Title
	if name == l.Text {
		arg = ""
	}
	if arg == "" {
		arg = strings.TrimSpace(l.Detail)
	}
	ui.Column(c).AlignSelf(ui.Stretch).Children(func() {
		row := ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, g).Size(11, 11).TextColor(pal.textFaint).Shrink(0)
			ui.Text(c, name).FontSize(11.5).FontWeight(500).TextColor(pal.textMuted).SingleLine().Shrink(0)
			if arg != "" {
				ui.Text(c, shortText(arg, 90)).Font("monospace").FontSize(11).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
		})
		// A tool's arguments open under its line, and its output, which
		// the click takes from the line.
		open := a.toolOpen(session, at, row.Clicked())
		if row.Clicked() {
			c.Invalidate()
		}
		var detail string
		switch {
		case l.Text == "Result":
			if l.Detail != "" {
				detail = l.Detail
			}
		case l.Title != "":
			detail = l.Detail
		}
		if detail == "" || !open {
			return
		}
		ui.Scroll(c.Key("detail")).MaxHeight(240).Padding(8, 10).Margin(4, 20, 0, 18).Radius(8).Background(pal.text.Alpha(0.04)).Children(func() {
			ui.Text(c, shortText(detail, 4000)).Font("monospace").FontSize(11.5).TextColor(pal.textMuted).LineHeight(1.35).Wrap().Selectable()
		})
	})
}

// shortText is s in one line, cut at n characters.
func shortText(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
