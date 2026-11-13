// Package store keeps the dashboard's history in SQLite, through the pure-Go
// modernc driver (D3).
//
// Each poll writes one raw sample per Pi and adds it to that Pi's 10-minute
// rollup, in one transaction. Raw samples are kept for 48 hours, for the 1-hour
// view; rollups and events for 30 days, for everything else. So the 30-day
// view reads at most 4,320 rows per Pi, however long the dashboard has run.
//
// Nothing here stores an address or an endpoint: model.Sample has no field
// for one.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const (
	// RawRetention is how long raw samples are kept.
	RawRetention = 48 * time.Hour
	// Retention is how long rollups and events are kept: D2's 30 days, plus
	// a day so the oldest bucket of the 30-day view is whole.
	Retention = 31 * 24 * time.Hour
)

// schemaVersion is the database layout this code writes, in user_version.
const schemaVersion = 1

const ddl = `
CREATE TABLE sample (
  ts           INTEGER NOT NULL,
  host         TEXT    NOT NULL,
  role         TEXT    NOT NULL CHECK (role IN ('exit', 'gateway')),
  outcome      TEXT    NOT NULL,
  schema_seen  INTEGER NOT NULL,
  online       INTEGER,
  uptime_s     INTEGER,
  temp_c       REAL,
  cpu_pct      REAL,
  mem_pct      REAL,
  disk_pct     REAL,
  traffic_mbps REAL,
  latency_ms   REAL,
  undervoltage INTEGER,
  uv_events    INTEGER,
  path         TEXT    NOT NULL,
  fallback     INTEGER,
  tv_link      INTEGER,
  tv_gated     INTEGER,
  exit_match   INTEGER,
  route        TEXT    NOT NULL,
  PRIMARY KEY (host, ts)
) WITHOUT ROWID;
CREATE INDEX sample_ts ON sample (ts);

CREATE TABLE rollup (
  bucket      INTEGER NOT NULL,
  host        TEXT    NOT NULL,
  role        TEXT    NOT NULL CHECK (role IN ('exit', 'gateway')),
  n           INTEGER NOT NULL,
  n_ok        INTEGER NOT NULL,
  temp_sum    REAL NOT NULL, temp_n    INTEGER NOT NULL,
  cpu_sum     REAL NOT NULL, cpu_n     INTEGER NOT NULL,
  mem_sum     REAL NOT NULL, mem_n     INTEGER NOT NULL,
  disk_sum    REAL NOT NULL, disk_n    INTEGER NOT NULL,
  traffic_sum REAL NOT NULL, traffic_n INTEGER NOT NULL,
  lat_sum     REAL NOT NULL, lat_n     INTEGER NOT NULL,
  relayed     INTEGER NOT NULL,
  r_tunnel    INTEGER NOT NULL,
  r_own       INTEGER NOT NULL,
  r_held      INTEGER NOT NULL,
  r_nodata    INTEGER NOT NULL,
  uv          INTEGER NOT NULL,
  PRIMARY KEY (host, bucket)
) WITHOUT ROWID;
CREATE INDEX rollup_bucket ON rollup (bucket);

CREATE TABLE event (
  id     INTEGER PRIMARY KEY,
  ts     INTEGER NOT NULL,
  host   TEXT    NOT NULL,
  kind   TEXT    NOT NULL,
  detail TEXT    NOT NULL
);
CREATE INDEX event_ts ON event (ts);

CREATE TABLE host_state (
  host         TEXT    PRIMARY KEY,
  role         TEXT    NOT NULL CHECK (role IN ('exit', 'gateway')),
  last_ts      INTEGER NOT NULL,
  last_outcome TEXT    NOT NULL,
  last_ok_ts   INTEGER NOT NULL,
  uptime_s     INTEGER,
  boot_ts      INTEGER NOT NULL,
  undervoltage INTEGER,
  uv_events    INTEGER,
  fallback     INTEGER,
  tunnel_up    INTEGER,
  last_path    TEXT    NOT NULL
);
`

// Store is the dashboard's database.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and brings its layout up to
// date. A database written by a newer dashboard is refused rather than
// misread.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	// temp_store(MEMORY): the image has no /tmp for SQLite's temporary files.
	for _, p := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "temp_store(MEMORY)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database answers.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("reading the database version: %w", err)
	}
	switch {
	case v == schemaVersion:
		return nil
	case v > schemaVersion:
		return fmt.Errorf("the database is version %d, newer than this dashboard's %d", v, schemaVersion)
	case v != 0:
		return fmt.Errorf("the database is version %d, which this dashboard can't upgrade", v)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting the migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("creating tables: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("setting the database version: %w", err)
	}
	return tx.Commit()
}

