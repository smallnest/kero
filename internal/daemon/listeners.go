package daemon

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// listeningPorts returns the ports the processes of pids listen on, by the
// process holding each: TCP, and UDP apart from what only receives, as
// Bonjour's mDNSResponder does, which every Mac then has.
func listeningPorts(pids []int) map[int][]int {
	// lsof lists the machine's: this tells the pids' alone as it reads.
	mine := make(map[int]bool, len(pids))
	for _, pid := range pids {
		mine[pid] = true
	}
	lsof := "lsof"
	if v := os.Getenv("LSOF"); v != "" {
		lsof = v
	}
	b, err := exec.Command(lsof, "-F", "pfn", "-nP", "-iTCP", "-sTCP:LISTEN", "-iUDP").Output()
	if err != nil && len(b) == 0 {
		// lsof exits 1 when it finds nothing.
		return nil
	}
	// A field to a line: pPID, then a file's f, its n address, and its t
	// type, which lsof tells when asked: the file's type comes first.
	ports := map[int][]int{}
	pid := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		if line[0] == 'p' {
			pid, _ = strconv.Atoi(line[1:])
			continue
		}
		if !mine[pid] {
			continue
		}
		switch line[0] {
		case 'n':
			// A socket that only receives has no address.
			if line == "n*:*" || strings.Contains(line, "->") {
				continue
			}
			a := strings.ReplaceAll(line[1:], "%", ":")
			port, err := strconv.Atoi(a[strings.LastIndexByte(a, ':')+1:])
			if err == nil && port > 0 && !slices.Contains(ports[pid], port) {
				ports[pid] = append(ports[pid], port)
			}
		}
	}
	return ports
}
