// Package schema parses the Pis' status endpoint, schema 1, as D2's contract
// defines it.
//
// The parse is strict about shape and lenient about values. Every field the
// contract names must be present, with the right type, but any value may be
// null, because the contract says an unreadable value is sent as null rather
// than left out. Fields the contract doesn't name are ignored, since new ones
// may arrive within schema 1. A schema number other than 1 is a *SchemaError,
// and every other violation is a *FieldError.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"time"
)

// Version is the only schema this package reads.
const Version = 1

// Role is a Pi's role in the tunnel.
type Role string

const (
	RoleExit    Role = "exit"
	RoleGateway Role = "gateway"
)

// Path is how a tunnel entry reaches its peer.
type Path string

const (
	PathDirect    Path = "direct"
	PathPeerRelay Path = "peer_relay"
	PathDERP      Path = "derp"
	PathIdle      Path = "idle"
	PathOffline   Path = "offline"
)

// Status is one answer from GET /status.
type Status struct {
	Schema             int
	Host               string
	Role               Role
	Time               *time.Time
	UptimeS            *uint64
	CPU                *CPU
	TempC              *float64
	Undervoltage       *bool
	UndervoltageEvents *uint64
	Mem                *Mem
	Disk               *Disk
	// Net is nil when the Pi sent null.
	Net map[string]*Counters
	// Tunnel is nil when the Pi sent null, and empty when it sent [].
	Tunnel   []Tunnel
	PublicIP *PublicIP
	Gateway  *Gateway
}

// CPU holds the load averages and the cumulative /proc/stat ticks.
type CPU struct {
	Load *[3]float64
	Stat *CPUStat
}

// CPUStat is the "cpu" line of /proc/stat, in USER_HZ ticks.
type CPUStat struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal *uint64
}

// Mem is in KiB, as /proc/meminfo reports it.
type Mem struct {
	TotalKB, AvailableKB *uint64
}

// Disk is the root filesystem, in bytes.
type Disk struct {
	TotalB, UsedB, AvailB *uint64
}

// Counters are cumulative byte counters.
type Counters struct {
	RxBytes, TxBytes *uint64
}

// Tunnel is one peer in the Pi's tunnel list.
type Tunnel struct {
	Peer      *string
	Online    *bool
	Path      *Path
	Relay     *string
	RxBytes   *uint64
	TxBytes   *uint64
	LatencyMS *float64
}

// PublicIP is the Pi's latest public address check. The address is for the
// exit check only and must never be stored or published.
type PublicIP struct {
	Value   *netip.Addr
	Checked *time.Time
}

// Gateway holds the fields only a gateway sends.
type Gateway struct {
	TVPort      *TVPort
	TVPortGated *bool
	Fallback    *bool
}

// TVPort is the state of the port the TV is plugged into.
type TVPort struct {
	Link      *bool
	SpeedMbps *float64
}

// SchemaError is an answer whose schema number isn't 1.
type SchemaError struct {
	Got int
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("schema %d, expected %d", e.Got, Version)
}

// FieldError is an answer that breaks the contract's shape.
type FieldError struct {
	Path string
	Msg  string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Msg)
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// Parse reads one 200 answer from GET /status.
func Parse(body []byte) (*Status, error) {
	d := &decoder{}
	top := d.object("$", body)
	if d.err != nil {
		return nil, d.err
	}
	if top == nil {
		return nil, &FieldError{Path: "$", Msg: "is null"}
	}
	if err := checkSchema(d, top); err != nil {
		return nil, err
	}

	s := &Status{Schema: Version}
	if h := d.str("host", d.field(top, "$", "host")); h != nil {
		if !hostname.MatchString(*h) {
			d.fail("host", "is not a hostname")
		}
		s.Host = *h
	} else if d.err == nil {
		d.fail("host", "is null")
	}
	if r := d.str("role", d.field(top, "$", "role")); r != nil {
		switch Role(*r) {
		case RoleExit, RoleGateway:
			s.Role = Role(*r)
		default:
			d.fail("role", "is not a known role")
		}
	} else if d.err == nil {
		d.fail("role", "is null")
	}
	s.Time = d.time("time", d.field(top, "$", "time"))
	s.UptimeS = d.uint("uptime_s", d.field(top, "$", "uptime_s"))
	s.CPU = parseCPU(d, d.field(top, "$", "cpu"))
	s.TempC = d.num("temp_c", d.field(top, "$", "temp_c"))
	s.Undervoltage = d.boolean("undervoltage", d.field(top, "$", "undervoltage"))
	s.UndervoltageEvents = d.uint("undervoltage_events", d.field(top, "$", "undervoltage_events"))
	s.Mem = parseMem(d, d.field(top, "$", "mem"))
	s.Disk = parseDisk(d, d.field(top, "$", "disk"))
	s.Net = parseNet(d, d.field(top, "$", "net"))
	s.Tunnel = parseTunnels(d, d.field(top, "$", "tunnel"))
	s.PublicIP = parsePublicIP(d, d.field(top, "$", "public_ip"))
	s.Gateway = parseGateway(d, d.field(top, "$", "gateway"))
	if d.err != nil {
		return nil, d.err
	}
	return s, nil
}

