package daemon

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procName returns the name of a process's executable, without .exe.
// procList returns nil: ps lists the processes, as in an MSYS2 shell.
func procList() []osProc { return nil }

func procName(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	name := filepath.Base(windows.UTF16ToString(buf[:n]))
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// procArgs returns the arguments a process was started with, as the C
// runtime splits its command line.
func procArgs(pid int) []string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(h)
	// A UNICODE_STRING, then the characters it points to.
	buf := make([]byte, 4096)
	for {
		var n uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n)
		if err == windows.STATUS_INFO_LENGTH_MISMATCH && int(n) > len(buf) && n < 1<<20 {
			buf = make([]byte, n)
			continue
		}
		if err != nil {
			return nil
		}
		break
	}
	line := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String()
	args, err := windows.DecomposeCommandLine(line)
	if err != nil {
		return nil
	}
	return args
}

// procCwd returns the working directory of a process, from its process
// parameters. A PowerShell's is its location as the prompt leaves it
// (see shellArgs).
func procCwd(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var pbi windows.PROCESS_BASIC_INFORMATION
	if windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil) != nil || pbi.PebBaseAddress == nil {
		return ""
	}
	// The process is of the daemon's architecture, as the shells are: its
	// structures are laid out as these.
	var peb windows.PEB
	if !readMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb)) || peb.ProcessParameters == nil {
		return ""
	}
	var params windows.RTL_USER_PROCESS_PARAMETERS
	if !readMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&params), unsafe.Sizeof(params)) {
		return ""
	}
	path := params.CurrentDirectory.DosPath
	if path.Length == 0 || path.Buffer == nil {
		return ""
	}
	chars := make([]uint16, path.Length/2)
	if !readMemory(h, uintptr(unsafe.Pointer(path.Buffer)), unsafe.Pointer(&chars[0]), uintptr(path.Length)) {
		return ""
	}
	dir := windows.UTF16ToString(chars)
	// "C:\src\", but "C:\".
	if len(dir) > 3 {
		dir = strings.TrimRight(dir, `\`)
	}
	return dir
}

func readMemory(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) bool {
	var n uintptr
	return windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &n) == nil && n == size
}

// procEntry is a process of a snapshot: its parent, and its executable's
// name.
type procEntry struct {
	pid, parent uint32
	name        string
}

var (
	snapMu   sync.Mutex
	snapAt   time.Time
	snapList []procEntry
)

// processes returns the processes running, from a snapshot that the
// panes' ticks share.
func procEntries() []procEntry {
	snapMu.Lock()
	defer snapMu.Unlock()
	if time.Since(snapAt) < 200*time.Millisecond {
		return snapList
	}
	snapAt = time.Now()
	snapList = nil // a new slice: callers may still hold the last one
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		snapList = append(snapList, procEntry{pid: e.ProcessID, parent: e.ParentProcessID, name: windows.UTF16ToString(e.ExeFile[:])})
	}
	return snapList
}

// started returns when a process started, zero when it is gone.
func started(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0
	}
	return created.Nanoseconds()
}

// newestChild returns the child of a process that started last, 0 when it
// has none. A process whose parent's id was reused is no child.
func newestChild(procs []procEntry, parent uint32) uint32 {
	parentStart := started(parent)
	var best uint32
	var bestStart int64
	for _, e := range procs {
		if e.parent != parent || e.pid == parent || strings.EqualFold(e.name, "conhost.exe") {
			continue
		}
		t := started(e.pid)
		if t == 0 || t < parentStart {
			continue
		}
		if best == 0 || t > bestStart {
			best, bestStart = e.pid, t
		}
	}
	return best
}

func foreground(shell int) int {
	procs := procEntries()
	child := newestChild(procs, uint32(shell))
	if child == 0 {
		return 0
	}
	for pid, depth := child, 0; pid != 0 && depth < 4; pid, depth = newestChild(procs, pid), depth+1 {
		if detectAgent(procName(int(pid)), procArgs(int(pid))) != "" {
			return int(pid)
		}
	}
	return int(child)
}

// procParent returns a process's parent, 0 when it is gone.
func procParent(pid int) int {
	for _, e := range procEntries() {
		if int(e.pid) == pid {
			return int(e.parent)
		}
	}
	return 0
}
