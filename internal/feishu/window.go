package feishu

import (
	"context"
	"errors"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"kero/internal/proto"
)

// A chat window is a Feishu chat and a Kero session as one conversation.
// The session's pane shows the agent at work; the window shows the
// conversation the agent keeps, the way the chat sees it. The window
// sends a prompt to the chat as the user, which the bot then gives the
// session, and the bot's answer of the turn comes back to it.

// chatAPI reads the chats the app's bot is in.
type chatAPI interface {
	Chats(ctx context.Context) ([]proto.FeishuChat, error)
}

// larkChats is the app's chats, as the bot is in them.
type larkChats struct{ c *lark.Client }

func (a larkChats) Chats(ctx context.Context) ([]proto.FeishuChat, error) {
	var out []proto.FeishuChat
	page := ""
	for i := 0; i < 20; i++ {
		b := larkim.NewListChatReqBuilder().PageSize(100)
		if page != "" {
			b.PageToken(page)
		}
		res, err := a.c.Im.Chat.List(ctx, b.Build())
		if err != nil {
			return nil, err
		}
		if !res.Success() {
			return nil, fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
		}
		if res.Data == nil {
			break
		}
		for _, c := range res.Data.Items {
			if c == nil || c.ChatId == nil {
				continue
			}
			out = append(out, proto.FeishuChat{ID: *c.ChatId, Name: deref(c.Name), Type: deref(c.ChatMode)})
		}
		if res.Data.HasMore == nil || !*res.Data.HasMore || deref(res.Data.PageToken) == "" {
			break
		}
		page = *res.Data.PageToken
	}
	return out, nil
}

// Sends sends a chat window's prompt to its chat, as the user.
func (b *Bot) Sends(chatID, text string) (proto.FeishuSent, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return proto.FeishuSent{}, errors.New("there is no text to send")
	}
	var id string
	var err error
	b.mu.Lock()
	peer := b.chatLocked(chatKey(chatID, "")).peer
	b.mu.Unlock()
	if peer != "" {
		// The window's prompt reaches the chat as the user's, which the
		// bot then hears, and answers as it answers the user's.
		id, err = b.msgs.Send("open_id", peer, text)
	} else {
		id, err = b.msgs.Send("chat_id", chatID, feedRef+" "+chatID+"\n"+text)
	}
	if err != nil {
		return proto.FeishuSent{}, err
	}
	return proto.FeishuSent{Message: id}, nil
}

// feedRef starts a chat window's prompt, before the prompt itself.
const feedRef = "kero-window"

// windowEvent tells a message that a chat window sends: the bot hears it
// back with its first line, as it sends it. The window's prompt is no
// promise, and the bot leaves it to the chat: the window keeps the
// turns in order.
func (b *Bot) windowEvent(m *larkim.EventMessage) (leave, ok bool) {
	l := strings.Split(messageText(deref(m.Content)), "\n")
	if len(l) < 2 {
		return false, false
	}
	if !strings.HasPrefix(l[0], feedRef+" ") {
		return false, false
	}
	return b.claimRef(strings.TrimPrefix(l[0], feedRef+" "), deref(m.ChatId)), true
}

// claimRef tells whether another host takes the window's event first: the
// one that took it, if any, else the one that answers the chat's
// messages, if one is known. Its turn's answer then comes to that host.
func (b *Bot) claimRef(ref, chatID string) bool {
	// Kerod keeps the event to the first host that claims it, which is
	// the host the chat's messages go to, when /host named one.
	b.mu.Lock()
	want := b.chatLocked(chatKey(chatID, "")).host
	b.mu.Unlock()
	me := b.host()
	if want != "" {
		// The chat's host claims the event, and so it keeps it.
		if want != me {
			return true
		}
		b.takeIt(ref)
		return false
	}
	took := b.takeAt(ref, me)
	return took != me
}
