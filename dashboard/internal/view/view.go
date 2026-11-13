// Package view turns the latest snapshot and the stored history into what the
// page shows: the banner, the traffic paths, one card per Pi, the Headscale
// card, the exit-route strip, the charts, events and checks (D2). Charts and
// gauges are computed here, in Go, as the mockup computes them in JavaScript
// (D3).
//
// What reaches the page is built only from host names of tagged Pis,
// validated numbers, and fixed wording chosen by enum values. No error text,
// no address, and nothing a Pi sent as free text.
package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// GitHub is the repository the page links to (D2).
const GitHub = "https://github.com/kolchurinvv/raspberry-tunnel"

// The gauges' scales (D2).
const (
	TrafficFullMbps = 50.0 // stands in until the tunnel's real throughput is measured
	TempLow         = 20.0
	TempHigh        = 85.0
	TempHot         = 60.0 // where the Pi lowers its clock
	PctHot          = 85.0 // CPU, memory and SD card
	// KeyWarnBefore is how soon a key expiry becomes a check failure.
	KeyWarnBefore = 30 * 24 * time.Hour
	// StaleAfter is how old the latest poll may be before the page stops
	// calling it current.
	StaleAfter = 2 * time.Minute
)

// Level is how good something is, and picks its colour and icon.
type Level string

const (
	Good Level = "good"
	Warn Level = "warn"
	Crit Level = "crit"
	Info Level = "info"
	// Unknown is for what the dashboard can't see: grey, with a question mark.
	Unknown Level = "unknown"
)

func (l Level) rank() int {
	switch l {
	case Info, Unknown:
		return 0
	case Good:
		return 1
	case Warn:
		return 2
	case Crit:
		return 3
	default:
		panic("view: unknown level " + string(l))
	}
}

// Icon is a level's icon: an SVG path, coloured by the level's CSS class.
type Icon struct {
	Level Level
	D     string
}

func iconOf(l Level) Icon {
	const ring = "M3 12a9 9 0 1 0 18 0a9 9 0 1 0 -18 0"
	switch l {
	case Good:
		return Icon{l, ring + " M8 12.5l2.5 2.5L16 9.5"}
	case Warn:
		return Icon{l, "M12 3.5 2.5 20h19L12 3.5z M12 10v4.5 M12 17.5v.01"}
	case Crit:
		return Icon{l, ring + " M9 9l6 6M15 9l-6 6"}
	case Info:
		return Icon{l, "M9 12a3 3 0 1 0 6 0a3 3 0 1 0 -6 0"}
	case Unknown:
		return Icon{l, ring + " M9.6 9.4a2.5 2.5 0 1 1 3.4 2.4c-.6.3-1 .8-1 1.5v.5 M12 16.8v.01"}
	default:
		panic("view: unknown level " + string(l))
	}
}

// Input is everything one render needs.
type Input struct {
	Now    time.Time
	Loc    *time.Location
	Window Window
	Snap   collect.Snapshot
	Hist   History
	// HistErr is set when the history couldn't be read.
	HistErr bool
	Events  []model.Event
	// Dev marks the page as showing sample data.
	Dev bool
}

// Page is the view model the templates render.
type Page struct {
	Range    string
	Ranges   []RangeLink
	Dev      bool
	Updated  string
	Banner   Banner
	Hero     Hero
	Topo     Topo
	Cards    []Card
	Route    RouteStrip
	Charts   []Chart
	XTicks   []Tick
	Events   []EventRow
	NoEvents string
	Checks   []CheckRow
	Hover    Hover
	Footnote string
	GitHub   string
}

// RangeLink is one button of the range switch: a plain link, so it works
// without JavaScript.
type RangeLink struct {
	Key, Label, Href string
	Current          bool
}

// Banner is the status banner.
type Banner struct {
	Icon        Icon
	Title, Text string
}

// Hero is the share of the range the TVs spent on the tunnel.
type Hero struct {
	Value, Label string
}

