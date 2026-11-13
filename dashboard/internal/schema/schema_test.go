package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// d2Example is D2's schema 1 example, with its comments taken out.
const d2Example = `{
  "schema": 1,
  "host": "pi-b",
  "role": "gateway",
  "time": "2026-11-13T20:40:00Z",
  "uptime_s": 187200,
  "cpu": {
    "load": [0.42, 0.31, 0.22],
    "stat": {
      "user": 0, "nice": 0, "system": 0,
      "idle": 0, "iowait": 0, "irq": 0, "softirq": 0, "steal": 0
    }
  },
  "temp_c": 47.2,
  "undervoltage": false,
  "undervoltage_events": 0,
  "mem": { "total_kb": 0, "available_kb": 0 },
  "disk": { "total_b": 0, "used_b": 0, "avail_b": 0 },
  "net": {
    "wan0": { "rx_bytes": 0, "tx_bytes": 0 }
  },
  "tunnel": [
    {
      "peer": "pi-a",
      "online": true,
      "path": "direct",
      "relay": null,
      "rx_bytes": 0, "tx_bytes": 0,
      "latency_ms": 11.2
    }
  ],
  "public_ip": {
    "value": "203.0.113.7",
    "checked": "2026-11-13T20:36:00Z"
  },
  "gateway": {
    "tv_port": { "link": true, "speed_mbps": 100 },
    "tv_port_gated": true,
    "fallback": false
  }
}`

func example(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(d2Example), &m); err != nil {
		t.Fatalf("example: %v", err)
	}
	return m
}

func encode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestParseD2Example(t *testing.T) {
	s, err := Parse([]byte(d2Example))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Host != "pi-b" || s.Role != RoleGateway || s.Schema != 1 {
		t.Errorf("host, role, schema = %q, %q, %d", s.Host, s.Role, s.Schema)
	}
	if s.UptimeS == nil || *s.UptimeS != 187200 {
		t.Errorf("uptime_s = %v", s.UptimeS)
	}
	if s.CPU == nil || s.CPU.Load == nil || s.CPU.Load[0] != 0.42 || s.CPU.Stat == nil || s.CPU.Stat.Idle == nil {
		t.Errorf("cpu = %+v", s.CPU)
	}
	if s.TempC == nil || *s.TempC != 47.2 {
		t.Errorf("temp_c = %v", s.TempC)
	}
	if len(s.Tunnel) != 1 || *s.Tunnel[0].Peer != "pi-a" || *s.Tunnel[0].Path != PathDirect || s.Tunnel[0].Relay != nil {
		t.Errorf("tunnel = %+v", s.Tunnel)
	}
	if s.Tunnel[0].LatencyMS == nil || *s.Tunnel[0].LatencyMS != 11.2 {
		t.Errorf("latency_ms = %v", s.Tunnel[0].LatencyMS)
	}
	if s.PublicIP == nil || s.PublicIP.Value == nil || s.PublicIP.Value.String() != "203.0.113.7" {
		t.Errorf("public_ip = %+v", s.PublicIP)
	}
	if s.Gateway == nil || *s.Gateway.Fallback || !*s.Gateway.TVPortGated || !*s.Gateway.TVPort.Link || *s.Gateway.TVPort.SpeedMbps != 100 {
		t.Errorf("gateway = %+v", s.Gateway)
	}
	if c := s.Net["wan0"]; c == nil || c.RxBytes == nil {
		t.Errorf("net = %+v", s.Net)
	}
}

// Every value may be null: the contract sends null for what can't be read.
func TestParseNulls(t *testing.T) {
	m := example(t)
	for _, k := range []string{"time", "uptime_s", "cpu", "temp_c", "undervoltage", "undervoltage_events", "mem", "disk", "net", "tunnel", "public_ip", "gateway"} {
		m[k] = nil
	}
	s, err := Parse(encode(t, m))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Time != nil || s.UptimeS != nil || s.CPU != nil || s.TempC != nil || s.Undervoltage != nil ||
		s.UndervoltageEvents != nil || s.Mem != nil || s.Disk != nil || s.Net != nil || s.Tunnel != nil ||
		s.PublicIP != nil || s.Gateway != nil {
		t.Errorf("nulls not kept as nil: %+v", s)
	}

	// Nested values may be null too.
	m = example(t)
	m["cpu"] = map[string]any{"load": nil, "stat": nil}
	m["mem"] = map[string]any{"total_kb": nil, "available_kb": nil}
	m["tunnel"] = []any{map[string]any{"peer": "pi-a", "online": nil, "path": nil, "relay": nil, "rx_bytes": nil, "tx_bytes": nil, "latency_ms": nil}}
	m["public_ip"] = map[string]any{"value": nil, "checked": nil}
	m["gateway"] = map[string]any{"tv_port": nil, "tv_port_gated": nil, "fallback": nil}
	s, err = Parse(encode(t, m))
	if err != nil {
		t.Fatalf("Parse with nested nulls: %v", err)
	}
	if s.CPU.Load != nil || s.CPU.Stat != nil || s.Mem.TotalKB != nil || s.Tunnel[0].Path != nil || s.PublicIP.Value != nil || s.Gateway.Fallback != nil {
		t.Errorf("nested nulls not kept as nil")
	}
}

