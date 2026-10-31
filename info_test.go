package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"kero/locales"
)

// A process's time as ps tells it reads in words: days as hours.
func TestDuration(t *testing.T) {
	for in, want := range map[string]string{
		"62:17:03": "62h 17m", "62-17:03": "1488h 17m", "62-17:03:00": "1505h 3m", "02:14": "2m 14s", "51": "51s", "51s": "51s", "": "",
		"1:02:03": "1h 2m", "0:00:05": "5s", "62h17m03s": "62h 17m",
	} {
		if got := duration(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Its action shows the side panel's Info view, and the way back puts the
// panel away.
func TestInfoToggle(t *testing.T) {
	a := testApp(t)
	a.prefs.Side = ""
	a.showSide(sideChanges)
	a.run("info.toggle", nil)
	if a.prefs.Side != sideInfo {
		t.Fatalf("the Info view did not show: %q", a.prefs.Side)
	}
	tt := ui.NewTester(a.view, 1100, 640)
	tt.Frame()
	if !tt.HasText(locales.InfoCopyPath()) {
		t.Errorf("the Info view shows no directory: %v", tt.Texts())
	}
	a.run("info.toggle", nil)
	if a.prefs.Side != "" {
		t.Errorf("the panel stayed: %q", a.prefs.Side)
	}
}

// The side panel's Info tab shows the view, by the Changes and Files tabs.
func TestSideInfoTab(t *testing.T) {
	a := testApp(t)
	a.prefs.Side = sideChanges
	tt := ui.NewTester(a.view, 1100, 640)
	tt.Frame()
	if !tt.HasText(locales.SideInfo()) {
		t.Fatalf("no Info tab: %v", tt.Texts())
	}
	// As a click on it does.
	a.showSide(sideInfo)
	tt.Frame()
	if !tt.HasText(locales.InfoCopyPath()) {
		t.Errorf("the side panel's Info view is not shown: %v", tt.Texts())
	}
	if err := tt.Click(locales.InfoCopyPath()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.ToastCopied()) {
		t.Errorf("Copy Path shows no toast: %v", tt.Texts())
	}
}
