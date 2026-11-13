package model

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// gw is a healthy gateway answer at poll n.
func gw(n int) Sample {
	return Sample{
		TS: t0.Add(time.Duration(n) * Interval), Host: "pi-b", Role: RoleGateway, Outcome: OutcomeOK,
		UptimeS: ptr(int64(1000 + 30*n)), Undervoltage: ptr(false), UndervoltageEvents: ptr(int64(0)),
		Path: PathDirect, Fallback: ptr(false), TVLink: ptr(true), Route: RouteTunnel,
	}
}

func failed(n int, o Outcome) Sample {
	return Sample{TS: t0.Add(time.Duration(n) * Interval), Host: "pi-b", Role: RoleGateway, Outcome: o, Route: RouteNoData}
}

// run feeds samples through Derive and returns every event's kind and detail.
func run(samples ...Sample) []string {
	var st HostState
	var out []string
	for _, s := range samples {
		var evs []Event
		st, evs = Derive(st, s)
		for _, e := range evs {
			if !e.TS.Equal(s.TS) || e.Host != s.Host {
				panic("event not stamped with its sample")
			}
			out = append(out, string(e.Kind)+":"+e.Detail)
		}
	}
	return out
}

func TestDeriveEvents(t *testing.T) {
	with := func(s Sample, f func(*Sample)) Sample { f(&s); return s }
	cases := []struct {
		name    string
		samples []Sample
		want    []string
	}{
		{"first sample gives nothing", []Sample{gw(0)}, nil},
		{"first sample failing gives nothing", []Sample{failed(0, OutcomeTimeout)}, nil},
		{"steady gives nothing", []Sample{gw(0), gw(1), gw(2)}, nil},
		{"unreachable then back", []Sample{gw(0), failed(1, OutcomeTimeout), failed(2, OutcomeOffline), gw(3)},
			[]string{"unreachable:timeout", "reachable:"}},
		{"reboot: uptime went down", []Sample{gw(0), with(gw(1), func(s *Sample) { s.UptimeS = ptr(int64(40)) })},
			[]string{"reboot:"}},
		{"reboot during an outage longer than the old uptime",
			[]Sample{gw(0), failed(1, OutcomeTimeout), with(gw(200), func(s *Sample) { s.UptimeS = ptr(int64(3000)) })},
			[]string{"unreachable:timeout", "reachable:", "reboot:"}},
		{"no reboot when uptime keeps pace over a gap",
			[]Sample{gw(0), failed(1, OutcomeTimeout), gw(200)},
			[]string{"unreachable:timeout", "reachable:"}},
		{"undervoltage now", []Sample{gw(0), with(gw(1), func(s *Sample) { s.Undervoltage = ptr(true) })},
			[]string{"undervoltage:"}},
		{"undervoltage count went up", []Sample{gw(0), with(gw(1), func(s *Sample) { s.UndervoltageEvents = ptr(int64(2)) })},
			[]string{"undervoltage:"}},
		{"undervoltage after a reboot with a lower count",
			[]Sample{with(gw(0), func(s *Sample) { s.UndervoltageEvents = ptr(int64(5)) }),
				with(gw(1), func(s *Sample) { s.UptimeS = ptr(int64(30)); s.UndervoltageEvents = ptr(int64(1)) })},
			[]string{"reboot:", "undervoltage:"}},
		{"undervoltage continuing gives one event",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Undervoltage = ptr(true) }), with(gw(2), func(s *Sample) { s.Undervoltage = ptr(true) })},
			[]string{"undervoltage:"}},
		{"fallback set then cleared",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Fallback = ptr(true); s.Path = PathOffline; s.Route = RouteOwn }), gw(2)},
			[]string{"fallback_set:", "tunnel_down:offline", "fallback_cleared:", "tunnel_up:direct"}},
		{"path relayed then direct",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathDERP }), gw(2)},
			[]string{"path:direct>derp", "path:derp>direct"}},
		{"idle hides nothing",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathIdle }), with(gw(2), func(s *Sample) { s.Path = PathPeerRelay })},
			[]string{"path:direct>peer_relay"}},
		{"idle after direct is no event",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathIdle }), gw(2)}, nil},
		{"no exit node set is down",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathNone; s.Route = RouteHeld })},
			[]string{"tunnel_down:none"}},
		{"tailscaled unreachable is down",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathNoTailscale; s.Route = RouteHeld })},
			[]string{"tunnel_down:no_tailscale"}},
		{"unknown path changes nothing",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Path = PathUnknown; s.Route = RouteNoData }), gw(2)}, nil},
		{"null fallback isn't a change",
			[]Sample{gw(0), with(gw(1), func(s *Sample) { s.Fallback = nil; s.Route = RouteNoData }), gw(2)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := run(c.samples...)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("events = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDeriveExitHasNoGatewayEvents(t *testing.T) {
	a := gw(0)
	a.Role, a.Route, a.Path, a.Fallback = RoleExit, RouteNone, PathUnknown, nil
	b := a
	b.TS = a.TS.Add(Interval)
	b.Fallback = ptr(true) // ignored on the exit
	if got := run(a, b); got != nil {
		t.Errorf("events = %q", got)
	}
}

func TestDeriveKeepsLastOKState(t *testing.T) {
	st, _ := Derive(HostState{}, gw(0))
	st, _ = Derive(st, failed(1, OutcomeTimeout))
	if !st.LastOKTS.Equal(gw(0).TS) || st.LastOutcome != OutcomeTimeout || !st.LastTS.Equal(failed(1, "").TS) {
		t.Errorf("state = %+v", st)
	}
	if st.UptimeS == nil || *st.UptimeS != 1000 {
		t.Errorf("uptime lost across a failed poll: %v", st.UptimeS)
	}
}

func TestRouteOf(t *testing.T) {
	cases := []struct {
		ok       bool
		fallback *bool
		path     Path
		link     *bool
		want     Route
	}{
		{false, ptr(false), PathDirect, ptr(true), RouteNoData},
		{true, nil, PathDirect, ptr(true), RouteNoData},
		{true, ptr(true), PathOffline, ptr(true), RouteOwn},
		{true, ptr(true), PathDirect, ptr(false), RouteOwn},
		// A port down with the tunnel up is the TV off, not held.
		{true, ptr(false), PathDirect, ptr(false), RouteTVOff},
		{true, ptr(false), PathOffline, ptr(false), RouteHeld},
		{true, ptr(false), PathOffline, ptr(true), RouteHeld},
		{true, ptr(false), PathNone, ptr(true), RouteHeld},
		{true, ptr(false), PathNoTailscale, ptr(true), RouteHeld},
		{true, ptr(false), PathUnknown, ptr(true), RouteNoData},
		{true, ptr(false), PathDirect, ptr(true), RouteTunnel},
		{true, ptr(false), PathDERP, nil, RouteTunnel},
		{true, ptr(false), PathIdle, ptr(true), RouteTunnel},
	}
	for _, c := range cases {
		if got := RouteOf(c.ok, c.fallback, c.path, c.link); got != c.want {
			t.Errorf("RouteOf(%v, %v, %q, %v) = %q, want %q", c.ok, c.fallback, c.path, c.link, got, c.want)
		}
	}
}

func TestSampleValidate(t *testing.T) {
	good := gw(0)
	if err := good.Validate(); err != nil {
		t.Fatalf("good sample: %v", err)
	}
	bad := []func(*Sample){
		func(s *Sample) { s.TS = time.Time{} },
		func(s *Sample) { s.Host = "" },
		func(s *Sample) { s.Role = "router" },
		func(s *Sample) { s.Outcome = "maybe" },
		func(s *Sample) { s.Path = "sideways" },
		func(s *Sample) { s.Route = RouteNone },
		func(s *Sample) { s.Role = RoleExit },
		func(s *Sample) { s.Outcome = OutcomeTimeout },
	}
	for i, f := range bad {
		s := good
		f(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("case %d: invalid sample passed: %+v", i, s)
		}
	}
}
