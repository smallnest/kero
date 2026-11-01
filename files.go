package main

import (
	"bytes"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"kero/internal/client"
	"kero/internal/proto"

	"kero/locales"
)

// The Files tab is the tree of the worktree of the pane the user is in,
// else of its directory, as kerod lists it (fs.list), a folder at a time
// as the user unfolds it: what git ignores shows faded, and .git not at
// all. A click on a file opens it in the viewer beside the panes, as text,
// or, an image, as an image; its header opens it in a file pane, in the
// host's editor, or in the OS app.

// filesState is the Files tab's.
type filesState struct {
	// dirs are the folders listed, open the folders unfolded, by host and
	// path (filesKey); places the trees' places, by host and root.
	dirs   map[string]*dirList
	open   map[string]bool
	places map[string]*ui.ListState
	// gen counts the times the tab came to show, each of which lists its
	// folders anew; shown tells it shows this frame, and showed that it
	// did the frame before.
	gen           int
	shown, showed bool
	// views are what the viewer shows, by session.
	views map[string]*fileView
}

// dirList is a folder as kerod listed it.
type dirList struct {
	entries []proto.FSEntry
	err     string
	busy    bool
	stamp   string // the worktree's status it was listed at (gitStamp)
	gen     int
	fetched time.Time
}

// fileItem is a row of the tree: a file or a folder, depth folders in, or
// why a folder shows nothing, err.
type fileItem struct {
	proto.FSEntry
	path  string // on the host
	rel   string // in the root, with slashes
	depth int
	open  bool
	err   string
}

// fileView is a file the viewer shows, as its text, or as an image.
type fileView struct {
	pane string // a pane of the host it is on
	path string // on the host
	rel  string // in the Files' root, which its header shows
	st   *editor.State
	bm   *ui.Bitmap // of an image
	err  string
	// gone, large and binary say why it shows no text: there is no file
	// there any more, or one too large, or one that isn't text.
	gone, large, binary bool
	open                bool // the tab shows it over its tree
	busy                bool
	stamp               string
	fetched             time.Time
}

// The viewer bounds a file shown as text, and one shown as an image.
const (
	maxView  = 4 << 20
	maxImage = 32 << 20
)

// errFilesGone is a file read that is no longer there.
var errFilesGone = errors.New("no such file")

// withGit gives a pane its session's worktree, the one that holds its
// directory, when the pane has none itself.
func withGit(s *proto.Session, p *proto.Pane) *proto.Pane {
	if p == nil || p.Git != nil {
		return p
	}
	dir := p.WorkDir()
	if dir == "" {
		return p
	}
	for i := range s.Panes {
		if g := s.Panes[i].Git; g != nil && withinDir(dir, g.Root) {
			cp := *p
			cp.Git = g
			return &cp
		}
	}
	return p
}

