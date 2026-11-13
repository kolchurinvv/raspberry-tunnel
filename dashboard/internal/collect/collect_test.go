package collect_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/store"
)

var t0 = time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)

// fakePi answers GET /status the way D2's contract says, or misbehaves on
// request. It is the only fake here: a real Pi can't be told to time out.
type fakePi struct {
	mu     sync.Mutex
	status map[string]any
	mode   string // "", "slow", "500", "garbage", "text", "schema2", "huge"
}

func (f *fakePi) set(fn func(m map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.status)
}

func (f *fakePi) setMode(m string) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
}

func (f *fakePi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	mode := f.mode
	body, err := json.Marshal(f.status)
	f.mu.Unlock()
	if err != nil {
		panic(err)
	}
	if r.URL.Path != "/status" {
		http.NotFound(w, r)
		return
	}
	switch mode {
	case "slow":
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		return
	case "500":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"schema": 1, "error": "tailscaled not answering at 100.101.102.1"}`))
		return
	case "garbage":
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schema": 1, "host": "pi-b", "public_ip": {"value": "203.0.113.98"`))
		return
	case "text":
		w.Header().Set("Content-Type", "text/plain")
		w.Write(body)
		return
	case "schema2":
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schema": 2, "host": "pi-b"}`))
		return
	case "huge":
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes.Repeat([]byte(" "), 70<<10))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func exitStatus() map[string]any {
	return map[string]any{
		"schema": 1, "host": "pi-a", "role": "exit", "time": t0.Format(time.RFC3339), "uptime_s": 100000,
		"cpu":    map[string]any{"load": []float64{0.1, 0.1, 0.1}, "stat": stat(1000, 9000)},
		"temp_c": 51.5, "undervoltage": false, "undervoltage_events": nil,
		"mem":  map[string]any{"total_kb": 1000, "available_kb": 750},
		"disk": map[string]any{"total_b": 1000, "used_b": 120, "avail_b": 880},
		"net":  map[string]any{"wan0": map[string]any{"rx_bytes": 1, "tx_bytes": 2}},
		"tunnel": []any{map[string]any{"peer": "pi-b", "online": true, "path": "direct", "relay": nil,
			"rx_bytes": 1000, "tx_bytes": 0, "latency_ms": nil}},
		"public_ip": map[string]any{"value": "203.0.113.7", "checked": t0.Add(-2 * time.Minute).Format(time.RFC3339)},
		"gateway":   nil,
	}
}

func gatewayStatus() map[string]any {
	return map[string]any{
		"schema": 1, "host": "pi-b", "role": "gateway", "time": t0.Format(time.RFC3339), "uptime_s": 187200,
		"cpu":    map[string]any{"load": []float64{0.42, 0.31, 0.22}, "stat": stat(2000, 8000)},
		"temp_c": 47.2, "undervoltage": false, "undervoltage_events": nil,
		"mem":  map[string]any{"total_kb": 2000, "available_kb": 1500},
		"disk": map[string]any{"total_b": 100, "used_b": 50, "avail_b": 50},
		"net":  map[string]any{"wan0": map[string]any{"rx_bytes": 0, "tx_bytes": 0}},
		"tunnel": []any{map[string]any{"peer": "pi-a", "online": true, "path": "direct", "relay": nil,
			"rx_bytes": 0, "tx_bytes": 0, "latency_ms": 11.2}},
		"public_ip": map[string]any{"value": "203.0.113.7", "checked": t0.Add(-time.Minute).Format(time.RFC3339)},
		"gateway": map[string]any{"tv_port": map[string]any{"link": true, "speed_mbps": 100},
			"tv_port_gated": true, "fallback": false},
	}
}

// stat is a /proc/stat cpu line with the given busy and idle ticks.
func stat(busy, idle int) map[string]any {
	return map[string]any{"user": busy, "nice": 0, "system": 0, "idle": idle, "iowait": 0, "irq": 0, "softirq": 0, "steal": 0}
}

type fakeDisc struct {
	mu    sync.Mutex
	peers []collect.Peer
	err   error
}

