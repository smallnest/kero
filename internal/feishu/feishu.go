// Package feishu connects a Feishu app to kerod's sessions: a message to
// the bot, or to it in a group, goes to the coding agent in the chat's
// session, and the end of the pane's screen comes back to the chat
// (docs/feishu.md).
package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"kero/internal/proto"
	"kero/internal/settings"
)

// Luna is the API: what the bridge asks of kerod, which inside kerod is
// kerod itself (the bridge's Local), and larkAPI of Feishu, so that a
// test takes a fake of each.
type Luna interface {
	// State is every session, as the "state" method gives it.
	State() (proto.State, error)
	// Call runs a control method, as a client's, decoding into out.
	Call(method string, params, out any) error
	// Kept is a message as Feishu has it, which the hosts of a shared app
	// hear.
	Kept(id string) (proto.FeishuMessage, error)
}

// kerod is the API of the kerod that runs the bot, for what every host
// of a shared app shares: Run sets it, and a test's bot keeps what it
// asks of the fake.
var kerod Luna

// clientCall is the part of client.Client a Luna is built from.
type clientCall interface {
	Call(method string, params, out any) error
}

// Remote is a Luna over a client's connection.
type Remote struct{ C clientCall }

type myLuna = Luna

func (r Remote) State() (proto.State, error) {
	var st proto.State
	err := r.C.Call("state", nil, &st)
	return st, err
}

func (r Remote) Call(method string, params, out any) error { return r.C.Call(method, params, out) }

func (r Remote) Kept(id string) (proto.FeishuMessage, error) {
	var res proto.FeishuMessage
	err := r.C.Call("feishu.message", proto.IDParams{ID: id}, &res)
	return res, err
}

// Messages is what Feishu does for a chat.
type Messages interface {
	// Send sends text to a chat, or to a person by open_id, and returns
	// the message's ID.
	Send(receiveIDType, receiveID, text string) (string, error)
	// SendCard sends a card's JSON to a chat, and returns the message's
	// ID.
	SendCard(receiveIDType, receiveID, cardJSON string) (string, error)
	// UpdateCard replaces the card of a message the bot sent.
	UpdateCard(messageID, cardJSON string) error
	// React marks a message with an emoji, by its Feishu name, as
	// "OnIt".
	React(messageID, emoji string) error
	// Download is a message's file or image, by its file_key or
	// image_key.
	Download(key string, image bool) ([]byte, error)
	// SendReply sends text as an answer to a message, and returns the
	// answer's ID.
	SendReply(messageID, text string) (string, error)
	// Get is a message by its ID.
	Get(messageID string) (feishuMessage, error)
}

// feishuMessage is a message an event names.
type feishuMessage struct {
	MessageId   *string `json:"message_id,omitempty"`
	ChatId      *string `json:"chat_id,omitempty"`
	ChatType    *string `json:"chat_type,omitempty"`
	MessageType *string `json:"msg_type,omitempty"`
	Content     *string `json:"content,omitempty"`
	ParentId    *string `json:"parent_id,omitempty"`
	RootId      *string `json:"root_id,omitempty"`
}

