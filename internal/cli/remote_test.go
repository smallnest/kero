package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"kero/internal/remote"
	"kero/internal/settings"
)

func TestSplitHost(t *testing.T) {
	t.Setenv("KERO_HOST", "")
	for _, c := range []struct {
		args []string
		host string
		rest []string
	}{
		{[]string{"ls"}, "", []string{"ls"}},
		{[]string{"-H", "build", "ls", "-j"}, "build", []string{"ls", "-j"}},
		{[]string{"--host", "me@devbox", "new"}, "me@devbox", []string{"new"}},
		{[]string{"--host=wsl://Ubuntu", "ls"}, "wsl://Ubuntu", []string{"ls"}},
		{[]string{"-Hbuild", "ls"}, "build", []string{"ls"}},
		{[]string{"-H", "local", "ls"}, "", []string{"ls"}},
		// A -H after the command is the command's.
		{[]string{"send", "x", "-H"}, "", []string{"send", "x", "-H"}},
	} {
		host, _, rest, err := splitHost(c.args)
		if err != nil || host != c.host || !slices.Equal(rest, c.rest) {
			t.Errorf("%q: %q %q %v", c.args, host, rest, err)
		}
	}
	t.Setenv("KERO_HOST", "build")
	if host, env, _, _ := splitHost([]string{"ls"}); host != "build" || !env {
		t.Errorf("$KERO_HOST: %q", host)
	}
	if host, env, _, _ := splitHost([]string{"-H", "other", "ls"}); host != "other" || env {
		t.Errorf("-H over $KERO_HOST: %q", host)
	}
	for _, bad := range [][]string{{"-H"}, {"-H", "build"}} {
		if _, _, _, err := splitHost(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestIsCommand(t *testing.T) {
	for arg, want := range map[string]bool{"ls": true, "-H": true, "--host=x": true, "-Hbuild": true, "hosts": true, "__bridge": true, "-psn_0_1": false, "": false} {
		if IsCommand(arg) != want {
			t.Errorf("IsCommand(%q) = %v", arg, !want)
		}
	}
}

func TestRemoteRepoPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"src/a.go":          "src/a.go",
		`src\a.go`:          "src/a.go",
		"/srv/app/src/a.go": "src/a.go",
		"../../etc/passwd":  "etc/passwd",
	} {
		got, err := repoPath("/srv/app", in, true)
		if err != nil || got != want {
			t.Errorf("repoPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := repoPath("/srv/app", "/etc/passwd", true); err == nil {
		t.Error("a path outside the worktree passes")
	}
}

// Inside a pane, $KERO_HOST, which a profile may set, gives way to the
// pane's machine; a command of this machine alone ignores it.
func TestKeroHostScope(t *testing.T) {
	t.Setenv("KERO_HOST", "build")
	t.Setenv("KERO_PANE", "p1")
	if host, _, _, _ := splitHost([]string{"ls"}); host != "" {
		t.Errorf("inside a pane: %q", host)
	}
	if host, _, _, _ := splitHost([]string{"-H", "build", "ls"}); host != "build" {
		t.Errorf("-H inside a pane: %q", host)
	}
	t.Setenv("KERO_PANE", "")
	if !localOnly["hook"] || !localOnly["config"] || localOnly["ls"] {
		t.Error("localOnly")
	}
	onHost = "build"
	defer func() { onHost = "" }()
	t.Setenv("KERO_PANE", "p1")
	if current() != "" {
		t.Error("a pane of this machine named on another")
	}
}

// `kero hosts tailcat` turns this machine's tailcat listener on and off,
// and rotates its pair code, in the settings file it reads.
func TestHostsTailcat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KERO_HOME", home)
	t.Setenv("KERO_CONFIG", filepath.Join(home, "config"))
	if err := tailcatCmd([]string{"enable"}); err == nil {
		t.Error("a listener with Tailcat off")
	}
	set := settings.Load()
	set.Tailcat = true
	if err := settings.Save(set); err != nil {
		t.Fatal(err)
	}
	if err := tailcatCmd([]string{"enable", "7400"}); err != nil {
		t.Fatal(err)
	}
	set = settings.Load()
	if l, port, ok := set.TailcatListen(); !ok || l != "tailcat://:7400" || port != "7400" {
		t.Fatalf("listen %q", settings.Load().Listen)
	}
	if err := tailcatCmd([]string{"port", "http"}); err == nil {
		t.Error("a bad port")
	}
	if err := tailcatCmd([]string{"disable"}); err != nil {
		t.Fatal(err)
	}
	set = settings.Load()
	if _, _, ok := set.TailcatListen(); ok {
		t.Errorf("listen %q", settings.Load().Listen)
	}
	rot := settings.Load().TailcatRotate
	if err := tailcatCmd([]string{"rotate"}); err != nil {
		t.Fatal(err)
	}
	if settings.Load().TailcatRotate == rot {
		t.Error("kerod told not to start its listener again")
	}
	if st, err := os.Stat(remote.TailcatKeyPath()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v, %v", st, err)
	}
}
