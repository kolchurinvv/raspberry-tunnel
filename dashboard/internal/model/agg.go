package model

import "time"

// Mean is a running mean of the values that were known.
type Mean struct {
	Sum float64
	N   int
}

// Add counts v if it is known.
func (m *Mean) Add(v *float64) {
	if v != nil {
		m.Sum += *v
		m.N++
	}
}

// Merge adds another running mean.
func (m *Mean) Merge(o Mean) {
	m.Sum += o.Sum
	m.N += o.N
}

// Value is the mean, or nil when nothing was known.
func (m Mean) Value() *float64 {
	if m.N == 0 {
		return nil
	}
	v := m.Sum / float64(m.N)
	return &v
}

// Agg sums up one Pi's samples over a stretch of time. The store keeps one
// per Pi per 10 minutes, which is what keeps the 7- and 30-day views quick.
type Agg struct {
	N, NOK                                   int
	Temp, CPU, Mem, Disk, Traffic, Latency   Mean
	Relayed                                  int // samples with the tunnel relayed
	RouteTunnel, RouteOwn, RouteHeld, NoData int // gateways: samples in each exit-route state
	TVOff                                    int // gateways: samples with the tunnel up and the TV's port down
	Undervoltage                             int // samples with undervoltage now
}

// AggOf is one sample as an Agg.
func AggOf(s Sample) Agg {
	a := Agg{N: 1}
	if s.OK() {
		a.NOK = 1
	}
	a.Temp.Add(s.TempC)
	a.CPU.Add(s.CPUPct)
	a.Mem.Add(s.MemPct)
	a.Disk.Add(s.DiskPct)
	a.Traffic.Add(s.TrafficMbps)
	a.Latency.Add(s.LatencyMS)
	if s.Path.Relayed() {
		a.Relayed = 1
	}
	switch s.Route {
	case RouteTunnel:
		a.RouteTunnel = 1
	case RouteOwn:
		a.RouteOwn = 1
	case RouteHeld:
		a.RouteHeld = 1
	case RouteNoData:
		a.NoData = 1
	case RouteTVOff:
		a.TVOff = 1
	case RouteNone:
	default:
		panic("model.AggOf: unknown route " + string(s.Route))
	}
	if s.Undervoltage != nil && *s.Undervoltage {
		a.Undervoltage = 1
	}
	return a
}

// Merge adds another Agg.
func (a *Agg) Merge(o Agg) {
	a.N += o.N
	a.NOK += o.NOK
	a.Temp.Merge(o.Temp)
	a.CPU.Merge(o.CPU)
	a.Mem.Merge(o.Mem)
	a.Disk.Merge(o.Disk)
	a.Traffic.Merge(o.Traffic)
	a.Latency.Merge(o.Latency)
	a.Relayed += o.Relayed
	a.RouteTunnel += o.RouteTunnel
	a.RouteOwn += o.RouteOwn
	a.RouteHeld += o.RouteHeld
	a.NoData += o.NoData
	a.TVOff += o.TVOff
	a.Undervoltage += o.Undervoltage
}

// RollupWidth is the width of the store's rollup buckets.
const RollupWidth = 10 * time.Minute

// Rollup is one Pi's Agg over one rollup bucket.
type Rollup struct {
	Bucket time.Time
	Host   string
	Role   Role
	Agg
}
