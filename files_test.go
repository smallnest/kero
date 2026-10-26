package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"kero/internal/proto"
	"kero/locales"
)

// The Files view shows a session's folder as a tree, its folders opening
// on a click, and the file chosen in it under the tree: its text, or why
// it shows none.
func TestFilesView(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	a.prefs.Side = sideFiles
	// A host connected, whose folders never come: the tree shows what it
	// was given.
	a.local().cl, _ = projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		return nil, errors.New("not in a test")
	})

	// What kerod would answer: the pane's own folder, which is the tree's.
	s.Panes[0].ProcessDir = "/tmp/demo"
	pane, root := a.filesRoot(s)
	tree := a.tree(pane, root)
	tree.dirs[root] = &fileDir{paths: []string{"/tmp/demo/src", "/tmp/demo/main.go", "/tmp/demo/logo.png", "/tmp/demo/blob.bin"}}
	for _, p := range tree.dirs[root].paths {
		tree.isDir[p] = p == "/tmp/demo/src"
	}
	tree.dirs["/tmp/demo/src"] = &fileDir{paths: []string{"/tmp/demo/src/a.go"}}

	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	for _, want := range []string{"Files", "Changes", "demo", "src", "main.go", "logo.png"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q; texts %q", want, tt.Texts())
		}
	}

	// A folder opens on a click.
	if err := tt.Click("src"); err != nil {
		t.Fatalf("click src: %v", err)
	}
	tt.Frame()
	if !tt.HasText("a.go") {
		t.Errorf("a folder did not open; texts %q", tt.Texts())
	}

	// A file opens under the tree.
	f := &openFile{path: "/tmp/demo/main.go"}
	b := editor.NewBuffer("package main\n\nfunc main() {}\n")
	b.SetLanguage(editor.LanguageFor("main.go"))
	f.st = &editor.State{Buffer: b}
	tree.shown, tree.file = "/tmp/demo/main.go", f
	tt.Frame()
	if !tt.HasText("Code editor") {
		t.Errorf("no text of the file; texts %q", tt.Texts())
	}

	// A binary file is told of, not shown.
	tree.shown, tree.file = "/tmp/demo/blob.bin", &openFile{path: "/tmp/demo/blob.bin", binary: true}
	tt.Frame()
	if !tt.HasText("Binary file") {
		t.Errorf("no note of a binary file; texts %q", tt.Texts())
	}

	// A session with no folder says why.
	s.Panes[0].Dir, s.Panes[0].ProcessDir, s.Panes[1].Dir = "", "", ""
	tt.Frame()
	if !tt.HasText("No folder") {
		t.Errorf("no note of a session without a folder; texts %q", tt.Texts())
	}
}

// What a click on a file does follows the row under the tree: it shows
// the file there, opens it in a file pane, or in the host's editor.
func TestFileOpenChoice(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	a.prefs.Side = sideFiles
	a.local().cl, _ = projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		return nil, errors.New("not in a test")
	})
	s.Panes[0].ProcessDir = "/tmp/demo"
	pane, root := a.filesRoot(s)
	tree := a.tree(pane, root)
	tree.dirs[root] = &fileDir{paths: []string{"/tmp/demo/main.go"}}
	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	// The row names every choice, the first by default.
	for _, want := range []string{"Open with", "Preview", "File Pane", "Editor"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q; texts %q", want, tt.Texts())
		}
	}

	// The default shows the file under the tree.
	a.chooseFile(tree, "/tmp/demo/main.go")
	if tree.shown != "/tmp/demo/main.go" {
		t.Errorf("a click showed %q, want the file under the tree", tree.shown)
	}

	// A file pane, once chosen, splits the tree's pane on the file.
	var called proto.CreateParams
	a.local().cl, _ = projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		if method != "split" {
			return nil, errors.New("not in a test")
		}
		if err := json.Unmarshal(raw, &called); err != nil {
			return nil, err
		}
		return s, nil
	})
	// The row still shows with the file under the tree: the file pane
	// leaves it be.
	tt.Frame()
	if err := tt.Click("File Pane"); err != nil {
		t.Fatalf("click File Pane: %v", err)
	}
	tt.Frame()
	if a.prefs.FileOpen != "pane" {
		t.Fatalf("the choice is %q, want pane", a.prefs.FileOpen)
	}
	a.chooseFile(tree, "/tmp/demo/main.go")
	// The call runs on the app's own goroutine; a frame lands it.
	for i := 0; i < 100 && called.File == ""; i++ {
		tt.Frame()
	}
	if called.File != "/tmp/demo/main.go" || called.Split != tree.pane {
		t.Errorf("split %+v, want a file pane on main.go beside %s", called, tree.pane)
	}
}

