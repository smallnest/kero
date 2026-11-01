package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kero/internal/git"
	"kero/internal/proto"
)

// The Files tab makes, moves and deletes files on a host through these:
// they change what the user kerod runs as can, as the panes' programs do.

// pokeAt has the panes where a file is, its worktree else its directory,
// show their changes anew, as the user changed it.
func (d *Daemon) pokeAt(path string) {
	root := filepath.Dir(path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if r, err := git.Open(ctx, root); err == nil {
		root = r.Root
	}
	cancel()
	var dirs []string
	d.mu.Lock()
	for _, o := range d.panes {
		o.mu.Lock()
		if dir := o.info.WorkDir(); dir != "" && (proto.Within(dir, root) || proto.Within(path, dir)) {
			dirs = append(dirs, dir)
		}
		o.mu.Unlock()
	}
	d.mu.Unlock()
	for _, dir := range dirs {
		d.git.poke(dir)
	}
}

// changeable returns a path of the host the user may make, move or
// delete: absolute, and neither the root of its volume nor the home.
func changeable(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is not an absolute path", p)
	}
	p = filepath.Clean(p)
	if filepath.Dir(p) == p || p == filepath.Clean(proto.Home()) {
		return "", fmt.Errorf("%s can't change", p)
	}
	return p, nil
}

// fsCreate makes a file, empty, or a folder, with the folders it is in.
func (d *Daemon) fsCreate(p proto.FSCreateParams) error {
	path, err := changeable(p.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if p.Dir {
		err = os.Mkdir(path, 0o755)
	} else {
		var f *os.File
		if f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			err = f.Close()
		}
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s is there already", filepath.Base(path))
	}
	if err == nil {
		d.pokeAt(path)
	}
	return err
}

// fsRename moves a file or folder to a path that is free, or to another
// case of its own name.
func (d *Daemon) fsRename(p proto.FSRenameParams) error {
	from, err := changeable(p.From)
	if err != nil {
		return err
	}
	to, err := changeable(p.To)
	if err != nil {
		return err
	}
	if st, err := os.Lstat(to); err == nil {
		if fst, ferr := os.Lstat(from); ferr != nil || !os.SameFile(st, fst) {
			return fmt.Errorf("%s is there already", filepath.Base(to))
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	d.pokeAt(from)
	d.pokeAt(to)
	return nil
}

// fsDelete deletes a file or folder, and what is in it.
func (d *Daemon) fsDelete(p proto.FSDeleteParams) error {
	path, err := changeable(p.Path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	d.pokeAt(path)
	return nil
}
