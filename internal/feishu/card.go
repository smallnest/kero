package feishu

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kero/internal/settings"
)

// Card is a turn's card: its title, the agent's text, the state it shows,
// the time it took, what the agent used, and the session it is about,
// which its button opens in Kero. Meta is the turn's summary, under the
// text; Note what it says under it, as an error's words.
type Card struct {
	Title    string
	Text     string
	State    string // working, done, asks or error
	Duration time.Duration
	Usage    Usage
	Session  string
	Meta     string
	Note     string
}

// cardState is a card's header color: grey while the agent works, green
// once it is done, orange while it asks, red when it failed.
var cardState = map[string]string{"working": "grey", "done": "green", "asks": "yellow", "error": "red"}

// cards tells whether turns show as cards, as the configuration says.
func cards() bool { return settings.Load().FeishuCards }

// markdown tells whether a card's text is Markdown.
func markdown() bool { return settings.Load().FeishuMarkdown }

// maxAnswer is how much of a turn's text a card shows: Feishu takes 30 KB
// for a card, and the escapes and the rest of it take some.
const maxAnswer = 20 << 10

// cardJSON is a card's content, as the interactive message takes it: the
// card's JSON, as a string.
func cardJSON(c Card, lang string) (string, error) {
	if c.State == "" {
		c.State = "done"
	}
	color := cardState[c.State]
	if color == "" {
		color = "blue"
	}
	title := orDash(c.Title)
	if c.State == "working" {
		title += " · " + word(lang, "state.working")
	}
	body := plainBody(strings.TrimSpace(c.Text))
	if len(body) > maxAnswer {
		body = truncate(body, maxAnswer)
	}
	// The agent's words are Markdown, as they wrote it: its titles,
	// lists, tables and code blocks read as they are written.
	if markdown() {
		body = markdownText(body)
	}
	var elems []any
	if body != "" {
		text := map[string]any{"tag": "markdown", "content": body}
		if markdown() {
			elems = append(elems, text)
		} else {
			elems = append(elems, map[string]any{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": body}})
		}
	}

	// A rule keeps the footer apart from the text above it.
	if len(elems) > 0 {
		elems = append(elems, map[string]any{"tag": "hr"})
	}
	// The footer is the card's last, quiet line: its state in colored
	// words, the time the turn took, and what the agent used. A card with
	// none of them, as a note's, keeps a line without them.
	footer := c.footer(lang)
	elems = append(elems, map[string]any{"tag": "markdown", "content": footer, "text_size": "notation"})
	// The session, under the footer, only when its button is off: the
	// button opens it.
	if c.Session != "" && !settings.Load().FeishuOpenButton {
		elems = append(elems, map[string]any{"tag": "markdown", "content": escapeMarkdown(c.Session), "text_size": "notation"})
	}
	if c.Session != "" && settings.Load().FeishuOpenButton {
		// Schema 2.0 puts a button in the body itself: it has no action
		// element to group buttons.
		elems = append(elems, map[string]any{
			"tag":   "button",
			"type":  "primary",
			"text":  map[string]any{"tag": "plain_text", "content": word(lang, "open")},
			"value": map[string]any{"kero": "open", "session": c.Session},
		})
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": title},
			"template": color,
		},
		"body": map[string]any{"direction": "vertical", "elements": elems},
	}
	data, err := json.Marshal(card)
	return string(data), err
}

// footer is a card's last line: its state in colored words, what the
// agent used, and the time the turn took, each of them only when it is
// known; a card with none of them, as a note's, has no footer.
func (c Card) footer(lang string) string {
	parts := []string{}
	if c.Usage.Model != "" {
		// The model, in its own font, apart from the plain words.
		parts = append(parts, codeSpan(c.Usage.Model))
	}
	if used := c.Usage.text(); used != "" {
		parts = append(parts, used)
	}
	if c.Duration > 0 {
		parts = append(parts, durationText(c.Duration))
	}
	if len(parts) == 0 {
		return ""
	}
	tag := "<text_tag color='" + textTagColor(c.State) + "'>" + escapeMarkdown(word(lang, "state."+c.State)) + "</text_tag>"
	return strings.Join(append([]string{tag}, parts...), " · ")
}

// textTagColor is a card's state as a colored tag takes it: green once
// the agent is done, orange while it asks, red when it failed, and
// neutral while it works.
func textTagColor(state string) string {
	switch state {
	case "done":
		return "green"
	case "asks":
		return "orange"
	case "error":
		return "red"
	}
	return "neutral"
}

// durationText is a time as a card shows it: 32s, 1m24s, or 1h5m3s.
func durationText(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	h, m, sec := s/3600, s/60%60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, sec)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

// Usage is what a turn used, as its agent's result reports it: the tokens
// it took, with the cached ones, and gave, and the conversation's
// context.
type Usage struct {
	Model                string
	Input, Output, Cache int
	Context, MaxContext  int
}

// UsageOf reads what a Claude Code turn used from its stream's result.
func UsageOf(raw string) Usage {
	var u Usage
	var m struct {
		Type  string `json:"type"`
		Usage struct {
			Input     int `json:"input_tokens"`
			Output    int `json:"output_tokens"`
			CacheRead int `json:"cache_read_input_tokens"`
			CacheNew  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			ContextWindow int    `json:"contextWindow"`
			Canonical     string `json:"canonicalModel"`
		} `json:"modelUsage"`
	}
	for _, line := range strings.Split(raw, "\n") {
		if json.Unmarshal([]byte(line), &m) != nil || m.Type != "result" {
			continue
		}
		u = Usage{Input: m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheNew,
			Output: m.Usage.Output, Cache: m.Usage.CacheRead}
		for name, mu := range m.ModelUsage {
			if mu.ContextWindow > u.MaxContext {
				u.MaxContext = mu.ContextWindow
			}
			// The model the turn's answer names, as its gateway names it,
			// over one the usage does, which the gateway may not know.
			if u.Model == "" {
				u.Model = firstNotEmpty(mu.Canonical, name)
			}
		}
		u.Context = u.Input + u.Output
	}
	return u
}

// text is what a turn used, as a card's footer shows it: the tokens it
// took and gave, and the conversation's context, each only when known.
func (u Usage) text() string {
	var parts []string
	if u.Input > 0 {
		parts = append(parts, "↑"+countText(u.Input))
	}
	if u.Output > 0 {
		parts = append(parts, "↓"+countText(u.Output))
	}
	if u.Cache > 0 {
		parts = append(parts, "cache "+countText(u.Cache))
	}
	if u.Context > 0 && u.MaxContext > 0 {
		parts = append(parts, fmt.Sprintf("ctx %s/%s %d%%", countText(u.Context), countText(u.MaxContext),
			int(math.Round(float64(u.Context)/float64(u.MaxContext)*100))))
	}
	return strings.Join(parts, " · ")
}

// codeSpan is words as Markdown code, which a card shows in its own font.
func codeSpan(s string) string {
	ticks := "`"
	for strings.Contains(s, ticks) {
		ticks += "`"
	}
	return ticks + s + ticks
}

// countText is a count as a footer shows it: 680, 4.2k, or 1m.
func countText(n int) string {
	switch {
	case n >= 1_000_000:
		return scaledText(float64(n)/1_000_000, "m")
	case n >= 1_000:
		return scaledText(float64(n)/1_000, "k")
	}
	return strconv.Itoa(n)
}

// scaledText is a count in thousands or millions, cut down, not up.
func scaledText(v float64, suffix string) string {
	if v >= 100 || v == math.Trunc(v) {
		return fmt.Sprintf("%d%s", int(math.Round(v)), suffix)
	}
	return strconv.FormatFloat(math.Floor(v*10)/10, 'f', -1, 64) + suffix
}

var ruleRe = regexp.MustCompile(`(?m)^[─━═╌╍┄┈]{4,}.*$`)

// plainBody is a turn's text for a card: without the rows of borders.
func plainBody(t string) string {
	t = ansiRe.ReplaceAllString(t, "")
	t = ruleRe.ReplaceAllString(t, "")
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(t, "\n\n"))
}

