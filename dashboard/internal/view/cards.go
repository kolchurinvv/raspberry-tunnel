package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// Gauge is one liquid-fill circle. Its value is drawn twice, one copy clipped
// to the liquid and one to the empty part, so it stays readable wherever the
// level crosses it (D2).
type Gauge struct {
	Label, Value, Unit, Inline string
	Big, Hot, NoData           bool
	// Pct is the liquid's height; ClipIn and ClipOut clip the two copies.
	Pct             int
	ClipIn, ClipOut string
	Delay           string
	Aria            string
}

// Stat is one line under the gauges.
type Stat struct {
	Label, Value, Note string
}

// NodeRow is one tagged node on the Headscale card.
type NodeRow struct {
	Host, State, Seen, Key string
	Icon                   Icon
}

// Card is one Pi, or the Headscale server.
type Card struct {
	Name, Role, Pill string
	Colour           int // 0 for the Headscale card's neutral swatch
	PillIcon         Icon
	Dim, Headscale   bool
	Gauges           []Gauge
	GaugeNote        string
	Stats            []Stat
	Nodes            []NodeRow
}

type gauges struct{ n int }

func (g *gauges) make(label, value, unit string, pct float64, hot, big bool) Gauge {
	lvl := int(math.Round(clamp(pct, 4, 100)))
	edge := min(100, lvl+4)
	ga := Gauge{
		Label: label, Value: value, Unit: unit, Hot: hot, Big: big, Pct: lvl,
		ClipIn: fmt.Sprintf("%d", 100-edge), ClipOut: fmt.Sprintf("%d", edge),
		Delay: fmt.Sprintf("%.1f", -float64(g.n)*0.9),
		Aria:  strings.TrimSpace(label + " " + value + " " + unit),
	}
	if hot {
		ga.Aria += ", high"
	}
	g.n++
	return ga
}

func (g *gauges) none(label string, big bool) Gauge {
	ga := g.make(label, dash, "no data", 0, false, big)
	ga.Pct, ga.NoData, ga.ClipIn, ga.ClipOut, ga.Aria = 0, true, "100", "0", label+": no data"
	return ga
}

func (g *gauges) traffic(v *float64) Gauge {
	if v == nil {
		return g.none("Tunnel traffic", true)
	}
	return g.make("Tunnel traffic", f1(v), "Mbit/s", *v/TrafficFullMbps*100, false, true)
}

// isHot judges the whole degree the page shows (fmt rounds half to even), so
// a Pi shown at 70 °C is always amber and always warned about.
func isHot(v float64) bool { return math.RoundToEven(v) >= TempHot }

func (g *gauges) temp(v *float64) Gauge {
	if v == nil {
		return g.none("Temperature", false)
	}
	hot := isHot(*v)
	ga := g.make("Temperature", f0(v), "", (*v-TempLow)/(TempHigh-TempLow)*100, hot, false)
	ga.Inline = "°C"
	ga.Aria = "Temperature " + f0(v) + " °C"
	if hot {
		ga.Aria += ", high"
	}
	return ga
}

func (g *gauges) pct(label string, v *float64, big bool) Gauge {
	if v == nil {
		return g.none(label, big)
	}
	v = clampPct(v)
	return g.make(label, pctText(v), "", *v, *v >= PctHot, big)
}

func (st *state) cards() []Card {
	var out []Card
	g := &gauges{}
	for _, x := range st.all {
		out = append(out, st.piCard(x, g))
	}
	return append(out, st.headscaleCard(g))
}

