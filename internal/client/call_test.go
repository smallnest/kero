package client

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"kero/internal/proto"
)

// A call that ends in an error still decodes its result, as a wait that
// times out brings the pane: a bot's poll takes a turn's status from it.
func TestCallKeepsTheResultOfAFailedCall(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	c := &Client{conn: cli, enc: json.NewEncoder(cli), pending: map[int64]chan response{}, closed: make(chan struct{})}
	go c.read(bufio.NewReader(cli))
	go func() {
		if _, err := proto.ReadLine(bufio.NewReader(srv), proto.MaxLine); err != nil {
			return
		}
		json.NewEncoder(srv).Encode(proto.Message{
			ID:     1,
			Result: proto.Pane{ID: "p1", Status: proto.Working},
			Error:  "timed out",
		})
	}()
	var p proto.Pane
	err := c.Call("wait", nil, &p)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a wait that timed out ended %v", err)
	}
	if p.ID != "p1" || p.Status != proto.Working {
		t.Errorf("the timed out call brought %+v, want p1 working", p)
	}
}
