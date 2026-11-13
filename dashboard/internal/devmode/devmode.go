// Package devmode runs the dashboard with fake Pis: no tsnet, and no network
// but the page's own listener. The fake Pis answer D2's schema 1 in-process,
// through an http.RoundTripper, so the real parsing, sampling, storage and
// rendering all run. It backfills 30 days of history so every range has
// something to show.
//
// The page says "Sample data" in this mode.
package devmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// Scenario is what the fake tunnel is doing now.
type Scenario string

const (
	Healthy  Scenario = "healthy"
	Relayed  Scenario = "relayed"  // Pi B's tunnel relayed for the last 18 minutes
	Fallback Scenario = "fallback" // Pi A unreachable for the last 9 minutes, every TV on its own connection
	CutOff   Scenario = "cutoff"   // the dashboard itself cut off from the tailnet for the last 9 minutes
)

// ParseScenario reads a scenario name.
func ParseScenario(s string) (Scenario, error) {
	switch Scenario(s) {
	case Healthy, Relayed, Fallback, CutOff:
		return Scenario(s), nil
	}
	return "", fmt.Errorf("unknown scenario %q: want healthy, relayed, fallback or cutoff", s)
}

// Sim is a fake tunnel: Pi A and one to three gateways.
type Sim struct {
	scenario Scenario
	end      time.Time // the scenario's "now", where its current trouble ends
	hosts    []string
	gateways int

	mu    sync.Mutex
	clock time.Time // zero: follow the wall clock
	state map[string]*fakePi
}

type fakePi struct {
	last   time.Time
	boot   time.Time
	busy   float64 // CPU ticks, USER_HZ, summed over the cores
	idle   float64
	tunnel map[string]float64 // peer → bytes both ways
	ifaces map[string]float64
}

// New returns a Sim with n gateways (1 to 3), whose current trouble, if any,
// is at end.
func New(s Scenario, n int, end time.Time) (*Sim, error) {
	if n < 1 || n > 3 {
		return nil, fmt.Errorf("gateways: %d, want 1 to 3", n)
	}
	sim := &Sim{scenario: s, end: end, gateways: n, state: map[string]*fakePi{}}
	sim.hosts = []string{"pi-a", "pi-b", "pi-c", "pi-d"}[:n+1]
	return sim, nil
}

func (s *Sim) now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clock.IsZero() {
		return time.Now()
	}
	return s.clock
}

func (s *Sim) set(t time.Time) {
	s.mu.Lock()
	s.clock = t
	s.mu.Unlock()
}

// addr is a fake tailnet address. It never leaves the collector, like a real
// one.
func addr(i int) netip.Addr { return netip.AddrFrom4([4]byte{100, 64, 0, byte(i + 1)}) }

// Peers implements collect.Discoverer.
func (s *Sim) Peers(context.Context) ([]collect.Peer, error) {
	t := s.now()
	if s.scenario == CutOff && !t.Before(s.end.Add(-9*time.Minute)) {
		return nil, fmt.Errorf("%w: the dev scenario", collect.ErrTailnetDown)
	}
	var out []collect.Peer
	for i, h := range s.hosts {
		role := model.RoleGateway
		if i == 0 {
			role = model.RoleExit
		}
		p := collect.Peer{Host: h, Role: role, Addr: addr(i), Online: true}
		if i == 0 && s.exitDown(t) {
			p.Online, p.LastSeen = false, t.Add(-time.Minute)
			if s.scenario == Fallback {
				p.LastSeen = s.end.Add(-9 * time.Minute)
			}
		}
		if i == 1 {
			exp := s.end.Add(140 * 24 * time.Hour)
			p.KeyExpiry = &exp
		}
		out = append(out, p)
	}
	return out, nil
}

// NodeMetrics implements collect.NodeSource.
func (s *Sim) NodeMetrics(context.Context) (collect.NodeMetrics, error) {
	t := s.now()
	cpu := 6 + 3*h(float64(t.Unix()/60)*0.37)
	mem := 76 + 2*h(float64(t.Unix()/600)*0.11)
	return collect.NodeMetrics{CPUPct: &cpu, MemPct: &mem}, nil
}

// Client returns an HTTP client whose requests go to the fake Pis.
func (s *Sim) Client() *http.Client { return &http.Client{Transport: s} }

// RoundTrip implements http.RoundTripper, answering GET /status as the Pi
// at the request's address.
func (s *Sim) RoundTrip(r *http.Request) (*http.Response, error) {
	t := s.now()
	ip, err := netip.ParseAddr(r.URL.Hostname())
	if err != nil {
		return nil, err
	}
	idx := int(ip.As4()[3]) - 1
	if idx < 0 || idx >= len(s.hosts) {
		return nil, errors.New("dial: no route to host")
	}
	if idx == 0 && s.exitDown(t) {
		return nil, context.DeadlineExceeded
	}
	if r.URL.Path != "/status" {
		return respond(r, http.StatusNotFound, `{"schema":1,"error":"not found"}`), nil
	}
	body, err := json.Marshal(s.status(idx, t))
	if err != nil {
		panic(fmt.Sprintf("devmode: encoding a fake status: %v", err))
	}
	return respond(r, http.StatusOK, string(body)), nil
}