// ParseError reads the body of a 500 answer, {"schema": 1, "error": "..."},
// and returns its message. The message comes from the Pi and may name local
// details, so it belongs in the dashboard's own log, never on the page.
func ParseError(body []byte) (string, error) {
	d := &decoder{}
	top := d.object("$", body)
	if d.err != nil {
		return "", d.err
	}
	if top == nil {
		return "", &FieldError{Path: "$", Msg: "is null"}
	}
	if err := checkSchema(d, top); err != nil {
		return "", err
	}
	msg := d.str("error", d.field(top, "$", "error"))
	if d.err != nil {
		return "", d.err
	}
	if msg == nil {
		return "", &FieldError{Path: "error", Msg: "is null"}
	}
	return *msg, nil
}

func checkSchema(d *decoder, top map[string]json.RawMessage) error {
	raw := d.field(top, "$", "schema")
	if d.err != nil {
		return d.err
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil || isNull(raw) {
		return &FieldError{Path: "schema", Msg: "is not an integer"}
	}
	if n != Version {
		return &SchemaError{Got: n}
	}
	return nil
}

func parseCPU(d *decoder, raw json.RawMessage) *CPU {
	m := d.object("cpu", raw)
	if m == nil {
		return nil
	}
	c := &CPU{}
	if load := d.field(m, "cpu", "load"); load != nil && !isNull(load) {
		var arr []json.RawMessage
		if err := json.Unmarshal(load, &arr); err != nil {
			d.fail("cpu.load", "is not an array")
		} else if len(arr) != 3 {
			d.fail("cpu.load", fmt.Sprintf("has %d values, expected 3", len(arr)))
		} else {
			var l [3]float64
			for i, v := range arr {
				p := d.num(fmt.Sprintf("cpu.load[%d]", i), v)
				if p == nil {
					d.fail(fmt.Sprintf("cpu.load[%d]", i), "is null")
					break
				}
				l[i] = *p
			}
			c.Load = &l
		}
	}
	if sm := d.object("cpu.stat", d.field(m, "cpu", "stat")); sm != nil {
		u := func(k string) *uint64 { return d.uint("cpu.stat."+k, d.field(sm, "cpu.stat", k)) }
		c.Stat = &CPUStat{
			User: u("user"), Nice: u("nice"), System: u("system"), Idle: u("idle"),
			IOWait: u("iowait"), IRQ: u("irq"), SoftIRQ: u("softirq"), Steal: u("steal"),
		}
	}
	return c
}

func parseMem(d *decoder, raw json.RawMessage) *Mem {
	m := d.object("mem", raw)
	if m == nil {
		return nil
	}
	return &Mem{
		TotalKB:     d.uint("mem.total_kb", d.field(m, "mem", "total_kb")),
		AvailableKB: d.uint("mem.available_kb", d.field(m, "mem", "available_kb")),
	}
}

func parseDisk(d *decoder, raw json.RawMessage) *Disk {
	m := d.object("disk", raw)
	if m == nil {
		return nil
	}
	return &Disk{
		TotalB: d.uint("disk.total_b", d.field(m, "disk", "total_b")),
		UsedB:  d.uint("disk.used_b", d.field(m, "disk", "used_b")),
		AvailB: d.uint("disk.avail_b", d.field(m, "disk", "avail_b")),
	}
}

func parseCounters(d *decoder, path string, raw json.RawMessage) *Counters {
	m := d.object(path, raw)
	if m == nil {
		return nil
	}
	return &Counters{
		RxBytes: d.uint(path+".rx_bytes", d.field(m, path, "rx_bytes")),
		TxBytes: d.uint(path+".tx_bytes", d.field(m, path, "tx_bytes")),
	}
}

func parseNet(d *decoder, raw json.RawMessage) map[string]*Counters {
	m := d.object("net", raw)
	if m == nil {
		return nil
	}
	out := make(map[string]*Counters, len(m))
	for name, v := range m {
		out[name] = parseCounters(d, "net."+name, v)
	}
	return out
}

func parseTunnels(d *decoder, raw json.RawMessage) []Tunnel {
	if raw == nil || isNull(raw) {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		d.fail("tunnel", "is not an array")
		return nil
	}
	out := make([]Tunnel, 0, len(arr))
	for i, v := range arr {
		p := fmt.Sprintf("tunnel[%d]", i)
		m := d.object(p, v)
		if m == nil {
			if d.err == nil {
				d.fail(p, "is null")
			}
			return nil
		}
		t := Tunnel{
			Peer:      d.str(p+".peer", d.field(m, p, "peer")),
			Online:    d.boolean(p+".online", d.field(m, p, "online")),
			Relay:     d.str(p+".relay", d.field(m, p, "relay")),
			RxBytes:   d.uint(p+".rx_bytes", d.field(m, p, "rx_bytes")),
			TxBytes:   d.uint(p+".tx_bytes", d.field(m, p, "tx_bytes")),
			LatencyMS: d.num(p+".latency_ms", d.field(m, p, "latency_ms")),
		}
		if t.Peer != nil && !hostname.MatchString(*t.Peer) {
			d.fail(p+".peer", "is not a hostname")
		}
		if ps := d.str(p+".path", d.field(m, p, "path")); ps != nil {
			switch Path(*ps) {
			case PathDirect, PathPeerRelay, PathDERP, PathIdle, PathOffline:
				path := Path(*ps)
				t.Path = &path
			default:
				d.fail(p+".path", "is not a known path")
			}
		}
		if t.LatencyMS != nil && *t.LatencyMS < 0 {
			d.fail(p+".latency_ms", "is negative")
		}
		out = append(out, t)
	}
	return out
}

func parsePublicIP(d *decoder, raw json.RawMessage) *PublicIP {
	m := d.object("public_ip", raw)
	if m == nil {
		return nil
	}
	p := &PublicIP{Checked: d.time("public_ip.checked", d.field(m, "public_ip", "checked"))}
	if v := d.str("public_ip.value", d.field(m, "public_ip", "value")); v != nil {
		a, err := netip.ParseAddr(*v)
		if err != nil {
			// Don't echo the value: it's an address, and errors reach logs.
			d.fail("public_ip.value", "is not an IP address")
		} else {
			p.Value = &a
		}
	}
	return p
}

func parseGateway(d *decoder, raw json.RawMessage) *Gateway {
	m := d.object("gateway", raw)
	if m == nil {
		return nil
	}
	g := &Gateway{
		TVPortGated: d.boolean("gateway.tv_port_gated", d.field(m, "gateway", "tv_port_gated")),
		Fallback:    d.boolean("gateway.fallback", d.field(m, "gateway", "fallback")),
	}
	if pm := d.object("gateway.tv_port", d.field(m, "gateway", "tv_port")); pm != nil {
		g.TVPort = &TVPort{
			Link:      d.boolean("gateway.tv_port.link", d.field(pm, "gateway.tv_port", "link")),
			SpeedMbps: d.num("gateway.tv_port.speed_mbps", d.field(pm, "gateway.tv_port", "speed_mbps")),
		}
		if g.TVPort.SpeedMbps != nil && *g.TVPort.SpeedMbps < 0 {
			d.fail("gateway.tv_port.speed_mbps", "is negative")
		}
	}
	return g
}

// decoder keeps the first error and turns every later call into a no-op, so
// the parse reads as a list of fields rather than a ladder of error checks.
type decoder struct {
	err error
}

func (d *decoder) fail(path, msg string) {
	if d.err == nil {
		d.err = &FieldError{Path: path, Msg: msg}
	}
}

func isNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// field returns a required key's raw value. A missing key is an error, since
// the contract sends null rather than leaving a field out.
func (d *decoder) field(m map[string]json.RawMessage, path, key string) json.RawMessage {
	if d.err != nil || m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		p := key
		if path != "$" {
			p = path + "." + key
		}
		d.fail(p, "is missing")
		return nil
	}
	return v
}

