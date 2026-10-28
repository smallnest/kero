package remote

import (
	"github.com/tailscale/tailcat"
)

// A client of a tailcat listener says its key in the tailcat protocol
// itself, a meow ping, before it opens any stream, and again while the
// server has not registered it: a new client of tailcat sends its first
// streams into the tunnel without waiting, and if the server has not
// taken it yet, its first of them arrives as a meow ping, and the
// others as nothing. A client whose first stream is a meow ping is one of
// tailcat's; kero's own remote connection opens with its Hello, which
// begins "{\"kind\":\"mux\"".

// peerOf names who connects over c, whose first line is hello: "tailcat
// [vzc7…]", the key its meow ping says, or, for its remote connection,
// "tailcat", as the server's log of it has the key.
func peerOf(hello []byte) (string, bool) {
	if k, _, ok := tailcat.ParseMeowPing(hello); ok && !k.IsZero() {
		return "tailcat [" + k.ShortString() + "]", true
	}
	return "", false
}
