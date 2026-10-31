package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"kero/internal/proto"
	"kero/locales"
)

// TestInfoDemo lays out the side panel's Info view with processes and
// ports, for a look at it with KERO_SHOTS.
func TestInfoDemo(t *testing.T) {
	a := testApp(t)
	p := a.session("s1").FocusPane()
	st := a.paneInfoOf(p)
	st.dir = "/Users/smallnest/ai/kero"
	st.procs = []proto.Process{
		{PID: 4211, Name: "zsh", Age: "2-03:11:40"},
		{PID: 88130, Name: "claude", Age: "01:12:05", Ports: []int{3000}},
		{PID: 88201, Name: "node", Age: "58:17", CPU: 3.2, Ports: []int{5173, 9229}},
	}
	st.procsAt, st.listedAt = true, time.Now()
	st.ports = 3
	a.showSide(sideInfo)
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	shot(t, tt, "info-demo")
}

// TestInfoMagpieDemo lays out the Info view with Magpie's card, for a look
// at it with KERO_SHOTS.
func TestInfoMagpieDemo(t *testing.T) {
	a := testApp(t)
	p := a.session("s1").FocusPane()
	st := a.paneInfoOf(p)
	st.dir = "/Users/smallnest/ai/kero"
	st.procs = []proto.Process{
		{PID: 4211, Name: "zsh", Age: "2-03:11:40"},
		{PID: 88130, Name: "claude", Age: "01:12:05"},
	}
	st.procsAt, st.listedAt = true, time.Now()
	a.magpie = &magpieInfo{ran: true, readAt: time.Now(), usage: &magpieUsage{
		Today:   magpieBucket{Calls: 2081, Input: 1488819, Output: 359513, CacheRead: 246954880, Reasoning: 333, Cost: 1.1799},
		Session: 412, SessionCost: 0.214,
		Latest: magpieCall{Status: 200, Served: "deepseek-flash", Ms: 1446},
		Windows: []magpieWindow{
			{Provider: "OpenCode Go", Name: "Month", Left: 4},
			{Provider: "OpenCode Go", Name: "5 hours", Left: 37},
			{Provider: "Antigravity", Name: "Claude & GPT · 7 days", Left: 88},
			{Provider: "Antigravity", Name: "Claude & GPT · 5 hours", Left: 100},
		},
	}}
	a.showSide(sideInfo)
	st.readAt, st.busy, st.listedAt = time.Now(), true, time.Now()
	a.magpie.readAt, a.magpie.busy = time.Now(), true
	tt := ui.NewTester(a.view, 1100, 900)
	for i := 0; i < 60 && tt.FramePending(); i++ {
		tt.Frame()
		tt.Advance(time.Second)
	}
	shot(t, tt, "info-magpie")
	if b, ok := tt.Find(locales.InfoMagpie()); ok {
		shotCrop(t, tt, b, image.Rect(-8, -8, 360, 420), "info-magpie-card")
	}
}

// shotCrop saves the part of the frame around b, moved by off.
func shotCrop(t *testing.T, tt *ui.Tester, b ui.Rect, off image.Rectangle, name string) {
	dir := os.Getenv("KERO_SHOTS")
	if dir == "" {
		return
	}
	img := tt.Image()
	r := image.Rect(int(b.X)+off.Min.X, int(b.Y)+off.Min.Y, int(b.X)+off.Max.X, int(b.Y)+off.Max.Y).Intersect(img.Bounds())
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img.SubImage(r))
}