func (d *fakeDisc) Peers(context.Context) ([]collect.Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]collect.Peer(nil), d.peers...), d.err
}

type fakeNode struct{}

func (fakeNode) NodeMetrics(context.Context) (collect.NodeMetrics, error) {
	c, m := 7.0, 76.0
	return collect.NodeMetrics{CPUPct: &c, MemPct: &m}, nil
}

type rig struct {
	t      *testing.T
	exit   *fakePi
	gw     *fakePi
	disc   *fakeDisc
	store  *store.Store
	dbPath string
	col    *collect.Collector
	client *http.Client
	routes map[string]string // tailnet address → test server address
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, exit: &fakePi{status: exitStatus()}, gw: &fakePi{status: gatewayStatus()}, routes: map[string]string{}}
	for addr, pi := range map[string]*fakePi{"100.101.102.1:9190": r.exit, "100.101.102.2:9190": r.gw} {
		srv := httptest.NewServer(pi)
		t.Cleanup(srv.Close)
		r.routes[addr] = srv.Listener.Addr().String()
	}
	r.disc = &fakeDisc{peers: []collect.Peer{
		{Host: "pi-a", Role: model.RoleExit, Addr: netip.MustParseAddr("100.101.102.1"), Online: true},
		{Host: "pi-b", Role: model.RoleGateway, Addr: netip.MustParseAddr("100.101.102.2"), Online: true},
	}}
	r.dbPath = filepath.Join(t.TempDir(), "dashboard.db")
	st, err := store.Open(context.Background(), r.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r.store = st
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		to, ok := r.routes[addr]
		if !ok {
			return nil, errors.New("no route to " + addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, to)
	}
	r.client = &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
	r.col = collect.New(collect.Options{
		Discoverer: r.disc, Store: st, Log: quiet(), Node: fakeNode{}, Timeout: 300 * time.Millisecond, Client: r.client,
	})
	return r
}

func (r *rig) samples() map[string][]model.Sample {
	r.t.Helper()
	all, err := r.store.Samples(context.Background(), t0.Add(-time.Hour), t0.Add(24*time.Hour))
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string][]model.Sample{}
	for _, s := range all {
		out[s.Host] = append(out[s.Host], s)
	}
	return out
}

func (r *rig) events() []string {
	r.t.Helper()
	evs, err := r.store.Events(context.Background(), t0.Add(-time.Hour), t0.Add(24*time.Hour), 100)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for i := len(evs) - 1; i >= 0; i-- {
		out = append(out, evs[i].Host+":"+string(evs[i].Kind)+":"+evs[i].Detail)
	}
	return out
}

func near(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-6 }