const insertSample = `INSERT INTO sample (ts, host, role, outcome, schema_seen, online, uptime_s, temp_c, cpu_pct,
  mem_pct, disk_pct, traffic_mbps, latency_ms, undervoltage, uv_events, path, fallback, tv_link, tv_gated,
  exit_match, route) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT (host, ts) DO NOTHING`

const upsertRollup = `INSERT INTO rollup (bucket, host, role, n, n_ok, temp_sum, temp_n, cpu_sum, cpu_n, mem_sum,
  mem_n, disk_sum, disk_n, traffic_sum, traffic_n, lat_sum, lat_n, relayed, r_tunnel, r_own, r_held, r_nodata, uv)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT (host, bucket) DO UPDATE SET role = excluded.role,
  n = n + excluded.n, n_ok = n_ok + excluded.n_ok,
  temp_sum = temp_sum + excluded.temp_sum, temp_n = temp_n + excluded.temp_n,
  cpu_sum = cpu_sum + excluded.cpu_sum, cpu_n = cpu_n + excluded.cpu_n,
  mem_sum = mem_sum + excluded.mem_sum, mem_n = mem_n + excluded.mem_n,
  disk_sum = disk_sum + excluded.disk_sum, disk_n = disk_n + excluded.disk_n,
  traffic_sum = traffic_sum + excluded.traffic_sum, traffic_n = traffic_n + excluded.traffic_n,
  lat_sum = lat_sum + excluded.lat_sum, lat_n = lat_n + excluded.lat_n,
  relayed = relayed + excluded.relayed, r_tunnel = r_tunnel + excluded.r_tunnel, r_own = r_own + excluded.r_own,
  r_held = r_held + excluded.r_held, r_nodata = r_nodata + excluded.r_nodata, uv = uv + excluded.uv`

const upsertState = `INSERT INTO host_state (host, role, last_ts, last_outcome, last_ok_ts, uptime_s, boot_ts,
  undervoltage, uv_events, fallback, tunnel_up, last_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT (host) DO UPDATE SET role = excluded.role, last_ts = excluded.last_ts,
  last_outcome = excluded.last_outcome, last_ok_ts = excluded.last_ok_ts, uptime_s = excluded.uptime_s,
  boot_ts = excluded.boot_ts, undervoltage = excluded.undervoltage, uv_events = excluded.uv_events,
  fallback = excluded.fallback, tunnel_up = excluded.tunnel_up, last_path = excluded.last_path`

// WriteCycle stores one poll: its samples, their rollups, the events derived
// from them and each Pi's new derivation state, all or nothing. A sample for a
// slot already stored is skipped, and so is its rollup, so a retried poll
// can't count twice.
func (s *Store) WriteCycle(ctx context.Context, samples []model.Sample, events []model.Event, states []model.HostState) error {
	for _, smp := range samples {
		if err := smp.Validate(); err != nil {
			return fmt.Errorf("refusing an invalid sample: %w", err)
		}
	}
	for _, e := range events {
		if !e.Kind.Valid() || e.Host == "" || e.TS.IsZero() {
			return fmt.Errorf("refusing an invalid event: %+v", e)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a write: %w", err)
	}
	defer tx.Rollback()
	for _, smp := range samples {
		res, err := tx.ExecContext(ctx, insertSample, smp.TS.Unix(), smp.Host, string(smp.Role), string(smp.Outcome),
			smp.SchemaSeen, nb(smp.Online), ni(smp.UptimeS), nf(smp.TempC), nf(smp.CPUPct), nf(smp.MemPct),
			nf(smp.DiskPct), nf(smp.TrafficMbps), nf(smp.LatencyMS), nb(smp.Undervoltage), ni(smp.UndervoltageEvents),
			string(smp.Path), nb(smp.Fallback), nb(smp.TVLink), nb(smp.TVGated), nb(smp.ExitMatch), string(smp.Route))
		if err != nil {
			return fmt.Errorf("storing a sample for %s: %w", smp.Host, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("storing a sample for %s: %w", smp.Host, err)
		} else if n == 0 {
			continue
		}
		a := model.AggOf(smp)
		bucket := smp.TS.Truncate(model.RollupWidth).Unix()
		if _, err := tx.ExecContext(ctx, upsertRollup, bucket, smp.Host, string(smp.Role), a.N, a.NOK,
			a.Temp.Sum, a.Temp.N, a.CPU.Sum, a.CPU.N, a.Mem.Sum, a.Mem.N, a.Disk.Sum, a.Disk.N,
			a.Traffic.Sum, a.Traffic.N, a.Latency.Sum, a.Latency.N, a.Relayed,
			a.RouteTunnel, a.RouteOwn, a.RouteHeld, a.NoData, a.Undervoltage); err != nil {
			return fmt.Errorf("adding to the rollup for %s: %w", smp.Host, err)
		}
	}
	for _, e := range events {
		if _, err := tx.ExecContext(ctx, "INSERT INTO event (ts, host, kind, detail) VALUES (?, ?, ?, ?)",
			e.TS.Unix(), e.Host, string(e.Kind), e.Detail); err != nil {
			return fmt.Errorf("storing an event for %s: %w", e.Host, err)
		}
	}
	for _, st := range states {
		var tunnelUp any
		if st.TunnelUp != nil {
			tunnelUp = *st.TunnelUp
		}
		if _, err := tx.ExecContext(ctx, upsertState, st.Host, string(st.Role), st.LastTS.Unix(), string(st.LastOutcome),
			unixOrZero(st.LastOKTS), ni(st.UptimeS), unixOrZero(st.BootTS), nb(st.Undervoltage),
			ni(st.UndervoltageEvents), nb(st.Fallback), tunnelUp, string(st.LastPath)); err != nil {
			return fmt.Errorf("storing the state of %s: %w", st.Host, err)
		}
	}
	return tx.Commit()
}

// Prune drops raw samples older than RawRetention, and rollups and events
// older than Retention.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	for _, q := range []struct {
		sql    string
		before time.Time
	}{
		{"DELETE FROM sample WHERE ts < ?", now.Add(-RawRetention)},
		{"DELETE FROM rollup WHERE bucket < ?", now.Add(-Retention)},
		{"DELETE FROM event WHERE ts < ?", now.Add(-Retention)},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, q.before.Unix()); err != nil {
			return fmt.Errorf("pruning: %w", err)
		}
	}
	return nil
}