// pi is one Pi with everything the builders need, worked out once.
type pi struct {
	collect.Pi
	Names
	// Current is whether this Pi's latest poll answered, and the dashboard's
	// own view is sound.
	Current bool
	// Show is the sample to show values from: the latest answer, or the last
	// good one, dimmed, while the Pi doesn't answer or the dashboard is cut
	// off. Nil when there is none.
	Show *model.Sample
	// Colour is the series colour class number (1 to 4).
	Colour int
}

type state struct {
	in       Input
	live     bool // the dashboard's own view is sound and current
	exit     *pi
	gateways []*pi
	all      []*pi
}

// gatewayColours are the series colours for gateways in order; Pi A is 2.
var gatewayColours = []int{1, 3, 4}

func newState(in Input) *state {
	st := &state{in: in}
	st.live = in.Snap.Tailnet == collect.TailnetOK && !in.Snap.At.IsZero() && in.Now.Sub(in.Snap.At) <= StaleAfter
	gi := 0
	for _, p := range in.Snap.Pis {
		x := &pi{Pi: p, Names: NamesOf(p.Host)}
		x.Current = st.live && p.Latest.OK()
		if p.Latest.OK() {
			s := p.Latest
			x.Show = &s
		} else if p.LastOK != nil {
			s := *p.LastOK
			x.Show = &s
		}
		switch p.Role {
		case model.RoleExit:
			x.Colour = 2
			st.exit = x
		case model.RoleGateway:
			x.Colour = gatewayColours[gi%len(gatewayColours)]
			gi++
			st.gateways = append(st.gateways, x)
		default:
			panic("view: unknown role " + string(p.Role))
		}
	}
	if st.exit != nil {
		st.all = append(st.all, st.exit)
	}
	st.all = append(st.all, st.gateways...)
	return st
}

func (st *state) exitName() string {
	if st.exit != nil {
		return st.exit.Name
	}
	return "the exit node"
}

func (st *state) exitApt() string {
	if st.exit != nil {
		return st.exit.Apt
	}
	return "the exit's apartment"
}

// Build makes the page.
func Build(in Input) Page {
	if in.Loc == nil {
		panic("view.Build: no location")
	}
	st := newState(in)
	p := Page{Range: in.Window.Key, Dev: in.Dev, GitHub: GitHub}
	for _, r := range Ranges {
		p.Ranges = append(p.Ranges, RangeLink{Key: r.Key, Label: r.Label, Href: "/?range=" + r.Key, Current: r.Key == in.Window.Key})
	}
	if in.Snap.At.IsZero() {
		p.Updated = "Not updated yet"
	} else {
		p.Updated = "Updated " + in.Snap.At.In(in.Loc).Format("15:04:05")
	}
	p.Banner = st.banner()
	p.Hero = st.hero()
	p.Topo = st.topo()
	p.Cards = st.cards()
	p.Route = st.route()
	p.Charts, p.Hover = st.charts()
	p.XTicks = xTicks(in.Window, in.Loc)
	p.Events, p.NoEvents = st.events()
	p.Checks = st.checks()
	p.Footnote = "Pulled from each Pi over the tailnet every 30 s. Kept for 30 days."
	if in.HistErr {
		p.Footnote += " The history can't be read right now, so the charts are empty."
	}
	return p
}

// powerOf is D2's power state: fine with no undervoltage now and none counted
// since boot. The Pis' kernels can't count since boot, so the count is
// usually null; that is unknown, never zero, and power is then judged on
// undervoltage now alone.
func powerOf(s *model.Sample) (Level, string, string) {
	switch {
	case s == nil:
		return Info, dash, "no data"
	case s.Undervoltage != nil && *s.Undervoltage:
		return Warn, "Low", "undervoltage now"
	case s.UndervoltageEvents != nil && *s.UndervoltageEvents > 0:
		return Warn, "Was low", plural(int(*s.UndervoltageEvents), "time", "times") + " since boot"
	case s.Undervoltage == nil:
		return Info, dash, "no sensor"
	case s.UndervoltageEvents == nil:
		return Good, "Steady", "no undervoltage now"
	default:
		return Good, "Steady", "no undervoltage since boot"
	}
}