// Two good answers: every value is stored, and read back from the database.
func TestCycleStoresAnswers(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.col.Cycle(ctx, t0)
	// 30 seconds later: 3,750,000 more tunnel bytes is 1 Mbit/s, and 300 of
	// 1200 more ticks busy is 25% CPU.
	r.gw.set(func(m map[string]any) {
		m["uptime_s"] = 187230
		m["time"] = t0.Add(30 * time.Second).Format(time.RFC3339)
		m["cpu"].(map[string]any)["stat"] = stat(2300, 8900)
		m["tunnel"].([]any)[0].(map[string]any)["rx_bytes"] = 3_000_000
		m["tunnel"].([]any)[0].(map[string]any)["tx_bytes"] = 750_000
	})
	r.exit.set(func(m map[string]any) {
		m["uptime_s"] = 100030
		m["tunnel"].([]any)[0].(map[string]any)["rx_bytes"] = 1000 + 7_500_000
	})
	r.col.Cycle(ctx, t0.Add(30*time.Second))

	got := r.samples()
	if len(got["pi-a"]) != 2 || len(got["pi-b"]) != 2 {
		t.Fatalf("samples = %d, %d; want 2 each", len(got["pi-a"]), len(got["pi-b"]))
	}
	first, b := got["pi-b"][0], got["pi-b"][1]
	if first.CPUPct != nil || first.TrafficMbps != nil {
		t.Errorf("first sample has rates without a previous one: %v, %v", first.CPUPct, first.TrafficMbps)
	}
	if b.Outcome != model.OutcomeOK || b.Role != model.RoleGateway || !b.TS.Equal(t0.Add(30*time.Second)) {
		t.Errorf("sample = %+v", b)
	}
	if !near(b.TrafficMbps, 1) || !near(b.CPUPct, 25) || !near(b.TempC, 47.2) || !near(b.MemPct, 25) ||
		!near(b.DiskPct, 50) || !near(b.LatencyMS, 11.2) || b.UptimeS == nil || *b.UptimeS != 187230 {
		t.Errorf("values: traffic %v cpu %v temp %v mem %v disk %v latency %v uptime %v",
			b.TrafficMbps, b.CPUPct, b.TempC, b.MemPct, b.DiskPct, b.LatencyMS, b.UptimeS)
	}
	if b.Path != model.PathDirect || b.Route != model.RouteTunnel || b.ExitMatch == nil || !*b.ExitMatch ||
		b.Fallback == nil || *b.Fallback || b.TVGated == nil || !*b.TVGated || b.TVLink == nil || !*b.TVLink {
		t.Errorf("gateway fields: %+v", b)
	}
	// Null undervoltage events stay unknown, never zero.
	if b.UndervoltageEvents != nil || b.Undervoltage == nil || *b.Undervoltage {
		t.Errorf("undervoltage = %v, events = %v", b.Undervoltage, b.UndervoltageEvents)
	}
	a := got["pi-a"][1]
	if !near(a.TrafficMbps, 2) || a.Route != model.RouteNone || a.Path != model.PathUnknown || a.ExitMatch != nil {
		t.Errorf("exit sample = %+v", a)
	}

	// The rollup holds both samples.
	rolls, err := r.store.Rollups(ctx, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, ro := range rolls {
		if ro.N != 2 || ro.NOK != 2 {
			t.Errorf("rollup %s: n %d ok %d", ro.Host, ro.N, ro.NOK)
		}
	}
	snap := r.col.Snapshot()
	if snap.Tailnet != collect.TailnetOK || len(snap.Pis) != 2 || snap.Pis[0].Host != "pi-a" || !snap.NodeOK {
		t.Errorf("snapshot = %+v", snap)
	}
}

// Nulls everywhere are an answer, with nothing known.
func TestCycleNulls(t *testing.T) {
	r := newRig(t)
	r.gw.set(func(m map[string]any) {
		for _, k := range []string{"time", "uptime_s", "cpu", "temp_c", "undervoltage", "undervoltage_events", "mem", "disk", "net", "tunnel", "public_ip", "gateway"} {
			m[k] = nil
		}
	})
	r.col.Cycle(context.Background(), t0)
	b := r.samples()["pi-b"][0]
	if b.Outcome != model.OutcomeOK || b.TempC != nil || b.MemPct != nil || b.Fallback != nil || b.ExitMatch != nil ||
		b.Path != model.PathUnknown || b.Route != model.RouteNoData {
		t.Errorf("sample = %+v", b)
	}
}

// A counter that went down was reset: no rate for that interval, and a
// reboot when the uptime went down too.
func TestCycleCounterReset(t *testing.T) {
	ctx := context.Background()
	// Start the counters high, so the second answer is lower.
	r2 := newRig(t)
	r2.gw.set(func(m map[string]any) { m["tunnel"].([]any)[0].(map[string]any)["rx_bytes"] = 9_000_000 })
	r2.col.Cycle(ctx, t0)
	r2.gw.set(func(m map[string]any) {
		m["uptime_s"] = 40
		m["cpu"].(map[string]any)["stat"] = stat(10, 50)
		m["tunnel"].([]any)[0].(map[string]any)["rx_bytes"] = 1000
	})
	r2.col.Cycle(ctx, t0.Add(30*time.Second))
	b := r2.samples()["pi-b"][1]
	if b.TrafficMbps != nil || b.CPUPct != nil {
		t.Errorf("rates across a reset: traffic %v, cpu %v", b.TrafficMbps, b.CPUPct)
	}
	if got := r2.events(); len(got) != 1 || got[0] != "pi-b:reboot:" {
		t.Errorf("events = %q", got)
	}
}

// Every way a poll can fail is data: stored with its outcome, never a crash.
func TestCycleFailures(t *testing.T) {
	cases := []struct {
		mode   string
		setup  func(r *rig)
		want   model.Outcome
		schema int
	}{
		{"slow", nil, model.OutcomeTimeout, 0},
		{"500", nil, model.OutcomeHTTPError, 0},
		{"garbage", nil, model.OutcomeInvalid, 0},
		{"text", nil, model.OutcomeInvalid, 0},
		{"huge", nil, model.OutcomeInvalid, 0},
		{"schema2", nil, model.OutcomeSchema, 2},
		{"refused", func(r *rig) { r.routes["100.101.102.2:9190"] = closedPort(r.t) }, model.OutcomeUnreachable, 0},
		{"offline", func(r *rig) {
			r.routes["100.101.102.2:9190"] = closedPort(r.t)
			r.disc.peers[1].Online = false
		}, model.OutcomeOffline, 0},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			r := newRig(t)
			ctx := context.Background()
			r.col.Cycle(ctx, t0)
			if c.setup != nil {
				c.setup(r)
			} else {
				r.gw.setMode(c.mode)
			}
			r.col.Cycle(ctx, t0.Add(30*time.Second))
			s := r.samples()["pi-b"]
			if len(s) != 2 {
				t.Fatalf("samples = %d", len(s))
			}
			b := s[1]
			if b.Outcome != c.want || b.SchemaSeen != c.schema || b.Route != model.RouteNoData || b.TempC != nil {
				t.Errorf("sample = %+v, want outcome %s", b, c.want)
			}
			if got := r.events(); len(got) != 1 || got[0] != "pi-b:unreachable:"+string(c.want) {
				t.Errorf("events = %q", got)
			}
			// The last good answer is still there for the page.
			for _, p := range r.col.Snapshot().Pis {
				if p.Host == "pi-b" && (p.LastOK == nil || !p.LastOK.TS.Equal(t0)) {
					t.Errorf("last good answer lost: %+v", p.LastOK)
				}
			}
		})
	}
}