func (st *state) piCard(x *pi, g *gauges) Card {
	in := st.in
	c := Card{Name: x.Name, Colour: x.Colour, Role: capital(x.Apt)}
	s := x.Show
	c.Dim = s != nil && !x.Current
	var traffic, temp, cpu, mem, disk *float64
	if s != nil {
		temp, cpu, mem, disk = s.TempC, s.CPUPct, s.MemPct, s.DiskPct
		// Old traffic means nothing for a Pi that stopped answering; while the
		// dashboard is cut off, every value is the last it had, dimmed.
		if x.Current || !st.live {
			traffic = s.TrafficMbps
		}
	}
	c.Gauges = []Gauge{g.traffic(traffic), g.temp(temp), g.pct("CPU", cpu, false), g.pct("Memory", mem, false), g.pct("SD card", disk, false)}

	// The pill.
	switch {
	case !st.live:
		c.PillIcon, c.Pill = iconOf(Unknown), st.noDataSince()
	case !x.Current:
		l := Warn
		if x.Role == model.RoleExit {
			l = Crit
		}
		if x.Latest.Outcome == model.OutcomeSchema {
			c.PillIcon, c.Pill = iconOf(Warn), "Unsupported schema"
		} else {
			c.PillIcon, c.Pill = iconOf(l), "No answer since "+st.lastOK(x)
		}
	case x.Role == model.RoleGateway && x.Latest.Route == model.RouteOwn:
		c.PillIcon, c.Pill = iconOf(Warn), x.TV+" on own connection"
	case x.Role == model.RoleGateway && x.Latest.Route == model.RouteHeld:
		c.PillIcon, c.Pill = iconOf(Warn), x.TV+" held"
	case x.Role == model.RoleGateway && x.Latest.Route == model.RouteTVOff:
		c.PillIcon, c.Pill = iconOf(Info), x.TV+" likely off"
	case x.Role == model.RoleGateway && x.Latest.Path.Relayed():
		c.PillIcon, c.Pill = iconOf(Warn), "Relayed"
	default:
		c.PillIcon, c.Pill = iconOf(Good), "Online"
	}

	// Role.
	if x.Role == model.RoleExit {
		c.Stats = append(c.Stats, Stat{"Role", "Exit node",
			"for " + plural(len(st.gateways), "apartment", "apartments") + ", out through " + x.Apt + "'s connection"})
	} else {
		note := dash
		if r := x.Show; r != nil && (x.Current || !st.live) {
			switch r.Route {
			case model.RouteTunnel:
				note = x.TV + " on the tunnel"
				if r.TVLink != nil && *r.TVLink {
					note += ", port up"
				}
				if r.Path.Relayed() {
					note = x.TV + " on the tunnel, relayed"
				}
			case model.RouteOwn:
				note = x.TV + " on its own connection"
			case model.RouteHeld:
				note = x.TV + " held: the tunnel is down"
			case model.RouteTVOff:
				note = x.TV + "'s port is down, likely off"
			case model.RouteNoData:
				note = "route unknown"
			default:
				panic("view: unknown route " + string(r.Route))
			}
		}
		c.Stats = append(c.Stats, Stat{"Role", "Gateway", note})
	}
	// Uptime.
	if s != nil && s.UptimeS != nil {
		up := time.Duration(*s.UptimeS) * time.Second
		note := "since " + clock(s.TS.Add(-up), in.Now, in.Loc)
		if !x.Current {
			note = "as of " + clock(s.TS, in.Now, in.Loc)
		}
		c.Stats = append(c.Stats, Stat{"Uptime", span(up), note})
	} else {
		c.Stats = append(c.Stats, Stat{"Uptime", dash, "no data"})
	}
	// Power.
	_, pv, pn := powerOf(s)
	c.Stats = append(c.Stats, Stat{"Power", pv, pn})
	// Last report.
	switch {
	case !st.live && s == nil:
		c.Stats = append(c.Stats, Stat{"Last report", dash, st.unknownWhy()})
	case !st.live:
		c.Stats = append(c.Stats, Stat{"Last report", ago(s.TS, in.Now), "last data " + clock(s.TS, in.Now, in.Loc)})
	case x.Current:
		c.Stats = append(c.Stats, Stat{"Last report", ago(x.Latest.TS, in.Now), "pulled every 30 s"})
	case x.State.LastOKTS.IsZero():
		c.Stats = append(c.Stats, Stat{"Last report", "Never", failureLabel(x.Latest.Outcome)})
	default:
		c.Stats = append(c.Stats, Stat{"Last report", ago(x.State.LastOKTS, in.Now),
			"last good " + clock(x.State.LastOKTS, in.Now, in.Loc) + ", now " + failureLabel(x.Latest.Outcome)})
	}
	return c
}