type issue struct {
	level       Level
	title, text string
}

func (st *state) banner() Banner {
	in := st.in
	switch in.Snap.Tailnet {
	case collect.TailnetStarting:
		return Banner{iconOf(Unknown), "Starting up", "The dashboard is joining the tunnel's tailnet, so it can't tell yet how the tunnel is doing. " +
			st.lastDataSentence() + "Fresh data shows up within a minute."}
	case collect.TailnetNotTagged:
		return Banner{iconOf(Unknown), "The dashboard isn't set up", "Its tailnet node isn't tagged tag:dashboard, so it doesn't poll the Pis. " +
			st.lastDataSentence() + "This is a problem with the dashboard, not with the tunnel."}
	case collect.TailnetDown:
		return Banner{iconOf(Unknown), "The dashboard can't see the tailnet", st.sinceSentence() +
			"the dashboard itself has been cut off from the tunnel's tailnet, so it can't tell how the tunnel is doing. " +
			st.lastDataSentence() + "This is a problem with the dashboard, not with the tunnel."}
	case collect.TailnetOK:
	default:
		panic("view: unknown tailnet state " + string(in.Snap.Tailnet))
	}
	if !st.live {
		return Banner{iconOf(Unknown), "No current data", st.sinceSentence() +
			"the dashboard hasn't polled the Pis, so it can't tell how the tunnel is doing. " +
			st.lastDataSentence() + "This is a problem with the dashboard, not with the tunnel."}
	}

	var issues []issue
	exit := st.exit
	// Critical: a TV on its own connection, or the exit unreachable.
	var own []*pi
	for _, g := range st.gateways {
		if g.Current && g.Latest.Fallback != nil && *g.Latest.Fallback {
			own = append(own, g)
		}
	}
	if len(own) > 0 {
		why := "the tunnel went down"
		if exit == nil || !exit.Current {
			why = st.exitName() + " stopped answering"
		}
		if len(own) == 1 {
			g := own[0]
			issues = append(issues, issue{Crit, g.TV + " is on " + g.Apt + "'s own connection",
				capital(why) + ", so " + g.Name + " moved " + g.TV + " to " + g.Apt + "'s own connection. " + g.TV +
					" stays online, but websites see " + g.Apt + "'s address until the tunnel returns."})
		} else {
			var tvs []string
			for _, g := range own {
				tvs = append(tvs, g.TV)
			}
			issues = append(issues, issue{Crit, joinAnd(tvs) + " are on their own connections",
				capital(why) + ", so each gateway moved its TV to its own apartment's connection. The TVs stay online, but websites see each apartment's own address until the tunnel returns."})
		}
	}
	switch {
	case exit == nil:
		issues = append(issues, issue{Crit, "No exit node", "The dashboard sees no Pi tagged tag:exit, so there's no tunnel to leave through."})
	case !exit.Current:
		issues = append(issues, issue{Crit, exit.Name + " isn't answering",
			exit.Name + " hasn't answered since " + st.lastOK(exit) + " (" + failureLabel(exit.Latest.Outcome) + "). Gateways move their TVs to their own connections while the tunnel is down."})
	}

	// Warnings.
	for _, g := range st.gateways {
		switch {
		case !g.Current:
			issues = append(issues, issue{Warn, g.Name + " isn't answering",
				g.Name + " hasn't answered since " + st.lastOK(g) + " (" + failureLabel(g.Latest.Outcome) + "), so the dashboard can't tell where " + g.TV + "'s traffic goes."})
		case g.Latest.Path.Relayed():
			issues = append(issues, issue{Warn, g.Name + "'s tunnel is relayed",
				g.Name + " reaches " + st.exitName() + " through a relay rather than directly, " + ms(g.Latest.LatencyMS) + " between the Pis. " + g.TV + " still leaves through " + st.exitApt() + "'s connection."})
		}
		if g.Current && g.Latest.ExitMatch != nil && !*g.Latest.ExitMatch {
			issues = append(issues, issue{Warn, g.Name + "'s exit address doesn't match",
				g.Name + "'s last public address check didn't match " + st.exitName() + "'s, so " + g.TV + " may not be leaving through " + st.exitApt() + "'s connection."})
		}
	}
	for _, x := range st.all {
		if !x.Current {
			continue
		}
		s := x.Latest
		if l, v, note := powerOf(&s); l == Warn {
			issues = append(issues, issue{Warn, x.Name + " is short of power", x.Name + "'s power: " + strings.ToLower(v) + ", " + note + "."})
		}
		if s.TempC != nil && *s.TempC >= TempHot {
			issues = append(issues, issue{Warn, x.Name + " is running hot",
				x.Name + " is at " + degC(s.TempC) + ". From 60 °C it lowers its clock."})
		}
	}

	if len(issues) == 0 {
		return Banner{iconOf(Good), "All good", st.allGood()}
	}
	worst := issues[0]
	for _, is := range issues[1:] {
		if is.level.rank() > worst.level.rank() {
			worst = is
		}
	}
	text := worst.text
	var others []string
	for _, is := range issues {
		if is != worst {
			others = append(others, is.title)
		}
	}
	if len(others) > 0 {
		text += " Also: " + strings.Join(others, "; ") + "."
	}
	return Banner{iconOf(worst.level), worst.title, text}
}

