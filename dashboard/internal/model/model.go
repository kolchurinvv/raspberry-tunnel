// Package model holds what the dashboard keeps about each Pi: one sample per
// poll, the events derived from consecutive samples, and the state that the
// derivation carries from one poll to the next.
//
// Nothing here may hold an address or an endpoint. A sample keeps only
// whether the exit check matched, never the addresses it compared (D2).
package model

import (
	"fmt"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/schema"
)

// Interval is how often the dashboard asks every Pi for its status (D2).
const Interval = 30 * time.Second

// Role is a Pi's role, taken from its tailnet tag.
type Role = schema.Role

const (
	RoleExit    = schema.RoleExit
	RoleGateway = schema.RoleGateway
)

// Outcome is how one poll of one Pi went. A failed poll is data, not an
// error: D2 wants the page to say how a pull failed.
type Outcome string

const (
	OutcomeOK          Outcome = "ok"
	OutcomeOffline     Outcome = "offline"     // the tailnet says the Pi is offline, and the request failed
	OutcomeTimeout     Outcome = "timeout"     // no answer within the deadline
	OutcomeUnreachable Outcome = "unreachable" // the connection failed
	OutcomeHTTPError   Outcome = "http_error"  // answered with a status other than 200
	OutcomeInvalid     Outcome = "invalid"     // answered with something that breaks the contract
	OutcomeSchema      Outcome = "schema"      // answered with a schema other than 1
)

// Valid reports whether o is one of the outcomes above.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeOK, OutcomeOffline, OutcomeTimeout, OutcomeUnreachable, OutcomeHTTPError, OutcomeInvalid, OutcomeSchema:
		return true
	}
	return false
}

// Path is a gateway's tunnel path, as its Pi reports it. Besides the paths in
// the contract:
//   - PathNone: its tunnel list is empty, so no exit node is set;
//   - PathNoTailscale: its tunnel list is null, as tailscaled can't be reached;
//   - PathUnknown: it sent a tunnel entry without a path, or nothing to read
//     one from.
type Path string

const (
	PathUnknown     Path = ""
	PathNone        Path = "none"
	PathNoTailscale Path = "no_tailscale"
	PathDirect           = Path(schema.PathDirect)
	PathPeerRelay        = Path(schema.PathPeerRelay)
	PathDERP             = Path(schema.PathDERP)
	PathIdle             = Path(schema.PathIdle)
	PathOffline          = Path(schema.PathOffline)
)

// Valid reports whether p is one of the paths above.
func (p Path) Valid() bool {
	switch p {
	case PathUnknown, PathNone, PathNoTailscale, PathDirect, PathPeerRelay, PathDERP, PathIdle, PathOffline:
		return true
	}
	return false
}

// Relayed reports whether the path goes through a relay.
func (p Path) Relayed() bool { return p == PathDERP || p == PathPeerRelay }

// Up reports whether the tunnel can carry traffic.
func (p Path) Up() bool {
	return p == PathDirect || p == PathPeerRelay || p == PathDERP || p == PathIdle
}

// Down reports whether the tunnel is known to be down. PathUnknown is
// neither up nor down.
func (p Path) Down() bool {
	return p == PathOffline || p == PathNone || p == PathNoTailscale
}

// Route is which connection a gateway's TV leaves through at one moment: D2's
// four exit-route states, plus the TV being off.
type Route string

const (
	RouteNone   Route = ""       // not a gateway
	RouteTunnel Route = "tunnel" // the gateway is up, with no fallback rule
	RouteOwn    Route = "own"    // the fallback rule is set
	RouteHeld   Route = "held"   // the tunnel is down with no fallback rule yet
	RouteNoData Route = "nodata" // the gateway didn't answer
	// RouteTVOff: the tunnel is up but the TV's port is down, which is usually
	// the TV being off. The user ruled it no failure, so it isn't D2's held.
	RouteTVOff Route = "tvoff"
)

// Valid reports whether r is one of the routes above.
func (r Route) Valid() bool {
	switch r {
	case RouteNone, RouteTunnel, RouteOwn, RouteHeld, RouteNoData, RouteTVOff:
		return true
	}
	return false
}