func closedPort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// The exit check needs both checks fresh; an old one says nothing.
func TestExitCheck(t *testing.T) {
	cases := []struct {
		name string
		gw   func(m map[string]any)
		want *bool
	}{
		{"match", nil, ptr(true)},
		{"mismatch", func(m map[string]any) { m["public_ip"].(map[string]any)["value"] = "198.51.100.4" }, ptr(false)},
		{"stale", func(m map[string]any) {
			m["public_ip"].(map[string]any)["checked"] = t0.Add(-20 * time.Minute).Format(time.RFC3339)
		}, nil},
		{"never checked", func(m map[string]any) { m["public_ip"] = nil }, nil},
		{"same address, IPv4-mapped", func(m map[string]any) { m["public_ip"].(map[string]any)["value"] = "::ffff:203.0.113.7" }, ptr(true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			if c.gw != nil {
				r.gw.set(c.gw)
			}
			r.col.Cycle(context.Background(), t0)
			got := r.samples()["pi-b"][0].ExitMatch
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Errorf("ExitMatch = %v, want %v", got, c.want)
			}
		})
	}
	// The exit's own check gone stale makes every gateway's unknown.
	r := newRig(t)
	r.exit.set(func(m map[string]any) {
		m["public_ip"].(map[string]any)["checked"] = t0.Add(-11 * time.Minute).Format(time.RFC3339)
	})
	r.col.Cycle(context.Background(), t0)
	if got := r.samples()["pi-b"][0].ExitMatch; got != nil {
		t.Errorf("ExitMatch with a stale exit check = %v", *got)
	}
}

func ptr[T any](v T) *T { return &v }

