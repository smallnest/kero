package main

import (
	"math"

	"github.com/egoist/mygo"
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
// room for it, hands over the edge, else hanging from the window's top
// edge, inside it. offset is where across the window its middle stands,
// 0 at the left to 1 at the right. setH is the room a Codex pet needs
// above the edge; inside tells that it hangs inside the window.
func petSpot(win, work mygo.Rectangle, offset float32, setH float32) (mygo.Rectangle, bool) {
	off := offset
	if off < 0 {
		off = 0
	}
	if off > 1 {
		off = 1
	}
	top := float32(win.Y)
	wide := float32(win.Width)
	room := float32(work.Y)
	x := float32(win.X) + off*wide - petW/2
	if x < float32(work.X)+8 {
		x = float32(work.X) + 8
	}
	if x > float32(work.X)+float32(work.Width)-petW-8 {
		x = float32(work.X) + float32(work.Width) - petW - 8
	}
	perch := top + petPerch
	y, height := spotRoom(perch, room, setH)
	// No room above the edge: it hangs from the edge, inside the window,
	// what it says down to its feet on the edge.
	inside := perch-setH < room
	if inside {
		if x < float32(win.X)+8 {
			x = float32(win.X) + 8
		}
		if x > float32(win.X)+wide-petW-8 {
			x = float32(win.X) + wide - petW - 8
		}
		y, height = top, perch-top
	}
	return mygo.Rectangle{X: int(math.Round(float64(x))), Y: int(math.Round(float64(y))), Width: petW, Height: int(math.Round(float64(height)))}, inside
}