// Sample is one poll of one Pi, timestamped with the dashboard's clock.
type Sample struct {
	TS      time.Time
	Host    string
	Role    Role
	Outcome Outcome
	// SchemaSeen is the schema number a Pi sent, for OutcomeSchema only.
	SchemaSeen int
	// Online is the Pi's state in the dashboard's tailnet view.
	Online             *bool
	UptimeS            *int64
	TempC              *float64
	CPUPct             *float64
	MemPct             *float64
	DiskPct            *float64
	TrafficMbps        *float64
	LatencyMS          *float64
	Undervoltage       *bool
	UndervoltageEvents *int64
	// Gateways only.
	Path      Path
	Fallback  *bool
	TVLink    *bool
	TVGated   *bool
	ExitMatch *bool
	Route     Route
}

// OK reports whether the Pi answered with a valid status.
func (s Sample) OK() bool { return s.Outcome == OutcomeOK }

// Validate checks the invariants every stored sample holds. A violation is
// the dashboard's own bug, so callers fail loudly on it.
func (s Sample) Validate() error {
	switch {
	case s.TS.IsZero():
		return fmt.Errorf("sample for %q has no time", s.Host)
	case s.Host == "":
		return fmt.Errorf("sample at %v has no host", s.TS)
	case s.Role != RoleExit && s.Role != RoleGateway:
		return fmt.Errorf("sample for %q has role %q", s.Host, s.Role)
	case !s.Outcome.Valid():
		return fmt.Errorf("sample for %q has outcome %q", s.Host, s.Outcome)
	case !s.Path.Valid():
		return fmt.Errorf("sample for %q has path %q", s.Host, s.Path)
	case !s.Route.Valid():
		return fmt.Errorf("sample for %q has route %q", s.Host, s.Route)
	case s.Role == RoleGateway && s.Route == RouteNone:
		return fmt.Errorf("gateway sample for %q has no route", s.Host)
	case s.Role == RoleExit && s.Route != RouteNone:
		return fmt.Errorf("exit sample for %q has route %q", s.Host, s.Route)
	case !s.OK() && s.Role == RoleGateway && s.Route != RouteNoData:
		return fmt.Errorf("unanswered sample for %q has route %q", s.Host, s.Route)
	}
	return nil
}

// RouteOf is a gateway's exit-route state, from D2:
//   - no data: the gateway didn't answer, or didn't say whether its fallback
//     rule is set;
//   - own connection: the fallback rule is set;
//   - held: the tunnel is down with no fallback rule yet;
//   - TV off: the tunnel is up but the TV's port is down, likely the TV off;
//   - tunnel: otherwise.
//
// With the fallback rule off but the tunnel's state unreadable, the dashboard
// can't tell tunnel from held, so that is no data too.
func RouteOf(ok bool, fallback *bool, path Path, tvLink *bool) Route {
	if !ok || fallback == nil {
		return RouteNoData
	}
	if *fallback {
		return RouteOwn
	}
	if path == PathUnknown {
		return RouteNoData
	}
	if !path.Up() {
		return RouteHeld
	}
	if tvLink != nil && !*tvLink {
		return RouteTVOff
	}
	return RouteTunnel
}

// EventKind is a closed set: the view has a sentence for each.
type EventKind string

const (
	EventUnreachable     EventKind = "unreachable"      // Detail: the outcome
	EventReachable       EventKind = "reachable"        // Detail: ""
	EventReboot          EventKind = "reboot"           // Detail: ""
	EventUndervoltage    EventKind = "undervoltage"     // Detail: ""
	EventFallbackSet     EventKind = "fallback_set"     // Detail: ""
	EventFallbackCleared EventKind = "fallback_cleared" // Detail: ""
	EventTunnelDown      EventKind = "tunnel_down"      // Detail: the path, offline or none
	EventTunnelUp        EventKind = "tunnel_up"        // Detail: the path, or idle
	EventPathChange      EventKind = "path"             // Detail: "<from>><to>"

	// The dashboard's own: Host is DashboardHost.
	EventTailnetLost EventKind = "tailnet_lost" // Detail: ""
	EventTailnetBack EventKind = "tailnet_back" // Detail: ""
)

// DashboardHost stands for the dashboard itself in events. The underscore
// keeps it from ever matching a Pi's host name.
const DashboardHost = "_dashboard"

// Valid reports whether k is one of the kinds above.
func (k EventKind) Valid() bool {
	switch k {
	case EventUnreachable, EventReachable, EventReboot, EventUndervoltage, EventFallbackSet,
		EventFallbackCleared, EventTunnelDown, EventTunnelUp, EventPathChange, EventTailnetLost, EventTailnetBack:
		return true
	}
	return false
}

