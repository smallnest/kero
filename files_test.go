package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"reflect"
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

// The Files tree shows each file's and folder's icons, and a row's menu
// makes files in its folder, and renames and deletes the row.
func TestFilesIconsAndMenu(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	g := &proto.GitInfo{Root: "/tmp/demo", Branch: "main", Default: "main"}
	// The focused pane's, whose dir in the worktree of the other.
	s.Panes[0].Dir = "/tmp/demo"
	s.Panes[1].Git = g
	a.prefs.Side = sideFiles
	h := a.hostOf(s.ID)
	a.side.files.dirs = map[string]*dirList{
		filesKey(h, "/tmp/demo"): {entries: []proto.FSEntry{{Name: "src", Dir: true}, {Name: "main.go"}}},
	}
	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	// The root's menu has none of the header's: the menu's name shows
	// first, in the root's header.
	if r, _ := tt.Find(locales.FilesCollapseFolders()); r.Y < 40 {
		t.Errorf("the header still has a menu, at %v", r)
	}
	if fileIconTheme.fileIconOf("main.go") != "lang-go" || fileIconTheme.fileIconOf("x.png") != "image-duo" {
		t.Error("the tree shows one icon for files of different kinds")
	}

	// The root's buttons put a field for a new file's, a new folder's,
	// at the top of the root, which they unfold; and fold its folders.
	var made []proto.FSCreateParams
	cl, stop := projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		if method != "fs.create" {
			return nil, errors.New("unexpected method " + method)
		}
		var p proto.FSCreateParams
		err := json.Unmarshal(raw, &p)
		made = append(made, p)
		return nil, err
	})
	defer stop()
	a.local().cl = cl
	a.side.files.open[filesKey(h, "/tmp/demo/src")] = true
	for _, c := range []struct {
		label, op, name string
		dir             bool
	}{{locales.FilesNewFile(), editNewFile, "new.go", false}, {locales.FilesNewFolder(), editNewFolder, "pkg", true}} {
		if err := tt.Click(c.label); err != nil {
			t.Fatalf("click %s: %v", c.label, err)
		}
		tt.Frame()
		if e := a.side.files.edit; e == nil || e.op != c.op || e.dir != "/tmp/demo" {
			t.Fatalf("%s put %+v", c.label, e)
		}
		if !a.side.files.open[filesKey(h, "/tmp/demo")] {
			t.Fatalf("%s didn't unfold the root", c.label)
		}
		// The field takes the focus, which settles in a frame or two,
		// and typing comes after.
		time.Sleep(20 * time.Millisecond)
		tt.TypeKey(0, ui.Key(c.name[0]), c.name)
		tt.Frame()
		if e := a.side.files.edit; e == nil || e.name != c.name {
			t.Fatalf("%s: the field shows %+v after typing %q", c.label, e, c.name)
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if e := a.side.files.edit; e != nil {
			t.Fatalf("%s: Enter left %+v", c.label, e)
		}
	}
	for i := 0; i < 100 && len(made) < 2; i++ {
		time.Sleep(10 * time.Millisecond)
		tt.Frame()
	}
	if len(made) != 2 {
		t.Fatalf("the buttons made %v", made)
	}
	if p := made[0]; p.Path != "/tmp/demo/new.go" || p.Dir {
		t.Errorf("the file is %+v", p)
	}
	if p := made[1]; p.Path != "/tmp/demo/pkg" || !p.Dir {
		t.Errorf("the folder is %+v", p)
	}
	// Collapse folds the folders the tree has open.
	a.side.files.open[filesKey(h, "/tmp/demo/src")] = true
	if err := tt.Click(locales.FilesCollapseFolders()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.side.files.open[filesKey(h, "/tmp/demo/src")] {
		t.Error("Collapse Folders left src unfolded")
	}

	// Rename puts the field in the row's place, and Delete asks first.
	if err := tt.RightClick("main.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	want := []string{locales.FilesNewFile(), locales.FilesNewFolder(), "-", locales.FilesRename(), locales.FilesDeleteItem()}
	if got := tt.Menu(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the row's menu shows %q, want %q", got, want)
	}
	if err := tt.ChooseMenuItem(locales.FilesRename()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if e := a.side.files.edit; e == nil || e.op != editRename || e.path != "/tmp/demo/main.go" {
		t.Fatalf("Rename put %+v", a.side.files.edit)
	}
	a.side.files.edit = nil
	if err := tt.RightClick("src"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.FilesDeleteItem()); err != nil {
		t.Fatal(err)
	}
	if c := a.confirm; c == nil || !c.open || c.title != locales.FilesDeleteFolderTitle("src") {
		t.Fatalf("Delete asked %+v", a.confirm)
	}
	a.confirm = nil
}

// A window too narrow for the panel beside the panes leaves it to them,
// kept open, which toggling opens anyway, until the window widens.
func TestPanelMakesRoom(t *testing.T) {
	a := testApp(t)
	a.prefs.Side = sideFiles
	tt := ui.NewTester(a.view, 1280, 720)
	settle := func() {
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
	}
	settle()
	if a.side.crowded {
		t.Fatal("the window crowds the panel")
	}
	// The cards, as the panel shows beside them, come in from the sidebar.
	for i := 0; i < 20 && !tt.HasText("Split Right (⌘D)"); i++ {
		tt.Advance(time.Second)
		tt.Frame()
	}
	tt.SetSize(640, 720)
	settle()
	if !a.side.crowded || a.prefs.Side != sideFiles || a.rightShown() {
		t.Fatalf("a narrow window left the panel crowded %v, shown %v, opened %q", a.side.crowded, a.rightShown(), a.prefs.Side)
	}
	a.toggleSide("")
	settle()
	if !a.rightShown() || !a.side.forced {
		t.Fatal("toggling didn't show the panel in a narrow window")
	}
	// Room again, then narrow again, hides it.
	tt.SetSize(1290, 720)
	settle()
	if !a.rightShown() {
		t.Fatal("a wide window hid the panel")
	}
	a.showSide(sideInfo)
	tt.SetSize(640, 720)
	settle()
	if a.rightShown() {
		t.Error("narrowing the window again kept the panel")
	}
}
