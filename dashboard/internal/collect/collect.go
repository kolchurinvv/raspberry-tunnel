// Package collect asks every Pi for its status every 30 seconds, turns each
// answer, or the lack of one, into a sample, derives events from consecutive
// samples, and hands both to the store.
//
// Addresses stop here. A Peer's tailnet address is used to dial and nothing
// else, and a Pi's public address is compared with the exit's and dropped.
// What leaves this package, samples and the Snapshot, holds neither.
package collect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/schema"
)

// StatusPort is the Pis' status endpoint port (D2).
const StatusPort = 9190

// DefaultTimeout bounds one request to one Pi. D2 has the Pi answer within 2
// seconds, and a gateway can spend about 1 of them on its unanswered ping
// while the exit is down; the rest covers the connection over the tailnet.
const DefaultTimeout = 3 * time.Second

// ExitCheckMaxAge is how old a public address check may be and still count.
// A Pi keeps its last successful check when a newer one fails, so an old
// check says nothing about where traffic leaves now.
const ExitCheckMaxAge = 10 * time.Minute

// maxBody bounds a status answer. A real one is about 1 KiB.
const maxBody = 64 << 10

// Peer is a Pi as the dashboard's tailnet view shows it.
type Peer struct {
	Host string
	Role model.Role
	// Addr is the Pi's tailnet address, for dialing only.
	Addr      netip.Addr
	Online    bool
	LastSeen  time.Time
	KeyExpiry *time.Time
}

// Discoverer lists the Pis: the peers tagged tag:exit and tag:gateway, at
// most one exit, one peer per host name, gateways in name order.
//
// When the dashboard's own view of the tailnet can't be trusted, Peers fails
// with ErrStarting, ErrNotTagged or ErrTailnetDown, so the page blames the
// dashboard rather than the tunnel.
type Discoverer interface {
	Peers(ctx context.Context) ([]Peer, error)
}

// The dashboard's own tailnet problems, which Discoverer.Peers wraps.
var (
	ErrStarting    = errors.New("the dashboard is still joining the tailnet")
	ErrNotTagged   = errors.New("the dashboard's node isn't tagged tag:dashboard")
	ErrTailnetDown = errors.New("the dashboard isn't connected to the control server")
)

// TailnetState is the dashboard's own standing on the tailnet.
type TailnetState string

const (
	TailnetStarting  TailnetState = "starting"
	TailnetOK        TailnetState = "ok"
	TailnetDown      TailnetState = "down"
	TailnetNotTagged TailnetState = "untagged"
)

func tailnetStateOf(err error) TailnetState {
	switch {
	case err == nil:
		return TailnetOK
	case errors.Is(err, ErrStarting):
		return TailnetStarting
	case errors.Is(err, ErrNotTagged):
		return TailnetNotTagged
	default:
		return TailnetDown
	}
}

// NodeMetrics is the CPU and memory of the cluster node hosting Headscale.
type NodeMetrics struct {
	CPUPct, MemPct *float64
}

// NodeSource reads NodeMetrics. Without one, the gauges show no data.
type NodeSource interface {
	NodeMetrics(ctx context.Context) (NodeMetrics, error)
}

// Store is where the collector writes. One call per poll, in one transaction.
type Store interface {
	WriteCycle(ctx context.Context, samples []model.Sample, events []model.Event, states []model.HostState) error
	Prune(ctx context.Context, now time.Time) error
	HostStates(ctx context.Context) ([]model.HostState, error)
	LatestSamples(ctx context.Context) ([]model.Sample, error)
	LastOKSamples(ctx context.Context) ([]model.Sample, error)
}

// Tailnet is a Pi's node as the tailnet view shows it, without its address.
type Tailnet struct {
	Known     bool
	Online    bool
	LastSeen  time.Time
	KeyExpiry *time.Time
}

// Pi is one Pi in a Snapshot.
type Pi struct {
	Host    string
	Role    model.Role
	Tailnet Tailnet
	// Latest is the latest sample, answered or not.
	Latest model.Sample
	// LastOK is the latest answered sample, if there was one.
	LastOK *model.Sample
	State  model.HostState
}

