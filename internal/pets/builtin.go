package pets

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"kero/internal/settings"
)

// builtinURL is the pet packs kero offers to install, beside Kero the
// frog: the ones its README lists. Kero does not carry them, as their
// licenses mostly allow no redistribution; and the Codex app's own pet is
// there too, when it has one.
const builtinURL = "https://raw.githubusercontent.com/smallnest/kero/dev/artifacts/pets/"

// Builtin is a pet kero offers to install.
type Builtin struct {
	// Name is the pet's, as a pack installs it: "doge".
	Name string
	// Title is what the pet is called: "Doge", as its pet.json says.
	Title string
	// ID is what the pet's pet.json calls it, where that differs from
	// Name: "zero", of the pack "zero-2"; "" where it is installed and
	// not known yet.
	ID string
}

// Builtins lists the pets kero offers, beside Kero the frog, which it
// does not fetch: the Codex app's active pet, if it has one, and the
// packs of builtinURL, as the README there lists them.
func Builtins() []Builtin {
	var ps []Builtin
	if id := CodexActive(); id != "" {
		p := Builtin{Name: settings.PetSetCodex, Title: id}
		if pet, err := LoadID(id); err == nil {
			p.Name = id
			if pet.Name != "" {
				p.Title = pet.Name
			}
		}
		ps = append(ps, p)
	}
	for _, name := range packNames {
		title := builtinTitles[name]
		if title == "" {
			title = name
		}
		if pet, err := Load(filepath.Join(Dir(), name)); err == nil && pet.Name != "" {
			title = pet.Name
		}
		b := Builtin{Name: name, Title: title, ID: builtinIDs[name]}
		if b.ID == "" {
			b.ID = name
		}
		ps = append(ps, b)
	}
	return ps
}

// packNames are the packs builtinURL holds, as artifacts/pets/README.md
// lists them, and their names.
var packNames = []string{
	"sato-v2",
	"ella-wave",
	"doge",
	"deepseek-whalechan--qimi",
	"minty--somnusochi",
	"doudizhu-laonongmin--chenyijing131-art",
	"moomew-coder-cat--ping",
	"gift--legeling",
	"dyt",
	"zero-2",
	"tianyi",
	"qingyun-sky",
	"xiaoyu",
}

var builtinIDs = map[string]string{"zero-2": "zero"}

var builtinTitles = map[string]string{
	"sato-v2":                                "사토",
	"ella-wave":                              "Ella Wave",
	"doge":                                   "Doge",
	"deepseek-whalechan--qimi":               "蓝色大肥鱼·Q版女仆",
	"minty--somnusochi":                      "Minty",
	"doudizhu-laonongmin--chenyijing131-art": "斗地主老农民",
	"moomew-coder-cat--ping":                 "MooMew Coder",
	"gift--legeling":                         "礼物 / Gift",
	"dyt":                                    "dyt",
	"zero-2":                                 "Zero",
	"tianyi":                                 "洛天依",
	"qingyun-sky":                            "Seiun Sky",
	"xiaoyu":                                 "小雨",
}

// InstallBuiltin installs the built-in pet name into kero's pets: from
// builtinURL, or for settings.PetSetCodex, from the Codex app's. It
// returns the pet's name, as Install returns it.
func InstallBuiltin(name string) (string, error) {
	if name == settings.PetSetCodex {
		id := CodexActive()
		if id == "" {
			return "", fmt.Errorf("the Codex app has no active pet")
		}
		return Install(filepath.Join(codexDir(), id))
	}
	if !ValidName(name) {
		return "", fmt.Errorf("%q names no pet of kero's", name)
	}
	b, err := fetch(builtinURL + name + ".codex-pet.zip")
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	tmp := filepath.Join(os.TempDir(), "kero-"+name+".codex-pet.zip")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	return Install(tmp)
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// fetch gets a URL.
func fetch(url string) ([]byte, error) {
	res, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}
