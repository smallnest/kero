package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"kero/internal/actions"
	"kero/internal/pets"
	"kero/internal/proto"
	"kero/internal/settings"
	"kero/locales"
)

// The pet follows the most urgent session: one waiting beats any number
// of failures, which beat finishes, which beat work.
func TestPetMood(t *testing.T) {
	a := testApp(t)
	a.pet.lastActive = time.Now()
	s := &a.st.Sessions[0]
	cases := []struct {
		name   string
		status proto.Status
		unread bool
		want   petMood
	}{
		{"idle", proto.Idle, false, petIdle},
		{"working", proto.Working, false, petWorking},
		{"done", proto.Idle, true, petDone},
		{"done exited", proto.Exited, true, petDone},
		{"failed", proto.Failed, true, petFailed},
		{"waiting", proto.Waiting, true, petWaiting},
	}
	for _, c := range cases {
		s.Status, s.Unread = c.status, c.unread
		if got, _ := a.petCurrent(time.Now()); got != c.want {
			t.Errorf("%s: mood %v, want %v", c.name, got, c.want)
		}
	}
	// A session that needs the user counts.
	s.Status, s.Unread = proto.Waiting, true
	a.st.Sessions = append(a.st.Sessions, proto.Session{ID: "s2", Project: "p1"})
	a.st.Sessions[1].Status, a.st.Sessions[1].Unread = proto.Waiting, true
	if got, n := a.petCurrent(time.Now()); got != petWaiting || n != 2 {
		t.Errorf("two waiting: %v, %d", got, n)
	}
}

// Each mood is the Codex app's row for it: at its desk at work, waiting as
// it waits, a failure failing, a finish jumping, and at rest, or asleep,
// idle.
func TestPetSetState(t *testing.T) {
	cases := []struct {
		mood petMood
		want pets.State
	}{
		{petIdle, pets.Idle},
		{petWorking, pets.Working},
		{petWaiting, pets.Waiting},
		{petDone, pets.Jump},
		{petFailed, pets.Fail},
		{petAsleep, pets.Idle},
	}
	for _, c := range cases {
		if got := petSetState(c.mood); got != c.want {
			t.Errorf("mood %v: state %v, want %v", c.mood, got, c.want)
		}
	}
}

// A pat on a Codex pet shows its wave for a while, even while a mood shows
// itself, and then the mood is back.
func TestPetClickWaves(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	a.pet.since, a.pet.lastActive = now, now
	if got, _ := a.petSetStateAt(petIdle, now); got != pets.Idle {
		t.Fatalf("at rest %v, want idle", got)
	}
	a.petPetted(now, petIdle)
	if got, since := a.petSetStateAt(petIdle, now); got != pets.Wave || !since.Equal(now) {
		t.Errorf("a pat shows %v since %v, want the wave", got, since)
	}
	if got, _ := a.petSetStateAt(petWorking, now); got != pets.Wave {
		t.Errorf("a pat while working shows %v, want the wave", got)
	}
	if got, _ := a.petSetStateAt(petIdle, now.Add(petPat)); got != pets.Idle {
		t.Errorf("after the wave %v, want idle", got)
	}
}

// All calm for long enough, and the pet dozes; work wakes it.
func TestPetDozes(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	a.pet.lastActive = now.Add(-petDoze - time.Second)
	if got, _ := a.petCurrent(now); got != petAsleep {
		t.Fatalf("mood %v, want asleep", got)
	}
	a.st.Sessions[0].Status = proto.Working
	a.petNote(now)
	if got, _ := a.petCurrent(now); got != petWorking {
		t.Errorf("work woke it to %v", got)
	}
}

// The pet stands over the window's top edge where the screen leaves room
// for it, at the left, center or right, and otherwise hangs from the
// window's top edge, inside.
func TestPetSpot(t *testing.T) {
	a := testApp(t)
	// The room a Codex pet takes above the edge at its default size: 1.5
	// times the Codex app's 82 DIPs, and a little.
	a.set.PetSize = settings.DefaultPetSize
	setH := petSetBodyH(a)
	if setH != 123+8 {
		t.Fatalf("petSetBodyH = %v, want %d", setH, 131)
	}
	for _, size := range []float32{0.5, 1, 1.5, 2, 3} {
		a.set.PetSize = size
		if h := petSetBodyH(a); h != float32(math.Round(float64(82*size)))+8 {
			t.Errorf("size %v: petSetBodyH = %v", size, h)
		}
	}
	work := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 875}
	main := mygo.Rectangle{X: 100, Y: 300, Width: 1000, Height: 700}
	for _, c := range []struct {
		offset float32
		x      int
	}{{0.5, 540}, {0.25, 290}, {0, 40}} {
		r, bottom := petSpot(main, work, c.offset, setH)
		// Its window runs from its feet's line on the edge down to the
		// speech's top at 181.
		if bottom || r.Y != 181 || r.Height != 143 || r.X != c.x {
			t.Errorf("offset %v: %+v bottom %v, want x %d", c.offset, r, bottom, c.x)
		}
	}
	// Little room above: the speech gives way, the Codex pet still stands
	// on the edge, its feet on it, and as wide as a pet its size is. edge
	// is the main window's top where what is above it is just setH.
	edge := work.Y + int(setH) - petPerch
	for _, c := range []struct {
		name   string
		mainY  int
		bottom bool
		y      int
		height int
	}{
		// Its window shows all of the pet, as tall as it stands above the
		// edge.
		{"little room", edge + 1, false, 26, 143},
		{"enough", edge, false, 25, 143},
		// No room for a Codex pet, or the window's top off the screen: it
		// stands inside the window at its bottom edge, as tall as it stands
		// above the edge, so that the whole of it shows.
		{"no room", edge - 1, true, edge - 1 + 700 - int(setH) - petPerch, int(setH) + petPerch},
		{"half on", 13, true, 13 + 700 - int(setH) - petPerch, int(setH) + petPerch},
	} {
		main.Y = c.mainY
		got, bottom := petSpot(main, work, 0.5, setH)
		if bottom != c.bottom || got.Height != c.height || got.Y != c.y {
			t.Errorf("%s at %d: %+v bottom %v, want y %d height %d bottom %v", c.name, c.mainY, got, bottom, c.y, c.height, c.bottom)
		}
	}
	// A frog, or a small Codex pet, stands inside a window at its bottom,
	// as tall as the frog shows there: a window at the screen's top leaves
	// no room for its hands above it. Its window shows the frog below the
	// edge: its bottom left there at 607.
	main.Y = 25
	got, bottom := petSpot(main, work, 0.5, 87)
	if !bottom || got.Height != petH || got.Y != 607 {
		t.Errorf("inside: %+v bottom %v, want y 607", got, bottom)
	}
}

