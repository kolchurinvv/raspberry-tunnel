package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

var t0 = time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dashboard.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func gateway(ts time.Time) model.Sample {
	return model.Sample{
		TS: ts, Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOK, Online: ptr(true),
		UptimeS: ptr(int64(187200)), TempC: ptr(47.2), CPUPct: ptr(12.5), MemPct: ptr(23.0), DiskPct: ptr(12.0),
		TrafficMbps: ptr(14.25), LatencyMS: ptr(11.2), Undervoltage: ptr(false), UndervoltageEvents: ptr(int64(0)),
		Path: model.PathDERP, Fallback: ptr(false), TVLink: ptr(true), TVGated: ptr(true), ExitMatch: ptr(true),
		Route: model.RouteTunnel,
	}
}

func exitDown(ts time.Time) model.Sample {
	return model.Sample{TS: ts, Host: "pi-a", Role: model.RoleExit, Outcome: model.OutcomeTimeout, Online: ptr(true)}
}

func TestWriteAndReadBackSamples(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	in := []model.Sample{gateway(t0), exitDown(t0)}
	if err := s.WriteCycle(ctx, in, nil, nil); err != nil {
		t.Fatalf("WriteCycle: %v", err)
	}
	got, err := s.Samples(ctx, t0, t0.Add(time.Second))
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	// Ordered by time, then host.
	want := []model.Sample{exitDown(t0), gateway(t0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, want)
	}
	// Nulls stay nulls.
	if got[0].TempC != nil || got[0].Fallback != nil || got[0].Path != model.PathUnknown {
		t.Errorf("unanswered sample gained values: %+v", got[0])
	}
}

func TestWriteSkipsADuplicateSlot(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.WriteCycle(ctx, []model.Sample{gateway(t0)}, nil, nil); err != nil {
			t.Fatalf("WriteCycle %d: %v", i, err)
		}
	}
	r, err := s.Rollups(ctx, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[0].N != 1 {
		t.Errorf("a repeated slot was counted twice: %+v", r)
	}
}

