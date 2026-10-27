package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/pets"
	"kero/internal/settings"

	"kero/locales"
)

// Pet sets are the pets on the window's edge beside Kero the frog: Codex
// pets' spritesheets, which the pet shows frame by frame for each mood,
// where Kero the frog is drawn (pet_draw.go).

const (
	petSetFrame  = 150 * time.Millisecond // the Codex app's pace, 6⅔ frames a second
	petSetCalm   = time.Second            // with less motion
	petSetFrame0 = 82                     // how tall the Codex app shows a frame, in DIPs, its feet on the edge
)

// petSetGround is how tall a Codex pet's frame shows, in DIPs, from its
// size setting: feet on the edge.
func petSetGround(a *App) float32 {
	n := float64(petSetFrame0) * float64(a.set.PetSize)
	return float32(math.Round(n))
}

// petSetBodyH is how tall the room for a Codex pet is: its frame, and a
// little, where it stands.
func petSetBodyH(a *App) float32 { return petSetGround(a) + 8 }

// petSet is the pet kero shows, where it is no frog.
type petSet struct {
	name string // the setting's name, which finds the pet again
	pet  *pets.Pet
}

// petSetLoad reads the pet the settings show, if it is no frog, and
// says when it is not there, for the pet shows Kero the frog then.
func (a *App) petSetLoad() *petSet {
	if a.set.PetSet == settings.PetSetKero {
		return nil
	}
	p, err := pets.LoadID(a.set.PetSet)
	if err != nil {
		if !a.pet.setAgain {
			// Once a window: the pet's view asks every frame.
			a.pet.setAgain = true
			a.toast(locales.SetPetMissing(a.set.PetSet, err.Error()))
		}
		return nil
	}
	return &petSet{name: a.set.PetSet, pet: p}
}

// petSetReload reads the pet again, as settings changed, or a pack
// installed.
func (a *App) petSetReload() {
	a.petSet = a.petSetLoad()
	if a.pet.win != nil {
		a.pet.win.Invalidate()
	}
}

// petSetState is the spritesheet row of each mood: an agent that works is
// at its desk, one that waits asks for you, a failure fails, a finish
// jumps, and a doze is the longest row a sheet of its version has.
func petSetState(p *pets.Pet, mood petMood) pets.State {
	switch mood {
	case petWorking:
		return pets.Working
	case petWaiting:
		return pets.Wave
	case petDone:
		return pets.Jump
	case petFailed:
		return pets.Fail
	case petAsleep:
		if p.Has(pets.Review) {
			return pets.Review
		}
		return pets.LookRight
	}
	return pets.Idle
}

// petSetFrameAt is the frame of a state at now, which turns over since.
func (a *App) petSetFrameAt(state pets.State, since, now time.Time) int {
	period := petSetFrame
	if a.pet.calm {
		period = petSetCalm
	}
	n := max(pets.Frames[state], 1)
	return int((max(now.Sub(since), 0) / period) % time.Duration(n))
}

// petShows is the pet the window shows, read once.
func (a *App) petShows() *petSet {
	if a.petSet == nil {
		a.petSet = a.petSetLoad()
	}
	return a.petSet
}

// petSetDraw draws a Codex pet into its box, feet on the window's edge,
// as the Codex app draws it: a frame of the sheet, the row of its mood,
// cut out and shown a pixel art pixel for a few of the screen's.
func (a *App) petSetDraw(p *ui.Painter, r ui.Rect, set *petSet, state pets.State, frame int) {
	src := set.pet.Frame(state, frame)
	h := petSetGround(a)
	w := h * pets.FrameW / pets.FrameH
	x, y := r.X+(r.W-w)/2, r.Y+r.H-h
	// Its shadow, as much taller as it is.
	k := h / petSetFrame0
	p.FillPath(petEllipse(r.X+r.W/2, r.Y+r.H-1.5*k, w*0.38, 5*k), ui.RGBA(15, 25, 20, 0.13))
	f := ui.Rect{X: float32(src.Min.X), Y: float32(src.Min.Y), W: pets.FrameW, H: pets.FrameH}
	p.ImageRegion(set.pet.Sheet, f, ui.Rect{X: x, Y: y, W: w, H: h}, ui.ImageOptions{})
}