func TestParseEmptyTunnel(t *testing.T) {
	m := example(t)
	m["tunnel"] = []any{}
	s, err := Parse(encode(t, m))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Tunnel == nil || len(s.Tunnel) != 0 {
		t.Errorf("tunnel [] = %#v, want empty and non-nil", s.Tunnel)
	}
}

func TestParseUnknownFieldsAllowed(t *testing.T) {
	m := example(t)
	m["new_field"] = map[string]any{"x": 1}
	m["gateway"].(map[string]any)["new_gateway_field"] = true
	m["net"].(map[string]any)["eth9"] = map[string]any{"rx_bytes": 1, "tx_bytes": 2}
	if _, err := Parse(encode(t, m)); err != nil {
		t.Fatalf("unknown fields rejected: %v", err)
	}
}

func TestParseWrongSchema(t *testing.T) {
	for _, n := range []int{0, 2, 17} {
		m := example(t)
		m["schema"] = n
		_, err := Parse(encode(t, m))
		got, ok := IsSchemaError(err)
		if !ok || got != n {
			t.Errorf("schema %d: err = %v, want a SchemaError for %d", n, err, n)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		path   string
	}{
		{"missing schema", func(m map[string]any) { delete(m, "schema") }, "schema"},
		{"schema not a number", func(m map[string]any) { m["schema"] = "1" }, "schema"},
		{"missing top field", func(m map[string]any) { delete(m, "temp_c") }, "temp_c"},
		{"missing nested field", func(m map[string]any) { delete(m["mem"].(map[string]any), "available_kb") }, "mem.available_kb"},
		{"missing tunnel field", func(m map[string]any) { delete(m["tunnel"].([]any)[0].(map[string]any), "path") }, "tunnel[0].path"},
		{"unknown role", func(m map[string]any) { m["role"] = "router" }, "role"},
		{"unknown path", func(m map[string]any) { m["tunnel"].([]any)[0].(map[string]any)["path"] = "teleport" }, "tunnel[0].path"},
		{"negative counter", func(m map[string]any) { m["tunnel"].([]any)[0].(map[string]any)["rx_bytes"] = -5 }, "tunnel[0].rx_bytes"},
		{"fractional counter", func(m map[string]any) { m["uptime_s"] = 1.5 }, "uptime_s"},
		{"number as string", func(m map[string]any) { m["temp_c"] = "47.2" }, "temp_c"},
		{"bool as number", func(m map[string]any) { m["undervoltage"] = 0 }, "undervoltage"},
		{"bad time", func(m map[string]any) { m["time"] = "yesterday" }, "time"},
		{"bad address", func(m map[string]any) { m["public_ip"].(map[string]any)["value"] = "not-an-ip" }, "public_ip.value"},
		{"load of two", func(m map[string]any) { m["cpu"].(map[string]any)["load"] = []any{1, 2} }, "cpu.load"},
		{"tunnel not an array", func(m map[string]any) { m["tunnel"] = map[string]any{} }, "tunnel"},
		{"tunnel entry null", func(m map[string]any) { m["tunnel"] = []any{nil} }, "tunnel[0]"},
		{"net entry not an object", func(m map[string]any) { m["net"].(map[string]any)["wan0"] = 5 }, "net.wan0"},
		{"negative latency", func(m map[string]any) { m["tunnel"].([]any)[0].(map[string]any)["latency_ms"] = -1 }, "tunnel[0].latency_ms"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := example(t)
			c.mutate(m)
			_, err := Parse(encode(t, m))
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("err = %v, want a FieldError", err)
			}
			if fe.Path != c.path {
				t.Errorf("path = %q, want %q", fe.Path, c.path)
			}
		})
	}
}

func TestParseNotJSON(t *testing.T) {
	for _, body := range []string{"", "null", "[]", "{", `{"schema":1} trailing`, "<html>"} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) succeeded", body)
		}
	}
}

// A rejected address must not be echoed into the error, which reaches logs.
func TestParseErrorDoesNotEchoAddress(t *testing.T) {
	m := example(t)
	m["public_ip"].(map[string]any)["value"] = "203.0.113.999"
	_, err := Parse(encode(t, m))
	if err == nil || strings.Contains(err.Error(), "203.0.113") {
		t.Errorf("err = %v", err)
	}
}

// NixOS allows underscores in host names, and an odd name costs only itself,
// never the whole answer: the page names Pis by their tailnet names.
func TestParseLenientNames(t *testing.T) {
	cases := []struct {
		host, peer         any
		wantHost, wantPeer string
	}{
		{"pi_c", "pi_a", "pi_c", "pi_a"},
		{"pi b; <x>", "bad name", "", ""},
		{nil, nil, "", ""},
		{"-pi", "pi-a-", "", ""},
	}
	for _, c := range cases {
		m := example(t)
		m["host"] = c.host
		m["tunnel"].([]any)[0].(map[string]any)["peer"] = c.peer
		s, err := Parse(encode(t, m))
		if err != nil {
			t.Fatalf("host %v, peer %v: %v", c.host, c.peer, err)
		}
		peer := ""
		if s.Tunnel[0].Peer != nil {
			peer = *s.Tunnel[0].Peer
		}
		if s.Host != c.wantHost || peer != c.wantPeer {
			t.Errorf("host %v, peer %v: got %q, %q", c.host, c.peer, s.Host, peer)
		}
	}
}