func (st *state) allGood() string {
	switch len(st.gateways) {
	case 0:
		return st.exitName() + " is up. No gateway has joined yet."
	case 1:
		g := st.gateways[0]
		path := "a direct tunnel"
		if g.Latest.Path == model.PathIdle {
			path = "an idle tunnel"
		}
		return g.TV + " leaves through " + st.exitApt() + "'s connection over " + path + ", " + ms(g.Latest.LatencyMS) +
			" between the Pis. Both Pis report normal temperatures and steady power."
	default:
		return fmt.Sprintf("All %d apartments leave through %s, so every TV shows %s's address. Every Pi reports normal temperature and steady power.",
			len(st.gateways), st.exitName(), st.exitApt())
	}
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// lastOK is when a Pi last answered, or "the dashboard started" if never.
func (st *state) lastOK(x *pi) string {
	if x.State.LastOKTS.IsZero() {
		return "the dashboard started"
	}
	return clock(x.State.LastOKTS, st.in.Now, st.in.Loc)
}

func (st *state) hero() Hero {
	var tunnel, total int
	for _, g := range st.gateways {
		for i := 0; i < st.in.Window.N; i++ {
			a := st.in.Hist.Bucket(g.Host, i)
			tunnel += a.RouteTunnel
			total += a.RouteTunnel + a.RouteOwn + a.RouteHeld
		}
	}
	who := "TVs"
	if len(st.gateways) == 1 {
		who = st.gateways[0].TV
	}
	label := who + " on the tunnel, last " + st.in.Window.Word
	if total == 0 {
		return Hero{dash, label}
	}
	share := float64(tunnel) / float64(total) * 100
	if share >= 99.95 {
		return Hero{"100%", label}
	}
	return Hero{fmt.Sprintf("%.1f%%", share), label}
}

// EventRow is one line of the events list.
type EventRow struct {
	Icon             Icon
	Time, Node, Text string
}

func (st *state) events() ([]EventRow, string) {
	byHost := map[string]Names{}
	for _, x := range st.all {
		byHost[x.Host] = x.Names
	}
	var rows []EventRow
	byHost[model.DashboardHost] = Names{Name: "Dashboard"}
	for _, e := range st.in.Events {
		n, ok := byHost[e.Host]
		if !ok {
			// Only Pis the dashboard knows now are named on the page.
			continue
		}
		l, text := st.eventText(e, n)
		rows = append(rows, EventRow{iconOf(l), clock(e.TS, st.in.Now, st.in.Loc), n.Name, text})
		if len(rows) == 8 {
			break
		}
	}
	if len(rows) == 0 {
		return nil, "Nothing happened in the last " + st.in.Window.Word + "."
	}
	return rows, ""
}

func (st *state) eventText(e model.Event, n Names) (Level, string) {
	exitName := st.exitName()
	isExit := st.exit != nil && st.exit.Host == e.Host
	switch e.Kind {
	case model.EventUnreachable:
		l := Warn
		if isExit {
			l = Crit
		}
		o := model.Outcome(e.Detail)
		if !o.Valid() || o == model.OutcomeOK {
			o = model.OutcomeUnreachable
		}
		return l, "Stopped answering (" + failureLabel(o) + ")"
	case model.EventReachable:
		return Good, "Answering again"
	case model.EventReboot:
		return Info, "Rebooted"
	case model.EventUndervoltage:
		return Warn, "Undervoltage seen"
	case model.EventFallbackSet:
		return Warn, n.TV + " moved to " + n.Apt + "'s own connection"
	case model.EventFallbackCleared:
		return Good, n.TV + " back on the tunnel"
	case model.EventTunnelDown:
		if model.Path(e.Detail) == model.PathNone {
			return Warn, "No exit node set"
		}
		return Warn, "Tunnel to " + exitName + " went down"
	case model.EventTunnelUp:
		return Good, "Tunnel to " + exitName + " back up, " + pathWord(validPath(e.Detail))
	case model.EventTailnetLost:
		return Unknown, "Lost the tailnet. Showing the last data it had"
	case model.EventTailnetBack:
		return Good, "Back on the tailnet"
	case model.EventPathChange:
		from, to, _ := strings.Cut(e.Detail, ">")
		f, t := validPath(from), validPath(to)
		l := Good
		if t.Relayed() {
			l = Warn
		}
		return l, "Path to " + exitName + " changed from " + pathWord(f) + " to " + pathWord(t)
	default:
		panic("view: unknown event kind " + string(e.Kind))
	}
}

// validPath reads a path from a stored event's detail; anything unexpected
// reads as unknown rather than reaching the page.
func validPath(s string) model.Path {
	p := model.Path(s)
	if !p.Valid() {
		return model.PathUnknown
	}
	return p
}

// CheckRow is one line of the checks list.
type CheckRow struct {
	Icon        Icon
	Label, Text string
}

func (st *state) checks() []CheckRow {
	row := func(l Level, label, text string) CheckRow { return CheckRow{iconOf(l), label, text} }
	if !st.live {
		text := "Unknown: no recent poll"
		switch st.in.Snap.Tailnet {
		case collect.TailnetDown:
			text = "Unknown while the dashboard is cut off"
		case collect.TailnetStarting:
			text = "Unknown until the first poll"
		case collect.TailnetNotTagged:
			text = "Unknown: the dashboard isn't set up"
		}
		var out []CheckRow
		for _, label := range []string{"Exit address", "TV port firewall", "Fallback rule", "Power", "Node keys"} {
			out = append(out, row(Unknown, label, text))
		}
		return out
	}
	multi := len(st.gateways) > 1
	var out []CheckRow

	// The exit address matches.
	{
		var bad, unknown []string
		for _, g := range st.gateways {
			switch {
			case !g.Current || g.Latest.ExitMatch == nil:
				unknown = append(unknown, g.Name)
			case !*g.Latest.ExitMatch:
				bad = append(bad, g.Name)
			}
		}
		switch {
		case len(st.gateways) == 0:
			out = append(out, row(Info, "Exit address", "No gateway to check"))
		case st.exit == nil || !st.exit.Current:
			out = append(out, row(Info, "Exit address", "No answer from "+st.exitName()+" to compare with"))
		case len(bad) > 0:
			out = append(out, row(Warn, "Exit address", joinAnd(bad)+"'s public address doesn't match "+st.exitName()+"'s"))
		case len(unknown) > 0:
			out = append(out, row(Info, "Exit address", "Not checked yet for "+joinAnd(unknown)))
		case multi:
			out = append(out, row(Good, "Exit address", "Every gateway's public address matches "+st.exitName()+"'s"))
		default:
			out = append(out, row(Good, "Exit address", st.gateways[0].Name+"'s public address matches "+st.exitName()+"'s"))
		}
	}
	// Each TV port is gated.
	{
		var bad, unknown []string
		for _, g := range st.gateways {
			switch {
			case !g.Current || g.Latest.TVGated == nil:
				unknown = append(unknown, g.Name)
			case !*g.Latest.TVGated:
				bad = append(bad, g.Name)
			}
		}
		switch {
		case len(st.gateways) == 0:
			out = append(out, row(Info, "TV port firewall", "No gateway to check"))
		case len(bad) > 0:
			out = append(out, row(Crit, "TV port firewall", "TV port not gated on "+joinAnd(bad)))
		case len(unknown) > 0:
			out = append(out, row(Info, "TV port firewall", "No answer from "+joinAnd(unknown)))
		case multi:
			out = append(out, row(Good, "TV port firewall", "tv-port table loaded on every gateway"))
		default:
			out = append(out, row(Good, "TV port firewall", "tv-port table loaded, TV port gated on it"))
		}
	}
	// Each fallback rule is off.
	{
		var set, unknown []string
		for _, g := range st.gateways {
			switch {
			case !g.Current || g.Latest.Fallback == nil:
				unknown = append(unknown, g.Name)
			case *g.Latest.Fallback:
				set = append(set, g.TV)
			}
		}
		switch {
		case len(st.gateways) == 0:
			out = append(out, row(Info, "Fallback rule", "No gateway to check"))
		case len(set) > 0:
			out = append(out, row(Warn, "Fallback rule", "Set: "+joinAnd(set)+" on its own connection"))
		case len(unknown) > 0:
			out = append(out, row(Info, "Fallback rule", "No answer from "+joinAnd(unknown)))
		case multi:
			out = append(out, row(Good, "Fallback rule", "Not set on any gateway"))
		default:
			out = append(out, row(Good, "Fallback rule", "Not set, "+st.gateways[0].TV+" on the tunnel"))
		}
	}
	// Power.
	{
		var bad, unknown []string
		for _, x := range st.all {
			var s *model.Sample
			if x.Current {
				s = x.Show
			}
			switch l, _, _ := powerOf(s); l {
			case Warn:
				bad = append(bad, x.Name)
			case Info:
				unknown = append(unknown, x.Name)
			}
		}
		switch {
		case len(st.all) == 0:
			out = append(out, row(Info, "Power", "No Pi to check"))
		case len(bad) > 0:
			out = append(out, row(Warn, "Power", "Undervoltage on "+joinAnd(bad)))
		case len(unknown) > 0:
			out = append(out, row(Info, "Power", "No reading from "+joinAnd(unknown)))
		default:
			out = append(out, row(Good, "Power", "No undervoltage on any Pi now"))
		}
	}
	return append(out, st.keyCheck())
}

// keyCheck is D2's "no key expires within 30 days", from the tailnet view.
func (st *state) keyCheck() CheckRow {
	var expired, soon []string
	known := 0
	for _, x := range st.all {
		if !x.Tailnet.Known {
			continue
		}
		known++
		e := x.Tailnet.KeyExpiry
		switch {
		case e == nil:
		case !e.After(st.in.Now):
			expired = append(expired, x.Host)
		case e.Sub(st.in.Now) < KeyWarnBefore:
			soon = append(soon, x.Host+" on "+e.In(st.in.Loc).Format("2 Jan"))
		}
	}
	switch {
	case len(expired) > 0:
		return CheckRow{iconOf(Crit), "Node keys", "Expired: " + joinAnd(expired)}
	case len(soon) > 0:
		return CheckRow{iconOf(Warn), "Node keys", "Expiring: " + joinAnd(soon)}
	case known == 0:
		return CheckRow{iconOf(Info), "Node keys", "No current data"}
	default:
		return CheckRow{iconOf(Good), "Node keys", "None expire within 30 days"}
	}
}

// RouteSeg is a run of buckets in one exit-route state.
type RouteSeg struct {
	Class string
	Grow  int
}

// RouteRow is one gateway's TV across the range.
type RouteRow struct {
	TV   string
	Segs []RouteSeg
}

// RouteStrip is the exit-route strip: one row per gateway's TV (D2). TV A
// has none, as nothing measures it.
type RouteStrip struct {
	Rows  []RouteRow
	Empty string
}

// routeAt is a gateway's state over bucket i: the worst state seen in it, so
// a short stretch on the own connection isn't averaged away.
func routeAt(a model.Agg) model.Route {
	switch {
	case a.RouteOwn > 0:
		return model.RouteOwn
	case a.RouteHeld > 0:
		return model.RouteHeld
	case a.RouteTunnel > 0:
		return model.RouteTunnel
	case a.TVOff > 0:
		return model.RouteTVOff
	default:
		return model.RouteNoData
	}
}

func routeClass(r model.Route) string {
	switch r {
	case model.RouteTunnel:
		return "rs-tunnel"
	case model.RouteOwn:
		return "rs-own"
	case model.RouteHeld:
		return "rs-held"
	case model.RouteNoData:
		return "rs-nodata"
	case model.RouteTVOff:
		return "rs-tvoff"
	default:
		panic("view: unknown route " + string(r))
	}
}

func routeText(r model.Route) (Level, string) {
	switch r {
	case model.RouteTunnel:
		return Good, "on the tunnel"
	case model.RouteOwn:
		return Warn, "on its own connection"
	case model.RouteHeld:
		return Crit, "held: tunnel or port down"
	case model.RouteNoData:
		return Unknown, "no data"
	case model.RouteTVOff:
		return Info, "TV port down, likely off"
	default:
		panic("view: unknown route " + string(r))
	}
}

func (st *state) route() RouteStrip {
	if len(st.gateways) == 0 {
		return RouteStrip{Empty: "No gateway has joined yet."}
	}
	var rs RouteStrip
	for _, g := range st.gateways {
		row := RouteRow{TV: g.TV}
		for i := 0; i < st.in.Window.N; i++ {
			c := routeClass(routeAt(st.in.Hist.Bucket(g.Host, i)))
			if n := len(row.Segs); n > 0 && row.Segs[n-1].Class == c {
				row.Segs[n-1].Grow++
			} else {
				row.Segs = append(row.Segs, RouteSeg{c, 1})
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs
}

// noDataSince is what blind statuses say: "No data since 21:31".
func (st *state) noDataSince() string {
	if st.in.Snap.At.IsZero() {
		return "No data yet"
	}
	return "No data since " + clock(st.in.Snap.At, st.in.Now, st.in.Loc)
}

// unknownWhy finishes "unknown …" for the dashboard's own state.
func (st *state) unknownWhy() string {
	switch st.in.Snap.Tailnet {
	case collect.TailnetStarting:
		return "unknown until the first poll"
	case collect.TailnetNotTagged:
		return "unknown: the dashboard isn't set up"
	case collect.TailnetDown:
		return "unknown while cut off"
	default:
		return "unknown: no recent poll"
	}
}

// sinceSentence starts a blind banner's text: "Since 21:31 ".
func (st *state) sinceSentence() string {
	if st.in.Snap.At.IsZero() {
		return "Since it started, "
	}
	return "Since " + clock(st.in.Snap.At, st.in.Now, st.in.Loc) + " "
}

func (st *state) lastDataSentence() string {
	for _, x := range st.all {
		if x.Show != nil {
			return "Below is the last data it had. "
		}
	}
	return ""
}