const sampleCols = `ts, host, role, outcome, schema_seen, online, uptime_s, temp_c, cpu_pct, mem_pct, disk_pct,
  traffic_mbps, latency_ms, undervoltage, uv_events, path, fallback, tv_link, tv_gated, exit_match, route`

// Samples returns the raw samples with from <= ts < to, oldest first.
func (s *Store) Samples(ctx context.Context, from, to time.Time) ([]model.Sample, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sampleCols+" FROM sample WHERE ts >= ? AND ts < ? ORDER BY ts, host",
		from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("reading samples: %w", err)
	}
	return scanSamples(rows)
}

// LastOKSamples returns each Pi's latest answered raw sample, so the page
// can show a Pi's last values after a restart during its outage.
func (s *Store) LastOKSamples(ctx context.Context) ([]model.Sample, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sampleCols+` FROM sample AS s
  WHERE outcome = 'ok' AND ts = (SELECT max(ts) FROM sample WHERE host = s.host AND outcome = 'ok') ORDER BY host`)
	if err != nil {
		return nil, fmt.Errorf("reading the last good samples: %w", err)
	}
	return scanSamples(rows)
}

// LatestSamples returns each Pi's latest raw sample.
func (s *Store) LatestSamples(ctx context.Context) ([]model.Sample, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sampleCols+` FROM sample AS s
  WHERE ts = (SELECT max(ts) FROM sample WHERE host = s.host) ORDER BY host`)
	if err != nil {
		return nil, fmt.Errorf("reading the latest samples: %w", err)
	}
	return scanSamples(rows)
}