func (st *state) headscaleCard(g *gauges) Card {
	in := st.in
	c := Card{Name: "Headscale", Headscale: true, Role: "The Headscale server · coordinates, carries no traffic"}
	var cpu, mem *float64
	// These come from the cluster, not the tailnet, so they stay live while
	// the dashboard is cut off.
	if in.Snap.NodeOK {
		cpu, mem = in.Snap.Node.CPUPct, in.Snap.Node.MemPct
	}
	c.Gauges = []Gauge{g.pct("CPU", cpu, true), g.pct("Memory", mem, true)}
	if cpu != nil || mem != nil {
		c.GaugeNote = "The whole cluster node it runs on."
	} else {
		c.GaugeNote = "No data: the cluster's metrics API isn't available."
	}

	online, total := 0, 0
	var offline, names []string
	for _, x := range st.all {
		if !x.Tailnet.Known {
			continue
		}
		total++
		names = append(names, x.Host)
		row := NodeRow{Host: x.Host}
		if x.Tailnet.Online {
			online++
			row.Icon, row.State, row.Seen = iconOf(Good), "Online", "now"
		} else {
			offline = append(offline, x.Host)
			row.Icon, row.State = iconOf(Warn), "Offline"
			row.Seen = "never"
			if !x.Tailnet.LastSeen.IsZero() {
				row.Seen = clock(x.Tailnet.LastSeen, in.Now, in.Loc)
			}
		}
		row.Key = "doesn't expire"
		if e := x.Tailnet.KeyExpiry; e != nil {
			if !e.After(in.Now) {
				row.Key, row.Icon = "expired "+e.In(in.Loc).Format("2 Jan"), iconOf(Crit)
			} else {
				row.Key = "expires " + e.In(in.Loc).Format("2 Jan 2006")
			}
		}
		c.Nodes = append(c.Nodes, row)
	}

	switch {
	case !st.live || total == 0:
		c.PillIcon, c.Pill = iconOf(Unknown), "Nodes unknown"
		c.Stats = []Stat{{"Pis online", dash, st.unknownWhy()}, {"Node keys", dash, st.unknownWhy()}}
		c.Nodes = nil
		return c
	case online == total:
		c.PillIcon, c.Pill = iconOf(Good), "Running"
	default:
		c.PillIcon, c.Pill = iconOf(Warn), fmt.Sprintf("%d of %d online", online, total)
	}
	note := strings.Join(names, ", ")
	if len(offline) > 0 {
		note = joinAnd(offline) + " offline"
	}
	c.Stats = append(c.Stats, Stat{"Pis online", fmt.Sprintf("%d of %d", online, total), note})
	keys := st.keyCheck()
	value := "OK"
	if keys.Icon.Level != Good {
		value = "Check"
	}
	c.Stats = append(c.Stats, Stat{"Node keys", value, strings.ToLower(keys.Text[:1]) + keys.Text[1:]})
	return c
}

// TopoBox is one box of the traffic-paths diagram.
type TopoBox struct {
	Title, Sub, Status string
	Icon               Icon
	Colour             int
}

// TunnelLink is a gateway's tunnel in the diagram.
type TunnelLink struct {
	Icon       Icon
	Label, Sub string
	Line       string // a CSS class for the line's style
	Colour     int
}

// TopoRow is one gateway apartment: its TV, its Pi and its tunnel.
type TopoRow struct {
	TV, Pi TopoBox
	Tunnel TunnelLink
}

// Topo is the traffic-paths diagram (D2). It grows a row per gateway, and
// the rows converge on the exit.
type Topo struct {
	Caption   string
	Headscale TopoBox
	Rows      []TopoRow
	Multi     bool
	Exit      TopoBox
	ExitLine  string
	Internet  TopoBox
}

