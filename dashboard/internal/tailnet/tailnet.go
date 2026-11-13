// Package tailnet finds the Pis in the dashboard's own tailnet view (D3): the
// peer tagged tag:exit is the exit node, and the peers tagged tag:gateway are
// the gateways. It holds no Headscale credentials; the view is what the
// control server sends every node.
//
// The view itself sits behind StatusSource, so tests can feed one seeded with
// addresses, endpoints and names that must never reach the page.
package tailnet

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/schema"
)

// The tags that matter here.
const (
	TagDashboard = "tag:dashboard"
	TagExit      = "tag:exit"
	TagGateway   = "tag:gateway"
)

// ControlGrace is how long the dashboard's own node may be out of its long
// poll with the control server before its view counts as stale. The poll
// reconnects now and then, which shouldn't blank the page.
const ControlGrace = 2 * time.Minute

// StatusSource is the dashboard's own tailnet view. tsnet's local client is
// one.
type StatusSource interface {
	Status(ctx context.Context) (*ipnstate.Status, error)
}

// Discoverer turns the view into the Pis, and refuses a view it can't trust.
type Discoverer struct {
	src StatusSource
	log *slog.Logger
	now func() time.Time

	mu          sync.Mutex
	started     time.Time
	everRunning bool
	lastInPoll  time.Time
	lastNotes   string
}

// New returns a Discoverer over src.
func New(src StatusSource, log *slog.Logger, now func() time.Time) *Discoverer {
	if now == nil {
		now = time.Now
	}
	return &Discoverer{src: src, log: log, now: now, started: now()}
}

// Peers implements collect.Discoverer.
func (d *Discoverer) Peers(ctx context.Context) ([]collect.Peer, error) {
	st, err := d.src.Status(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	// Still starting, until ControlGrace has passed without ever running.
	starting := !d.everRunning && d.now().Sub(d.started) <= ControlGrace
	if err != nil {
		if starting {
			return nil, fmt.Errorf("%w: %v", collect.ErrStarting, err)
		}
		return nil, fmt.Errorf("%w: %v", collect.ErrTailnetDown, err)
	}
	if st.BackendState != "Running" {
		if starting {
			return nil, fmt.Errorf("%w: state %s", collect.ErrStarting, st.BackendState)
		}
		return nil, fmt.Errorf("%w: state %s", collect.ErrTailnetDown, st.BackendState)
	}
	d.everRunning = true
	if st.Self == nil || !hasTag(st.Self, TagDashboard) {
		return nil, collect.ErrNotTagged
	}
	// Self.Online is whether this node is in its long poll with the control
	// server, which is what keeps the view current.
	now := d.now()
	if st.Self.Online {
		d.lastInPoll = now
	}
	switch {
	case d.lastInPoll.IsZero() && starting:
		return nil, fmt.Errorf("%w: not yet in contact with the control server", collect.ErrStarting)
	case d.lastInPoll.IsZero():
		return nil, fmt.Errorf("%w: never in contact with the control server", collect.ErrTailnetDown)
	case now.Sub(d.lastInPoll) > ControlGrace:
		return nil, fmt.Errorf("%w: out of contact since %s", collect.ErrTailnetDown, d.lastInPoll.Format(time.RFC3339))
	}

	peers, notes := Select(st)
	if j := strings.Join(notes, "; "); j != d.lastNotes {
		d.lastNotes = j
		for _, n := range notes {
			d.log.Warn("tailnet view", "note", n)
		}
	}
	return peers, nil
}

func hasTag(ps *ipnstate.PeerStatus, tag string) bool {
	if ps.Tags == nil {
		return false
	}
	return slices.Contains(ps.Tags.AsSlice(), tag)
}

type candidate struct {
	ps   *ipnstate.PeerStatus
	role model.Role
	addr netip.Addr
}

// better orders candidates for one place: online first, then the most
// recently seen, then the most recently registered, then the newest node.
// A re-flashed Pi leaves its old node behind under the same name, offline.
func better(a, b candidate) int {
	if a.ps.Online != b.ps.Online {
		if a.ps.Online {
			return -1
		}
		return 1
	}
	if c := b.ps.LastSeen.Compare(a.ps.LastSeen); c != 0 {
		return c
	}
	if c := b.ps.Created.Compare(a.ps.Created); c != 0 {
		return c
	}
	if c := cmp.Compare(b.ps.NodeID, a.ps.NodeID); c != 0 {
		return c
	}
	return cmp.Compare(string(a.ps.ID), string(b.ps.ID))
}

// Select picks the Pis out of a view: one exit, and one gateway per host
// name, in name order. It returns notes on what it skipped, for the log.
// Nothing depends on map order.
func Select(st *ipnstate.Status) ([]collect.Peer, []string) {
	var cands []candidate
	var notes []string
	for _, ps := range st.Peer {
		var role model.Role
		switch {
		case hasTag(ps, TagExit):
			role = model.RoleExit
		case hasTag(ps, TagGateway):
			role = model.RoleGateway
		default:
			continue
		}
		if !schema.Hostname.MatchString(ps.HostName) {
			notes = append(notes, fmt.Sprintf("skipped a %s peer whose name isn't a host name", role))
			continue
		}
		addr := pickAddr(ps.TailscaleIPs)
		if !addr.IsValid() {
			notes = append(notes, fmt.Sprintf("skipped %s: it has no tailnet address", ps.HostName))
			continue
		}
		cands = append(cands, candidate{ps: ps, role: role, addr: addr})
	}
	slices.SortFunc(cands, better)
	slices.Sort(notes)

	var out []collect.Peer
	taken := map[string]bool{}
	exitChosen := false
	for _, c := range cands {
		if c.role != model.RoleExit {
			continue
		}
		if exitChosen {
			notes = append(notes, fmt.Sprintf("ignored %s: another peer is the exit", c.ps.HostName))
			continue
		}
		exitChosen = true
		taken[c.ps.HostName] = true
		out = append(out, peerOf(c))
	}
	var gws []collect.Peer
	for _, c := range cands {
		if c.role != model.RoleGateway {
			continue
		}
		if taken[c.ps.HostName] {
			notes = append(notes, fmt.Sprintf("ignored a second %s node: an old one, or a name used twice", c.ps.HostName))
			continue
		}
		taken[c.ps.HostName] = true
		gws = append(gws, peerOf(c))
	}
	slices.SortFunc(gws, func(a, b collect.Peer) int { return cmp.Compare(a.Host, b.Host) })
	return append(out, gws...), notes
}

func peerOf(c candidate) collect.Peer {
	p := collect.Peer{Host: c.ps.HostName, Role: c.role, Addr: c.addr, Online: c.ps.Online, LastSeen: c.ps.LastSeen}
	if c.ps.KeyExpiry != nil {
		t := *c.ps.KeyExpiry
		p.KeyExpiry = &t
	}
	return p
}

// pickAddr prefers the IPv4 tailnet address.
func pickAddr(ips []netip.Addr) netip.Addr {
	for _, a := range ips {
		if a.Is4() {
			return a
		}
	}
	for _, a := range ips {
		if a.IsValid() {
			return a
		}
	}
	return netip.Addr{}
}