// Event is something that changed between two consecutive samples of a Pi.
// Its detail comes only from closed sets (outcomes and paths), never from
// what a Pi sent as free text.
type Event struct {
	TS     time.Time
	Host   string
	Kind   EventKind
	Detail string
}

// HostState is what event derivation remembers about a Pi between polls. The
// store keeps it, so a restart doesn't lose or repeat events.
type HostState struct {
	Host        string
	Role        Role
	LastTS      time.Time
	LastOutcome Outcome
	LastOKTS    time.Time
	// From the last valid answer.
	UptimeS            *int64
	BootTS             time.Time // LastOKTS minus UptimeS, when both are known
	Undervoltage       *bool
	UndervoltageEvents *int64
	Fallback           *bool
	// TunnelUp is whether the gateway's tunnel was last seen up; nil before
	// the first answer that says.
	TunnelUp *bool
	// LastPath is the last direct or relayed path seen, so idle stretches
	// between them don't hide a change.
	LastPath Path
}

// rebootSlack is how far a Pi's boot time may seem to move between two
// answers without counting as a reboot. Both times are estimates: the
// dashboard's poll time minus the Pi's uptime.
const rebootSlack = 5 * time.Minute

// Derive takes a Pi's previous state and its new sample, and returns the new
// state and the events between them. The first sample of a Pi has nothing to
// compare with, so it gives no events.
func Derive(prev HostState, s Sample) (HostState, []Event) {
	next := prev
	next.Host, next.Role = s.Host, s.Role
	next.LastTS, next.LastOutcome = s.TS, s.Outcome

	first := prev.LastTS.IsZero()
	var evs []Event
	add := func(k EventKind, detail string) {
		if !first {
			evs = append(evs, Event{TS: s.TS, Host: s.Host, Kind: k, Detail: detail})
		}
	}

	wasOK := prev.LastOutcome == OutcomeOK
	if !s.OK() {
		if wasOK {
			add(EventUnreachable, string(s.Outcome))
		}
		return next, evs
	}
	if !first && !wasOK {
		add(EventReachable, "")
	}

	// A reboot: the uptime went down, or the boot time moved later, which
	// also catches a reboot during an outage longer than the old uptime.
	rebooted := false
	var boot time.Time
	if s.UptimeS != nil {
		boot = s.TS.Add(-time.Duration(*s.UptimeS) * time.Second)
		if prev.UptimeS != nil && (*s.UptimeS < *prev.UptimeS ||
			(!prev.BootTS.IsZero() && boot.Sub(prev.BootTS) > rebootSlack)) {
			rebooted = true
			add(EventReboot, "")
		}
	}

	// Undervoltage: seen now and not before, or the count since boot went up.
	uvBefore := prev.UndervoltageEvents
	if rebooted {
		zero := int64(0)
		uvBefore = &zero
	}
	if (s.Undervoltage != nil && *s.Undervoltage && (prev.Undervoltage == nil || !*prev.Undervoltage)) ||
		(s.UndervoltageEvents != nil && uvBefore != nil && *s.UndervoltageEvents > *uvBefore) {
		add(EventUndervoltage, "")
	}

	if s.Role == RoleGateway {
		if s.Fallback != nil && prev.Fallback != nil && *s.Fallback != *prev.Fallback {
			if *s.Fallback {
				add(EventFallbackSet, "")
			} else {
				add(EventFallbackCleared, "")
			}
		}
		if s.Path != PathUnknown {
			up := s.Path.Up()
			changedUp := prev.TunnelUp != nil && *prev.TunnelUp != up
			switch {
			case changedUp && !up:
				add(EventTunnelDown, string(s.Path))
			case changedUp && up:
				add(EventTunnelUp, string(s.Path))
			case up && s.Path != PathIdle && prev.LastPath != PathUnknown && prev.LastPath != s.Path:
				add(EventPathChange, string(prev.LastPath)+">"+string(s.Path))
			}
			next.TunnelUp = &up
			if up && s.Path != PathIdle {
				next.LastPath = s.Path
			}
		}
		if s.Fallback != nil {
			next.Fallback = s.Fallback
		}
	}

	next.LastOKTS = s.TS
	if s.UptimeS != nil {
		next.UptimeS = s.UptimeS
		next.BootTS = boot
	}
	if s.Undervoltage != nil {
		next.Undervoltage = s.Undervoltage
	}
	if s.UndervoltageEvents != nil {
		next.UndervoltageEvents = s.UndervoltageEvents
	}
	return next, evs
}