// codeFence keeps a card's text as it is: a terminal's, whose lines are
// no Markdown, in a block that takes any text.
func codeFence(t string) string {
	if t == "" {
		return ""
	}
	ticks := "```"
	for strings.Contains(t, ticks) {
		ticks += "`"
	}
	return ticks + "text\n" + t + "\n" + ticks
}

// escapeMarkdown keeps a note's words from being taken for Markdown.
func escapeMarkdown(s string) string {

	return strings.NewReplacer("_", `\_`, "*", `\*`, "~", `\~`, "`", "\\`").Replace(s)
}

// word is the few words a card says, in the language of the chat.
func word(lang, key string) string {
	w := map[string]map[string]string{
		"en_us": {
			"state.working": "Working", "state.done": "Done", "state.asks": "Waiting for you", "state.error": "Error",
			"asks": "The agent waits for your answer. Use /answer, or send a message.",
			"open": "Open in Kero", "opened": "Kero shows the session.", "gone": "This card's session is gone.",
			"inTerminal": "The answer follows in its own card.",
			"earlier":    "An earlier turn",
			"perm.title": "Permission needed",
			"perm.once":  "Allow once", "perm.always": "Always allow", "perm.deny": "Refuse",
			"perm.hint": "Refuse: add the reason under the heading, then take Refuse.",
			"perm.done": "Allowed.", "perm.settled": "Allowed for this session.",
			"perm.denied": "Refused.", "perm.answered": "This permission is answered already.",
		},
		"zh_cn": {
			"state.working": "运行中", "state.done": "已完成", "state.asks": "等待你回答", "state.error": "出错",
			"asks": "代理在等待你回答。用 /answer 回答，或发送一条消息。",
			"open": "在 Kero 中打开", "opened": "Kero 已打开该会话。", "gone": "这张卡片的会话已不存在。",
			"inTerminal": "回答在下一张卡片中。",
			"earlier":    "较早的一轮",
			"perm.title": "需要授权",
			"perm.once":  "允许一次", "perm.always": "始终允许", "perm.deny": "拒绝",
			"perm.hint": "拒绝时可在标题下写上原因，再点拒绝。",
			"perm.done": "已允许。", "perm.settled": "本会话已允许。",
			"perm.denied": "已拒绝。", "perm.answered": "这条授权已经处理过了。",
		},
		"ja_jp": {
			"state.working": "実行中", "state.done": "完了", "state.asks": "回答待ち", "state.error": "エラー",
			"asks":       "エージェントは回答を待っています。/answer で答えるか、メッセージを送ってください。",
			"inTerminal": "回答は次のカードにあります。",
			"earlier":    "以前のターン",
			"perm.title": "許可が必要です",
			"perm.once":  "1 回だけ許可", "perm.always": "常に許可", "perm.deny": "拒否",
			"perm.hint": "拒否するときは見出しの下に理由を書いてから拒否を押してください。",
			"perm.done": "許可しました。", "perm.settled": "このセッションで許可しました。",
			"perm.denied": "拒否しました。", "perm.answered": "この許可はすでに回答済みです。",
		},
	}
	if v, ok := w[lang][key]; ok {
		return v
	}
	return w["en_us"][key]
}

