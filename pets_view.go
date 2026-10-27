package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"kero/internal/pets"
	"kero/internal/settings"

	"kero/locales"
)

// listPets lists the pets the settings may show, as the pop-up picks
// them: Kero the frog, the Codex app's active pet, and each pet the Codex
// app has, then those kero installed.
func (a *App) listPets() []pets.Builtin {
	ps := []pets.Builtin{{Name: settings.PetSetKero, Title: locales.PetName()}}
	codex := pets.CodexIDs()
	if active := pets.CodexActive(); active != "" {
		title := active
		if p, err := pets.LoadID(active); err == nil {
			title = p.Name
		}
		ps = append(ps, pets.Builtin{Name: settings.PetSetCodex, Title: title})
		// The active pet is in the Codex app's list too, as the same
		// directory.
		codex = slices.DeleteFunc(codex, func(id string) bool { return id == active })
	}
	installed := pets.Installed()
	for _, dirs := range [][]string{codex, installed} {
		for _, id := range dirs {
			if slices.Contains(installed, id) {
				continue // kero's comes first
			}
			title := id
			if p, err := pets.LoadID(id); err == nil {
				title = p.Name
			}
			ps = append(ps, pets.Builtin{Name: id, Title: title, ID: id})
		}
	}
	return ps
}

// petChooser is the pop-up that chooses the pet on the edge.
func (a *App) petChooser(c *ui.Context, pal *palette) {
	ps := a.listPets()
	names := make([]string, 1, len(ps)+1)
	names[0] = a.set.PetSet
	titles := make(map[string]string, len(ps))
	for _, p := range ps {
		titles[p.Name] = p.Title
		if p.Name != a.set.PetSet {
			names = append(names, p.Name)
		}
	}
	t := a.set.PetSet
	popup(c, "pet-set", &t, names, func(n string) string {
		if s, ok := titles[n]; ok {
			return s
		}
		return n
	}, 240, pal)
	if t != a.set.PetSet {
		setPet(&a.set, t)
	}
}

// setPet changes the pet shown, and shows it.
func setPet(s *settings.Settings, name string) {
	s.PetSet = name
	s.Pet = true
}

// petGallery is the pet's setting's detail: what its description says,
// or what went wrong with it.
func (a *App) petGallery() string {
	if a.set.PetSet == settings.PetSetKero {
		return ""
	}
	p, err := pets.LoadID(a.set.PetSet)
	if err != nil {
		return locales.SetPetMissing(a.set.PetSet, err.Error())
	}
	desc := strings.TrimSpace(p.Description)
	// A pet of the same name in both directories: say which one shows.
	if p.ID != settings.PetSetCodex && slices.Contains(pets.CodexIDs(), p.ID) {
		desc += " (" + locales.SetPetKeroCopy() + ")"
	}
	return desc
}