func (a *larkAPI) Get(messageID string) (feishuMessage, error) {
	var m feishuMessage
	res, err := a.c.Im.Message.Get(context.Background(),
		larkim.NewGetMessageReqBuilder().MessageId(messageID).Build())
	if err != nil {
		return m, err
	}
	if !res.Success() {
		return m, fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	if res.Data == nil || len(res.Data.Items) == 0 || res.Data.Items[0] == nil {
		return m, errors.New("Feishu returned no message")
	}
	it := res.Data.Items[0]
	m.MessageId, m.ChatId, m.MessageType = it.MessageId, it.ChatId, it.MsgType
	if it.Body != nil {
		m.Content = it.Body.Content
	}
	m.ParentId, m.RootId = it.ParentId, it.RootId
	return m, nil
}

// Bot is one machine's connection to a Feishu app.
type Bot struct {
	cfg   settings.Feishu
	env   []string
	api   myLuna
	lark  *lark.Client
	ws    *larkws.Client
	msgs  Messages
	files Files  // where the messages' files go, nil in a test that takes none
	me    string // the open_id of the one who ran kero feishu setup
	logf  func(format string, args ...any)
	sync  bool // turns run in the handler's goroutine, for tests
	// began is when this bot started: the turns that ran before it are
	// the ones it takes on.
	began time.Time

	mu    sync.Mutex
	chats map[string]*chat // by chat key
	// binds are the chats' sessions, by chat key, which kerod's state
	// file keeps across its restarts.
	binds map[string]string
	seen  map[string]bool // event IDs, for a while
	order []string        // seen's, oldest first
	// notes tell what the bot's cards are about, by message ID, for the
	// button's click: kerod's directory keeps them across its restarts.
	notes map[string]record
	// heard tells, by event and host, when that host's answer came, as
	// Unix milliseconds.
	heard    map[string]int64
	hostname string
	// notice tells a chat that it has no host to take its messages.
	notice func(chatID, text string)
	// answers tells the messages this bot answered, which the hosts of
	// the app hear and this one hears again.
	answered map[string]bool
	// wants is the host the chat's answer names, and claims the event it
	// answers.
	wants  map[string]string
	claims map[string]string
	// kard are the chats' cards, which their next turns replace.
	kard map[string]kard
	// noticeSend is where a message that names an event goes: a chat's,
	// in place of an answer.
	noticeSend func(receiveIDType, receiveID, text string) (string, error)
	// times are the chats' turns, for a chat window, and pins the chat
	// each session's window sends to, nil in a test that has none.
	times map[string]turnTimes
	pins  map[string]string
	// Chats reads the chats the bot is in, for a chat window's.
	Chats func(ctx context.Context) ([]proto.FeishuChat, error)
}

// chat is a Feishu chat, bound to a session as its messages say.
type chat struct {
	id    string // the session it is bound to
	chat  string // its chat ID, which its answers go to
	event string // the event its answer names
	key   string // the chat's key
	busy  bool   // a turn runs: the bot takes the next one after it
	// running is the turn the chat runs, as its message left it.
	running turnInfo
	// command is the chat's agent, its command run in the background: its
	// session, its agent's session to continue, and where its turn's
	// output ended.
	cmd   string
	agent string
	dir   string
	sid   string
	from  int64
	// peer is the open_id of the one the chat is with, for a p2p chat:
	// a window's prompt reaches it as the user's, which the bot answers.
	peer string

	// host is the machine the chat's messages go to, set by /host: "" for
	// the one that hears them first, as kerod's directory keeps it.
	host string
	// card is the chat's last card, which a turn's replaces.
	card cardMsg
	// allowed are the tools the chat allowed for the session, which its
	// next turns run without asking.
	allowed map[string]bool
}

// turnInfo is the turn that runs in a chat: when it started, its
// message, and the event its end answers. A turn carries its own, so that
// the next one, or this bot's take on a turn of a restart, cannot change
// it under it.
type turnInfo struct {
	start time.Time
	chat  string
	text  string
	event string
	// asked is the answer a follow-up message answers, which the agent's
	// answer of the turn it continues may come under.
	asked string
	// pane is the pane an answer of the chat's went to, and start is
	// zero: the turn is the answer's, and the prompt's, whose text the
	// answer ends.
	pane string
}

// cardMsg is a card the bot sent, and the turn it shows.
type cardMsg struct {
	id   string
	turn string
	text string // what its card showed, as PATCH takes no same content
	seen bool   // a turn told it it works
}

// Config is what a bot needs.
type Config struct {
	Settings settings.Feishu
	Secret   settings.FeishuSecret
	Luna     Luna
	Log      func(format string, args ...any)
	// Env is the environment the app runs in, which the commands it runs
	// take: kerod's, which runs them, may be a launchd job's.
	Env []string
}

// New makes the bot; Run holds its connection.
func New(c Config) *Bot {
	if c.Log == nil {
		c.Log = func(string, ...any) {}
	}
	b := &Bot{
		cfg:      c.Settings,
		env:      c.Env,
		api:      c.Luna,
		me:       c.Secret.Me,
		logf:     c.Log,
		chats:    map[string]*chat{},
		binds:    map[string]string{},
		seen:     map[string]bool{},
		notes:    map[string]record{},
		heard:    map[string]int64{},
		wants:    map[string]string{},
		claims:   map[string]string{},
		times:    map[string]turnTimes{},
		pins:     map[string]string{},
		answered: map[string]bool{},
		began:    time.Now(),
	}
	b.lark = lark.NewClient(c.Secret.AppID, c.Secret.AppSecret, lark.WithLogLevel(larkcore.LogLevelError))
	b.msgs = &larkAPI{c: b.lark}
	b.Chats = larkChats{c: b.lark}.Chats
	b.files = b.msgs
	b.noticeSend = b.msgs.Send
	event := larkdispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(b.onMessage).OnP2CardActionTrigger(b.onCardAction)
	b.ws = larkws.NewClient(c.Secret.AppID, c.Secret.AppSecret,
		larkws.WithEventHandler(event),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithOnError(func(err error) { b.logf("feishu: %v", err) }))
	return b
}

// Run holds the connection until ctx ends, reconnecting as Feishu asks.
func (b *Bot) Run(ctx context.Context) error {
	kerod = b.api
	b.load()
	return b.ws.Start(ctx)
}

// Files downloads the messages' files from Feishu.
type Files interface {
	Download(key string, image bool) ([]byte, error)
	// SendReply sends text as an answer to a message, and returns the
	// answer's ID.
	SendReply(messageID, text string) (string, error)
}

// larkAPI sends to Feishu.
type larkAPI struct{ c *lark.Client }

func (a *larkAPI) Send(receiveIDType, receiveID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	res, err := a.c.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(receiveIDType).
			Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(receiveID).MsgType("text").Content(string(content)).Build()).
			Build())
	if err != nil {
		return "", err
	}
	if !res.Success() {
		return "", fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	if res.Data == nil || res.Data.MessageId == nil {
		return "", errors.New("Feishu returned no message")
	}
	return *res.Data.MessageId, nil
}