// Snapshot is the latest state, for the page.
type Snapshot struct {
	// At is the time of the latest poll that reached the tailnet.
	At time.Time
	// Tailnet is the dashboard's own standing. Anything but TailnetOK means
	// the Pis' current state is unknown, through no fault of theirs.
	Tailnet TailnetState
	// Pis has the exit first, then the gateways by name.
	Pis    []Pi
	Node   NodeMetrics
	NodeOK bool
}

// Exit returns the exit node, if one is known.
func (s Snapshot) Exit() *Pi {
	for i := range s.Pis {
		if s.Pis[i].Role == model.RoleExit {
			return &s.Pis[i]
		}
	}
	return nil
}

// Gateways returns the gateways, by name.
func (s Snapshot) Gateways() []Pi {
	var out []Pi
	for _, p := range s.Pis {
		if p.Role == model.RoleGateway {
			out = append(out, p)
		}
	}
	return out
}

// Options configures a Collector.
type Options struct {
	Discoverer Discoverer
	// Client sends the status requests. In production it dials over the
	// tailnet.
	Client *http.Client
	// Node may be nil.
	Node    NodeSource
	Store   Store
	Timeout time.Duration
	Now     func() time.Time
	Log     *slog.Logger
}

// Collector polls the Pis.
type Collector struct {
	opt Options

	mu       sync.Mutex
	states   map[string]model.HostState
	counters map[string]counters
	snap     Snapshot
}

// counters are the previous answer's cumulative counters, for rates.
type counters struct {
	ts        time.Time
	cpuBusy   uint64
	cpuTotal  uint64
	cpuKnown  bool
	tunnelSum map[string]uint64 // peer → rx + tx
}

