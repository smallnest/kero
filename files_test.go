package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/proto"
	"kero/locales"
)

// The Files tab is the worktree's tree, a folder listed as it unfolds,
// without .git; a click on a file opens it in the viewer, as its text,
// which follows the file, read only.
func TestFilesTab(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	g := &proto.GitInfo{Root: "/tmp/demo", Branch: "main", Default: "main"}
	s.Panes[0].Git, s.Panes[1].Git = g, g
	a.prefs.Side = sideFiles
	h := a.hostOf(s.ID)
	fs := &a.side.files
	fs.dirs = map[string]*dirList{
		filesKey(h, "/tmp/demo"): {entries: []proto.FSEntry{
			{Name: ".git", Dir: true}, {Name: "internal", Dir: true}, {Name: "main.go"},
		}},
		filesKey(h, "/tmp/demo/internal"): {entries: []proto.FSEntry{{Name: "auth.go"}}},
	}
	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	for _, want := range []string{"internal", "main.go"} {
		if _, ok := tt.Find(want); !ok {
			t.Errorf("no %q; texts %q", want, tt.Texts())
		}
	}
	for _, not := range []string{".git", "auth.go"} {
		if _, ok := tt.Find(not); ok {
			t.Errorf("%q shows", not)
		}
	}

	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("auth.go"); !ok {
		t.Fatalf("unfolding internal didn't show auth.go; texts %q", tt.Texts())
	}
	if err := tt.Click("auth.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	fv := fs.views[s.ID]
	if fv == nil || fv.path != "/tmp/demo/internal/auth.go" || fv.rel != "internal/auth.go" {
		t.Fatalf("a click on auth.go opened %+v", fv)
	}
	fv.fetched = time.Now()
	showFileText(fv, "package internal\r\n\r\nfunc Auth() {}\r\n")
	tt.Frame()
	tt.Frame()
	if got := fv.st.Buffer.String(); got != "package internal\n\nfunc Auth() {}\n" {
		t.Errorf("the viewer shows %q", got)
	}
	if !fv.st.ReadOnly {
		t.Error("the file's text is editable")
	}
	shot(t, tt, "side-files")

	// The file changing changes the text in place.
	st := fv.st
	showFileText(fv, "package internal\n\n// Auth signs in.\nfunc Auth() {}\n")
	if fv.st != st || fv.st.Buffer.String() != "package internal\n\n// Auth signs in.\nfunc Auth() {}\n" {
		t.Error("the file's text didn't change in place")
	}

	// The tab's button hides the viewer, which a click on the file shows
	// again.
	if err := tt.Click(a.tip(locales.FilesCloseFile(), "files-close")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if fv.open {
		t.Error("the close left the viewer open")
	}
	if err := tt.Click("auth.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !fv.open {
		t.Error("a click on the file didn't show the viewer again")
	}
}

func TestIsBinary(t *testing.T) {
	if isBinary([]byte("hello\nworld")) || !isBinary([]byte("PNG\x00\x01")) || isBinary(nil) {
		t.Error("isBinary is wrong")
	}
}

// The viewer shows an image as an image, and its header opens the file in
// a file pane, and in the host's editor.
func TestViewerImageAndOpen(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	var splits []proto.CreateParams
	cl, stop := projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		if method != "split" {
			return nil, errors.New("unexpected method " + method)
		}
		var p proto.CreateParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		splits = append(splits, p)
		return s, nil
	})
	defer stop()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	bm, err := ui.DecodeBitmap(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	a.prefs.Side = sideFiles
	a.local().cl = cl
	pane := s.Panes[0].ID
	fv := &fileView{pane: pane, path: "/tmp/demo/logo.png", rel: "logo.png", bm: bm, open: true, fetched: time.Now()}
	a.side.files.views = map[string]*fileView{s.ID: fv}
	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	if !tt.HasText("logo.png") {
		t.Fatalf("no image in the viewer; texts %q", tt.Texts())
	}
	if fv.st != nil {
		t.Error("an image shows as text")
	}
	for _, id := range []string{"files-edit", "files-editor"} {
		if err := tt.Click(a.tip(map[string]string{"files-edit": locales.FilesOpenInPane(), "files-editor": locales.FilesOpenInEditor()}[id], id)); err != nil {
			t.Fatalf("click %s: %v", id, err)
		}
	}
	for i := 0; i < 100 && len(splits) < 2; i++ {
		tt.Frame()
	}
	if len(splits) != 2 {
		t.Fatalf("the header split %d times, want 2", len(splits))
	}
	if p := splits[0]; p.Split != pane || p.File != "/tmp/demo/logo.png" || p.Direction != "right" {
		t.Errorf("a file pane split %+v", p)
	}
	if p := splits[1]; p.Split != pane || len(p.Command) == 0 || p.File != "" {
		t.Errorf("an editor split %+v", p)
	}
}