// The dashboard's own outage records no samples, and an event, while the
// cluster's node metrics stay live.
func TestCycleTailnetDown(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.col.Cycle(ctx, t0)
	r.disc.err = errors.Join(collect.ErrTailnetDown, errors.New("out of contact"))
	r.col.Cycle(ctx, t0.Add(30*time.Second))
	r.col.Cycle(ctx, t0.Add(60*time.Second))
	snap := r.col.Snapshot()
	if snap.Tailnet != collect.TailnetDown || !snap.At.Equal(t0) || !snap.NodeOK {
		t.Errorf("snapshot: tailnet %s, at %v, node %v", snap.Tailnet, snap.At, snap.NodeOK)
	}
	if got := r.samples(); len(got["pi-a"]) != 1 || len(got["pi-b"]) != 1 {
		t.Errorf("samples while cut off: %d, %d", len(got["pi-a"]), len(got["pi-b"]))
	}
	r.disc.err = nil
	r.col.Cycle(ctx, t0.Add(90*time.Second))
	want := []string{model.DashboardHost + ":tailnet_lost:", model.DashboardHost + ":tailnet_back:"}
	if got := r.events(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %q, want %q", got, want)
	}
	for _, c := range []struct {
		err  error
		want collect.TailnetState
	}{{collect.ErrStarting, collect.TailnetStarting}, {collect.ErrNotTagged, collect.TailnetNotTagged}, {errors.New("other"), collect.TailnetDown}} {
		r.disc.err = c.err
		r.col.Cycle(ctx, t0.Add(2*time.Minute))
		if got := r.col.Snapshot().Tailnet; got != c.want {
			t.Errorf("after %v: tailnet = %s, want %s", c.err, got, c.want)
		}
	}
}

// A restart in the same slot neither stores twice nor forgets events.
func TestLoadAfterRestart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.col.Cycle(ctx, t0)
	again := collect.New(collect.Options{Discoverer: r.disc, Store: r.store, Log: quiet(), Client: r.client, Timeout: 300 * time.Millisecond})
	if err := again.Load(ctx); err != nil {
		t.Fatal(err)
	}
	snap := again.Snapshot()
	if snap.Tailnet != collect.TailnetStarting || !snap.At.Equal(t0) || len(snap.Pis) != 2 || snap.Pis[1].LastOK == nil {
		t.Errorf("loaded snapshot = %+v", snap)
	}
	r.gw.setMode("500")
	r.col = again
	r.col.Cycle(ctx, t0) // the same slot: skipped
	r.col.Cycle(ctx, t0.Add(30*time.Second))
	if got := r.events(); len(got) != 1 || got[0] != "pi-b:unreachable:http_error" {
		t.Errorf("events after restart = %q", got)
	}
	if n := len(r.samples()["pi-b"]); n != 2 {
		t.Errorf("samples = %d, want 2", n)
	}
	// Restarting during the outage keeps the last good answer for the page.
	third := collect.New(collect.Options{Discoverer: r.disc, Store: r.store, Log: quiet(), Client: r.client})
	if err := third.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if p := third.Snapshot().Pis[1]; p.Latest.OK() || p.LastOK == nil || !p.LastOK.TS.Equal(t0) {
		t.Errorf("after a restart in an outage: latest %s, last good %+v", p.Latest.Outcome, p.LastOK)
	}
}

// No address reaches the database: not the tailnet addresses dialled, not
// the public addresses compared, not the ones in a failing Pi's error.
func TestNoAddressStored(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.col.Cycle(ctx, t0)
	r.gw.setMode("500")
	r.col.Cycle(ctx, t0.Add(30*time.Second))
	r.gw.setMode("garbage")
	r.col.Cycle(ctx, t0.Add(60*time.Second))
	r.store.Close()
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(r.dbPath + suffix)
		if err != nil {
			if suffix == "-wal" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatal(err)
		}
		for _, needle := range []string{"203.0.113", "100.101.102", "198.51.100", "127.0.0.1"} {
			if bytes.Contains(b, []byte(needle)) {
				t.Errorf("%s holds %s", filepath.Base(r.dbPath+suffix), needle)
			}
		}
	}
}
