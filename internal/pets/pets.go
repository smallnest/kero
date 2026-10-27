// Package pets reads Codex pets: the spritesheets that the Codex desktop
// app shows on its window's edge, which kero shows too, and installs the
// pet packs of codex-pets.net, codexpet.top and similar sites.
//
// A pet is a directory holding pet.json and a spritesheet, as the Codex
// app's ~/.codex/pets/<id>/ has them, or a .codex-pet.zip holding both.
// The spritesheet is a grid of frames 192×208 pixels: eight of them a row,
// and nine rows in version 1 (idle, running right and left, waving,
// jumping, failed, waiting, running, review), eleven in version 2, whose
// last two rows look to the right and to the left.
package pets

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png" // previews, and kero's own pet
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	_ "golang.org/x/image/webp" // spritesheets

	"kero/internal/proto"
	"kero/internal/settings"
)

// A frame is 192×208 pixels, as the Codex app draws it; a row holds
// eight.
const (
	FrameW, FrameH = 192, 208
	Columns        = 8
)

// State is what a pet shows, as the Codex app has its rows.
type State int

const (
	Idle State = iota
	RunRight
	RunLeft
	Wave
	Jump
	Fail
	Waiting
	Working // the Codex app's Running
	Review
	LookRight // version 2 only
	LookLeft  // version 2 only
)

// rows is the row of each state in a spritesheet.
var rows = []State{Idle, RunRight, RunLeft, Wave, Jump, Fail, Waiting, Working, Review, LookRight, LookLeft}

// Frames counts the frames of each state, all but those of idle, which
// has six, as the Codex app counts them. Only idle's count matters: a
// state plays and loops, and the rest of its row stays empty.
var Frames = map[State]int{Idle: 6, RunRight: 8, RunLeft: 8, Wave: 4, Jump: 5, Fail: 8, Waiting: 6, Working: 6, Review: 6, LookRight: 8, LookLeft: 8}

// version1Rows is how many rows a version 1 spritesheet has.
const version1Rows = 9

// Pet is a pet as a spritesheet shows it.
type Pet struct {
	// ID is the directory or pack name: "doge".
	ID string
	// Name is what the pet is called: pet.json's displayName or name.
	Name string
	// Description is pet.json's, in the author's words.
	Description string
	// Version is the spritesheet's, 1 or 2.
	Version int
	// Sheet is the spritesheet, its rows read top to bottom.
	Sheet *ui.Bitmap
}

// Has tells whether the pet has the state: version 1 has no look rows.
func (p *Pet) Has(s State) bool {
	return s < LookRight || p.Version >= 2
}

// Frame is where the frame of a state, from 0, is in the sheet.
func (p *Pet) Frame(s State, f int) image.Rectangle {
	row := max(int(s), 0)
	if !p.Has(s) {
		row = int(Idle)
	}
	return image.Rect(f*FrameW, row*FrameH, (f+1)*FrameW, (row+1)*FrameH)
}

// manifest is pet.json, as the Codex app and the pet sites write it.
type manifest struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"displayName"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	SpritesheetPath     string `json:"spritesheetPath"`
	SpriteVersionNumber int    `json:"spriteVersionNumber"`
}

// Load reads the pet in a directory.
func Load(dir string) (*Pet, error) {
	return load(os.DirFS(dir), filepath.Base(filepath.Clean(dir)))
}