func scanSamples(rows *sql.Rows) ([]model.Sample, error) {
	defer rows.Close()
	var out []model.Sample
	for rows.Next() {
		var (
			ts, schemaSeen                               int64
			host, role, outcome, path, route             string
			online, uv, fallback, tvLink, tvGated, match sql.NullBool
			uptime, uvEvents                             sql.NullInt64
			temp, cpu, mem, disk, traffic, lat           sql.NullFloat64
		)
		if err := rows.Scan(&ts, &host, &role, &outcome, &schemaSeen, &online, &uptime, &temp, &cpu, &mem, &disk,
			&traffic, &lat, &uv, &uvEvents, &path, &fallback, &tvLink, &tvGated, &match, &route); err != nil {
			return nil, fmt.Errorf("reading a sample: %w", err)
		}
		smp := model.Sample{
			TS: time.Unix(ts, 0).UTC(), Host: host, Role: model.Role(role), Outcome: model.Outcome(outcome),
			SchemaSeen: int(schemaSeen), Online: pb(online), UptimeS: pi(uptime), TempC: pf(temp), CPUPct: pf(cpu),
			MemPct: pf(mem), DiskPct: pf(disk), TrafficMbps: pf(traffic), LatencyMS: pf(lat), Undervoltage: pb(uv),
			UndervoltageEvents: pi(uvEvents), Path: model.Path(path), Fallback: pb(fallback), TVLink: pb(tvLink),
			TVGated: pb(tvGated), ExitMatch: pb(match), Route: model.Route(route),
		}
		// Only this code writes the table, so a row that doesn't validate
		// means a bug or a damaged file: say so rather than draw it.
		if err := smp.Validate(); err != nil {
			return nil, fmt.Errorf("a stored sample is invalid: %w", err)
		}
		out = append(out, smp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading samples: %w", err)
	}
	return out, nil
}

// Rollups returns the rollups with from <= bucket < to, oldest first.
func (s *Store) Rollups(ctx context.Context, from, to time.Time) ([]model.Rollup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket, host, role, n, n_ok, temp_sum, temp_n, cpu_sum, cpu_n,
  mem_sum, mem_n, disk_sum, disk_n, traffic_sum, traffic_n, lat_sum, lat_n, relayed, r_tunnel, r_own, r_held,
  r_nodata, uv FROM rollup WHERE bucket >= ? AND bucket < ? ORDER BY bucket, host`, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("reading rollups: %w", err)
	}
	defer rows.Close()
	var out []model.Rollup
	for rows.Next() {
		var r model.Rollup
		var bucket int64
		var role string
		a := &r.Agg
		if err := rows.Scan(&bucket, &r.Host, &role, &a.N, &a.NOK, &a.Temp.Sum, &a.Temp.N, &a.CPU.Sum, &a.CPU.N,
			&a.Mem.Sum, &a.Mem.N, &a.Disk.Sum, &a.Disk.N, &a.Traffic.Sum, &a.Traffic.N, &a.Latency.Sum, &a.Latency.N,
			&a.Relayed, &a.RouteTunnel, &a.RouteOwn, &a.RouteHeld, &a.NoData, &a.Undervoltage); err != nil {
			return nil, fmt.Errorf("reading a rollup: %w", err)
		}
		r.Bucket, r.Role = time.Unix(bucket, 0).UTC(), model.Role(role)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading rollups: %w", err)
	}
	return out, nil
}

// Events returns up to limit events with from <= ts < to, newest first.
func (s *Store) Events(ctx context.Context, from, to time.Time, limit int) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, host, kind, detail FROM event
  WHERE ts >= ? AND ts < ? ORDER BY ts DESC, id DESC LIMIT ?`, from.Unix(), to.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var ts int64
		var kind string
		if err := rows.Scan(&ts, &e.Host, &kind, &e.Detail); err != nil {
			return nil, fmt.Errorf("reading an event: %w", err)
		}
		e.TS, e.Kind = time.Unix(ts, 0).UTC(), model.EventKind(kind)
		if !e.Kind.Valid() {
			return nil, fmt.Errorf("a stored event has kind %q", kind)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}
	return out, nil
}

// HostStates returns every Pi's derivation state.
func (s *Store) HostStates(ctx context.Context) ([]model.HostState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host, role, last_ts, last_outcome, last_ok_ts, uptime_s, boot_ts,
  undervoltage, uv_events, fallback, tunnel_up, last_path FROM host_state ORDER BY host`)
	if err != nil {
		return nil, fmt.Errorf("reading host states: %w", err)
	}
	defer rows.Close()
	var out []model.HostState
	for rows.Next() {
		var st model.HostState
		var role, outcome, path string
		var lastTS, lastOK, boot int64
		var uptime, uvEvents sql.NullInt64
		var uv, fallback, up sql.NullBool
		if err := rows.Scan(&st.Host, &role, &lastTS, &outcome, &lastOK, &uptime, &boot, &uv, &uvEvents,
			&fallback, &up, &path); err != nil {
			return nil, fmt.Errorf("reading a host state: %w", err)
		}
		st.Role, st.LastOutcome, st.LastPath = model.Role(role), model.Outcome(outcome), model.Path(path)
		st.LastTS, st.LastOKTS, st.BootTS = time.Unix(lastTS, 0).UTC(), timeOrZero(lastOK), timeOrZero(boot)
		st.UptimeS, st.UndervoltageEvents = pi(uptime), pi(uvEvents)
		st.Undervoltage, st.Fallback, st.TunnelUp = pb(uv), pb(fallback), pb(up)
		if !st.LastOutcome.Valid() || !st.LastPath.Valid() {
			return nil, fmt.Errorf("the stored state of %s is invalid", st.Host)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading host states: %w", err)
	}
	return out, nil
}

func nb(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func nf(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func ni(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

func pb(n sql.NullBool) *bool {
	if !n.Valid {
		return nil
	}
	return &n.Bool
}

func pf(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

func pi(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(u int64) time.Time {
	if u == 0 {
		return time.Time{}
	}
	return time.Unix(u, 0).UTC()
}