func (st *state) topo() Topo {
	t := Topo{Multi: len(st.gateways) > 1}

	online, total := 0, 0
	for _, x := range st.all {
		if x.Tailnet.Known {
			total++
			if x.Tailnet.Online {
				online++
			}
		}
	}
	t.Headscale = TopoBox{Title: "Headscale", Sub: "The Headscale server · coordinates, carries no traffic"}
	switch {
	case !st.live:
		t.Headscale.Icon, t.Headscale.Status = iconOf(Unknown), st.noDataSince()
	case total == 0:
		t.Headscale.Icon, t.Headscale.Status = iconOf(Unknown), "No Pis seen yet"
	case online == total:
		t.Headscale.Icon, t.Headscale.Status = iconOf(Good), fmt.Sprintf("%d of %d Pis online", online, total)
	default:
		t.Headscale.Icon, t.Headscale.Status = iconOf(Warn), fmt.Sprintf("%d of %d Pis online", online, total)
	}

	exitUp := st.live && st.exit != nil && st.exit.Current
	var relayed, down, own, unknown, silent, idle []string
	for _, g := range st.gateways {
		row := TopoRow{
			TV: TopoBox{Title: capital(g.TV), Sub: g.Apt},
			Pi: TopoBox{Title: g.Name, Sub: "gateway, bridge", Colour: g.Colour},
		}
		link := TunnelLink{Colour: g.Colour}
		switch {
		case !st.live:
			row.TV.Icon, row.TV.Status = iconOf(Unknown), "Unknown"
			row.Pi.Icon, row.Pi.Status = iconOf(Unknown), st.noDataSince()
			link.Icon, link.Label, link.Sub, link.Line = iconOf(Unknown), "unknown", st.noDataSince(), "tl-none"
		case !g.Current:
			silent = append(silent, g.Name)
			row.TV.Icon, row.TV.Status = iconOf(Unknown), "Unknown"
			row.Pi.Icon, row.Pi.Status = iconOf(Crit), "No answer since "+st.lastOK(g)
			link.Icon, link.Label, link.Sub, link.Line = iconOf(Unknown), "unknown", dash, "tl-none"
		default:
			s := g.Latest
			switch {
			case s.TVLink == nil:
				row.TV.Icon, row.TV.Status = iconOf(Unknown), "Port unknown"
			case *s.TVLink:
				row.TV.Icon, row.TV.Status = iconOf(Good), "Bridged, link up"
			default:
				row.TV.Icon, row.TV.Status = iconOf(Info), "Port down, likely off"
			}
			if s.Route == model.RouteOwn {
				row.Pi.Icon, row.Pi.Status = iconOf(Warn), g.TV+" → own connection"
				own = append(own, g.Name)
			} else {
				row.Pi.Icon, row.Pi.Status = iconOf(Good), "Gateway · "+degC(s.TempC)
			}
			sub := ms(s.LatencyMS) + " · " + mbit(s.TrafficMbps)
			switch {
			case s.Path.Relayed():
				link.Icon, link.Line = iconOf(Warn), "tl-warn"
				relayed = append(relayed, g.Name)
			case s.Path == model.PathDirect:
				link.Icon, link.Line = iconOf(Good), "tl-up"
			case s.Path == model.PathIdle:
				link.Icon, link.Line = iconOf(Good), "tl-up"
				idle = append(idle, g.Name)
			case s.Path == model.PathUnknown:
				link.Icon, link.Line, sub = iconOf(Unknown), "tl-none", dash
				unknown = append(unknown, g.Name)
			default:
				link.Icon, link.Line, sub = iconOf(Crit), "tl-down", "No traffic"
				down = append(down, g.Name)
			}
			link.Label, link.Sub = pathWord(s.Path), sub
		}
		row.Tunnel = link
		t.Rows = append(t.Rows, row)
	}

	switch {
	case st.exit == nil:
		t.Exit = TopoBox{Title: "No exit node", Sub: "nothing is tagged tag:exit", Icon: iconOf(Crit), Status: "No exit", Colour: 2}
	default:
		x := st.exit
		t.Exit = TopoBox{Title: x.Name, Sub: x.Apt + " · exit node", Colour: x.Colour}
		switch {
		case !st.live:
			t.Exit.Icon, t.Exit.Status = iconOf(Unknown), st.noDataSince()
		case !x.Current:
			t.Exit.Icon, t.Exit.Status = iconOf(Crit), "No answer since "+st.lastOK(x)
		case t.Multi:
			t.Exit.Icon, t.Exit.Status = iconOf(Good), "Exit for "+plural(len(st.gateways), "apartment", "apartments")
		default:
			t.Exit.Icon, t.Exit.Status = iconOf(Good), "Exit node · "+degC(x.Latest.TempC)
		}
	}
	t.ExitLine = "el-up"
	if !exitUp {
		t.ExitLine = "el-down"
	}

	t.Internet = TopoBox{Title: "Internet", Sub: "what websites see"}
	matched, checked := 0, 0
	for _, g := range st.gateways {
		if g.Current && g.Latest.ExitMatch != nil {
			checked++
			if *g.Latest.ExitMatch {
				matched++
			}
		}
	}
	switch {
	case !st.live:
		t.Internet.Icon, t.Internet.Status = iconOf(Unknown), "Unknown"
	case len(own) > 0 && t.Multi:
		t.Internet.Icon, t.Internet.Status = iconOf(Warn), "Sees each apartment's own address"
	case len(own) > 0:
		t.Internet.Icon, t.Internet.Status = iconOf(Warn), "Sees "+st.gateways[0].Apt+"'s address"
	case checked > 0 && matched < checked:
		t.Internet.Icon, t.Internet.Status = iconOf(Warn), "Sees another address"
	case checked > 0:
		t.Internet.Icon, t.Internet.Status = iconOf(Good), "Sees "+st.exitApt()+"'s address"
	default:
		t.Internet.Icon, t.Internet.Status = iconOf(Unknown), "Not checked yet"
	}

	switch {
	case !st.live:
		t.Caption = st.noDataSince()
	case len(st.gateways) == 0:
		t.Caption = "No gateways yet"
	case !exitUp:
		t.Caption = st.exitName() + " not answering"
	case len(down) > 0:
		t.Caption = "Tunnel down: " + joinAnd(down)
	case len(silent) > 0:
		t.Caption = "No answer: " + joinAnd(silent)
	case len(unknown) > 0:
		t.Caption = "Path unknown: " + joinAnd(unknown)
	case len(relayed) > 0:
		t.Caption = "Relayed: " + joinAnd(relayed)
	case len(idle) > 0:
		t.Caption = "All up, " + joinAnd(idle) + " idle"
	default:
		// Every gateway answered with a direct path: nothing else reaches here.
		t.Caption = "All direct"
	}
	return t
}
