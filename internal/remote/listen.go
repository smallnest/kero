package remote

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
)

// Listen is one of kerod's network listeners, as the configuration's
// listen key and `kero daemon serve --listen` name it:
//
//   - tailnet://:7373, or tailnet:// for ListenPort: plain HTTP on this
//     machine's Tailscale addresses, which WireGuard encrypts; tailscaled
//     tells who connects.
//   - tailnet-serve:///path/to/serve.sock, or tailnet-serve:// for
//     serve.sock in kero's directory: a socket for Tailscale Serve to pass
//     HTTPS connections to (`tailscale serve --bg unix:/path/to/serve.sock`),
//     which tells who connects in its headers.
//   - tailcat://:7373, or tailcat://: a relay for Kero anywhere, over
//     the tailcat library (tailcat.go): WireGuard end to end,
//     with a public DERP relay only to reach the peer. There is no
//     Tailscale account: the pair code kerod shows is the secret to
//     connect, and the address of the host.
type Listen struct {
	Kind string // "tailnet", "tailnet-serve" or "tailcat"
	Port string // tailnet's or tailcat's
	Path string // tailnet-serve's socket, "" for the default
	// Tailcat is the pair code of a tailcat listener, as kerod makes
	// it: "" of a setting's, which kerod's key's is.
	Tailcat string
}

// ParseListen reads a listener.
func ParseListen(s string) (Listen, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return Listen{}, fmt.Errorf("%q is not a listener: %v", s, err)
	}
	switch u.Scheme {
	case "tailnet":
		if u.Hostname() != "" {
			return Listen{}, fmt.Errorf("%q: a tailnet listener takes the machine's Tailscale addresses: write tailnet://:port", s)
		}
		l := Listen{Kind: "tailnet", Port: u.Port()}
		if l.Port == "" {
			l.Port = ListenPort()
		}
		if n, err := strconv.Atoi(l.Port); err != nil || n < 1 || n > 65535 {
			return Listen{}, fmt.Errorf("%q: a port is 1 to 65535", s)
		}
		return l, nil
	case "tailnet-serve":
		if u.Host != "" {
			return Listen{}, fmt.Errorf("%q: write tailnet-serve:///path/to/socket", s)
		}
		return Listen{Kind: "tailnet-serve", Path: u.Path}, nil
	case "tailcat":
		if u.Hostname() != "" {
			return Listen{}, fmt.Errorf("%q: a tailcat listener takes any port: write tailcat://:port", s)
		}
		code := strings.TrimPrefix(u.Path, "/")
		if code != "" && !validPairCode(code) {
			return Listen{}, fmt.Errorf("%q: not a tailcat pair code", s)
		}
		l := Listen{Kind: "tailcat", Port: u.Port()}
		if l.Port == "" {
			l.Port = ListenPort()
		}
		if n, err := strconv.Atoi(l.Port); err != nil || n < 1 || n > 65535 {
			return Listen{}, fmt.Errorf("%q: a port is 1 to 65535", s)
		}
		return l, nil
	}
	return Listen{}, fmt.Errorf("%q: kerod listens on tailnet://:port, tailnet-serve:///socket or tailcat://:port", s)
}

// ListenPort is the port a tailnet listener takes when it names none:
// DefaultPort, or the next for a development build, whose kerod then
// listens beside the installed Kero's on the same machine.
func ListenPort() string {
	if mygo.IsDev() {
		return "7374"
	}
	return DefaultPort
}

func (l Listen) String() string {
	if l.Kind == "tailnet" || l.Kind == "tailcat" {
		return l.Kind + "://:" + l.Port
	}
	return "tailnet-serve://" + l.Path
}
