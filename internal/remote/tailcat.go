package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale/tailcat"

	"kero/internal/proto"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// A tailcat address reaches a host over Tailcat (github.com/tailscale/
// tailcat): WireGuard between the two machines, through a DERP relay to
// find each other, and directly once they do. There is no Tailscale, and
// no account: kerod's tailcat://:port listener makes a key and a pair
// code (TailcatAddress), a tc… string of the host's key, its pre-shared
// key and its region, which is the host's address here. Whoever has the
// pair code may connect, as whoever has a key may, so the address is a
// secret as much as a name.
//
// The pair code is whole in the address. A code whose region is only a
// number has the region's details looked up in the relay set (SetRelay),
// as kerod makes codes when the client's relay is not that.

// DefaultDERPMapURL is where tailcat finds its relays, unless SetRelay
// names a map of others: Tailscale's public, rate-limited ones.
const DefaultDERPMapURL = tailcat.DefaultDERPMapURL

// SetRelay sets the URL of the DERP map where tailcat finds relays: ""
// for DefaultDERPMapURL.
func SetRelay(u string) { relay.Store(&u) }

var relay atomic.Pointer[string]

func relayURL() string {
	if p := relay.Load(); p != nil && *p != "" {
		return *p
	}
	return DefaultDERPMapURL
}

// TailcatKey is what kerod's tailcat listener keeps on disk: its key,
// its pre-shared key, and the region of the relay it chose, so that its
// pair code stays the same across kerod's restarts.
type TailcatKey struct {
	Key    string `json:"key"`
	PSK    string `json:"psk"`
	Region string `json:"region,omitempty"` // a region of the relay set's
}

// TailcatKeyPath is where kerod keeps its tailcat key: "tailcat.json" in
// Kero's data folder.
func TailcatKeyPath() string { return filepath.Join(proto.Dir(), "tailcat.json") }

// LocalTailcatAddress is this machine's tailcat address, as kerod's key
// makes it: "tailcat://…", for kerod's listener at the settings' port.
func LocalTailcatAddress(ctx context.Context) (string, error) {
	k, err := LoadTailcatKey(TailcatKeyPath())
	if err != nil {
		return "", err
	}
	code, err := TailcatAddress(ctx, k)
	if err != nil {
		return "", err
	}
	return "tailcat://" + code, nil
}

