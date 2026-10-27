package pets

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sheet is a spritesheet of rows rows: a frame in the color of its row,
// so that a frame read tells where it came from.
func sheet(t *testing.T, rows int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, Columns*FrameW, rows*FrameH))
	for r := 0; r < rows; r++ {
		for y := r * FrameH; y < (r+1)*FrameH; y++ {
			for x := 0; x < Columns*FrameW; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(20 * r), G: 200, B: 100, A: 255})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pack writes a zip of a pet: its manifest, and its spritesheet.
func pack(t *testing.T, path, prefix, manifest string, sheetData []byte) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	files := map[string][]byte{"pet.json": []byte(manifest), "spritesheet.webp": sheetData}
	for _, name := range []string{"pet.json", "spritesheet.webp"} {
		w, err := zw.Create(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(files[name])
	}
	if prefix != "" {
		// macOS's, beside what it zips, which an install leaves out.
		w, _ := zw.Create("__MACOSX/" + prefix + "._pet.json")
		w.Write([]byte("junk"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	p, err := parse("dog", "Dog", "", 0, sheet(t, version1Rows))
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.Has(LookRight) {
		t.Errorf("a version 1 sheet: version %d, has look rows %v", p.Version, p.Has(LookRight))
	}
	if r := p.Frame(Working, 3); r.Min.X != 3*FrameW || r.Min.Y != 7*FrameH {
		t.Errorf("working frame 3 of a version 1 sheet is at %v", r.Min)
	}
	if r := p.Frame(LookLeft, 0); r.Min.Y != 0 {
		t.Errorf("a look frame of a version 1 sheet is idle's, at %v", r.Min)
	}
	p, err = parse("sato", "사토", "", 2, sheet(t, version1Rows+2))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has(LookLeft) || p.Frame(LookLeft, 7).Min != (image.Point{7 * FrameW, 10 * FrameH}) {
		t.Errorf("a look frame of a version 2 sheet is at %v", p.Frame(LookLeft, 7).Min)
	}
	for _, bad := range []struct {
		name    string
		version int
		data    []byte
	}{
		{"a short sheet", 0, sheet(t, 3)},
		{"a version 2 sheet said to be 1", 1, sheet(t, version1Rows+2)},
		{"a version 1 sheet said to be 2", 2, sheet(t, version1Rows)},
		{"not an image", 0, []byte("no")},
	} {
		if _, err := parse("x", "x", "", bad.version, bad.data); err == nil {
			t.Errorf("%s was read", bad.name)
		}
	}
}

func TestInstall(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	dir := t.TempDir()
	doge := `{"id":"doge","displayName":"Doge","description":"A Shiba."}`
	pack(t, filepath.Join(dir, "doge.codex-pet.zip"), "", doge, sheet(t, version1Rows))
	id, err := Install(filepath.Join(dir, "doge.codex-pet.zip"))
	if err != nil || id != "doge" {
		t.Fatalf("Install: %q, %v", id, err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "doge", "spritesheet.webp")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "doge", "__MACOSX")); err == nil {
		t.Error("macOS's __MACOSX was installed")
	}
	p, err := LoadID("doge")
	if err != nil || p.Name != "Doge" || p.Description != "A Shiba." || p.Version != 1 {
		t.Fatalf("LoadID: %+v, %v", p, err)
	}
	if got := Installed(); len(got) != 1 || got[0] != "doge" {
		t.Errorf("Installed: %v", got)
	}
	// A zip of the pet's directory, which replaces the pet.
	pack(t, filepath.Join(dir, "tree.zip"), "cat/", `{"id":"cat","name":"Cat","spriteVersionNumber":2}`, sheet(t, version1Rows+2))
	if id, err := Install(filepath.Join(dir, "tree.zip")); err != nil || id != "cat" {
		t.Fatalf("Install: %q, %v", id, err)
	}
	if p, err := LoadID("cat"); err != nil || p.Name != "Cat" || !p.Has(LookRight) {
		t.Fatalf("LoadID: %+v, %v", p, err)
	}
	// A pack whose pet.json names no id takes its file's name.
	pack(t, filepath.Join(dir, "hen.codex-pet.zip"), "", `{"displayName":"Hen"}`, sheet(t, version1Rows))
	if id, err := Install(filepath.Join(dir, "hen.codex-pet.zip")); err != nil || id != "hen" {
		t.Fatalf("Install: %q, %v", id, err)
	}
	// A directory, as the Codex app keeps a pet.
	codex := filepath.Join(dir, "codex", "fox")
	os.MkdirAll(codex, 0o755)
	os.WriteFile(filepath.Join(codex, "pet.json"), []byte(`{"id":"fox"}`), 0o644)
	os.WriteFile(filepath.Join(codex, "spritesheet.webp"), sheet(t, version1Rows), 0o644)
	if id, err := Install(codex); err != nil || id != "fox" {
		t.Fatalf("Install: %q, %v", id, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(Dir()), "evil")); err == nil {
		t.Error("a pack wrote outside the pets' directory")
	}
	if _, err := Install(filepath.Join(dir, "nothing.zip")); err == nil {
		t.Error("installed a file that is no zip")
	}
}

func TestCodexActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KERO_HOME", filepath.Join(home, "kero"))
	t.Setenv("HOME", home)
	if got := CodexActive(); got != "" {
		t.Errorf("no config.toml: active %q", got)
	}
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	cfg := "[desktop]\n" +
		"pets = { sound = true, active = \"doge\" } # the one Codex shows\n" +
		"\n[tui]\npets = { active = \"no\" }\n"
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(cfg), 0o644)
	if got := CodexActive(); got != "doge" {
		t.Errorf("active %q", got)
	}
}

func TestCodexIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KERO_HOME", filepath.Join(home, "kero"))
	t.Setenv("HOME", home)
	if got := CodexIDs(); len(got) != 0 {
		t.Errorf("no Codex pets: %v", got)
	}
	for _, id := range []string{"doge", "deepseek-2", ".hidden"} {
		os.MkdirAll(filepath.Join(codexDir(), id), 0o755)
	}
	os.WriteFile(filepath.Join(codexDir(), "README"), nil, 0o644)
	if got := CodexIDs(); !slices.Equal(got, []string{"deepseek-2", "doge"}) {
		t.Errorf("CodexIDs: %v", got)
	}
	// A pet named as both a Codex pet's and kero's reads kero's.
	for _, root := range []string{codexDir(), Dir()} {
		dir := filepath.Join(root, "doge")
		os.MkdirAll(dir, 0o755)
		name := "Codex Doge"
		if root == Dir() {
			name = "Kero Doge"
		}
		os.WriteFile(filepath.Join(dir, "pet.json"), []byte(`{"id":"doge","displayName":"`+name+`"}`), 0o644)
		os.WriteFile(filepath.Join(dir, "spritesheet.webp"), sheet(t, version1Rows), 0o644)
	}
	if p, err := LoadID("doge"); err != nil || p.Name != "Kero Doge" {
		t.Errorf("LoadID: %+v, %v", p, err)
	}
}

func TestBuiltins(t *testing.T) {
	t.Setenv("KERO_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	bs := Builtins()
	if len(bs) != len(packNames) {
		t.Fatalf("Builtins: %v", bs)
	}
	for _, b := range bs {
		if b.Title == "" {
			t.Errorf("%s has no name of its own", b.Name)
		}
	}
	// A pack's file is named as the pet it installs: doge.codex-pet.zip
	// installs doge.
	for _, name := range packNames {
		if strings.ContainsAny(name, `\/`) || strings.HasPrefix(name, ".") {
			t.Errorf("%q is no name for a pet pack", name)
		}
	}
}

// TestInstallRefusesBadPacks installs, each in a test of its own, a pack
// that is no pet, as each is read in a process of its own.
func TestInstallRefusesBadPacks(t *testing.T) {
	for _, bad := range []struct{ name, m string }{
		{"NotJSON", "not json"},
		{"SmallSheet", `{"id":"small","spritesheetPath":"sheet/pet.png"}`},
		{"OutsideSheet", `{"spritesheetPath":"../outside.webp"}`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("KERO_HOME", home)
			path := filepath.Join(t.TempDir(), "pack.zip")
			pack(t, path, "", bad.m, sheet(t, version1Rows))
			if id, err := Install(path); err == nil {
				t.Fatalf("installed as %q a pack whose pet.json says %s", id, bad.m)
			}
			if es, _ := os.ReadDir(Dir()); len(es) != 0 {
				t.Errorf("the refused pack left %d files", len(es))
			}
		})
	}
}