func respond(r *http.Request, code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": {"application/json"}},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: r, ContentLength: int64(len(body)), Close: true,
	}
}

// h is the mockup's hash: a repeatable number in [0, 1).
func h(n float64) float64 {
	x := math.Sin(n*12.9898+78.233) * 43758.5453
	return x - math.Floor(x)
}

func within(t time.Time, from time.Time, d time.Duration) bool {
	return !t.Before(from) && t.Before(from.Add(d))
}

// Episodes in the fake history, relative to end, like the mockup's.
func (s *Sim) exitDown(t time.Time) bool {
	return within(t, s.end.Add(-3*24*time.Hour-9*time.Hour), 98*time.Minute) ||
		within(t, s.end.Add(-26*time.Hour), 4*time.Minute) ||
		(s.scenario == Fallback && !t.Before(s.end.Add(-9*time.Minute)))
}

func (s *Sim) relayed(t time.Time, g int) bool {
	switch g {
	case 0:
		return within(t, s.end.Add(-6*24*time.Hour-5*time.Hour), 150*time.Minute) ||
			within(t, s.end.Add(-2*24*time.Hour-30*time.Hour), 42*time.Minute) ||
			(s.scenario == Relayed && !t.Before(s.end.Add(-18*time.Minute)))
	case 1:
		return within(t, s.end.Add(-9*24*time.Hour), 25*time.Minute)
	default:
		return within(t, s.end.Add(-3*24*time.Hour), 40*time.Minute)
	}
}

// streaming is a gateway's TV traffic in Mbit/s: evenings most days, and
// weekend afternoons.
func streaming(t time.Time, g int) float64 {
	day := float64(t.Unix() / 86400)
	min := float64(t.Unix()%86400) / 60
	o := float64(g) * 17.3
	on := false
	if h(day*1.3+o) > 0.2 {
		start := 1140 + math.Floor(h(day*3.1+o)*100)
		on = min >= start && min < start+80+math.Floor(h(day*7.7+o)*130)
	}
	if wd := t.Weekday(); (wd == time.Saturday || wd == time.Sunday) && h(day*5.5+o) > 0.35 {
		start := 780 + math.Floor(h(day*2.2+o)*180)
		on = on || (min >= start && min < start+60+math.Floor(h(day*9.1+o)*90))
	}
	if on {
		return 12 + 8*h(float64(t.Unix()/240)*0.37+o)
	}
	return 0.04 + 0.18*h(float64(t.Unix()/420)*0.91+o)
}

