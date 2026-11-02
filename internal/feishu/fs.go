package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kero/internal/proto"
)

// downloads is where the messages' files go: kerod's directory, so that a
// chat's session finds them after it restarts.
func downloads() string { return filepath.Join(proto.Dir(), "feishu-files") }

// media is a message's file or image, as Feishu gives it.
type media struct {
	key   string // file_key or image_key
	name  string // the file's name, or "" for an image
	image bool
}

// saved writes a message's file to a path of its own, and returns it.
func (b *Bot) saved(m media) (string, error) {
	if b.files == nil {
		return "", errors.New("no way to download files")
	}
	dir := filepath.Join(downloads(), time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	data, err := b.files.Download(m.key, m.image)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", orDash(m.name), err)
	}
	name := filepath.Base(m.name)
	if m.image {
		name = m.key + extOf(data)
	}
	if name == "" || name == "." || name == "/" {
		name = m.key
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// extOf tells the file's type from its first bytes.
func extOf(data []byte) string {
	switch {
	case len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) > 3 && string(data[:3]) == "\xff\xd8\xff":
		return ".jpg"
	case len(data) > 6 && (string(data[:6]) == "GIF89a" || string(data[:6]) == "GIF87a"):
		return ".gif"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	}
	return ".bin"
}

// attachment is a message's file or image, saved for the agent: what the
// prompt says of it, "" when the message has none.
func (b *Bot) attachment(chatID, typ, content string) string {
	files, images := messageFiles(content)
	var text string
	save := func(x media, what string) {
		path, err := b.saved(x)
		if err != nil {
			b.reply(chatID, err.Error())
			return
		}
		text += fmt.Sprintf("\n[%s: %s]", what, path)
	}
	switch typ {
	case "file":
		for _, f := range files {
			save(f, "a file the user sent")
		}
	case "image":
		for _, img := range images {
			save(img, "an image the user sent")
		}
	case "post":
		text = postText(content)
		for _, img := range images {
			save(img, "an image the user sent")
		}
	}
	return text
}

// messageFiles is a message's file_key and image_key, from its content:
// {"file_key":"…"} or {"image_key":"…"}, or a rich text's.
func messageFiles(content string) (files, images []media) {
	if files, images = postFiles(content); len(files)+len(images) > 0 {
		return files, images
	}
	var c struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
		ImageKey string `json:"image_key"`
	}
	if json.Unmarshal([]byte(content), &c) != nil {
		return nil, nil
	}
	if c.FileKey != "" {
		files = append(files, media{key: c.FileKey, name: c.FileName})
	}
	if c.ImageKey != "" {
		images = append(images, media{key: c.ImageKey, image: true})
	}
	return files, images
}

// kard is a chat's card, which its next turn replaces.
type kard struct {
	id    string
	turn  string
	shown string
	body  string
	state string
}

// cardFile is where the chats' cards go.
func cardFile() string { return filepath.Join(proto.Dir(), "feishu-cards-state.json") }

// saveCard keeps the chat's card, and its JSON, which the next turn
// replaces through a restart.
func (b *Bot) saveCard(key string, c cardMsg, lang, body, state string) {
	b.mu.Lock()
	if b.kard == nil {
		b.kard = map[string]kard{}
	}
	b.kard[key] = kard{id: c.id, turn: c.turn, shown: c.text, body: body, state: state}
	data, err := json.Marshal(b.kard)
	b.mu.Unlock()
	if err != nil || os.MkdirAll(filepath.Dir(cardFile()), 0o700) != nil {
		return
	}
	tmp := cardFile() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil && os.Rename(tmp, cardFile()) != nil {
		os.Remove(tmp)
	}
}

// kardStateValue is the chat's card, as the bot keeps it.
func (b *Bot) kardStateValue(key string) kard {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.kard[key]
}

func (b *Bot) loadCards() {
	data, err := os.ReadFile(cardFile())
	if err != nil {
		return
	}
	states := map[string]kard{}
	if json.Unmarshal(data, &states) != nil {
		return
	}
	b.mu.Lock()
	b.kard = states
	b.mu.Unlock()
}

// record tells what one of the bot's own cards is about: the chat, the
// session, and the turn of a prompt, "" for the rest.
type record struct {
	Chat    string `json:"chat"`
	Session string `json:"session,omitempty"`
	Turn    string `json:"turn,omitempty"`
}

// cardNotes is where the cards' records go, for the button's click.
func cardNotes() string { return filepath.Join(proto.Dir(), "feishu-cards.json") }

func (b *Bot) remember(id string, r record) {
	if id == "" {
		return
	}
	b.mu.Lock()
	b.notes[id] = r
	b.mu.Unlock()
	b.saveNotes()
}

// forget takes a message's record off, as another replaced its card.
func (b *Bot) forget(id string) {
	b.mu.Lock()
	_, had := b.notes[id]
	delete(b.notes, id)
	b.mu.Unlock()
	if had {
		b.saveNotes()
	}
}

func (b *Bot) note(id string) (record, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.notes[id]
	return r, ok
}

func (b *Bot) saveNotes() {
	b.mu.Lock()
	data, err := json.Marshal(b.notes)
	b.mu.Unlock()
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(cardNotes()), 0o700) != nil {
		return
	}
	tmp := cardNotes() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil && os.Rename(tmp, cardNotes()) != nil {
		os.Remove(tmp)
	}
}

func (b *Bot) loadNotes() {
	data, err := os.ReadFile(cardNotes())
	if err != nil {
		return
	}
	notes := map[string]record{}
	if json.Unmarshal(data, &notes) != nil {
		return
	}
	b.mu.Lock()
	for id, r := range notes {
		b.notes[id] = r
	}
	b.mu.Unlock()
}

// postFiles is a rich text's images: an "img" part's image_key, and a
// "media" one's file_key.
func postFiles(content string) (files, images []media) {
	var lines [][]postPart
	if json.Unmarshal([]byte(content), &lines) != nil {
		var byLang map[string]struct {
			Content [][]postPart `json:"content"`
		}
		if json.Unmarshal([]byte(content), &byLang) != nil {
			return nil, nil
		}
		for _, l := range byLang {
			lines = l.Content
			break
		}
	}
	for _, line := range lines {
		for _, p := range line {
			switch {
			case p.Tag == "img" && p.ImageKey != "":
				images = append(images, media{key: p.ImageKey, image: true})
			case p.Tag == "media" && p.FileKey != "":
				files = append(files, media{key: p.FileKey})
			}
		}
	}
	return files, images
}

func (b *Bot) saveFiles(content string) []string {
	var paths []string
	files, images := messageFiles(content)
	for _, f := range append(files, images...) {
		if p, err := b.saved(f); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}
