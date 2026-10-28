package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/tailscale/tailcat"
)

// A tailcat listener (tailcat.go) for kerod: a Tailcat server that kerod's
// remote server answers the clients of, as it answers an http(s)://
// listener's, at the listener's pair code as its address.

// TailcatRelay is kerod's tailcat listener.
type TailcatRelay struct {
	*TailcatServer
	ln net.Listener
}

// TailcatServer is a Tailcat server with kerod's remote server on one of
// its ports.
type TailcatServer struct {
	*tailcat.Server
	PairCode string // kerod's address, tailcat:// and the pair code
}

// ListenTailcat starts kerod's tailcat listener on port for k's key,
// whose pair code it makes, and answers the connections it takes with
// srv, within ctx. Kerod is "kero", for whomever lists the clients.
func ListenTailcat(ctx context.Context, k *TailcatKey, port string, srv *Server) (*TailcatRelay, error) {
	code, err := TailcatAddress(ctx, k)
	if err != nil {
		return nil, err
	}
	// The region's map, which the code's region came from: the server
	// starts at that region.
	dm, err := tailcat.FetchDERPMap(ctx, tailcat.DERPMapURL(relayURL()))
	if err != nil {
		return nil, fmt.Errorf("the tailcat relay: %w", err)
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(code))
	if err != nil {
		return nil, fmt.Errorf("tailcat: %w", err)
	}
	if len(ci.Region) == 0 {
		return nil, errors.New("tailcat: the pair code names no relay")
	}
	region := relayRegion(dm, k.Region)
	if region == nil {
		return nil, fmt.Errorf("tailcat: region %s is not in %s", k.Region, relayURL())
	}
	priv := &tailcat.PrivateKey{Public: ci}
	if err := priv.Private.UnmarshalText([]byte(k.Key)); err != nil {
		return nil, fmt.Errorf("tailcat key: %w", err)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("tailcat: %q is no port", port)
	}
	s := &tailcat.Server{
		Key:          priv.Private,
		PresharedKey: priv.Public.PresharedKey,
		Region:       region,
		// The relay map, which names the clients' region.
		DERPMapURL: relayURL(),
	}
	ln, err := s.Listen(ctx, "tcp", ":"+strconv.FormatUint(n, 10))
	if err != nil {
		s.Close()
		return nil, err
	}
	// Kerod says who connects: the pair code tells a client's key.
	srv.Peer = func(c net.Conn, hello []byte) string { return peerName(hello) }
	r := &TailcatRelay{TailcatServer: &TailcatServer{Server: s, PairCode: "tailcat://" + code}, ln: ln}
	go r.serve(srv)
	return r, nil
}

func (r *TailcatRelay) serve(srv *Server) {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		go srv.Serve(c)
	}
}

// Close stops the listener, and the server with it.
func (r *TailcatRelay) Close() {
	r.ln.Close()
	r.Server.Close()
}

// peerName names a client, from its Hello, hello: "tailcat [vzc7…]",
// once it has said its key.
func peerName(hello []byte) string {
	if name, ok := peerOf(hello); ok {
		return name
	}
	return "tailcat"
}
