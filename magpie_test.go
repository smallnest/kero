package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// magpieFake is a gateway's answers: its usage's series from day back,
// then pages of requests, 500 to each, newest first.
func magpieFake(t *testing.T, day time.Time, series []string, rows []map[string]any, calls int) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"name":"magpie","apis":["/v1/magpie/usage"]}`)
	})
	mux.HandleFunc("/v1/magpie/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/magpie/usage" {
			http.NotFound(w, r)
			return
		}
		var s []map[string]any
		for _, raw := range series {
			var b map[string]any
			json.Unmarshal([]byte(raw), &b)
			s = append(s, b)
		}
		json.NewEncoder(w).Encode(map[string]any{"calls": 1, "series": s})
	})
	mux.HandleFunc("/v1/magpie/quotas", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"name":"A","windows":[{"name":"5h","remaining":60},{"name":"inf","remaining":100,"unlimited":true}]},{"name":"B","windows":[{"name":"7d","remaining":12.5}]}]}`)
	})
	mux.HandleFunc("/v1/magpie/usage/requests", func(w http.ResponseWriter, r *http.Request) {
		offset := queryInt(r, "offset")
		if r.URL.Query().Get("offset") == "" {
			offset = 0
		}
		limit := queryInt(r, "limit")
		if r.URL.Query().Get("limit") == "" {
			limit = 500
		}
		page := []map[string]any{}
		if offset < len(rows) {
			page = rows[offset:min(offset+limit, len(rows))]
		}
		// Its total counts the calls of the period, before the series
		// holds one, and those of its older pages are past what it keeps.
		json.NewEncoder(w).Encode(map[string]any{"total": calls, "since": day.Format(time.RFC3339), "rows": page})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// queryInt is a query's number, or 500 for none.
func queryInt(r *http.Request, key string) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 500
}

func magpieRow(t time.Time, session string, cost float64) map[string]any {
	return map[string]any{"t": t.Format(time.RFC3339), "session": session, "status": 200, "served": "m", "ms": 900, "cost": cost}
}

func TestFetchMagpie(t *testing.T) {
	now := time.Now()
	day := startOfDay(now)
	today := magpieRow(now.Add(-time.Minute), "sess", 0.02)
	rows := []map[string]any{
		{"t": now.Format(time.RFC3339), "session": "other", "status": 200, "served": "m", "ms": 1446, "cost": 0.5},
		today,
		magpieRow(now.Add(-2*time.Minute), "sess", 0.03),
		magpieRow(now.Add(-3*time.Minute), "other", 0.04),
	}
	// A day before is another day: not counted.
	rows = append(rows, magpieRow(day.Add(-time.Minute), "sess", 9))
	// A call before the first one of it, on a page that the session's
	// calls do not fill.
	for i := 0; i < 4; i++ {
		rows = append(rows, magpieRow(day.Add(-time.Hour), "other", 0))
	}
	srv := magpieFake(t, day, []string{
		`{"time":"` + day.AddDate(0, 0, -1).Format(time.RFC3339) + `","calls":7,"cost":5}`,
		`{"time":"` + day.Format(time.RFC3339) + `","calls":2081,"input":10,"output":5,"cache_read":985,"cost":1.1799}`,
	}, rows, 38525)
	u, err := fetchMagpie(srv.URL, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if u.Today.Calls != 2081 || u.Today.Tokens() != 1000 {
		t.Errorf("today = %+v", u.Today)
	}
	if u.Session != 2 || u.SessionCost < 0.049 || u.SessionCost > 0.051 {
		t.Errorf("session = %d, %v", u.Session, u.SessionCost)
	}
	if u.Latest.Served != "m" || u.Latest.Ms != 1446 || u.Latest.Status != 200 {
		t.Errorf("latest = %+v", u.Latest)
	}
	if len(u.Windows) != 2 || u.Windows[0].Provider != "B" || u.Windows[1].Left != 60 {
		t.Errorf("windows = %+v", u.Windows)
	}
}

func TestFetchMagpiePages(t *testing.T) {
	now := time.Now()
	day := startOfDay(now)
	var rows []map[string]any
	// The session's calls lie on the second page.
	for i := 0; i < 600; i++ {
		s := "other"
		if i >= 520 && i < 523 {
			s = "sess"
		}
		rows = append(rows, magpieRow(now.Add(-time.Duration(i)*time.Second), s, 0.01))
	}
	srv := magpieFake(t, day, nil, rows, 38525)
	u, err := fetchMagpie(srv.URL, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if u.Session != 3 || u.SessErr {
		t.Errorf("session = %d, err %v", u.Session, u.SessErr)
	}
}

func TestFetchMagpieStopsAtYesterday(t *testing.T) {
	now := time.Now()
	day := startOfDay(now)
	var rows []map[string]any
	// The whole page is the day before: today's calls are past it.
	for i := 0; i < 500; i++ {
		rows = append(rows, magpieRow(day.Add(-time.Duration(i)*time.Second), "sess", 1))
	}
	srv := magpieFake(t, day, nil, rows, 38525)
	u, err := fetchMagpie(srv.URL, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if u.Session != 0 || u.SessErr {
		t.Errorf("session = %d, err %v", u.Session, u.SessErr)
	}
}

func TestMagpieCost(t *testing.T) {
	for c, want := range map[float64]string{0: "$0", 0.00061: "$0.0006", 0.0421: "$0.042", 1.1799: "$1.18", 41.67: "$41.7", 102.31: "$102"} {
		if got := magpieCost(c); got != want {
			t.Errorf("magpieCost(%v) = %q, want %q", c, got, want)
		}
	}
}

func TestMagpieTokens(t *testing.T) {
	for n, want := range map[int]string{0: "0", 812: "812", 2870: "2.9k", 34567: "34.6k", 248821: "248k", 1200000: "1.2M", 248460000: "248M"} {
		if got := magpieTokens(n); got != want {
			t.Errorf("magpieTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestPaneSessionCounted checks that Magpie's session is the one the
// pane's agent told kerod, and nothing while it told of none.
func TestPaneSessionCounted(t *testing.T) {
	a := testApp(t)
	p := a.session("s1").FocusPane()
	if s := a.paneSession(); s != "" {
		t.Errorf("a pane without an agent has session %q", s)
	}
	p.Agent = "claude"
	// Without kerod's answer, the pane's agent has no session.
	if s := a.paneSession(); s != "" {
		t.Errorf("an agent whose session kerod does not tell has %q", s)
	}
}