// New returns a Collector. It panics on missing options, which is a wiring bug.
func New(opt Options) *Collector {
	if opt.Discoverer == nil || opt.Client == nil || opt.Store == nil || opt.Log == nil {
		panic("collect.New: Discoverer, Client, Store and Log are required")
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	// A Pi never redirects: a 3xx is an answer with an error, not somewhere
	// else to go. The copy leaves the caller's client as it was.
	client := *opt.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	opt.Client = &client
	return &Collector{opt: opt, states: map[string]model.HostState{}, counters: map[string]counters{},
		snap: Snapshot{Tailnet: TailnetStarting}}
}

// Load restores the derivation state and the latest samples from the store,
// so a restart neither repeats nor loses events, and the page isn't empty
// until the first poll.
func (c *Collector) Load(ctx context.Context) error {
	states, err := c.opt.Store.HostStates(ctx)
	if err != nil {
		return fmt.Errorf("loading host states: %w", err)
	}
	latest, err := c.opt.Store.LatestSamples(ctx)
	if err != nil {
		return fmt.Errorf("loading latest samples: %w", err)
	}
	lastOK, err := c.opt.Store.LastOKSamples(ctx)
	if err != nil {
		return fmt.Errorf("loading the last good samples: %w", err)
	}
	good := map[string]model.Sample{}
	for _, s := range lastOK {
		good[s.Host] = s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, st := range states {
		c.states[st.Host] = st
	}
	var pis []Pi
	var at time.Time
	for _, s := range latest {
		p := Pi{Host: s.Host, Role: s.Role, Latest: s, State: c.states[s.Host]}
		if ok, found := good[s.Host]; found {
			p.LastOK = &ok
		}
		pis = append(pis, p)
		if s.TS.After(at) {
			at = s.TS
		}
	}
	sortPis(pis)
	c.snap = Snapshot{At: at, Tailnet: TailnetStarting, Pis: pis}
	return nil
}

// Snapshot returns a copy of the latest state.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.snap
	s.Pis = append([]Pi(nil), c.snap.Pis...)
	return s
}

// Run polls on every 30-second boundary of the clock until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	lastPrune := time.Time{}
	for {
		ts := c.opt.Now().Truncate(model.Interval)
		c.Cycle(ctx, ts)
		if now := c.opt.Now(); now.Sub(lastPrune) >= 10*time.Minute {
			if err := c.opt.Store.Prune(ctx, now); err != nil && ctx.Err() == nil {
				c.opt.Log.Error("pruning old samples", "err", err)
			}
			lastPrune = now
		}
		wait := time.Until(c.opt.Now().Truncate(model.Interval).Add(model.Interval))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// answer is one Pi's poll result.
type answer struct {
	peer       Peer
	status     *schema.Status
	outcome    model.Outcome
	schemaSeen int
	// why is for the log only, scrubbed of addresses before it gets there.
	why error
}

// Cycle runs one poll, stamped ts. It never fails: what goes wrong out there
// becomes data, and a store failure is logged while the live view carries on.
func (c *Collector) Cycle(ctx context.Context, ts time.Time) {
	// The cluster node's metrics don't depend on the tailnet: read them
	// whatever happens to the Pis' polls.
	var node NodeMetrics
	var nodeErr error
	var nodeWG sync.WaitGroup
	if c.opt.Node != nil {
		nodeWG.Go(func() {
			nctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
			defer cancel()
			node, nodeErr = c.opt.Node.NodeMetrics(nctx)
		})
	}
	nodeDone := func() bool {
		nodeWG.Wait()
		if nodeErr != nil && ctx.Err() == nil {
			c.opt.Log.Warn("reading node metrics", "err", nodeErr)
		}
		return c.opt.Node != nil && nodeErr == nil
	}

	peers, err := c.opt.Discoverer.Peers(ctx)
	if err != nil {
		// The dashboard's own problem: record no samples, so the history
		// shows a gap rather than Pis that seemed to fail.
		state := tailnetStateOf(err)
		nodeOK := nodeDone()
		c.mu.Lock()
		was := c.snap.Tailnet
		c.snap.Tailnet, c.snap.Node, c.snap.NodeOK = state, node, nodeOK
		c.mu.Unlock()
		if was == state || ctx.Err() != nil {
			return
		}
		if state == TailnetNotTagged {
			c.opt.Log.Error("not polling the Pis: the dashboard's node needs a pre-auth key for tag:dashboard", "reason", Scrub(err.Error()))
		} else {
			c.opt.Log.Warn("not polling the Pis", "reason", Scrub(err.Error()))
		}
		if was == TailnetOK && state == TailnetDown {
			lost := []model.Event{{TS: ts, Host: model.DashboardHost, Kind: model.EventTailnetLost}}
			if err := c.opt.Store.WriteCycle(ctx, nil, lost, nil); err != nil && ctx.Err() == nil {
				c.opt.Log.Error("storing an event", "err", err)
			}
		}
		return
	}
	checkPeers(peers)

	answers := make([]answer, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Go(func() { answers[i] = c.fetch(ctx, p) })
	}
	wg.Wait()
	nodeOK := nodeDone()

	// The exit's public address, for the exit check; dropped after this call.
	var exitIP *netip.Addr
	for _, a := range answers {
		if a.peer.Role == model.RoleExit {
			exitIP = freshIP(a.status, ts)
		}
	}

	c.mu.Lock()
	var samples []model.Sample
	var events []model.Event
	var states []model.HostState
	var pis []Pi
	for _, a := range answers {
		prev, seen := c.states[a.peer.Host]
		if seen && !ts.After(prev.LastTS) {
			// Already polled in this slot, before a restart.
			if old := c.findPi(a.peer.Host); old != nil {
				pis = append(pis, *old)
			}
			continue
		}
		// Log a Pi's trouble when it changes, not every 30 seconds.
		if a.why != nil && (a.outcome != prev.LastOutcome || !seen) {
			c.opt.Log.Info("poll", "host", a.peer.Host, "outcome", a.outcome, "why", Scrub(a.why.Error()))
		} else if a.outcome == model.OutcomeOK && seen && prev.LastOutcome != model.OutcomeOK {
			c.opt.Log.Info("poll", "host", a.peer.Host, "outcome", a.outcome)
		}
		s := c.sample(ts, a, exitIP)
		if err := s.Validate(); err != nil {
			c.mu.Unlock()
			panic(fmt.Sprintf("collect: built an invalid sample: %v", err))
		}
		next, evs := model.Derive(prev, s)
		c.states[s.Host] = next
		samples = append(samples, s)
		events = append(events, evs...)
		states = append(states, next)

		p := Pi{Host: s.Host, Role: s.Role, Latest: s, State: next, Tailnet: Tailnet{
			Known: true, Online: a.peer.Online, LastSeen: a.peer.LastSeen, KeyExpiry: a.peer.KeyExpiry,
		}}
		if s.OK() {
			ok := s
			p.LastOK = &ok
		} else if old := c.findPi(s.Host); old != nil && old.LastOK != nil {
			p.LastOK = old.LastOK
		}
		pis = append(pis, p)
	}
	sortPis(pis)
	if c.snap.Tailnet == TailnetDown {
		events = append(events, model.Event{TS: ts, Host: model.DashboardHost, Kind: model.EventTailnetBack})
	}
	c.snap = Snapshot{At: ts, Tailnet: TailnetOK, Pis: pis, Node: node, NodeOK: nodeOK}
	c.mu.Unlock()

	if len(samples) == 0 && len(events) == 0 {
		return
	}
	if err := c.opt.Store.WriteCycle(ctx, samples, events, states); err != nil && ctx.Err() == nil {
		c.opt.Log.Error("storing samples", "err", err, "samples", len(samples))
	}
}

// findPi must be called with c.mu held.
func (c *Collector) findPi(host string) *Pi {
	for i := range c.snap.Pis {
		if c.snap.Pis[i].Host == host {
			return &c.snap.Pis[i]
		}
	}
	return nil
}

// checkPeers holds the Discoverer's contract. The discoverers are this
// program's own code, so a violation is a bug, and it stops the program.
func checkPeers(peers []Peer) {
	seen := map[string]bool{}
	exits := 0
	for _, p := range peers {
		switch {
		case p.Host == "" || !p.Addr.IsValid():
			panic("collect: discoverer returned a peer with no name or address")
		case seen[p.Host]:
			panic(fmt.Sprintf("collect: discoverer returned %q twice", p.Host))
		case p.Role == model.RoleExit:
			exits++
		case p.Role != model.RoleGateway:
			panic(fmt.Sprintf("collect: discoverer returned role %q", p.Role))
		}
		seen[p.Host] = true
	}
	if exits > 1 {
		panic("collect: discoverer returned more than one exit")
	}
}

func sortPis(pis []Pi) {
	sort.SliceStable(pis, func(i, j int) bool {
		if (pis[i].Role == model.RoleExit) != (pis[j].Role == model.RoleExit) {
			return pis[i].Role == model.RoleExit
		}
		return pis[i].Host < pis[j].Host
	})
}

// fetch asks one Pi for its status.
func (c *Collector) fetch(ctx context.Context, p Peer) answer {
	a := answer{peer: p}
	ctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
	defer cancel()
	url := "http://" + netip.AddrPortFrom(p.Addr, StatusPort).String() + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		panic(fmt.Sprintf("collect: building a request: %v", err))
	}
	req.Close = true
	req.Header.Set("Accept", "application/json")

	fail := func(o model.Outcome, err error) answer {
		a.outcome, a.why = o, err
		return a
	}
	resp, err := c.opt.Client.Do(req)
	if err != nil {
		return fail(transportOutcome(p, err), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fail(transportOutcome(p, err), err)
	}
	if len(body) > maxBody {
		return fail(model.OutcomeInvalid, fmt.Errorf("answer over %d bytes", maxBody))
	}
	if resp.StatusCode != http.StatusOK {
		// Only the code: a Pi's body, its error text included, is never logged.
		return fail(model.OutcomeHTTPError, fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return fail(model.OutcomeInvalid, errors.New("content type isn't application/json"))
	}
	st, err := schema.Parse(body)
	if err != nil {
		if n, ok := schema.IsSchemaError(err); ok {
			a.schemaSeen = n
			return fail(model.OutcomeSchema, err)
		}
		return fail(model.OutcomeInvalid, err)
	}
	if st.Role != p.Role {
		a.why = errors.New("the Pi's role disagrees with its tag; using the tag")
	}
	a.status, a.outcome = st, model.OutcomeOK
	return a
}

// transportOutcome says how a request that got no answer failed.
func transportOutcome(p Peer, err error) model.Outcome {
	var ne net.Error
	switch {
	case !p.Online:
		return model.OutcomeOffline
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return model.OutcomeTimeout
	default:
		return model.OutcomeUnreachable
	}
}

// sample turns one answer into a sample, with rates from the previous one.
// It must be called with c.mu held.
func (c *Collector) sample(ts time.Time, a answer, exitIP *netip.Addr) model.Sample {
	online := a.peer.Online
	s := model.Sample{TS: ts, Host: a.peer.Host, Role: a.peer.Role, Outcome: a.outcome, Online: &online, SchemaSeen: a.schemaSeen}
	if a.peer.Role == model.RoleGateway {
		s.Route = model.RouteNoData
	}
	st := a.status
	if st == nil {
		return s
	}

	if st.UptimeS != nil && *st.UptimeS <= math.MaxInt64 {
		u := int64(*st.UptimeS)
		s.UptimeS = &u
	}
	s.TempC = st.TempC
	s.Undervoltage = st.Undervoltage
	if st.UndervoltageEvents != nil && *st.UndervoltageEvents <= math.MaxInt64 {
		n := int64(*st.UndervoltageEvents)
		s.UndervoltageEvents = &n
	}
	if m := st.Mem; m != nil && m.TotalKB != nil && m.AvailableKB != nil && *m.TotalKB > 0 && *m.AvailableKB <= *m.TotalKB {
		s.MemPct = pct(float64(*m.TotalKB-*m.AvailableKB), float64(*m.TotalKB))
	}
	if d := st.Disk; d != nil && d.UsedB != nil && d.AvailB != nil && *d.UsedB+*d.AvailB > 0 {
		// As df counts it: used of what non-root can use.
		s.DiskPct = pct(float64(*d.UsedB), float64(*d.UsedB+*d.AvailB))
	}

	// Rates come from two consecutive answers. A counter that went down was
	// reset (a reboot, a recreated interface or peer), so that interval has
	// no rate rather than a negative one.
	cur := counters{ts: ts, tunnelSum: map[string]uint64{}}
	if cs := cpuStat(st); cs != nil {
		cur.cpuBusy, cur.cpuTotal, cur.cpuKnown = cs[0], cs[1], true
	}
	cur.tunnelSum = tunnelCounters(a.peer.Role, st.Tunnel)
	prev, hasPrev := c.counters[s.Host]
	c.counters[s.Host] = cur
	dt := ts.Sub(prev.ts).Seconds()
	if hasPrev && dt > 0 && dt <= (3*model.Interval).Seconds() {
		if prev.cpuKnown && cur.cpuKnown && cur.cpuTotal > prev.cpuTotal && cur.cpuBusy >= prev.cpuBusy {
			s.CPUPct = pct(float64(cur.cpuBusy-prev.cpuBusy), float64(cur.cpuTotal-prev.cpuTotal))
		}
		s.TrafficMbps = trafficRate(a.peer.Role, st.Tunnel, prev.tunnelSum, cur.tunnelSum, dt)
	}

	if a.peer.Role == model.RoleGateway {
		var t *schema.Tunnel
		if len(st.Tunnel) > 0 {
			t = &st.Tunnel[0]
		}
		switch {
		case st.Tunnel == nil:
			s.Path = model.PathUnknown
		case t == nil:
			s.Path = model.PathNone
		case t.Path != nil:
			s.Path = model.Path(*t.Path)
		case t.Online != nil && !*t.Online:
			s.Path = model.PathOffline
		}
		if t != nil {
			s.LatencyMS = t.LatencyMS
		}
		if g := st.Gateway; g != nil {
			s.Fallback, s.TVGated = g.Fallback, g.TVPortGated
			if g.TVPort != nil {
				s.TVLink = g.TVPort.Link
			}
		}
		if ip := freshIP(st, ts); ip != nil && exitIP != nil {
			m := *ip == *exitIP
			s.ExitMatch = &m
		}
		s.Route = model.RouteOf(true, s.Fallback, s.Path, s.TVLink)
	}
	return s
}

// cpuStat returns the busy and total ticks, or nil if any is missing.
func cpuStat(st *schema.Status) *[2]uint64 {
	if st.CPU == nil || st.CPU.Stat == nil {
		return nil
	}
	x := st.CPU.Stat
	for _, v := range []*uint64{x.User, x.Nice, x.System, x.Idle, x.IOWait, x.IRQ, x.SoftIRQ, x.Steal} {
		if v == nil {
			return nil
		}
	}
	busy := *x.User + *x.Nice + *x.System + *x.IRQ + *x.SoftIRQ + *x.Steal
	return &[2]uint64{busy, busy + *x.Idle + *x.IOWait}
}

// trafficRate is the tunnel's throughput in Mbit/s, both directions: a
// gateway's one tunnel, or the sum over all of the exit's.
func trafficRate(role model.Role, tunnels []schema.Tunnel, prev, cur map[string]uint64, dt float64) *float64 {
	var peers []string
	if role == model.RoleGateway {
		if len(tunnels) == 0 || tunnels[0].Peer == nil {
			return nil
		}
		peers = []string{*tunnels[0].Peer}
	} else {
		for p := range cur {
			peers = append(peers, p)
		}
	}
	var bytes uint64
	n := 0
	for _, p := range peers {
		now, ok1 := cur[p]
		before, ok2 := prev[p]
		if !ok1 || !ok2 {
			continue
		}
		if now < before {
			return nil
		}
		bytes += now - before
		n++
	}
	if n == 0 {
		return nil
	}
	r := float64(bytes) * 8 / dt / 1e6
	return &r
}

func pct(part, whole float64) *float64 {
	if whole <= 0 {
		return nil
	}
	p := part / whole * 100
	return &p
}

// freshIP is a status's public address, if its check is recent. The age is
// measured on the Pi's own clock, from its "time", so a skewed clock doesn't
// matter; without "time", the dashboard's poll time stands in.
func freshIP(st *schema.Status, ts time.Time) *netip.Addr {
	if st == nil || st.PublicIP == nil || st.PublicIP.Value == nil || st.PublicIP.Checked == nil {
		return nil
	}
	ref := ts
	if st.Time != nil {
		ref = *st.Time
	}
	age := ref.Sub(*st.PublicIP.Checked)
	if age > ExitCheckMaxAge || age < -time.Minute {
		return nil
	}
	// An IPv4 address written as IPv6 is still the same address.
	a := st.PublicIP.Value.Unmap()
	return &a
}

// tunnelCounters sums each tunnel peer's bytes. On the exit, an entry for an
// offline peer is skipped, and when a name repeats, as a re-flashed Pi's old
// node does, the online entry wins, so a stale node's counters never stand in
// for the live one's.
func tunnelCounters(role model.Role, tunnels []schema.Tunnel) map[string]uint64 {
	type pick struct {
		sum    uint64
		online bool
	}
	picks := map[string]pick{}
	for _, t := range tunnels {
		if t.Peer == nil || t.RxBytes == nil || t.TxBytes == nil {
			continue
		}
		online := t.Online != nil && *t.Online
		if role == model.RoleExit && t.Online != nil && !online {
			continue
		}
		if prev, seen := picks[*t.Peer]; seen && (prev.online || !online) {
			continue
		}
		picks[*t.Peer] = pick{*t.RxBytes + *t.TxBytes, online}
	}
	out := make(map[string]uint64, len(picks))
	for peer, p := range picks {
		out[peer] = p.sum
	}
	return out
}
