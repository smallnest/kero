package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kero/internal/settings"
)

// homePaths are the account's places for programs, which a launchd job's
// PATH, the system's alone, does not have: agents install to them.
func homePaths() []string {
	home := settings.UserHome()
	if home == "" {
		return nil
	}
	paths := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".cargo", "bin"),
	}
	// A Node an agent's CLI runs under, as nvm's and fnm's, newest last.
	for _, pattern := range []string{
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
		filepath.Join(home, ".local", "share", "fnm", "node-versions", "*", "installation", "bin"),
	} {
		if versions, err := filepath.Glob(pattern); err == nil {
			paths = append(paths, byVersion(versions)...)
		}
	}
	return append(paths, "/opt/homebrew/bin", "/usr/local/bin")
}

// withHomePaths is a PATH, as launchd gives it, with the account's
// places after its own: those its login shell puts, whose profiles set
// other agents' as well, then the ones homePaths knows. A job started by
// the app's window takes its environment, the shell's, and those places
// are the shell's already.
func withHomePaths(path string) string {
	sep := string(os.PathListSeparator)
	have := map[string]bool{}
	for _, p := range strings.Split(path, sep) {
		have[p] = true
	}
	places := append(loginPaths(), homePaths()...)
	for _, p := range places {
		if !have[p] {
			have[p] = true
			path = strings.TrimSuffix(path, sep) + sep + p
		}
	}
	return path
}

var login struct {
	mu    sync.Mutex
	at    time.Time
	paths []string
}

// loginPaths are the PATH the account's login shell puts, read from it
// at most once a minute: a profile may be slow.
func loginPaths() []string {
	login.mu.Lock()
	defer login.mu.Unlock()
	if time.Since(login.at) < time.Minute {
		return login.paths
	}
	login.at = time.Now()
	login.paths = readLoginPaths(userShell())
	return login.paths
}

// readLoginPaths asks a shell, as a terminal starts it, for its PATH.
func readLoginPaths(shell string) []string {
	if shell == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lic", `printf %s "$PATH"`).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, p := range strings.Split(string(out), string(os.PathListSeparator)) {
		if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "/var/run/com.apple.security") {
			paths = append(paths, p)
		}
	}
	return paths
}

// version is a versioned directory's tag, as "v20.0.0".
var version = regexp.MustCompile(`^v\d+(\.\d+)*$`)

// byVersion sorts versioned directories, as v9.1.0 before v20.0.0.
func byVersion(paths []string) []string {
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := numbers(paths[i]), numbers(paths[j])
		for k := 0; k < min(len(a), len(b)); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return paths
}

// numbers returns the numbers of a path's version tags.
func numbers(path string) []int {
	var nums []int
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if !version.MatchString(part) {
			continue
		}
		for _, n := range strings.Split(part[1:], ".") {
			v, _ := strconv.Atoi(n)
			nums = append(nums, v)
		}
	}
	return nums
}
