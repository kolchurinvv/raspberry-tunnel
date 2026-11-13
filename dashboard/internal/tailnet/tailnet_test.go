package tailnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

var now = time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)

func tags(t ...string) *views.Slice[string] {
	v := views.SliceOf(t)
	return &v
}

type peerSpec struct {
	host    string
	tags    []string
	ip      string
	online  bool
	created time.Time
	id      tailcfg.NodeID
}

func status(self *ipnstate.PeerStatus, specs ...peerSpec) *ipnstate.Status {
	st := &ipnstate.Status{BackendState: "Running", Self: self, Peer: map[key.NodePublic]*ipnstate.PeerStatus{}}
	for _, s := range specs {
		ps := &ipnstate.PeerStatus{
			HostName: s.host, Tags: tags(s.tags...), Online: s.online, Created: s.created, NodeID: s.id,
			ID:           tailcfg.StableNodeID(s.host + "-" + string(rune('0'+s.id))),
			TailscaleIPs: []netip.Addr{netip.MustParseAddr(s.ip), netip.MustParseAddr(fmt.Sprintf("fd7a:115c:a1e0::%x", 0xa0+int(s.id)))},
		}
		if !s.online {
			ps.LastSeen = now.Add(-time.Hour)
		}
		st.Peer[key.NewNode().Public()] = ps
	}
	return st
}

func self() *ipnstate.PeerStatus {
	return &ipnstate.PeerStatus{HostName: "dashboard", Tags: tags(TagDashboard), Online: true}
}

type fakeSource struct {
	st  *ipnstate.Status
	err error
}

func (f *fakeSource) Status(context.Context) (*ipnstate.Status, error) { return f.st, f.err }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func hosts(ps []collect.Peer) []string {
	var out []string
	for _, p := range ps {
		out = append(out, string(p.Role)+":"+p.Host+"@"+p.Addr.String())
	}
	return out
}

func TestSelect(t *testing.T) {
	old := now.Add(-30 * 24 * time.Hour)
	st := status(self(),
		peerSpec{host: "pi-a", tags: []string{TagExit}, ip: "100.64.0.1", online: true, created: now, id: 5},
		// A re-flashed Pi's old node: same name, offline, older.
		peerSpec{host: "pi-a", tags: []string{TagExit}, ip: "100.64.0.9", online: false, created: old, id: 1},
		peerSpec{host: "pi-c", tags: []string{TagGateway}, ip: "100.64.0.3", online: true, created: now, id: 3},
		peerSpec{host: "pi-b", tags: []string{TagGateway}, ip: "100.64.0.2", online: true, created: old, id: 2},
		// Two online nodes for pi-b: the newer registration wins.
		peerSpec{host: "pi-b", tags: []string{TagGateway}, ip: "100.64.0.8", online: true, created: now, id: 8},
		peerSpec{host: "someones-phone", ip: "100.64.0.4", online: true, id: 4},
		peerSpec{host: "bad name", tags: []string{TagGateway}, ip: "100.64.0.6", online: true, id: 6},
	)
	want := []string{"exit:pi-a@100.64.0.1", "gateway:pi-b@100.64.0.8", "gateway:pi-c@100.64.0.3"}
	// Map order is random; the choice must not be.
	for i := 0; i < 50; i++ {
		got, notes := Select(st)
		if !reflect.DeepEqual(hosts(got), want) {
			t.Fatalf("run %d: Select = %q, want %q", i, hosts(got), want)
		}
		if len(notes) != 3 {
			t.Fatalf("notes = %q", notes)
		}
	}
}

func TestSelectPrefersOnlineOverNewer(t *testing.T) {
	st := status(self(),
		peerSpec{host: "pi-a", tags: []string{TagExit}, ip: "100.64.0.1", online: true, created: now.Add(-time.Hour), id: 1},
		peerSpec{host: "pi-a", tags: []string{TagExit}, ip: "100.64.0.9", online: false, created: now, id: 9},
	)
	got, _ := Select(st)
	if len(got) != 1 || got[0].Addr.String() != "100.64.0.1" || !got[0].Online {
		t.Errorf("Select = %q", hosts(got))
	}
}

func TestPeersStates(t *testing.T) {
	clock := now
	src := &fakeSource{err: errors.New("local API not ready")}
	d := New(src, quiet(), func() time.Time { return clock })
	ctx := context.Background()

	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrStarting) {
		t.Errorf("before start: %v", err)
	}
	src.err, src.st = nil, &ipnstate.Status{BackendState: "NeedsLogin"}
	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrStarting) {
		t.Errorf("needs login at start: %v", err)
	}

	untagged := self()
	untagged.Tags = nil
	src.st = status(untagged)
	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrNotTagged) {
		t.Errorf("untagged: %v", err)
	}

	src.st = status(self(), peerSpec{host: "pi-a", tags: []string{TagExit}, ip: "100.64.0.1", online: true, id: 1})
	if ps, err := d.Peers(ctx); err != nil || len(ps) != 1 {
		t.Errorf("running: %v, %v", ps, err)
	}

	// Out of the control poll: trusted for ControlGrace, then not.
	src.st.Self.Online = false
	clock = clock.Add(ControlGrace / 2)
	if _, err := d.Peers(ctx); err != nil {
		t.Errorf("within grace: %v", err)
	}
	clock = clock.Add(ControlGrace)
	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrTailnetDown) {
		t.Errorf("past grace: %v", err)
	}

	// Once it has run, a stopped backend is down, not starting.
	src.st = &ipnstate.Status{BackendState: "Stopped"}
	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrTailnetDown) {
		t.Errorf("stopped: %v", err)
	}
	src.st, src.err = nil, errors.New("local API gone")
	if _, err := d.Peers(ctx); !errors.Is(err, collect.ErrTailnetDown) {
		t.Errorf("local API failing: %v", err)
	}
}

func TestPeerCarriesTailnetFields(t *testing.T) {
	exp := now.Add(10 * 24 * time.Hour)
	st := status(self(), peerSpec{host: "pi-b", tags: []string{TagGateway}, ip: "100.64.0.2", online: false, id: 2})
	for _, ps := range st.Peer {
		ps.KeyExpiry = &exp
	}
	got, _ := Select(st)
	if len(got) != 1 {
		t.Fatalf("Select = %v", got)
	}
	p := got[0]
	if p.Role != model.RoleGateway || p.Online || !p.LastSeen.Equal(now.Add(-time.Hour)) || p.KeyExpiry == nil || !p.KeyExpiry.Equal(exp) {
		t.Errorf("peer = %+v", p)
	}
}
