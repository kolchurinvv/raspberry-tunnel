package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

var now = time.Date(2026, 11, 13, 21, 40, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func TestParseRange(t *testing.T) {
	cases := map[string]string{
		"1h": "1h", "24h": "24h", "7d": "7d", "30d": "30d",
		"": "24h", "1H": "24h", "2h": "24h", "30d ": "24h", "../": "24h", "<script>": "24h", "1h&x": "24h",
	}
	for in, want := range cases {
		if got := ParseRange(in).Key; got != want {
			t.Errorf("ParseRange(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindow(t *testing.T) {
	for _, r := range Ranges {
		w := WindowAt(r, now.Add(17*time.Second))
		if w.Index(now.Add(17*time.Second)) != r.N-1 {
			t.Errorf("%s: now isn't in the last bucket", r.Key)
		}
		if w.Index(w.Start) != 0 || w.Index(w.Start.Add(-time.Second)) != -1 || w.Index(w.End()) != -1 {
			t.Errorf("%s: edges wrong", r.Key)
		}
		if span := w.End().Sub(w.Start); span != time.Duration(r.N)*r.Width {
			t.Errorf("%s: span %v", r.Key, span)
		}
	}
	// The 30-day view covers 30 days.
	if r := ParseRange("30d"); time.Duration(r.N)*r.Width != 30*24*time.Hour {
		t.Errorf("30d spans %v", time.Duration(r.N)*r.Width)
	}
}

func ok(host string, role model.Role) model.Sample {
	s := model.Sample{
		TS: now, Host: host, Role: role, Outcome: model.OutcomeOK, Online: ptr(true), UptimeS: ptr(int64(3600)),
		TempC: ptr(47.0), CPUPct: ptr(12.0), MemPct: ptr(25.0), DiskPct: ptr(12.0), TrafficMbps: ptr(14.0),
		Undervoltage: ptr(false),
	}
	if role == model.RoleGateway {
		s.Path, s.Fallback, s.TVLink, s.TVGated, s.ExitMatch, s.LatencyMS = model.PathDirect, ptr(false), ptr(true), ptr(true), ptr(true), ptr(11.0)
		s.Route = model.RouteTunnel
	}
	return s
}

func piOf(s model.Sample) collect.Pi {
	p := collect.Pi{Host: s.Host, Role: s.Role, Latest: s, Tailnet: collect.Tailnet{Known: true, Online: true}}
	p.State.LastOKTS = now.Add(-9 * time.Minute)
	if s.OK() {
		x := s
		p.LastOK = &x
		p.State.LastOKTS = s.TS
	}
	return p
}

func input(pis ...collect.Pi) Input {
	return Input{Now: now, Loc: time.UTC, Window: WindowAt(DefaultRange, now),
		Snap: collect.Snapshot{At: now, Tailnet: collect.TailnetOK, Pis: pis}}
}

func healthy() []collect.Pi {
	return []collect.Pi{piOf(ok("pi-a", model.RoleExit)), piOf(ok("pi-b", model.RoleGateway))}
}

func TestBanner(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *Input)
		level  Level
		title  string
	}{
		{"healthy", nil, Good, "All good"},
		{"exit unreachable", func(in *Input) {
			s := in.Snap.Pis[0].Latest
			s.Outcome, s.TempC = model.OutcomeTimeout, nil
			in.Snap.Pis[0].Latest = s
		}, Crit, "Pi A isn't answering"},
		{"no exit", func(in *Input) { in.Snap.Pis = in.Snap.Pis[1:] }, Crit, "No exit node"},
		{"fallback", func(in *Input) {
			in.Snap.Pis[1].Latest.Fallback, in.Snap.Pis[1].Latest.Route = ptr(true), model.RouteOwn
		}, Crit, "TV B is on apartment B's own connection"},
		{"relayed", func(in *Input) { in.Snap.Pis[1].Latest.Path = model.PathDERP }, Warn, "Pi B's tunnel is relayed"},
		{"peer relay", func(in *Input) { in.Snap.Pis[1].Latest.Path = model.PathPeerRelay }, Warn, "Pi B's tunnel is relayed"},
		{"exit mismatch", func(in *Input) { in.Snap.Pis[1].Latest.ExitMatch = ptr(false) }, Warn, "Pi B's exit address doesn't match"},
		{"exit check unknown is not a mismatch", func(in *Input) { in.Snap.Pis[1].Latest.ExitMatch = nil }, Good, "All good"},
		{"hot", func(in *Input) { in.Snap.Pis[0].Latest.TempC = ptr(70.0) }, Warn, "Pi A is running hot"},
		// The page shows whole degrees, so what it shows as 70 °C warns.
		{"shown as 70", func(in *Input) { in.Snap.Pis[0].Latest.TempC = ptr(69.5) }, Warn, "Pi A is running hot"},
		{"just under hot", func(in *Input) { in.Snap.Pis[0].Latest.TempC = ptr(69.4) }, Good, "All good"},
		// A Pi in an enclosure idles at about 61 °C: past its soft limit, no warning (D4).
		{"past the soft limit", func(in *Input) { in.Snap.Pis[0].Latest.TempC = ptr(61.0) }, Good, "All good"},
		{"undervoltage now", func(in *Input) { in.Snap.Pis[1].Latest.Undervoltage = ptr(true) }, Warn, "Pi B is short of power"},
		{"undervoltage since boot", func(in *Input) { in.Snap.Pis[1].Latest.UndervoltageEvents = ptr(int64(2)) }, Warn, "Pi B is short of power"},
		{"gateway not answering", func(in *Input) {
			in.Snap.Pis[1].Latest = model.Sample{TS: now, Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeTimeout, Route: model.RouteNoData}
		}, Warn, "Pi B isn't answering"},
		// A gateway that answers with its own tunnel down is critical, even
		// while the dashboard reaches the exit (the user's ruling).
		{"gateway tunnel offline", func(in *Input) {
			in.Snap.Pis[1].Latest.Path, in.Snap.Pis[1].Latest.Route = model.PathOffline, model.RouteHeld
		}, Crit, "Pi B's tunnel is down"},
		{"gateway tunnel list empty", func(in *Input) {
			in.Snap.Pis[1].Latest.Path, in.Snap.Pis[1].Latest.Route = model.PathNone, model.RouteHeld
		}, Crit, "Pi B has no exit node set"},
		{"gateway tunnel list null", func(in *Input) {
			in.Snap.Pis[1].Latest.Path, in.Snap.Pis[1].Latest.Route = model.PathNoTailscale, model.RouteHeld
		}, Crit, "Tailscale isn't running on Pi B"},
		{"tunnel down with fallback set says fallback", func(in *Input) {
			in.Snap.Pis[1].Latest.Path, in.Snap.Pis[1].Latest.Route, in.Snap.Pis[1].Latest.Fallback = model.PathOffline, model.RouteOwn, ptr(true)
		}, Crit, "TV B is on apartment B's own connection"},
		// The TV's port down is usually the TV off: no alarm.
		{"port down", func(in *Input) {
			in.Snap.Pis[1].Latest.TVLink, in.Snap.Pis[1].Latest.Route = ptr(false), model.RouteTVOff
		}, Good, "All good"},
		{"unknown path", func(in *Input) {
			in.Snap.Pis[1].Latest.Path, in.Snap.Pis[1].Latest.Route = model.PathUnknown, model.RouteNoData
		}, Warn, "Pi B's tunnel path is unknown"},
		{"fallback unknown", func(in *Input) {
			in.Snap.Pis[1].Latest.Fallback, in.Snap.Pis[1].Latest.Route = nil, model.RouteNoData
		}, Warn, "Pi B's fallback rule is unknown"},
		{"critical beats warning", func(in *Input) {
			in.Snap.Pis[1].Latest.Path = model.PathDERP
			in.Snap.Pis[0].Latest.Outcome = model.OutcomeOffline
		}, Crit, "Pi A isn't answering"},
		// The dashboard's own trouble is never the tunnel's: never critical,
		// even with the exit's last poll failed.
		{"cut off", func(in *Input) {
			in.Snap.Tailnet = collect.TailnetDown
			in.Snap.At = now.Add(-9 * time.Minute)
			in.Snap.Pis[0].Latest.Outcome = model.OutcomeTimeout
		}, Unknown, "The dashboard can't see the tailnet"},
		{"starting", func(in *Input) { in.Snap.Tailnet = collect.TailnetStarting }, Unknown, "Starting up"},
		{"untagged", func(in *Input) { in.Snap.Tailnet = collect.TailnetNotTagged }, Unknown, "The dashboard isn't set up"},
		{"stale", func(in *Input) { in.Snap.At = now.Add(-5 * time.Minute) }, Unknown, "No current data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := input(healthy()...)
			if c.mutate != nil {
				c.mutate(&in)
			}
			b := Build(in).Banner
			if b.Icon.Level != c.level || b.Title != c.title {
				t.Errorf("banner = %s %q, want %s %q (%s)", b.Icon.Level, b.Title, c.level, c.title, b.Text)
			}
		})
	}
}

// The hot banner gives the soft limit and the hard limit, not the warning's
// own threshold (D4).
func TestHotBannerText(t *testing.T) {
	in := input(healthy()...)
	in.Snap.Pis[0].Latest.TempC = ptr(72.0)
	b := Build(in).Banner
	for _, w := range []string{"Pi A is at 72 °C.", "lowers its clock from 60 °C", "nears 85 °C"} {
		if !strings.Contains(b.Text, w) {
			t.Errorf("banner text %q lacks %q", b.Text, w)
		}
	}
}

func TestCutOffPage(t *testing.T) {
	in := input(healthy()...)
	in.Snap.Tailnet = collect.TailnetDown
	in.Snap.At = now.Add(-9 * time.Minute)
	in.Snap.NodeOK, in.Snap.Node = true, collect.NodeMetrics{CPUPct: ptr(7.0), MemPct: ptr(76.0)}
	p := Build(in)
	if !strings.Contains(p.Banner.Text, "Since 21:31") || !strings.Contains(p.Banner.Text, "not with the tunnel") {
		t.Errorf("banner text = %q", p.Banner.Text)
	}
	for _, c := range p.Checks {
		if c.Icon.Level != Unknown || c.Text != "Unknown while the dashboard is cut off" {
			t.Errorf("check %s = %s %q", c.Label, c.Icon.Level, c.Text)
		}
	}
	for _, c := range p.Cards {
		if c.Headscale {
			if c.Gauges[0].NoData || c.Gauges[0].Value != "7%" || c.Pill != "Nodes unknown" {
				t.Errorf("headscale card = %+v", c)
			}
			continue
		}
		if !c.Dim || c.Pill != "No data since 21:31" || c.Gauges[1].NoData {
			t.Errorf("%s card: dim %v pill %q temp %+v", c.Name, c.Dim, c.Pill, c.Gauges[1])
		}
	}
	if p.Topo.Caption != "No data since 21:31" || p.Topo.Rows[0].Tunnel.Line != "tl-none" || p.Topo.Rows[0].Pi.Status != "No data since 21:31" {
		t.Errorf("topo = %+v", p.Topo)
	}
}

func TestEvents(t *testing.T) {
	in := input(healthy()...)
	in.Events = []model.Event{
		{TS: now.Add(-time.Minute), Host: "pi-b", Kind: model.EventPathChange, Detail: "direct>derp"},
		{TS: now.Add(-2 * time.Minute), Host: "pi-b", Kind: model.EventFallbackSet},
		{TS: now.Add(-3 * time.Minute), Host: "pi-a", Kind: model.EventUnreachable, Detail: "timeout"},
		{TS: now.Add(-4 * time.Minute), Host: model.DashboardHost, Kind: model.EventTailnetLost},
		{TS: now.Add(-5 * time.Minute), Host: "pi-z", Kind: model.EventReboot}, // no longer known
		{TS: now.Add(-6 * time.Minute), Host: "pi-b", Kind: model.EventPathChange, Detail: "<b>>evil"},
		{TS: now.Add(-50 * time.Hour), Host: "pi-a", Kind: model.EventReboot},
	}
	got := Build(in).Events
	want := []string{
		"21:39|Pi B|Path to Pi A changed from direct to relayed (DERP)|warn",
		"21:38|Pi B|TV B moved to apartment B's own connection|warn",
		"21:37|Pi A|Stopped answering (timed out)|crit",
		"21:36|Dashboard|Lost the tailnet. Showing the last data it had|unknown",
		"21:34|Pi B|Path to Pi A changed|info",
		"Wed 19:40|Pi A|Rebooted|info",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}
	for i, e := range got {
		if s := e.Time + "|" + e.Node + "|" + e.Text + "|" + string(e.Icon.Level); s != want[i] {
			t.Errorf("event %d = %q, want %q", i, s, want[i])
		}
	}
}

func TestRouteStrip(t *testing.T) {
	w := WindowAt(Ranges[0], now)
	h := FromSamples(w, []model.Sample{
		{TS: w.Start, Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Route: model.RouteTunnel},
		{TS: w.Start.Add(30 * time.Second), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Route: model.RouteOwn},
		{TS: w.Start.Add(60 * time.Second), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Route: model.RouteHeld},
		{TS: w.Start.Add(90 * time.Second), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeTimeout, Route: model.RouteNoData},
		{TS: w.Start.Add(120 * time.Second), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Route: model.RouteTunnel},
		{TS: w.Start.Add(150 * time.Second), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Route: model.RouteTVOff},
	})
	in := input(healthy()...)
	in.Window, in.Hist = w, h
	p := Build(in)
	segs := p.Route.Rows[0].Segs
	var got []string
	for _, s := range segs {
		got = append(got, s.Class)
	}
	want := "rs-tunnel rs-own rs-held rs-nodata rs-tunnel rs-tvoff rs-nodata"
	if strings.Join(got, " ") != want {
		t.Errorf("segments = %q, want %q", got, want)
	}
	total := 0
	for _, s := range segs {
		total += s.Grow
	}
	if total != w.N {
		t.Errorf("segments cover %d buckets, want %d", total, w.N)
	}
	// 2 of 4 samples with the TV on were on the tunnel; the TV being off
	// counts neither way.
	if p.Hero.Value != "50.0%" {
		t.Errorf("hero = %+v", p.Hero)
	}
}

func TestGauges(t *testing.T) {
	g := &gauges{}
	if x := g.temp(ptr(20.0)); x.Pct != 4 || x.Hot {
		t.Errorf("20 °C = %+v", x)
	}
	if x := g.temp(ptr(85.0)); x.Pct != 100 || !x.Hot {
		t.Errorf("85 °C = %+v", x)
	}
	if x := g.temp(ptr(70.0)); !x.Hot {
		t.Errorf("70 °C isn't amber")
	}
	if x := g.temp(ptr(69.5)); x.Value != "70" || !x.Hot {
		t.Errorf("69.5 °C = %+v, want shown as 70 and amber", x)
	}
	if x := g.temp(ptr(69.4)); x.Hot {
		t.Errorf("69.4 °C is amber")
	}
	if x := g.pct("CPU", ptr(84.9), false); x.Hot {
		t.Errorf("84.9%% is amber")
	}
	if x := g.pct("CPU", ptr(85.0), false); !x.Hot {
		t.Errorf("85%% isn't amber")
	}
	if x := g.traffic(ptr(25.0)); x.Pct != 50 {
		t.Errorf("25 Mbit/s = %d%%, want 50", x.Pct)
	}
	if x := g.traffic(ptr(80.0)); x.Pct != 100 {
		t.Errorf("80 Mbit/s = %d%%, want 100", x.Pct)
	}
	if x := g.pct("CPU", ptr(130.0), false); x.Value != "100%" || x.Pct != 100 {
		t.Errorf("130%% shows as %+v, want 100%%", x)
	}
	if x := g.pct("CPU", ptr(-5.0), false); x.Value != "0%" {
		t.Errorf("-5%% shows as %q, want 0%%", x.Value)
	}
	if x := g.pct("Memory", nil, false); !x.NoData || x.Value != "—" || x.Pct != 0 {
		t.Errorf("no data = %+v", x)
	}
}

func TestPower(t *testing.T) {
	cases := []struct {
		uv     *bool
		events *int64
		level  Level
		value  string
	}{
		{ptr(false), nil, Good, "Steady"},
		{ptr(false), ptr(int64(0)), Good, "Steady"},
		{ptr(true), nil, Warn, "Low"},
		{ptr(false), ptr(int64(3)), Warn, "Was low"},
		{nil, nil, Unknown, "Unknown"},
	}
	for _, c := range cases {
		l, v, _ := powerOf(&model.Sample{Undervoltage: c.uv, UndervoltageEvents: c.events})
		if l != c.level || v != c.value {
			t.Errorf("power(%v, %v) = %s %q, want %s %q", c.uv, c.events, l, v, c.level, c.value)
		}
	}
}

func TestNames(t *testing.T) {
	if n := NamesOf("pi_c"); n.Name != "Pi C" || n.TV != "TV C" || n.Apt != "apartment C" {
		t.Errorf("NamesOf(pi_c) = %+v", n)
	}
	if n := NamesOf("raspberrypi"); n.Name != "raspberrypi" {
		t.Errorf("NamesOf(raspberrypi) = %+v", n)
	}
}

func TestChartsShape(t *testing.T) {
	in := input(healthy()...)
	p := Build(in)
	if len(p.Charts) != 4 {
		t.Fatalf("charts = %d", len(p.Charts))
	}
	temp := p.Charts[2]
	if temp.Ref == "" || !strings.HasPrefix(temp.RefLabel, "60 °C,") {
		t.Errorf("temperature chart has no 60 °C line")
	}
	// The fixture's 47 °C gives the chart a 30–70 °C scale.
	if want := fmt.Sprintf("%.2f", yOf(TempClock, 30, 70)); temp.RefTop != want {
		t.Errorf("the 60 °C line is drawn at y %s, want %s", temp.RefTop, want)
	}
	if p.Hover.N != in.Window.N || len(p.Hover.Times) != in.Window.N || len(p.Hover.Charts) != 4 || len(p.Hover.Route) != 1 {
		t.Errorf("hover data = n %d, times %d, charts %d, route %d", p.Hover.N, len(p.Hover.Times), len(p.Hover.Charts), len(p.Hover.Route))
	}
	if top, step := niceTop(12); top != 15 || step != 5 {
		t.Errorf("niceTop(12) = %v, %v", top, step)
	}
	if top, step := niceTop(230); top != 300 || step != 100 {
		t.Errorf("niceTop(230) = %v, %v", top, step)
	}
}

// A TV port that's down is usually the TV off: no alarm, but a note.
func TestPortDownIsNoAlarm(t *testing.T) {
	pis := healthy()
	pis[1].Latest.TVLink, pis[1].Latest.Route = ptr(false), model.RouteTVOff
	p := Build(input(pis...))
	if p.Banner.Icon.Level != Good {
		t.Errorf("banner = %s %q", p.Banner.Icon.Level, p.Banner.Title)
	}
	card := p.Cards[1]
	if card.Pill != "TV B likely off" || card.PillIcon.Level != Info || card.Stats[0].Note != "TV B's port is down, likely off" {
		t.Errorf("card pill %q, role note %q", card.Pill, card.Stats[0].Note)
	}
	if tv := p.Topo.Rows[0].TV; tv.Status != "Port down, likely off" || tv.Icon.Level != Info {
		t.Errorf("TV box = %+v", tv)
	}
}

// "All good" says only what the data says: never "direct" for an idle
// tunnel, never "steady power" or "normal temperatures" when unknown, and
// never the exit's address unless the check confirmed it.
func TestAllGoodText(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(pis []collect.Pi)
		want    []string
		notWant []string
		caption string
	}{
		{"healthy", nil,
			[]string{"TV B leaves through apartment A's connection over a direct tunnel, 11 ms between the Pis.", "Temperatures are normal and power is steady."},
			[]string{"unknown"}, "All direct"},
		{"idle", func(p []collect.Pi) { p[1].Latest.Path = model.PathIdle },
			[]string{"an idle tunnel"}, []string{"direct"}, "All up, Pi B idle"},
		{"no latency", func(p []collect.Pi) { p[1].Latest.LatencyMS = nil },
			[]string{"over a direct tunnel."}, []string{" ms"}, "All direct"},
		{"power unknown", func(p []collect.Pi) { p[0].Latest.Undervoltage = nil },
			[]string{"power is unknown on Pi A"}, []string{"steady"}, "All direct"},
		{"temperature unknown", func(p []collect.Pi) { p[1].Latest.TempC = nil },
			[]string{"Temperature is unknown on Pi B and power is steady."}, []string{"normal"}, "All direct"},
		{"exit check unconfirmed", func(p []collect.Pi) { p[1].Latest.ExitMatch = nil },
			[]string{"Pi B reaches Pi A over a direct tunnel", "isn't confirmed yet for Pi B"},
			[]string{"leaves through apartment A"}, "All direct"},
		{"TV off", func(p []collect.Pi) { p[1].Latest.TVLink, p[1].Latest.Route = ptr(false), model.RouteTVOff },
			[]string{"Pi B reaches Pi A", "TV B is likely off"}, []string{"TV B leaves through"}, "All direct"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pis := healthy()
			if c.mutate != nil {
				c.mutate(pis)
			}
			p := Build(input(pis...))
			if p.Banner.Title != "All good" {
				t.Fatalf("banner = %q: %s", p.Banner.Title, p.Banner.Text)
			}
			for _, w := range c.want {
				if !strings.Contains(p.Banner.Text, w) {
					t.Errorf("text lacks %q: %s", w, p.Banner.Text)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(p.Banner.Text, w) {
					t.Errorf("text has %q: %s", w, p.Banner.Text)
				}
			}
			if p.Topo.Caption != c.caption {
				t.Errorf("caption = %q, want %q", p.Topo.Caption, c.caption)
			}
		})
	}

	// Two gateways name each one's path.
	pis := append(healthy(), piOf(ok("pi-c", model.RoleGateway)))
	pis[2].Latest.Path = model.PathIdle
	if txt := Build(input(pis...)).Banner.Text; !strings.Contains(txt, "All 2 gateways reach Pi A: Pi B direct (11 ms) and Pi C idle (11 ms).") {
		t.Errorf("two gateways: %s", txt)
	}
}

// Every way a gateway's tunnel can be known down, unknown or unanswered has a
// caption of its own, and none reads "All direct".
func TestCaptions(t *testing.T) {
	for path, want := range map[model.Path]string{
		model.PathOffline: "Tunnel down: Pi B", model.PathNone: "Tunnel down: Pi B", model.PathNoTailscale: "Tunnel down: Pi B",
		model.PathUnknown: "Path unknown: Pi B", model.PathDERP: "Relayed: Pi B",
	} {
		pis := healthy()
		pis[1].Latest.Path = path
		if got := Build(input(pis...)).Topo.Caption; got != want {
			t.Errorf("%q: caption %q, want %q", path, got, want)
		}
	}
	pis := healthy()
	pis[1].Latest = model.Sample{TS: now, Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeTimeout, Route: model.RouteNoData}
	if got := Build(input(pis...)).Topo.Caption; got != "No answer: Pi B" {
		t.Errorf("no answer: caption %q", got)
	}
}
