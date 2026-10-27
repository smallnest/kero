package settings

import "strings"

// PetSetKero is the pet setting of Kero the frog, the one kero draws.
const PetSetKero = "kero"

// PetSetCodex is the pet setting of the Codex pet Codex has active now,
// under Settings › Pets › Active pet.
const PetSetCodex = "codex"

// validPetName tells whether name names a pet: kero's, the active Codex
// pet's, or a Codex pet's directory name. A pet's directory is a name of
// its own, so that no pet can point at a file elsewhere.
func validPetName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
