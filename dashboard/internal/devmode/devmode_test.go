package devmode_test

import (
	"context"
	"html"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/collect"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/devmode"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/store"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/view"
	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/web"
)

// Dev mode runs the real pipeline over fake Pis: the backfill must store a
// month of history, and every range must render from it.
func TestDevMode(t *testing.T) {
	for _, c := range []struct {
		scenario devmode.Scenario
		gateways int
		banner   string
	}{
		{devmode.Healthy, 1, "All good"},
		{devmode.Relayed, 2, "Pi B's tunnel is relayed"},
		{devmode.Fallback, 3, "are on their own connections"},
		{devmode.CutOff, 1, "The dashboard can't see the tailnet"},
	} {
		t.Run(string(c.scenario), func(t *testing.T) {
			ctx := context.Background()
			end := time.Now().Truncate(model.Interval)
			sim, err := devmode.New(c.scenario, c.gateways, end)
			if err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dashboard.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			opt := collect.Options{Store: db, Log: log}
			if err := devmode.Backfill(ctx, sim, db, opt); err != nil {
				t.Fatal(err)
			}

			// Read back: rollups across the month, raw samples for the last two days.
			rolls, err := db.Rollups(ctx, end.Add(-31*24*time.Hour), end.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if want := 30 * 24 * 6 * (c.gateways + 1); len(rolls) < want-(c.gateways+1)*2 {
				t.Errorf("rollups = %d, want about %d", len(rolls), want)
			}
			raw, err := db.Samples(ctx, end.Add(-time.Hour), end.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			want := 121
			if c.scenario == devmode.CutOff {
				want = 121 - 19 // a gap of 19 slots, from 9 minutes ago to now, while cut off
			}
			if len(raw) != want*(c.gateways+1) {
				t.Errorf("raw samples in the last hour = %d", len(raw))
			}
			evs, err := db.Events(ctx, end.Add(-31*24*time.Hour), end.Add(time.Minute), 100)
			if err != nil || len(evs) == 0 {
				t.Errorf("no events from a month with outages: %v", err)
			}

			opt.Discoverer, opt.Client, opt.Node = sim, sim.Client(), sim
			col := collect.New(opt)
			if err := col.Load(ctx); err != nil {
				t.Fatal(err)
			}
			col.Cycle(ctx, end.Add(model.Interval))
			w, err := web.New(web.Options{Snapshot: col.Snapshot, Store: db, Loc: time.UTC, Dev: true, Log: log,
				Now: func() time.Time { return end.Add(model.Interval + time.Second) }})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range view.Ranges {
				body, err := w.Render(ctx, r, false)
				if err != nil {
					t.Fatalf("%s: %v", r.Key, err)
				}
				page := html.UnescapeString(string(body))
				for _, want := range []string{"Sample data", c.banner, "Tunnel traffic", "Exit route"} {
					if !strings.Contains(page, want) {
						t.Errorf("%s: page lacks %q", r.Key, want)
					}
				}
				if strings.Contains(page, "ZgotmplZ") {
					t.Errorf("%s: the template refused a value", r.Key)
				}
			}
		})
	}
}

func TestDevModeRejects(t *testing.T) {
	if _, err := devmode.ParseScenario("chaos"); err == nil {
		t.Error("ParseScenario accepted chaos")
	}
	if _, err := devmode.New(devmode.Healthy, 4, time.Now()); err == nil {
		t.Error("New accepted 4 gateways")
	}
}