// langOf is the language a card is written in: the user's, as the
// configuration names it, or what the message is written in.
func langOf(s string) string {
	switch settings.Load().Language {
	case "zh-Hans":
		return "zh_cn"
	case "ja":
		return "ja_jp"
	}
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return "zh_cn"
		}
	}
	return "en_us"
}

// postText is a rich text's text: {"title":"…","content":[[{"tag":
// "text","text":"…"}]…]}, or the same under a language.
func postText(content string) string {
	type post struct {
		Title   string       `json:"title"`
		Content [][]postPart `json:"content"`
	}
	var p post
	if err := json.Unmarshal([]byte(content), &p); err != nil || p.Title == "" && p.Content == nil {
		// A rich text of a language.
		var byLang map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &byLang) != nil {
			return ""
		}
		for _, l := range []string{"zh_cn", "en_us", "ja_jp"} {
			var q post
			if r, ok := byLang[l]; ok && json.Unmarshal(r, &q) == nil {
				p = q
				break
			}
		}
	}
	var lines []string
	if p.Title != "" {
		lines = append(lines, p.Title)
	}
	for _, line := range p.Content {
		var s strings.Builder
		for _, part := range line {
			s.WriteString(part.String())
		}
		if s.Len() > 0 {
			lines = append(lines, s.String())
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// postPart is a part of a rich text's line: its text, or a link's.
type postPart struct {
	Tag      string `json:"tag"`
	Text     string `json:"text"`
	Href     string `json:"href"`
	ImageKey string `json:"image_key"`
	FileKey  string `json:"file_key"`
}

func (p postPart) String() string {
	if p.Href != "" {
		return "[" + p.Text + "](" + p.Href + ")"
	}
	return p.Text
}

// terminal tells an agent whose answer may hold a terminal's rows, which
// a card shows as they are.
func terminal(agent string) bool { return agent == "opencode" || agent == "codex" }

// bodyOf takes a card's body from its JSON, for an update, and keeps the
// header, which was sent with the card: the header's color stays.
func bodyOf(shown string) string {
	var m map[string]map[string]any
	if json.Unmarshal([]byte(shown), &m) != nil || m["header"] == nil {
		return shown
	}
	delete(m, "header")
	m["config"] = map[string]any{"update_multi": true}
	data, _ := json.Marshal(m)
	return string(data)
}

// markdownText is an agent's words for a card that reads Markdown.
func markdownText(t string) string { return t }
