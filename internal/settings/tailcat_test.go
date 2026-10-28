package settings

import (
	"context"
	"strings"
	"testing"

	"kero/internal/remote"
)

// The tailcat keys read as the configuration writes them, and a listener
// needs Tailcat on.
func TestTailcatConfig(t *testing.T) {
	k, err := remote.NewTailcatKey()
	if err != nil {
		t.Fatal(err)
	}
	code, err := remote.TailcatAddress(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	file := "tailcat = true\ntailcat-host = box=tailcat://" + code + "\ntailcat-port = 7400\ntailcat-relay = https://relay.example/derpmap.json\n"
	s, probs := parse(file)
	if len(probs) > 0 {
		t.Fatalf("problems %q", probs)
	}
	if !s.Tailcat || len(s.TailcatHosts) != 1 || s.TailcatHosts[0].Name != "box" || s.TailcatPort != "7400" || s.TailcatRelay != "https://relay.example/derpmap.json" {
		t.Errorf("settings %+v", s)
	}
	out := format(s, file)
	for _, line := range []string{"tailcat = true", "tailcat-host = box=tailcat://" + code, "tailcat-port = 7400", "tailcat-relay = https://relay.example/derpmap.json"} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("formatted without %q:\n%s", line, out)
		}
	}
	// Off, a tailcat listener is a problem, and is not kept.
	if _, probs := parse("listen = tailcat://:7373\n"); len(probs) == 0 {
		t.Error("a tailcat listener read with Tailcat off")
	}
	// A bad host, port or relay keeps none of them.
	for _, bad := range []string{"tailcat-host = box=devbox\n", "tailcat-port = http\n", "tailcat-relay = ftp://x\n"} {
		if _, probs := parse(bad); len(probs) == 0 {
			t.Errorf("%q read", bad)
		}
	}
	// A file of tailcat hosts from before the switch asked for Tailcat.
	old := Default()
	old.TailcatHosts = []TailcatHost{{Name: "box", Addr: "tailcat://" + code}}
	old.normalize()
	if !old.Tailcat {
		t.Error("a tailcat host with the switch off")
	}
}

// SetTailcatListen gives the settings one tailcat listener, on the port it
// names, and none for "".
func TestTailcatSetListen(t *testing.T) {
	s := Default()
	s.Listen = []string{"tailnet://:7373"}
	if _, _, ok := s.TailcatListen(); ok {
		t.Fatal("a tailcat listener without one")
	}
	s.SetTailcatListen("7400")
	s.SetTailcatListen("7401")
	l, port, ok := s.TailcatListen()
	if !ok || l != "tailcat://:7401" || port != "7401" || len(s.Listen) != 2 || s.Listen[1] != "tailcat://:7401" {
		t.Fatalf("listeners %q", s.Listen)
	}
	s.SetTailcatListen("")
	if _, _, ok := s.TailcatListen(); ok || len(s.Listen) != 1 || s.Listen[0] != "tailnet://:7373" {
		t.Fatalf("listeners %q", s.Listen)
	}
}