func TestRollupsSumTheirSamples(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	a := gateway(t0)
	b := gateway(t0.Add(30 * time.Second))
	b.TempC, b.TrafficMbps, b.Path, b.Route = ptr(49.2), nil, model.PathDirect, model.RouteOwn
	c := gateway(t0.Add(model.RollupWidth)) // the next bucket
	down := model.Sample{TS: t0.Add(time.Minute), Host: "pi-b", Role: model.RoleGateway, Outcome: model.OutcomeOffline, Route: model.RouteNoData}
	off := gateway(t0.Add(90 * time.Second))
	off.TVLink, off.Route, off.TempC, off.TrafficMbps, off.Path = ptr(false), model.RouteTVOff, nil, nil, model.PathDirect
	for _, smp := range []model.Sample{a, b, c, down, off} {
		if err := s.WriteCycle(ctx, []model.Sample{smp}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Rollups(ctx, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 {
		t.Fatalf("rollups = %d, want 2", len(r))
	}
	first := r[0]
	if !first.Bucket.Equal(t0) || first.Host != "pi-b" || first.Role != model.RoleGateway {
		t.Errorf("bucket = %v %s %s", first.Bucket, first.Host, first.Role)
	}
	if first.N != 4 || first.NOK != 3 {
		t.Errorf("n, n_ok = %d, %d; want 4, 3", first.N, first.NOK)
	}
	if v := first.Temp.Value(); v == nil || *v != (47.2+49.2)/2 {
		t.Errorf("mean temp = %v", v)
	}
	if first.Traffic.N != 1 || first.Traffic.Sum != 14.25 {
		t.Errorf("traffic = %+v, want only the known value", first.Traffic)
	}
	if first.Relayed != 1 || first.RouteTunnel != 1 || first.RouteOwn != 1 || first.NoData != 1 || first.RouteHeld != 0 || first.TVOff != 1 {
		t.Errorf("counts = %+v", first.Agg)
	}
	if r[1].N != 1 || !r[1].Bucket.Equal(t0.Add(model.RollupWidth)) {
		t.Errorf("second bucket = %+v", r[1])
	}
}

func TestEventsAndStatesReadBack(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	evs := []model.Event{
		{TS: t0, Host: "pi-b", Kind: model.EventPathChange, Detail: "direct>derp"},
		{TS: t0.Add(time.Minute), Host: "pi-a", Kind: model.EventUnreachable, Detail: "timeout"},
	}
	st := model.HostState{
		Host: "pi-b", Role: model.RoleGateway, LastTS: t0, LastOutcome: model.OutcomeOK, LastOKTS: t0,
		UptimeS: ptr(int64(100)), BootTS: t0.Add(-100 * time.Second), Undervoltage: ptr(false),
		UndervoltageEvents: ptr(int64(0)), Fallback: ptr(false), TunnelUp: ptr(true), LastPath: model.PathDERP,
	}
	if err := s.WriteCycle(ctx, []model.Sample{gateway(t0)}, evs, []model.HostState{st}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Events(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []model.Event{evs[1], evs[0]}) {
		t.Errorf("events = %+v, want newest first", got)
	}
	if got, _ := s.Events(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 1); len(got) != 1 {
		t.Errorf("limit ignored: %d events", len(got))
	}

	states, err := s.HostStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(states, []model.HostState{st}) {
		t.Errorf("states = %+v\nwant %+v", states, st)
	}

	// Updating the state replaces it.
	st.LastTS, st.LastOutcome, st.TunnelUp = t0.Add(30*time.Second), model.OutcomeTimeout, nil
	if err := s.WriteCycle(ctx, nil, nil, []model.HostState{st}); err != nil {
		t.Fatal(err)
	}
	states, _ = s.HostStates(ctx)
	if len(states) != 1 || !reflect.DeepEqual(states[0], st) {
		t.Errorf("updated state = %+v", states)
	}
}

func TestLatestSamples(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ts := t0.Add(time.Duration(i) * 30 * time.Second)
		if err := s.WriteCycle(ctx, []model.Sample{gateway(ts), exitDown(ts)}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LatestSamples(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].TS.Equal(t0.Add(time.Minute)) || got[0].Host != "pi-a" || got[1].Host != "pi-b" {
		t.Errorf("latest = %+v", got)
	}
}

func TestPrune(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := t0.Add(40 * 24 * time.Hour)
	old := gateway(now.Add(-RawRetention - time.Hour))  // raw gone, rollup kept
	ancient := gateway(now.Add(-Retention - time.Hour)) // both gone
	fresh := gateway(now.Add(-time.Hour))
	ev := []model.Event{{TS: ancient.TS, Host: "pi-b", Kind: model.EventReboot}, {TS: fresh.TS, Host: "pi-b", Kind: model.EventReboot}}
	if err := s.WriteCycle(ctx, []model.Sample{ancient, old, fresh}, ev, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	raw, _ := s.Samples(ctx, time.Unix(0, 0), now)
	if len(raw) != 1 || !raw[0].TS.Equal(fresh.TS) {
		t.Errorf("raw after prune = %d samples", len(raw))
	}
	r, _ := s.Rollups(ctx, time.Unix(0, 0), now)
	if len(r) != 2 {
		t.Errorf("rollups after prune = %d, want 2", len(r))
	}
	e, _ := s.Events(ctx, time.Unix(0, 0), now, 10)
	if len(e) != 1 {
		t.Errorf("events after prune = %d, want 1", len(e))
	}
}

func TestReopenKeepsData(t *testing.T) {
	s, path := open(t)
	ctx := context.Background()
	if err := s.WriteCycle(ctx, []model.Sample{gateway(t0)}, nil, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Samples(ctx, t0, t0.Add(time.Second))
	if err != nil || len(got) != 1 {
		t.Errorf("after reopen: %d samples, %v", len(got), err)
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("Open = %v, want a refusal", err)
	}
}

func TestRefusesInvalidWrites(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	bad := gateway(t0)
	bad.Route = model.RouteNone
	if err := s.WriteCycle(ctx, []model.Sample{bad}, nil, nil); err == nil {
		t.Error("stored a gateway sample with no route")
	}
	if err := s.WriteCycle(ctx, nil, []model.Event{{TS: t0, Host: "pi-b", Kind: "gossip"}}, nil); err == nil {
		t.Error("stored an event of unknown kind")
	}
	// Nothing was half-written.
	if got, _ := s.Samples(ctx, time.Unix(0, 0), t0.Add(time.Hour)); len(got) != 0 {
		t.Errorf("found %d samples after refused writes", len(got))
	}
}

// A stored row that breaks the invariants is reported, not drawn.
func TestReadRejectsDamagedRow(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	if err := s.WriteCycle(ctx, []model.Sample{gateway(t0)}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sample SET route = 'sideways'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Samples(ctx, t0, t0.Add(time.Second)); err == nil {
		t.Error("read a damaged row without complaint")
	}
}

func TestLastOKSamples(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	down := gateway(t0.Add(30 * time.Second))
	down.Outcome, down.Route, down.TempC = model.OutcomeTimeout, model.RouteNoData, nil
	for _, smp := range []model.Sample{gateway(t0), down, exitDown(t0)} {
		if err := s.WriteCycle(ctx, []model.Sample{smp}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastOKSamples(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// pi-a never answered; pi-b's last answer is the first one.
	if len(got) != 1 || got[0].Host != "pi-b" || !got[0].TS.Equal(t0) || got[0].TempC == nil {
		t.Errorf("last good = %+v", got)
	}
}