// A path of a host joins with the host's separator, which its own paths
// tell.
func TestHostJoin(t *testing.T) {
	for _, c := range []struct{ dir, name, want string }{
		{"/tmp/demo", "src", "/tmp/demo/src"},
		{"/tmp/", "src", "/tmp/src"},
		{`C:\Users\me`, "src", `C:\Users\me\src`},
		{`C:\Users\me\`, "src", `C:\Users\me\src`},
		{`C:`, "src", `C:\src`},
	} {
		if got := hostJoin(c.dir, c.name); got != c.want {
			t.Errorf("hostJoin(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
}

// A tree names a path from its folder, with either slash between.
func TestFileTreeRel(t *testing.T) {
	tree := &fileTree{root: "/tmp/demo"}
	for p, want := range map[string]string{
		"/tmp/demo/main.go":  "main.go",
		"/tmp/demo/src/a.go": "src/a.go",
		`/tmp/demo/src\a.go`: `src\a.go`,
	} {
		if got := tree.rel(p); got != want {
			t.Errorf("rel(%q) = %q, want %q", p, got, want)
		}
	}
	if !strings.HasPrefix(tree.rel("/tmp/demo/x"), "x") {
		t.Error("rel kept the folder's slash")
	}
	_ = proto.FSList{}
}

// The panel's shortcut opens it on the view it showed last, and closes
// it; each view's own opens it on that view.
func TestSideToggle(t *testing.T) {
	a := testApp(t)
	s := &a.st.Sessions[0]
	s.Panes[0].ProcessDir, s.Panes[0].Git = "/tmp/demo", &proto.GitInfo{Root: "/tmp/demo", Branch: "main"}
	s.Panes[1].Git = s.Panes[0].Git
	// Connected: a panel that opens makes calls as it shows.
	a.local().cl, _ = projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		return nil, errors.New("not in a test")
	})
	a.local().conn = connReady
	tt := ui.NewTester(a.view, 1280, 720)

	a.run("side.toggle", nil)
	tt.Frame()
	if a.prefs.Side != sideChanges {
		t.Fatalf("the panel opened on %q", a.prefs.Side)
	}
	if !tt.HasText(locales.SideChanges()) || !tt.HasText("What isn't committed") {
		t.Errorf("the panel did not show Changes; texts %q", tt.Texts())
	}
	a.run("side.toggle", nil)
	tt.Frame()
	if a.prefs.Side != "" {
		t.Errorf("side.toggle left the panel open on %q", a.prefs.Side)
	}
	// A view's own opens the panel on it, and closes it again.
	a.run("files.toggle", nil)
	tt.Frame()
	if a.prefs.Side != sideFiles {
		t.Errorf("files.toggle showed %q", a.prefs.Side)
	}
	if !tt.HasText(locales.SideFiles()) {
		t.Errorf("the panel did not show Files; texts %q", tt.Texts())
	}
	a.run("files.toggle", nil)
	if a.prefs.Side != "" {
		t.Errorf("files.toggle left the panel open on %q", a.prefs.Side)
	}
	a.run("changes.toggle", nil)
	tt.Frame()
	if a.prefs.Side != sideChanges {
		t.Errorf("changes.toggle showed %q", a.prefs.Side)
	}
}

// A file pane shows its file in the app's editor, colored by its
// language, and has no terminal of its own.
func TestFilePane(t *testing.T) {
	a := testApp(t)
	// A host connected, whose folders never come: the tree shows what it
	// was given.
	a.local().cl, _ = projectClient(t, func(method string, raw json.RawMessage) (any, error) {
		return nil, errors.New("not in a test")
	})
	s := &a.st.Sessions[0]
	s.Panes = append(s.Panes, proto.Pane{ID: "f", File: "/tmp/demo/main.go", Dir: "/tmp/demo", Status: proto.Idle, Progress: -1})
	s.Layout = s.Layout.Split("a", "f", "right", 0)
	p := s.PaneByID("f")
	if !p.FilePane() || !p.Shown() || p.Browser() {
		t.Fatalf("a file pane is not one: file %v, shown %v, browser %v", p.FilePane(), p.Shown(), p.Browser())
	}
	if name := p.PaneName(); name != "main.go" {
		t.Errorf("a file pane names itself %q", name)
	}
	if a.terms["f"] != nil {
		t.Error("a file pane has a terminal")
	}

	// What kerod would answer, read once.
	f := a.filePaneFor(p)
	f.read = true
	b := editor.NewBuffer("package main\n\nfunc main() {}\n")
	b.SetLanguage(editor.LanguageFor("main.go"))
	f.st = &editor.State{Buffer: b}
	tt := ui.NewTester(a.view, 1280, 720)
	tt.Frame()
	if !tt.HasText("Code editor") || !tt.HasText("main.go") {
		t.Errorf("the file pane did not show its file; texts %q", tt.Texts())
	}
	f.binary = true
	tt.Frame()
	if !tt.HasText("Binary file") {
		t.Errorf("a binary file was not told of; texts %q", tt.Texts())
	}
}
