package remote

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kero/internal/proto"
)

// A pair code is the address of a tailcat host, as its String writes it,
// and reads back.
func TestTailcatAddr(t *testing.T) {
	k, err := NewTailcatKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	code, err := TailcatAddress(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "tc") {
		t.Errorf("pair code %q", code)
	}
	a, err := ParseAddr("tailcat://" + code)
	if err != nil {
		t.Fatal(err)
	}
	if a.Scheme != "tailcat" || a.Code != code || a.String() != "tailcat://"+code || a.Bridged() || a.Label() == "" {
		t.Errorf("parsed %+v, String %q", a, a.String())
	}
	// The same key's code is the same, its region kept.
	again, err := TailcatAddress(ctx, k)
	if err != nil || again != code {
		t.Errorf("again %q, %v", again, err)
	}
	// The one who has the code has the host's keys.
	if _, err := ParseAddr("tailcat://tcnope"); err == nil {
		t.Error("a bad pair code read")
	}
	for _, bad := range []string{"tailcat://", "tailcat://tc/a", "tailcat://tc?x", "tailcat://tc "} {
		if _, err := ParseAddr(bad); err == nil {
			t.Errorf("%q read", bad)
		}
	}
}

// The key a tailcat listener makes stays, for the user alone: its pair
// code is a secret.
func TestTailcatKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailcat.json")
	k, err := LoadTailcatKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v, %v", st, err)
	}
	again, err := LoadTailcatKey(path)
	if err != nil || again.Key != k.Key || again.PSK != k.PSK {
		t.Errorf("read back %+v, %v", again, err)
	}
	// A new key's code is another, and keeps the region.
	k, err = RotateTailcatKey(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err = LoadTailcatKey(path)
	if err != nil || again.Key != k.Key || again.PSK != k.PSK {
		t.Errorf("rotated read back %+v, %v", again, err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("rotated key file %v, %v", st, err)
	}
}

// A client of a tailcat listener gets to its remote server, as over
// HTTP's.
func TestTailcatMux(t *testing.T) {
	if testing.Short() {
		t.Skip("tailcat starts its relay and network stacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	k, err := NewTailcatKey()
	if err != nil {
		t.Fatal(err)
	}
	port := "7374"
	srv := echoServer(t)
	srv.Stream = func(s net.Conn, hello []byte, mux proto.MuxHello) {
		defer s.Close()
		s.Write([]byte("pong\n"))
	}
	r, err := ListenTailcat(ctx, k, port, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := ParseAddr(r.PairCode)
	if err != nil {
		t.Fatal(err)
	}
	dctx := WithTailcatPort(ctx, port)
	tr, err := transport(dctx, a, "test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := handshake(ctx, tr, a, Options{App: "1.0", Client: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Welcome.Mux != proto.MuxVersion || c.Welcome.Host.Hostname != "box" {
		t.Fatalf("welcome %+v", c.Welcome)
	}
	st, err := c.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, _ := json.Marshal(proto.Hello{Kind: "control", Version: proto.Version})
	st.Write(append(b, '\n'))
	buf := make([]byte, 5)
	st.SetReadDeadline(time.Now().Add(20 * time.Second))
	if n, err := st.Read(buf); err != nil || string(buf[:n]) != "pong\n" {
		t.Errorf("echo %q, %v", buf[:n], err)
	}
}
