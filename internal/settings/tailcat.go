package settings

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"kero/internal/remote"
)

// TailcatHost is a host Kero reaches over tailcat, the app's own: the
// configuration's hosts are of a machine's file, which a client may not
// have, but the client's, kept here.
type TailcatHost struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"` // tailcat:// and the pair code
	Added bool   `json:"added,omitempty"`
}

// validTailcatAddr reports whether s is a tailcat address.
func validTailcatAddr(s string) bool {
	a, err := remote.ParseAddr(s)
	return err == nil && a.Scheme == "tailcat"
}

// ValidTailcatPort reports whether s is a port, as of a host's listener,
// or this machine's.
func ValidTailcatPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n <= 65535
}

// hostName reports whether name may name a tailcat host, as one of a
// machine's may.
func hostName(name string) bool {
	first := true
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !first {
			ok = ok || r == '.' || r == '-' || r == '_'
		}
		if !ok {
			return false
		}
		first = false
	}
	return !first && len(name) <= 64
}

// ValidDERPMapURL reports whether s may name a DERP map: an http or https
// URL, with no password in it for a setting to show.
func ValidDERPMapURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// TailcatPortDefault is a tailcat host's port, of a listener that names
// none: kerod's DefaultPort.
func TailcatPortDefault() string { return remote.DefaultPort }

// TailcatHostByName finds a tailcat host, by its name.
func (s Settings) TailcatHostByName(name string) int {
	return slices.IndexFunc(s.TailcatHosts, func(h TailcatHost) bool { return strings.EqualFold(h.Name, name) })
}

// TailcatNameTaken reports whether name names a host besides the tailcat
// host at i, -1 for a new one.
func (s Settings) TailcatNameTaken(name string, i int) bool {
	if s.Host(name) != nil || strings.EqualFold(name, "local") {
		return true
	}
	j := s.TailcatHostByName(name)
	return j >= 0 && j != i
}

// TailcatListen is a tailcat:// listen of the settings', and its port:
// the one line, as the Tailcat settings of Settings › Hosts keep it.
func (s *Settings) TailcatListen() (listen, port string, ok bool) {
	for _, l := range s.Listen {
		spec, err := remote.ParseListen(l)
		if err == nil && spec.Kind == "tailcat" {
			return l, spec.Port, true
		}
	}
	return "", "", false
}

// SetTailcatListen makes this machine a tailcat host with a listener on
// port, in place of the one it had, or none for "".
func (s *Settings) SetTailcatListen(port string) {
	s.Listen = slices.DeleteFunc(s.Listen, func(l string) bool {
		spec, err := remote.ParseListen(l)
		return err == nil && spec.Kind == "tailcat"
	})
	if port != "" {
		s.Listen = append(s.Listen, "tailcat://:"+port)
	}
}

// TailcatListenPortDefault is a listener of the settings' port when it
// names none.
func TailcatListenPortDefault() string { return remote.ListenPort() }