// load reads a pet from a file system that holds pet.json and the
// spritesheet at its root; id names it.
func load(fsys fs.FS, id string) (*Pet, error) {
	b, err := fs.ReadFile(fsys, "pet.json")
	if err != nil {
		return nil, fmt.Errorf("%s has no pet.json", id)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: pet.json is not valid JSON: %w", id, err)
	}
	name := m.DisplayName
	if name == "" {
		name = m.Name
	}
	if name == "" {
		name = id
	}
	sheet := "spritesheet.webp"
	if m.SpritesheetPath != "" {
		sheet = path.Clean(strings.ReplaceAll(m.SpritesheetPath, `\`, "/"))
	}
	if sheet == "." || strings.HasPrefix(sheet, "../") || strings.HasPrefix(sheet, "/") {
		return nil, fmt.Errorf("%s: pet.json's spritesheetPath must be a file in the pet's directory: %q", id, m.SpritesheetPath)
	}
	data, err := fs.ReadFile(fsys, sheet)
	if err != nil {
		return nil, fmt.Errorf("%s has no spritesheet %s", id, sheet)
	}
	return parse(id, name, m.Description, m.SpriteVersionNumber, data)
}

// parse decodes a spritesheet, and tells its version from its size, as
// the Codex app does: the count pet.json gives, where it gives one.
func parse(id, name, desc string, version int, sheet []byte) (*Pet, error) {
	bm, err := ui.DecodeBitmap(sheet)
	if err != nil {
		return nil, fmt.Errorf("%s: the spritesheet is not an image: %w", id, err)
	}
	w, h := bm.Size()
	if w != Columns*FrameW {
		return nil, fmt.Errorf("%s: the spritesheet is %d pixels wide, not %d", id, w, Columns*FrameW)
	}
	v := h / FrameH
	if h%FrameH != 0 || (v != version1Rows && v != version1Rows+2) {
		return nil, fmt.Errorf("%s: the spritesheet is %d×%d pixels, not %d×%d or %d×%d", id, w, h,
			Columns*FrameW, FrameH*version1Rows, Columns*FrameW, FrameH*(version1Rows+2))
	}
	got := 1
	if v == version1Rows+2 {
		got = 2
	}
	if version != 0 && version != got {
		return nil, fmt.Errorf("%s: pet.json says spritesheet version %d, but the spritesheet is version %d's", id, version, got)
	}
	return &Pet{ID: id, Name: name, Description: desc, Version: got, Sheet: bm.Pixelated()}, nil
}

// Names reads a pet's name and description from its pet.json alone,
// without its spritesheet, for a list that does not draw it.
func Names(dir string) (*Pet, error) {
	id := filepath.Base(filepath.Clean(dir))
	b, err := os.ReadFile(filepath.Join(dir, "pet.json"))
	if err != nil {
		return nil, fmt.Errorf("%s has no pet.json", id)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: pet.json is not valid JSON: %w", id, err)
	}
	name := m.DisplayName
	if name == "" {
		name = m.Name
	}
	if name == "" {
		name = id
	}
	return &Pet{ID: id, Name: name, Description: m.Description}, nil
}

// NameID is what Names reads for the pet that id names, from kero's pets
// first, then the Codex app's, as LoadID reads it.
func NameID(id string) (*Pet, error) {
	if id == settings.PetSetCodex {
		if id = CodexActive(); id == "" {
			return nil, errors.New("the Codex app has no active pet to show")
		}
	}
	var errs []error
	for _, dir := range []string{Dir(), codexDir()} {
		p, err := Names(filepath.Join(dir, id))
		if err == nil {
			return p, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// Dir is where kero installs its pets, and the packs the user installs:
// one directory a pet.
func Dir() string { return filepath.Join(proto.Dir(), "pets") }

// codexDir is where the Codex desktop app keeps its pets.
func codexDir() string { return filepath.Join(proto.Home(), ".codex", "pets") }

// LoadID reads the pet that id names, from kero's pets first, then the
// Codex app's; for settings.PetSetCodex, the one Codex has active now.
func LoadID(id string) (*Pet, error) {
	if id == settings.PetSetCodex {
		if id = CodexActive(); id == "" {
			return nil, errors.New("the Codex app has no active pet to show")
		}
	}
	var errs []error
	for _, dir := range []string{Dir(), codexDir()} {
		p, err := Load(filepath.Join(dir, id))
		if err == nil {
			return p, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// CodexActive is the pet Codex has active now: config.toml's [desktop]
// section has it, in its pets' "active" field, a pet directory's name:
//
//	[desktop]
//	pets = { active = "doge" }
func CodexActive() string {
	b, err := os.ReadFile(filepath.Join(proto.Home(), ".codex", "config.toml"))
	if err != nil {
		return ""
	}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && section == "desktop" && strings.TrimSpace(k) == "pets" {
			return activeIn(v)
		}
	}
	return ""
}

// activeIn finds "active"'s value in the inline table of a pets setting.
func activeIn(v string) string {
	_, rest, ok := strings.Cut(v, "{")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, "}")
	for _, field := range strings.Split(rest, ",") {
		if k, v, ok := strings.Cut(field, "="); ok && strings.TrimSpace(k) == "active" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// Installed lists the pets kero has, its built-in ones and those the
// user installed, as the settings name them.
func Installed() []string { return petDirs(Dir()) }

// CodexIDs lists the pets the Codex app has, in its pets' directory:
// their directory names, which the Codex app's config.toml names its
// active pet by.
func CodexIDs() []string { return petDirs(codexDir()) }

// petDirs lists the directories that name pets in root, sorted.
func petDirs(root string) []string {
	es, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range es {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

// Install installs the pet pack at path, a .codex-pet.zip or any zip of
// pet.json and a spritesheet, or a pet's directory, into kero's pets,
// replacing a pet of the same name. It returns the pet's name, as the
// settings have it.
func Install(path string) (string, error) {
	var fsys fs.FS
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		fsys = os.DirFS(path)
	} else {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return "", fmt.Errorf("%s is not a zip file: %w", path, err)
		}
		defer zr.Close()
		z := &zipFS{files: zr.File}
		if !z.petAt("") {
			// A zip of the pet's directory.
			for _, r := range z.roots() {
				if r.IsDir() && z.petAt(r.Name()) {
					z.prefix = r.Name() + "/"
					break
				}
			}
		}
		fsys = z
	}
	b, err := fs.ReadFile(fsys, "pet.json")
	if err != nil {
		return "", fmt.Errorf("%s is no Codex pet pack: it holds no pet.json", filepath.Base(path))
	}
	var m manifest
	if json.Unmarshal(b, &m) != nil {
		return "", fmt.Errorf("%s: pet.json is not valid JSON", filepath.Base(path))
	}
	id := m.ID
	if !ValidName(id) {
		// The pack's file: doge.codex-pet.zip, or the pet's directory.
		id = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zip"), ".codex-pet")
	}
	if !ValidName(id) {
		return "", fmt.Errorf("%s is no Codex pet pack: its pet.json has no id, and its name is no name for a pet", filepath.Base(path))
	}
	if _, err := load(fsys, id); err != nil {
		return "", err
	}
	root := Dir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	dst := filepath.Join(root, id)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	return id, os.Rename(tmp, dst)
}

// ValidName tells whether name can name a pet's directory: a name of its
// own, so that no pack can write outside it.
func ValidName(id string) bool {
	if id == "" || len(id) > 128 || strings.HasPrefix(id, ".") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// zipFS is the files of a zip under a prefix, without macOS's __MACOSX,
// the resource forks it keeps, as a file system.
type zipFS struct {
	files  []*zip.File
	prefix string
}

// lookup finds a path under the zip's prefix: a file's or a directory's
// entry, without macOS's __MACOSX and the resource forks it keeps.
func (z *zipFS) lookup(name string) *zip.File {
	name = path.Clean("/" + z.prefix + name)[1:]
	if name == "" {
		return nil
	}
	for _, f := range z.files {
		if n := strings.TrimSuffix(f.Name, "/"); n == name && !strings.HasPrefix(n, "__MACOSX/") && !strings.Contains(path.Base(n), "._") {
			return f
		}
	}
	return nil
}

// Open opens a file of the zip, or a directory of it.
func (z *zipFS) Open(name string) (fs.File, error) {
	if f := z.lookup(name); f != nil && !f.FileInfo().IsDir() {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		return zipFile{rc, f}, nil
	}
	if name = path.Clean(name); name == "." || z.isDir(name) {
		return zipDir{z, name}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (z *zipFS) isDir(name string) bool {
	return name == "." || z.lookup(name) != nil || len(z.children(name)) > 0
}

// children are what a directory holds: the files under it that are not in
// deeper directories, and those directories.
func (z *zipFS) children(name string) []fs.DirEntry {
	dir := ""
	if name != "." {
		dir = name + "/"
	}
	seen := map[string]bool{}
	var es []fs.DirEntry
	for _, f := range z.files {
		n := f.Name
		if !strings.HasPrefix(n, z.prefix+dir) || strings.HasPrefix(n, "__MACOSX/") || n == z.prefix {
			continue
		}
		rest := strings.TrimPrefix(n, z.prefix+dir)
		top, nested := rest, false
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			top, nested = rest[:i], true
		}
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		if nested {
			es = append(es, fs.FileInfoToDirEntry(zipInfo{name: top, dir: true}))
		} else {
			es = append(es, fs.FileInfoToDirEntry(f.FileInfo()))
		}
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name() < es[j].Name() })
	return es
}

// roots are what the zip holds at its top, a directory's compressed with
// the directory itself having an entry.
func (z *zipFS) roots() []fs.DirEntry { return z.children(".") }

// petAt tells whether dir, under the zip's prefix, holds pet.json.
func (z *zipFS) petAt(dir string) bool {
	f := z.lookup(path.Join(dir, "pet.json"))
	return f != nil && !f.FileInfo().IsDir()
}

// zipFile is a file of the zip.
type zipFile struct {
	io.ReadCloser
	f *zip.File
}

func (f zipFile) Stat() (fs.FileInfo, error) { return f.f.FileInfo(), nil }

// zipDir is a directory of the zip's entries, or of the zip's top.
type zipDir struct {
	z    *zipFS
	name string
}

func (d zipDir) Stat() (fs.FileInfo, error) { return zipInfo{name: path.Base(d.name), dir: true}, nil }
func (d zipDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}
func (d zipDir) Close() error                       { return nil }
func (d zipDir) ReadDir(int) ([]fs.DirEntry, error) { return d.z.children(d.name), nil }

// zipInfo is the info of a zip's directory.
type zipInfo struct {
	name string
	dir  bool
}

func (i zipInfo) Name() string       { return i.name }
func (i zipInfo) Size() int64        { return 0 }
func (i zipInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (i zipInfo) ModTime() time.Time { return time.Time{} }
func (i zipInfo) IsDir() bool        { return i.dir }
func (i zipInfo) Sys() any           { return nil }
