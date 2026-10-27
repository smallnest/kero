package main

import (
	"math"

	"github.com/egoist/mygo"

	"kero/internal/pets"
)

// spotRoom is where the pet's speech starts, and how tall its room is, for
// a pet whose hands are over the edge at perch, in a screen whose work
// area starts at room: the room runs from the top of its speech to the
// frame's feet, where a Codex pet's stand, setH below the edge. The speech
// gives way where the screen leaves little room for it, then the frog's
// frame goes while it still fits.
func spotRoom(perch, room, setH float32) (y, height float32) {
	// What it says, from its top to its hands, never to above the
	// screen, and never taller than the frog's frame.
	speech := float32(petH)
	if setH < speech {
		speech = setH
	}
	if perch-room < speech {
		speech = perch - room
	}
	// The frog's frame, where the edge is too high for a Codex pet, still
	// fits down to where the screen ends.
	cell := speech
	if foot := room + setH; petBodyH < foot && foot < perch && foot < cell {
		cell = foot
	}
	return perch - cell, speech
}

// petSpot is where the pet's window goes for the main window at win, in a
// screen's work area: above the window's top edge where the screen leaves
// room for it, hands over the edge, else standing inside the window at its
// bottom edge. offset is where across the window its middle stands, 0 at
// the left to 1 at the right. setH is the room a Codex pet needs above the
// edge; inside tells that it stands inside the window.
func petSpot(win, work mygo.Rectangle, offset float32, setH float32) (mygo.Rectangle, bool) {
	// A Codex pet as wide as a frog's window might be wider.
	wide := float32(petW)
	if setH > petBodyH {
		wide = float32(math.Round(float64(setH) * pets.FrameW / pets.FrameH))
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > 1 {
		off = 1
	}
	top := float32(win.Y)
	winWide := float32(win.Width)
	room := float32(work.Y)
	x := float32(win.X) + off*winWide - wide/2
	if x < float32(work.X)+8 {
		x = float32(work.X) + 8
	}
	if x > float32(work.X)+float32(work.Width)-wide-8 {
		x = float32(work.X) + float32(work.Width) - wide - 8
	}
	perch := top + petPerch
	y, height := spotRoom(perch, room, setH)
	// A Codex pet taller than the frog takes the whole of its window, so
	// that all of it shows above the edge.
	if setH+petPerch >= petH {
		y, height = perch-setH, setH+petPerch
	}
	// No room above the edge: it stands inside the window at its bottom
	// edge, within the window.
	inside := perch-setH < room
	if inside {
		if x < float32(win.X)+8 {
			x = float32(win.X) + 8
		}
		if x > float32(win.X)+winWide-wide-8 {
			x = float32(win.X) + winWide - wide - 8
		}
		y, height = float32(win.Y+win.Height-petH), float32(petH)
		// A Codex pet taller than the frog needs the top of its window.
		if setH+petPerch >= petH {
			// And it stands at the window's right rather than out of it.
			y, height = float32(win.Y+win.Height)-setH-petPerch, setH+petPerch
			if x > float32(win.X)+winWide-wide-8 {
				x = float32(win.X) + winWide - wide - 8
			}
			if x < float32(win.X)+8 {
				x = float32(win.X) + 8
			}
		}
	}
	return mygo.Rectangle{X: int(math.Round(float64(x))), Y: int(math.Round(float64(y))), Width: int(math.Round(float64(wide))), Height: int(math.Round(float64(height)))}, inside
}