// object returns nil for null, and fails for anything but an object.
func (d *decoder) object(path string, raw json.RawMessage) map[string]json.RawMessage {
	if d.err != nil || raw == nil || isNull(raw) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		d.fail(path, "is not an object")
		return nil
	}
	return m
}

func (d *decoder) str(path string, raw json.RawMessage) *string {
	if d.err != nil || raw == nil || isNull(raw) {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		d.fail(path, "is not a string")
		return nil
	}
	return &s
}

func (d *decoder) boolean(path string, raw json.RawMessage) *bool {
	if d.err != nil || raw == nil || isNull(raw) {
		return nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		d.fail(path, "is not a boolean")
		return nil
	}
	return &b
}

func (d *decoder) num(path string, raw json.RawMessage) *float64 {
	if d.err != nil || raw == nil || isNull(raw) {
		return nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		d.fail(path, "is not a number")
		return nil
	}
	return &f
}

// uint accepts only a non-negative integer literal. A counter is never
// negative, so a negative one breaks the contract.
func (d *decoder) uint(path string, raw json.RawMessage) *uint64 {
	if d.err != nil || raw == nil || isNull(raw) {
		return nil
	}
	var n uint64
	if err := json.Unmarshal(raw, &n); err != nil {
		var f float64
		if json.Unmarshal(raw, &f) == nil && f < 0 {
			d.fail(path, "is negative")
		} else {
			d.fail(path, "is not a non-negative integer")
		}
		return nil
	}
	return &n
}

func (d *decoder) time(path string, raw json.RawMessage) *time.Time {
	s := d.str(path, raw)
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		d.fail(path, "is not an RFC 3339 time")
		return nil
	}
	return &t
}

// IsSchemaError reports whether err is a wrong schema number, and which.
func IsSchemaError(err error) (int, bool) {
	var se *SchemaError
	if errors.As(err, &se) {
		return se.Got, true
	}
	return 0, false
}