func (s *Sim) status(idx int, t time.Time) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	host := s.hosts[idx]
	down := s.exitDown(t)
	p := s.state[host]
	if p == nil {
		p = &fakePi{last: t, boot: t.Add(-time.Duration(40+idx*7) * time.Hour), tunnel: map[string]float64{}, ifaces: map[string]float64{}}
		s.state[host] = p
	}
	// Pi A rebooted into a new generation 2 days ago.
	if idx == 0 && t.After(s.end.Add(-2*24*time.Hour)) && p.boot.Before(s.end.Add(-2*24*time.Hour)) {
		p.boot, p.busy, p.idle, p.tunnel = s.end.Add(-2*24*time.Hour), 0, 0, map[string]float64{}
	}
	dt := t.Sub(p.last).Seconds()
	if dt < 0 {
		dt = 0
	}
	p.last = t

	amb := 1.4 * math.Sin(float64(t.Unix()%86400)/86400*2*math.Pi-9.0/24*2*math.Pi)
	ts := float64(t.Unix())
	out := map[string]any{
		"schema": 1, "host": host, "time": t.UTC().Format(time.RFC3339),
		"uptime_s": int64(t.Sub(p.boot).Seconds()), "undervoltage": false, "undervoltage_events": nil,
		"mem":  map[string]any{"total_kb": 944060, "available_kb": int(944060 * (0.76 - 0.02*h(ts/600+float64(idx))))},
		"disk": map[string]any{"total_b": 31_000_000_000, "used_b": 3_500_000_000 + idx*90_000_000, "avail_b": 26_000_000_000},
	}
	var cpu, temp, load float64
	var tunnels []any
	if idx == 0 {
		var sum float64
		for g := 0; g < s.gateways; g++ {
			r := 0.0
			if !down {
				r = streaming(t, g)
			}
			sum += r
			peer := s.hosts[g+1]
			p.tunnel[peer] += r * 1e6 / 8 * dt
			path := "direct"
			if s.relayed(t, g) {
				path = "derp"
			}
			tunnels = append(tunnels, map[string]any{
				"peer": peer, "online": true, "path": path, "relay": relay(path),
				"rx_bytes": int64(p.tunnel[peer] * 0.03), "tx_bytes": int64(p.tunnel[peer] * 0.97), "latency_ms": nil,
			})
		}
		load = sum / 20
		cpu = 2 + 21*load + 3*h(ts*0.17)
		temp = 45.5 + amb + 4.5*math.Min(load, 2.5) + 0.8*h(ts*0.71)
		out["role"], out["gateway"] = "exit", nil
		out["public_ip"] = map[string]any{"value": "203.0.113.10", "checked": t.Add(-2 * time.Minute).UTC().Format(time.RFC3339)}
	} else {
		g := idx - 1
		r := 0.0
		if !down {
			r = streaming(t, g)
		}
		load = r / 20
		p.tunnel["pi-a"] += r * 1e6 / 8 * dt
		path, lat := "direct", any(9+2*float64(g)+5*h(ts*0.29+float64(g)))
		switch {
		case down:
			path, lat = "offline", nil
		case s.relayed(t, g):
			path, lat = "derp", 38+14*h(ts*0.13+float64(g))
		}
		tunnels = []any{map[string]any{
			"peer": "pi-a", "online": !down, "path": path, "relay": relay(path),
			"rx_bytes": int64(p.tunnel["pi-a"] * 0.97), "tx_bytes": int64(p.tunnel["pi-a"] * 0.03), "latency_ms": lat,
		}}
		cpu = 2 + 18*load + 3*h(ts*0.23+float64(g))
		temp = 43.2 + float64(g)*1.3 + amb*0.8 + 3.5*load + 0.8*h(ts*0.53+float64(g))
		ip := "203.0.113.10"
		if down {
			ip = fmt.Sprintf("198.51.100.%d", 20+g)
		}
		out["role"] = "gateway"
		out["public_ip"] = map[string]any{"value": ip, "checked": t.Add(-90 * time.Second).UTC().Format(time.RFC3339)}
		out["gateway"] = map[string]any{
			"tv_port": map[string]any{"link": true, "speed_mbps": 100}, "tv_port_gated": true, "fallback": down,
		}
	}
	// Four cores at 100 ticks a second.
	p.busy += cpu / 100 * 400 * dt
	p.idle += (1 - cpu/100) * 400 * dt
	out["cpu"] = map[string]any{
		"load": []float64{round2(load * 1.2), round2(load), round2(load * 0.8)},
		"stat": map[string]any{"user": int64(p.busy * 0.7), "nice": 0, "system": int64(p.busy) - int64(p.busy*0.7),
			"idle": int64(p.idle), "iowait": 0, "irq": 0, "softirq": 0, "steal": 0},
	}
	out["temp_c"] = round2(temp)
	out["tunnel"] = tunnels
	ifs := map[string]any{}
	names := []string{"wan0", "tailscale0"}
	if idx > 0 {
		names = []string{"wan0", "br0", "tv0", "tailscale0"}
	}
	for _, n := range names {
		p.ifaces[n] += (0.5 + load*10) * 1e6 / 8 * dt
		ifs[n] = map[string]any{"rx_bytes": int64(p.ifaces[n]), "tx_bytes": int64(p.ifaces[n] / 9)}
	}
	out["net"] = ifs
	return out
}

func relay(path string) any {
	if path == "derp" {
		return "fra"
	}
	return nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// batch holds writes in memory, to store a backfill in a few transactions.
type batch struct {
	collect.Store
	samples []model.Sample
	events  []model.Event
	states  map[string]model.HostState
}

func (b *batch) WriteCycle(_ context.Context, s []model.Sample, e []model.Event, st []model.HostState) error {
	b.samples = append(b.samples, s...)
	b.events = append(b.events, e...)
	for _, x := range st {
		b.states[x.Host] = x
	}
	return nil
}

func (b *batch) flush(ctx context.Context) error {
	var states []model.HostState
	for _, st := range b.states {
		states = append(states, st)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Host < states[j].Host })
	err := b.Store.WriteCycle(ctx, b.samples, b.events, states)
	b.samples, b.events = nil, nil
	return err
}

// Backfill writes 30 days of fake history ending at s's end: a pair of polls
// 30 seconds apart every 10 minutes, so each has a rate, then every 30
// seconds for the last two hours. It runs the real collector over the fake
// Pis, so the history is what the dashboard would have recorded.
func Backfill(ctx context.Context, s *Sim, store collect.Store, opt collect.Options) error {
	b := &batch{Store: store, states: map[string]model.HostState{}}
	opt.Store, opt.Discoverer, opt.Client, opt.Node = b, s, s.Client(), nil
	c := collect.New(opt)
	defer s.set(time.Time{})

	start := s.end.Add(-30 * 24 * time.Hour).Truncate(model.RollupWidth)
	fine := s.end.Add(-2 * time.Hour).Truncate(model.Interval)
	n := 0
	for t := start; t.Before(fine); t = t.Add(model.RollupWidth) {
		for _, ts := range []time.Time{t, t.Add(model.Interval)} {
			s.set(ts)
			c.Cycle(ctx, ts)
		}
		if n++; n%500 == 0 {
			if err := b.flush(ctx); err != nil {
				return err
			}
		}
	}
	for t := fine; !t.After(s.end); t = t.Add(model.Interval) {
		s.set(t)
		c.Cycle(ctx, t)
	}
	if err := b.flush(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

var _ http.RoundTripper = (*Sim)(nil)