func (a *larkAPI) SendCard(receiveIDType, receiveID, cardJSON string) (string, error) {
	res, err := a.c.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(receiveIDType).
			Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(receiveID).MsgType("interactive").Content(cardJSON).Build()).
			Build())
	if err != nil {
		return "", err
	}
	if !res.Success() {
		return "", fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	if res.Data == nil || res.Data.MessageId == nil {
		return "", errors.New("Feishu returned no message")
	}
	return *res.Data.MessageId, nil
}

func (a *larkAPI) UpdateCard(messageID, cardJSON string) error {
	res, err := a.c.Im.Message.Patch(context.Background(),
		larkim.NewPatchMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewPatchMessageReqBodyBuilder().Content(cardJSON).Build()).
			Build())
	if err != nil {
		return err
	}
	if !res.Success() {
		return fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	return nil
}

func (a *larkAPI) React(messageID, emoji string) error {
	res, err := a.c.Im.MessageReaction.Create(context.Background(),
		larkim.NewCreateMessageReactionReqBuilder().
			MessageId(messageID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().EmojiType(emoji).Build()).
				Build()).
			Build())
	if err != nil {
		return err
	}
	if !res.Success() {
		return fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	return nil
}

func (a *larkAPI) Download(key string, image bool) ([]byte, error) {
	typ := "file"
	if image {
		typ = "image"
	}
	res, err := a.c.Im.MessageResource.Get(context.Background(),
		larkim.NewGetMessageResourceReqBuilder().MessageId(key).FileKey(key).Type(typ).Build())
	if err != nil {
		return nil, err
	}
	if !res.Success() {
		return nil, fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	return io.ReadAll(io.LimitReader(res.File, 64<<20))
}

func (a *larkAPI) SendReply(messageID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	res, err := a.c.Im.Message.Reply(context.Background(),
		larkim.NewReplyMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().MsgType("text").Content(string(content)).Build()).
			Build())
	if err != nil {
		return "", err
	}
	if !res.Success() {
		return "", fmt.Errorf("Feishu answered %d %s", res.Code, res.Msg)
	}
	if res.Data == nil || res.Data.MessageId == nil {
		return "", errors.New("Feishu returned no message")
	}
	return *res.Data.MessageId, nil
}

// Probe takes a tenant access token for an app, telling a wrong app ID or
// secret. The rest, as the bot capability and scopes, shows when the
// connection and the first reply fail, and the log says why.
func Probe(appID, appSecret string) error {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	res, err := http.Post(lark.FeishuBaseUrl+"/open-apis/auth/v3/tenant_access_token/internal",
		"application/json; charset=utf-8", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("Feishu is unreachable: %w", err)
	}
	defer res.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return fmt.Errorf("Feishu answered %s", res.Status)
	}
	if out.Code != 0 {
		return fmt.Errorf("Feishu refused the app: %d %s (check the App ID and App Secret)", out.Code, out.Msg)
	}
	return nil
}

// SendText sends text to a chat (receive_id_type chat_id) or a person
// (open_id), as kero feishu send does.
func SendText(appID, appSecret, receiveIDType, receiveID, text string) error {
	c := lark.NewClient(appID, appSecret, lark.WithLogLevel(larkcore.LogLevelError))
	_, err := (&larkAPI{c: c}).Send(receiveIDType, receiveID, text)
	return err
}
