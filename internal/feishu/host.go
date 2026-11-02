package feishu

import (
	"fmt"
	"slices"
	"strings"

	"kero/internal/proto"
)

// A Feishu app may connect from several machines, each with its own
// kerod: every host hears each event, and each reply to the bot's
// answer. The bot's answer names the event in its first line, which
// every host hears; the host that took the event marks the event's
// message with its answer, and the host the chat's messages go to
// answers that with the prompt. A chat goes to one host with /host, or
// else to the first that hears its message.

// heard is what a host says of an event, as the event's message carries
// it.
type heard struct {
	Host string
	Took bool
}

const (
	hopPrefix   = "kero-hop "
	heardPrefix = "kero-heard "
	fromPrefix  = "kero-from "
)

// hopText is an answer's first line: the event it answers, which every
// host hears.
func hopText(event string) string {
	if event == "" {
		return ""
	}
	return hopPrefix + event + "\n"
}

// heardText is a host's line of the event's message.
func (b *Bot) heardText(took bool) string {
	t := "0"
	if took {
		t = "1"
	}
	return heardPrefix + b.host() + " " + t + "\n"
}

// fromText is a chat's messages' host, set by /host.
func fromText(host string) string {
	if host == "" {
		return ""
	}
	return fromPrefix + host + "\n"
}

// hearing is the lines of a message: its event, the host the chat's
// messages go to, each host that answered, and the host that took the
// event.
func hearing(text string) (event, from string, posts []heard, claimed string) {
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, hopPrefix):
			event = strings.TrimSpace(strings.TrimPrefix(line, hopPrefix))
		case strings.HasPrefix(line, fromPrefix):
			from = strings.TrimSpace(strings.TrimPrefix(line, fromPrefix))
		}
	}
	return event, from, posts, claimed
}

// answer is what a host says of an event, and when its line came.
type answer struct {
	heard
	At int64
}

// host is the machine the bot runs on.
func (b *Bot) host() string {
	b.mu.Lock()
	host := b.hostname
	b.mu.Unlock()
	if host != "" {
		return host
	}
	var info proto.HostInfo
	if err := b.api.Call("host.info", nil, &info); err != nil || info.Hostname == "" {
		return ""
	}
	b.mu.Lock()
	b.hostname = info.Hostname
	b.mu.Unlock()
	return info.Hostname
}

// answers tells whether this host answers a chat's messages: the host
// the chat goes to, or, for none, the first of the hosts that hears one.
func (b *Bot) answers(chatID string) bool {
	me := b.host()
	b.mu.Lock()
	want := b.chatLocked(chatKey(chatID, "")).host
	hosts := slices.Clone(b.cfg.Host)
	b.mu.Unlock()

	if len(hosts) == 0 {
		return true
	}
	if want != "" {
		return want == me
	}
	// Until /host names one, the hosts that hear the messages answer
	// them: the first that takes an event answers its answer.
	return slices.Contains(hosts, me)
}

// hostCommand is /host: which machine the chat's messages go to, so
// that another Kero, in another window, takes them.
func (b *Bot) hostCommand(chatID, rest string) {
	key := chatKey(chatID, "")
	rest = strings.TrimSpace(rest)
	here := b.host()
	known := b.cfg.Host
	if rest == "" || rest == "list" {
		cur := "any"
		if host := b.chat(chatKey(chatID, "")).host; host != "" {
			cur = host
		}
		b.reply(chatID, fmt.Sprintf("This is %s. Messages here go to: %s. The hosts: %s.",
			here, cur, firstNotEmpty(strings.Join(known, ", "), here)))
		return
	}
	if rest == "here" || rest == "me" {
		rest = here
	}
	if rest != "all" {
		found := rest == here
		for _, h := range known {
			found = found || h == rest
		}
		if !found {
			b.reply(chatID, fmt.Sprintf("Kero knows no host %q. feishu-host names the hosts that share the app.", rest))
			return
		}
	}
	host := rest
	if rest == "all" {
		host = ""
	}
	b.mu.Lock()
	c := b.chatLocked(key)
	c.host = host
	if host != here {
		// The event this host's answer names: the new host answers the
		// answer now.
		b.wants[key] = host
	} else {
		delete(b.wants, key)
	}
	b.mu.Unlock()
	b.save()
	if host == "" {
		b.reply(chatID, "The first host that hears a message answers it.")
		return
	}
	if host == here {
		b.reply(chatID, "Messages here go to "+host+".")
		return
	}
	b.reply(chatID, fmt.Sprintf("Messages here go to %s. Its session is there: open Kero on it to see a turn. /host here brings the chat back, when this host hears it.", host))
}