// LoadTailcatKey reads kerod's tailcat key at path, or makes one, which
// it keeps there for the user alone: the pair code is a secret.
func LoadTailcatKey(path string) (*TailcatKey, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		var k TailcatKey
		if err := json.Unmarshal(b, &k); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return &k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := NewTailcatKey()
	if err != nil {
		return nil, err
	}
	b, _ = json.MarshalIndent(k, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// RotateTailcatKey gives the tailcat key at path a new pair code, which
// the old key's no longer opens, as when a code has gone to the wrong
// person. The new key keeps the region, which callers read again.
func RotateTailcatKey(path string) (*TailcatKey, error) {
	old, err := LoadTailcatKey(path)
	if err != nil {
		return nil, err
	}
	k, err := NewTailcatKey()
	if err != nil {
		return nil, err
	}
	k.Region = old.Region
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// NewTailcatKey makes a key for a tailcat listener.
func NewTailcatKey() (*TailcatKey, error) {
	t := tailcat.NewPrivateKey()
	node, err := t.Private.MarshalText()
	if err != nil {
		return nil, err
	}
	psk, err := t.Public.PresharedKey.MarshalText()
	if err != nil {
		return nil, err
	}
	return &TailcatKey{Key: string(node), PSK: string(psk)}, nil
}

// TailcatAddress is k's pair code at the relay set's nearest region,
// which k keeps for the next time. The code names the region, as
// "derp302" in this list, without the relay's details: a client looks
// them up in its relay set, which is the one kerod listens with.
func TailcatAddress(ctx context.Context, k *TailcatKey) (string, error) {
	t := &tailcat.PrivateKey{Public: tailcat.ConnInfo{RegionID: -1}}
	if err := t.Private.UnmarshalText([]byte(k.Key)); err != nil {
		return "", fmt.Errorf("tailcat key: %w", err)
	}
	if err := t.Public.PresharedKey.UnmarshalText([]byte(k.PSK)); err != nil {
		return "", fmt.Errorf("tailcat key's pre-shared key: %w", err)
	}
	region := hostRegion(k.Region)
	if region == nil {
		dm, err := tailcat.FetchDERPMap(ctx, tailcat.DERPMapURL(relayURL()))
		if err != nil {
			return "", fmt.Errorf("the tailcat relay: %w", err)
		}
		id, err := tailcat.PickBestRegion(ctx, dm)
		if err != nil {
			return "", fmt.Errorf("the tailcat relay: %w", err)
		}
		if region = dm.Regions[id]; region == nil {
			return "", errors.New("the tailcat relay: no region reached")
		}
		k.Region = regionNode(region)
	}
	t.Public.ServerPublic = tailcat.NodePublic{NodePublic: t.Private.Public()}
	t.Public.ServerDiscoPublic = tailcat.DiscoPublicForNode(t.Private)
	t.Public.Region = []*tailcfg.DERPRegion{region}
	t.Public.RegionID = 0
	return string(t.Public.Addr()), nil
}

// relayRegion is the region of the map named "derp302", as the map's
// nodes name their regions.
func relayRegion(dm *tailcfg.DERPMap, node string) *tailcfg.DERPRegion {
	for _, r := range dm.Regions {
		for _, n := range r.Nodes {
			if n.Name == node || n.HostName == node {
				return r
			}
		}
	}
	return nil
}

// hostRegion is the region k saved: the one of a node of the region,
// which tailcat takes for a region of the client's relay map. The node's
// name is the region's number, as the map's are.
func hostRegion(saved string) *tailcfg.DERPRegion {
	id, err := strconv.ParseInt(saved, 10, 64)
	if err != nil || !tailcfg.DERPRegionID(id).IsValid() {
		return nil
	}
	return &tailcfg.DERPRegion{
		RegionID:   tailcfg.DERPRegionID(id),
		RegionCode: "kero",
		RegionName: "Tailcat relay",
		Nodes:      []*tailcfg.DERPNode{{Name: saved, HostName: "relay", RegionID: tailcfg.DERPRegionID(id)}},
	}
}

// regionNode is the name k keeps of a region: its first node's name, its
// number in the relay map.
func regionNode(r *tailcfg.DERPRegion) string {
	if len(r.Nodes) > 0 && r.Nodes[0].Name != "" {
		return r.Nodes[0].Name
	}
	return strconv.Itoa(int(r.RegionID))
}

// validPairCode reports whether s may be a tailcat pair code.
func validPairCode(s string) bool {
	_, err := tailcat.ParseAddr(tailcat.Addr(s))
	return err == nil
}

func tailcatURL(a Addr) string { return "tailcat://" + a.Code }

// parseTailcat reads an address whose pair code is in it, as its String
// writes it: tailcat://tc….
func parseTailcat(s string) (Addr, error) {
	code := strings.TrimPrefix(s, "tailcat://")
	if code == "" || strings.ContainsAny(code, "/?# \t\r\n") {
		return Addr{}, fmt.Errorf("%q: a tailcat address is tailcat:// and the pair code alone", s)
	}
	if !validPairCode(code) {
		return Addr{}, fmt.Errorf("%q: not a tailcat pair code", s)
	}
	return Addr{Scheme: "tailcat", Code: code, Host: "tailcat"}, nil
}

// tailcatClient is a client of a tailcat host, with its client's streams
// open, and how the last one reaches the host.
type tailcatClient struct {
	*tailcat.Client
	addr    net.Addr     // of each connection it opens, as the server's
	Streams atomic.Int32 // open, and which may open
	mu      sync.Mutex
	path    string
	at      time.Time
}

func (c *tailcatClient) setPath(p string) {
	c.mu.Lock()
	c.path, c.at = p, time.Now()
	c.mu.Unlock()
}

var (
	tailcatMu      sync.Mutex
	tailcatClients = map[string]*tailcatClient{} // by pair code
)

// TailcatPath is how the clients of an address reach the host, as of
// when one last told: "direct" for a path between the machines, or the
// region of the relay between them.
func TailcatPath(a Addr) (string, time.Time, bool) {
	if a.Scheme != "tailcat" {
		return "", time.Time{}, false
	}
	tailcatMu.Lock()
	c := tailcatClients[a.Code]
	tailcatMu.Unlock()
	if c == nil {
		return "", time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path, c.at, !c.at.IsZero()
}

// openTailcat opens a tailcat connection to the listener on port of the
// host with the pair code code, as a remote connection runs over: the
// client's streams are the remote connection's.
func openTailcat(ctx context.Context, code, port string) (*tailcatTransport, error) {
	addr, err := tailcat.Addr(code).Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailcat: finding the relay: %w", err)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("tailcat: %q is no port", port)
	}
	c := &tailcatClient{Client: tailcat.NewClient(addr)}
	conn, err := c.DialTCPPort(ctx, uint16(n))
	if err != nil {
		c.Close()
		return nil, err
	}
	c.Streams.Store(1) // the remote connection's
	t := &tailcatTransport{Conn: conn, client: c, done: make(chan struct{})}
	t.watch()
	// A host's last connection, which this one replaces.
	tailcatMu.Lock()
	old := tailcatClients[code]
	tailcatClients[code] = c
	tailcatMu.Unlock()
	if old != nil && old != c {
		old.Close()
	}
	return t, nil
}

// watch tells now and then how the client reaches the host: directly,
// where the networks let them, or through the relay.
func (t *tailcatTransport) watch() {
	c := t.client
	go func() {
		tc := time.NewTicker(10 * time.Second)
		defer tc.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-tc.C:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			res, err := c.DiscoPing(ctx)
			cancel()
			if err == nil {
				c.setPath(pathOf(res))
			}
		}
	}()
}

// pathOf says how a ping came back.
func pathOf(res *ipnstate.PingResult) string {
	switch {
	case res.Endpoint != "":
		return "direct"
	case res.DERPRegionCode != "":
		return res.DERPRegionCode
	}
	return "relay"
}

// tailcatTransport is a remote connection's stream over tailcat: the
// client runs while a stream of the connection's does, which the remote
// connection counts.
type tailcatTransport struct {
	net.Conn
	client *tailcatClient
	done   chan struct{}
	once   sync.Once
}

func (t *tailcatTransport) Close() error {
	err := t.Conn.Close()
	t.release()
	return err
}

func (t *tailcatTransport) release() {
	if t.client.Streams.Add(-1) < 0 {
		t.once.Do(func() {
			close(t.done)
			t.client.Close()
		})
	}
}

// tailcatPortKey is the context key of the port of the host's tailcat
// listener, which its pair code, in whose port is always a registration
// port, does not name.
type tailcatPortKey struct{}

// WithTailcatPort gives ctx the port of a tailcat host's listener.
func WithTailcatPort(ctx context.Context, port string) context.Context {
	return context.WithValue(ctx, tailcatPortKey{}, port)
}
