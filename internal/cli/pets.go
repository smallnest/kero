package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"kero/internal/pets"
	"kero/internal/settings"
)

// petsCmd shows and chooses the pet on the window's edge, and installs
// Codex pets.
func petsCmd(args []string) error {
	if len(args) > 0 && args[0] == "--builtin" {
		args = append([]string{"install", "--builtin"}, args[1:]...)
	}
	sub := "ls"
	if len(args) > 0 {
		sub = args[0]
	}
	rest := args[min(1, len(args)):]
	switch sub {
	case "ls":
		return petsLs()
	case "install":
		if len(rest) > 0 && (rest[0] == "--list" || rest[0] == "-l") {
			return petsList()
		}
		if len(rest) > 0 && (rest[0] == "--builtin" || rest[0] == "-b") {
			names := rest[1:]
			if len(names) == 0 {
				for _, b := range pets.Builtins() {
					names = append(names, b.Name)
				}
			}
			for _, name := range names {
				id, err := pets.InstallBuiltin(name)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				fmt.Println("installed", id)
			}
			return nil
		}
		if len(rest) != 1 {
			return usageErr("kero pets install <pack> (a .codex-pet.zip, or a pet's directory), or --builtin [name…]")
		}
		id, err := pets.Install(rest[0])
		if err != nil {
			return err
		}
		fmt.Println("installed", id, "in", pets.Dir())
		fmt.Println("show it with: kero pets use", id)
		return nil
	case "use":
		if len(rest) != 1 {
			return usageErr("kero pets use <name|kero|codex>")
		}
		name := rest[0]
		if name != settings.PetSetKero && name != settings.PetSetCodex {
			if _, err := pets.LoadID(name); err != nil {
				return err
			}
		}
		s := settings.Load()
		s.PetSet = name
		s.Pet = true
		if err := settings.Save(s); err != nil {
			return err
		}
		fmt.Println("showing", name)
		return nil
	case "rm", "remove":
		if len(rest) != 1 {
			return usageErr("kero pets rm <name>")
		}
		if !pets.ValidName(rest[0]) {
			return usageErr("%q is no pet of kero's", rest[0])
		}
		dir := filepath.Join(pets.Dir(), rest[0])
		if _, err := os.Stat(filepath.Join(dir, "pet.json")); err != nil {
			return fmt.Errorf("%s is not an installed pet", rest[0])
		}
		if s := settings.Load(); s.PetSet == rest[0] {
			s.PetSet = settings.PetSetKero
			if err := settings.Save(s); err != nil {
				return err
			}
		}
		return os.RemoveAll(dir)
	}
	return usageErr("kero pets [ls|install|use|rm]")
}

// petsLs lists the pets Kero has, and which it shows.
func petsLs() error {
	shown := settings.Load().PetSet
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MARK\tPET\tNAME")
	for _, b := range pets.Builtins() {
		mark := ""
		if shown == b.Name {
			mark = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", mark, b.Name, b.Title)
	}
	for _, id := range pets.Installed() {
		mark := ""
		if shown == id {
			mark = "*"
		}
		title := id
		if p, err := pets.LoadID(id); err == nil {
			title = p.Name
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", mark, id, title)
	}
	return w.Flush()
}

// petsList lists the pets kero offers to install, which --builtin
// installs.
func petsList() error {
	for _, b := range pets.Builtins() {
		fmt.Printf("%s\t%s\n", b.Name, b.Title)
	}
	return nil
}