// withinDir tells that dir is root or a directory in it, with the paths
// of any host.
func withinDir(dir, root string) bool {
	root = strings.TrimRight(root, `/\`)
	if dir == root {
		return true
	}
	return strings.HasPrefix(dir, root+"/") || strings.HasPrefix(dir, root+`\`)
}

// filesKey keys a path of a host.
func filesKey(h *host, p string) string { return h.name + "\x00" + p }

// filesRoot is the folder the Files tab shows, and the pane it is of: the
// worktree of the one the user is in, else its directory.
func (a *App) filesRoot(s *proto.Session) (*proto.Pane, string) {
	p := withGit(s, s.PaneByID(a.focusOf(s)))
	switch {
	case p == nil:
		return nil, ""
	case p.Git != nil:
		return p, p.Git.Root
	case p.ProcessDir != "":
		return p, p.ProcessDir
	}
	return p, p.Dir
}

// filesSide is the Files tab: the root's tree, a folder listed as it
// unfolds, and again as the worktree changes.
func (a *App) filesSide(c *ui.Context, pal *palette, s *proto.Session) {
	fs := &a.side.files
	if !fs.showed {
		fs.gen++
	}
	fs.shown, fs.showed = true, true
	p, root := a.filesRoot(s)
	if root == "" {
		a.sideMessage(c, pal, locales.FilesNoFolder(), "")
		return
	}
	h := a.hostOf(p.ID)
	if fs.dirs == nil || len(fs.dirs) > 2000 {
		fs.dirs = map[string]*dirList{}
	}
	if fs.open == nil {
		fs.open = map[string]bool{}
	}
	stamp := gitStamp(p.Git)
	working := sessionWorking(s)
	if working {
		c.After(5 * time.Second)
	}
	asking := 0
	for _, l := range fs.dirs {
		if l.busy {
			asking++
		}
	}
	// The folders shown, listed again as the worktree's status changes,
	// as the tab shows anew, and every few seconds while an agent works,
	// a few at a time.
	list := func(dir string) *dirList {
		l := fs.dirs[filesKey(h, dir)]
		stale := l == nil || !l.busy && (l.stamp != stamp || l.gen != fs.gen || working && time.Since(l.fetched) > 5*time.Second)
		if stale && asking < 4 && h.cl != nil {
			asking++
			l = a.fetchDir(h, dir, p.Git != nil, stamp)
		}
		return l
	}
	var items []fileItem
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		l := list(dir)
		if l == nil || l.entries == nil && l.err == "" {
			return
		}
		if l.err != "" && l.entries == nil {
			items = append(items, fileItem{path: dir, rel: rel + "/!", depth: depth, err: l.err})
			return
		}
		for _, e := range l.entries {
			if e.Dir && e.Name == ".git" {
				continue
			}
			it := fileItem{FSEntry: e, path: h.joinPath(dir, e.Name), rel: path.Join(rel, e.Name), depth: depth}
			it.open = e.Dir && fs.open[filesKey(h, it.path)]
			items = append(items, it)
			if it.open {
				walk(it.path, it.rel, depth+1)
			}
		}
	}
	walk(root, "", 0)

	ui.Row(c.Key("root")).Height(28).Shrink(0).Padding(0, 10, 6, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, h.shortDir(root)).FontSize(12.5).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Tooltip(root)
		iconButton(c, "fold-folders", collapseIcon, locales.FilesCollapseFolders(), pal, func() {
			prefix := filesKey(h, root)
			for k := range fs.open {
				if strings.HasPrefix(k, prefix) {
					delete(fs.open, k)
				}
			}
		})
	})
	if l := fs.dirs[filesKey(h, root)]; l == nil || l.entries == nil {
		switch {
		case l != nil && l.err != "" && !l.busy:
			a.sideMessage(c, pal, locales.FilesCantReadFolder(), l.err)
		case h.cl != nil:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		}
		return
	}

	if fs.places == nil {
		fs.places = map[string]*ui.ListState{}
	}
	ls := fs.places[filesKey(h, root)]
	if ls == nil {
		ls = &ui.ListState{}
		fs.places[filesKey(h, root)] = ls
	}
	var shown string
	if v := fs.views[s.ID]; v != nil {
		shown = v.path
	}
	ls.Key = func(i int) any { return treeKey{items[i].rel, items[i].Dir, items[i].path == shown} }
	ui.List(c.Key("files"), ls, len(items), func(i int) {
		it := items[i]
		switch {
		case it.err != "":
			ui.Text(c, it.err).FontSize(12).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Padding(4, 8, 4, 6+12*float32(it.depth)+28).Tooltip(it.err)
		case it.Dir:
			key := filesKey(h, it.path)
			a.treeFolder(c, pal, it.Name, it.rel, it.depth, it.open, false, func() {
				if it.open {
					delete(fs.open, key)
				} else {
					fs.open[key] = true
				}
			})
		default:
			a.treeEntry(c, pal, it, it.path == shown, func() {
				if fs.views == nil {
					fs.views = map[string]*fileView{}
				}
				if fv := fs.views[s.ID]; fv != nil && fv.path == it.path {
					// The one it shows: open, or hidden as its button
					// hid it.
					a.openViewer(fv)
					return
				}
				fv := &fileView{pane: p.ID, path: it.path, rel: it.rel, open: true}
				fs.views[s.ID] = fv
				a.openViewer(fv)
			})
		}
	}).Grow(1).MinHeight(0).Padding(0, 10, 16, 10)
}

// treeKey is what a row of a tree is, as its list keeps its state by.
type treeKey struct {
	path    string
	dir     bool
	current bool
}

// treeRow is a row of a tree, depth folders in, the one there chosen.
func treeRow(c *ui.Context, pal *palette, depth int, current bool) ui.Element {
	row := ui.Row(c.Key("row")).Height(26).Padding(0, 8, 0, 6+12*float32(depth)).Gap(5).AlignItems(ui.Center).Radius(7)
	if pal.dark {
		// Room for the chosen row's edge in every row.
		row.Border(1, ui.Transparent)
	}
	switch {
	case current:
		row.Background(pal.rowSelected)
		chosen(row, pal)
	case row.Hovered():
		row.Background(pal.rowHover)
	}
	return row
}

// treeFolder is a folder of a tree, which a click folds or unfolds;
// faded, it is one git ignores.
func (a *App) treeFolder(c *ui.Context, pal *palette, name, tip string, depth int, open, faded bool, toggle func()) {
	row := treeRow(c, pal, depth, false).Tooltip(tip)
	if faded {
		row.Opacity(0.55)
	}
	if row.Clicked() {
		toggle()
	}
	row.Children(func() {
		chev := ui.Icon(c, chevronIcon).Size(9, 9).TextColor(pal.textFaint).Shrink(0)
		chev.Rotate(chev.AnimateTo("turn", 90*b2f(open), ui.Snappy))
		ui.Icon(c, folderIcon).Size(13, 13).TextColor(pal.textFaint).Shrink(0)
		ui.Text(c, name).FontSize(12.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
	})
}

// sessionWorking tells that an agent of the session works.
func sessionWorking(s *proto.Session) bool {
	for _, p := range s.Panes {
		if p.Status == proto.Working {
			return true
		}
	}
	return false
}

// treeEntry is a file of the Files tab's tree, which a click opens in the
// viewer.
func (a *App) treeEntry(c *ui.Context, pal *palette, it fileItem, current bool, open func()) ui.Element {
	row := treeRow(c, pal, it.depth, current).Tooltip(it.rel)
	if row.Clicked() {
		open()
	}
	row.Children(func() {
		ui.Box(c).Size(9, 9).Shrink(0)
		ui.Icon(c, docIcon).Size(13, 13).TextColor(pal.textFaint).Shrink(0)
		ui.Text(c, it.Name).FontSize(12.5).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0)
	})
	return row
}

// fetchDir asks kerod for a folder's entries, dotfiles too, marking what
// git ignores in a worktree.
func (a *App) fetchDir(h *host, dir string, ignored bool, stamp string) *dirList {
	fs := &a.side.files
	key := filesKey(h, dir)
	l := fs.dirs[key]
	if l == nil {
		l = &dirList{}
		fs.dirs[key] = l
	}
	l.busy = true
	gen, cl := fs.gen, h.cl
	go func() {
		var res proto.FSList
		err := cl.Call("fs.list", proto.FSListParams{Dir: dir, Hidden: true}, &res)
		a.update(func() {
			l.busy, l.fetched, l.stamp, l.gen = false, time.Now(), stamp, gen
			if err != nil {
				l.err = err.Error()
				return
			}
			l.entries, l.err = res.Entries, ""
		})
	}()
	return l
}

// openViewer opens a file in the viewer, which reads it.
func (a *App) openViewer(fv *fileView) {
	if !fv.open {
		fv.open, fv.fetched = true, time.Time{} // read anew
	}
	if cl := a.hostOf(fv.pane).cl; cl != nil && !fv.busy && fv.fetched.IsZero() {
		a.loadFile(cl, fv, "")
	}
}

// closeViewer closes the session's viewer.
func (a *App) closeViewer(sid string) {
	delete(a.side.files.views, sid)
}

// fileViewer is the viewer of a file of Files: an image as an image, else
// its text, read only, which it reads again as the worktree changes, and
// every few seconds while an agent works.
func (a *App) fileViewer(c *ui.Context, pal *palette, s *proto.Session, fv *fileView) {
	h := a.hostOf(fv.pane)
	stamp := ""
	if p := withGit(s, s.PaneByID(fv.pane)); p != nil {
		stamp = gitStamp(p.Git)
	}
	working := sessionWorking(s)
	if working {
		c.After(3 * time.Second)
	}
	if !fv.busy && h.cl != nil && (fv.fetched.IsZero() || fv.stamp != stamp || working && time.Since(fv.fetched) > 3*time.Second) {
		a.loadFile(h.cl, fv, stamp)
	}

	dir, name := path.Split(fv.rel)
	ui.Row(c.Key("hdr")).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(8).Children(func() {
		ui.Icon(c, docIcon).Size(14, 14).TextColor(pal.textFaint).Shrink(0)
		ui.Text(c, name).FontSize(14).FontWeight(600).TextColor(pal.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if dir = strings.TrimSuffix(dir, "/"); dir != "" {
			ui.Text(c, dir).FontSize(13).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0).Tooltip(fv.path)
		}
		ui.Box(c.Key("drag")).Grow(1).AlignSelf(ui.Stretch).DragWindow()
		if !fv.gone {
			pid, file := fv.pane, fv.path
			iconButton(c, "files-edit", docIcon, locales.FilesOpenInPane(), pal, func() { a.defer_(func() { a.openFileInPane(pid, file) }) })
			iconButton(c, "files-editor", splitRightIcon, locales.FilesOpenInEditor(), pal, func() { a.defer_(func() { a.openFileInEditor(pid, file) }) })
		}
		if !h.remote() && !fv.gone {
			full := fv.path
			iconButton(c, "open", openFileIcon, locales.ChangesOpenFile(), pal, func() { openPath(full) })
		}
	})
	ui.Column(c.Key("body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		switch {
		case fv.gone:
			a.sideMessage(c, pal, locales.FilesGone(), fv.rel)
		case fv.large:
			a.sideMessage(c, pal, locales.ChangesTooLarge(), "")
		case fv.binary:
			a.sideMessage(c, pal, locales.ChangesBinary(), "")
		case fv.st == nil && fv.err != "":
			a.sideMessage(c, pal, locales.FilesCantReadFile(), fv.err)
		case fv.bm != nil:
			ui.Column(c.Key("image")).Grow(1).MinHeight(0).Padding(8).Center().Children(func() {
				ui.Image(c, fv.bm).Shrink(1)
			})
		case fv.st == nil:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		default:
			fv.st.Font = ui.Font{Family: a.termFamily(), Size: max(a.set.FontSize-1, 10)}
			fv.st.Theme = a.codeTheme(c, pal)
			fv.st.ReadOnly = true
			fv.st.NoActiveLine = true
			fv.st.NoScrollPastEnd = true
			editor.View(c.Key("text-"+fv.path), fv.st).Grow(1).FillWidth().MinHeight(0)
		}
	})
}

// loadFile reads a file of the viewer: an image to show, else its text,
// anew for its first, else as an edit, so that it stays scrolled where it
// was.
func (a *App) loadFile(cl *client.Client, fv *fileView, stamp string) {
	fv.busy = true
	image := peekable[strings.ToLower(filepath.Ext(fv.path))]
	go func() {
		var st proto.FSStat
		var data []byte
		var bm *ui.Bitmap
		limit := int64(maxView)
		if image {
			limit = maxImage
		}
		err := cl.Call("fs.stat", proto.FSStatParams{Path: fv.path}, &st)
		if err == nil && st.Exists && !st.Dir && st.Size <= limit {
			var r proto.FSRead
			err = cl.Call("fs.read", proto.FSReadParams{Path: fv.path, Max: limit}, &r)
			data = r.Data
			if err == nil && image {
				bm, err = ui.DecodeBitmap(data)
				image = bm != nil // else its bytes as text
			}
		}
		a.update(func() {
			fv.busy, fv.fetched, fv.stamp = false, time.Now(), stamp
			if err != nil {
				fv.err = err.Error()
				return
			}
			fv.err = ""
			fv.gone = !st.Exists || st.Dir
			fv.large = !fv.gone && st.Size > limit
			fv.binary = !fv.gone && !fv.large && !image && isBinary(data)
			if fv.gone || fv.large || fv.binary {
				fv.bm = nil
				return
			}
			if image {
				fv.bm, fv.st = bm, nil
				return
			}
			fv.bm = nil
			showFileText(fv, string(data))
		})
	}()
}

// showFileText puts a file's text in its viewer's editor, read only.
func showFileText(fv *fileView, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if fv.st == nil {
		buf := editor.NewBuffer(text)
		buf.SetLanguage(editor.LanguageFor(path.Base(fv.rel)))
		fv.st = &editor.State{Buffer: buf, ReadOnly: true}
		fv.st.NoActiveLine = true
		return
	}
	replaceText(fv.st.Buffer, text)
}

// isBinary tells a file that isn't text, as git does: a NUL in its first
// 8000 bytes.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

// remoteError says what a call to a host's kerod came to: a method its
// kerod has not is told to update.
func remoteError(err error) string {
	if strings.Contains(err.Error(), "unknown method") {
		return locales.FilesUpdateHost()
	}
	return err.Error()
}

// openFileInPane opens a file in a file pane beside a pane of its host,
// which shows its text, or its image.
func (a *App) openFileInPane(pane, file string) {
	s := a.sessionOfPane(pane)
	if s == nil {
		return
	}
	a.split(s, proto.CreateParams{Split: pane, Direction: "right", File: file})
}

// openFileInEditor opens a file in the host's editor, in a pane of its
// shell.
func (a *App) openFileInEditor(pane, file string) {
	s := a.sessionOfPane(pane)
	if s == nil {
		return
	}
	a.split(s, proto.CreateParams{Split: pane, Direction: "right", Command: editorCommand(a.hostOf(pane), file)})
}
