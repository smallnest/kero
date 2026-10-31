package daemon

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"kero/internal/proto"
)

// osProc is a process, as procList lists it.
type osProc struct {
	PID, PPID int
	CPU       float64 // percent of a core
	Rune      string  // the executable's name
}

// processes lists a pane's processes: the terminal's shell and what runs
// under it, the oldest first, each with the port it listens on.
func processes(p proto.ProcessesParams) ([]proto.Process, error) {
	if p.PID <= 0 {
		return nil, nil
	}
	// ps tells the parents a shell that runs its commands in place (zsh -c)
	// leaves as they are, which the kernel's list sometimes tells as its
	// own shell's.
	list := psList()
	if len(list) == 0 {
		list = procList()
	}
	mine := descendant(p.PID, list)
	ids := make([]int, 0, len(mine))
	for _, e := range list {
		if mine[e.PID] {
			ids = append(ids, e.PID)
		}
	}
	ports := listeningPorts(ids)
	type item struct {
		e    osProc
		args []string
	}
	var items []item
	for _, e := range list {
		if mine[e.PID] {
			items = append(items, item{e, procArgs(e.PID)})
		}
	}
	// The parents come first: a process started after it runs under one
	// started before it.
	sort.SliceStable(items, func(i, j int) bool { return items[i].e.PID < items[j].e.PID })
	out := make([]proto.Process, 0, len(items))
	for _, it := range items {
		name := agentOf(it.e.Rune, it.args)
		if name == "" {
			name = strings.TrimSuffix(it.e.Rune, ".exe")
		}
		out = append(out, proto.Process{PID: it.e.PID, PPID: it.e.PPID, Name: name, CPU: it.e.CPU, Age: ageOf(it.e.PID), Ports: ports[it.e.PID]})
	}
	return out, nil
}

// descendant returns which processes are pid or run under it.
func descendant(pid int, list []osProc) map[int]bool {
	kids := map[int][]int{}
	for _, e := range list {
		kids[e.PPID] = append(kids[e.PPID], e.PID)
	}
	seen := map[int]bool{}
	var walk func(int)
	walk = func(id int) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, k := range kids[id] {
			walk(k)
		}
	}
	walk(pid)
	return seen
}

// agentOf returns the coding agent a process runs, as agents names it,
// for the processes their name does not tell: Claude Code's own under its
// versioned directory, as ~/.local/share/claude/versions/2.1.31, and
// its cli.js run by node.
func agentOf(rune string, args []string) string {
	if a := detectAgent(rune, args); a != "" {
		return a
	}
	for i, a := range args {
		if i > 2 {
			break
		}
		a = strings.ReplaceAll(a, `\`, "/")
		switch {
		case strings.Contains(a, "/.claude/"), strings.Contains(a, "claude-code/cli.js"):
			return "claude"
		case strings.Contains(a, "/.codex/"):
			return "codex"
		}
	}
	return ""
}

// ageOf is how long a process ran, as ps tells it: "" when unknown.
func ageOf(pid int) string {
	rows := psLines("-p", strconv.Itoa(pid))
	if len(rows) == 0 {
		return ""
	}
	f := strings.Fields(strings.Join(rows[0], " "))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// psLines runs ps with its output columns, and returns its rows, without
// its header.
func psLines(args ...string) [][]string {
	b, err := exec.Command("ps", args...).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines[1:] {
		if f := strings.Fields(line); len(f) > 0 {
			rows = append(rows, f)
		}
	}
	return rows
}

// psList lists ps's processes when procList tells none, as a system that
// hides its table of processes: Linux's, as on a Windows host's WSL.
func psList() []osProc {
	var list []osProc
	for _, f := range psLines("-ax", "-o", "pid=,ppid=,pcpu=,comm=") {
		if len(f) < 4 {
			continue
		}
		id, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		cpu, _ := strconv.ParseFloat(f[2], 64)
		list = append(list, osProc{PID: id, PPID: ppid, CPU: cpu, Rune: f[3]})
	}
	return list
}