func TestPetOffsetAt(t *testing.T) {
	main := mygo.Rectangle{X: 100, Width: 1000}
	for _, c := range []struct {
		left float32
		want float32
	}{{505, 0.5}, {5, 0}, {1005, 1}} {
		if got := petOffsetAt(main, c.left); got != c.want {
			t.Errorf("left %v: offset %v, want %v", c.left, got, c.want)
		}
	}
}

// A press on the frog pets it, even when it has nothing to jump to.
func TestPetClickWinks(t *testing.T) {
	a := testApp(t)
	a.pet.since, a.pet.lastActive = time.Now(), time.Now()
	tt := ui.NewTester(a.petView, petW, petH)
	tt.Frame()
	tt.ClickAt(petW/2, petH-30)
	tt.Frame()
	if a.pet.wink.IsZero() {
		t.Fatal("a click on the frog did not pet it")
	}
}

// Without room for its speech, the frog still stands on the edge, and
// no half a bubble shows.
func TestPetShortWindow(t *testing.T) {
	a := testApp(t)
	a.st.Sessions[0].Status, a.st.Sessions[0].Unread = proto.Waiting, true
	a.pet.since, a.pet.lastActive = time.Now(), time.Now()
	tt := ui.NewTester(a.petView, petW, petH-20)
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	shot(t, tt, "pet-perched-short")
	if tt.HasText("needs you") {
		t.Error("the speech shows in a window too short for it")
	}
}

// The frog's faces, for a look: KERO_SHOTS=dir writes them.
func TestPetLooks(t *testing.T) {
	for _, c := range []struct {
		name   string
		mood   petMood
		status proto.Status
		unread bool
		since  time.Duration
	}{
		{"idle", petIdle, proto.Idle, false, 900 * time.Millisecond},
		{"working", petWorking, proto.Working, false, 900 * time.Millisecond},
		{"waiting", petWaiting, proto.Waiting, true, 900 * time.Millisecond},
		{"done", petDone, proto.Idle, true, 500 * time.Millisecond},
		{"failed", petFailed, proto.Failed, true, 250 * time.Millisecond},
		{"asleep", petAsleep, proto.Idle, false, 0},
	} {
		a := testApp(t)
		a.st.Sessions[0].Status, a.st.Sessions[0].Unread = c.status, c.unread
		a.pet.lastActive = time.Now().Add(-c.since - time.Second)
		if c.mood == petAsleep {
			a.pet.lastActive = time.Now().Add(-petDoze - 2*time.Second)
		}
		a.pet.since = time.Now().Add(-c.since)
		tt := ui.NewTester(a.petView, petW, petH)
		tt.Advance(c.since + 100*time.Millisecond)
		tt.Frame()
		shot(t, tt, "pet-"+c.name)
		if got, _ := a.petCurrent(tt.Now()); c.mood != petAsleep && got != c.mood {
			t.Errorf("%s: mood %v", c.name, got)
		}
	}
}

// The Appearance page's switch, for a look: KERO_SHOTS=dir writes it.
func TestPetSettingRow(t *testing.T) {
	a := testApp(t)
	a.ss.draft = map[string]string{}
	a.ss.page = "appearance"
	tt := ui.NewTester(a.settingsView, 820, 640)
	tt.Frame()
	shot(t, tt, "settings-pet")
	if !tt.HasText(locales.SetPet()) || !tt.HasText(locales.SetPetSize()) {
		t.Errorf("the Appearance page does not offer the pet and its size; texts %q", tt.Texts())
	}
}

// The View action turns the frog on and off, under its shortcut.
func TestPetToggleAction(t *testing.T) {
	act, ok := actions.Find("pet.toggle")
	if !ok || actions.Keys(act, nil) == "" {
		t.Fatal("no pet.toggle action, or no shortcut for it")
	}
	a := testApp(t)
	if !a.set.Pet {
		t.Fatal("the pet starts off")
	}
	a.run("pet.toggle", nil)
	if a.set.Pet || a.applied.Pet {
		t.Error("the action left the pet on")
	}
	a.run("pet.toggle", nil)
	if !a.set.Pet || !a.applied.Pet {
		t.Error("the action did not turn the pet back on")
	}
}

// The action is in the command palette, found by what it does.
func TestPetToggleInPalette(t *testing.T) {
	a := testApp(t)
	tt := ui.NewTester(a.view, 1100, 640)
	a.openPalette()
	tt.Frame()
	tt.Type("frog")
	tt.Frame()
	if !tt.HasText(locales.ActionTogglePet()) {
		t.Errorf("frog finds no %q; texts %q", locales.ActionTogglePet(), tt.Texts())
	}
}
