package view

import (
	"fmt"
	"math"
	"strings"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// The charts draw in a 1000 × 100 box, stretched to fit, as the mockup does.
const (
	boxW = 1000.0
	boxH = 100.0
)

// YTick is a label on a chart's value axis.
type YTick struct {
	Label, Top string
}

// LegendItem is one series in a chart's legend, with its current value.
type LegendItem struct {
	Name, Value string
	Colour      int
	Swatch      bool
}

// Line is one series' path.
type Line struct {
	D      string
	Colour int
}

// Chart is one line chart (D2): tunnel traffic, tunnel latency, temperature
// or CPU.
type Chart struct {
	Key, Title, Sub string
	Legend          []LegendItem
	BandKey         bool
	YTicks          []YTick
	Grid, Bands     string
	Ref, RefLabel   string
	RefTop          string
	Area            string
	AreaColour      int
	// Lines are drawn last first, so the first series is on top.
	Lines []Line
}

// Hover is the data the page's script reads to move one marker across every
// chart. Values are positions and preformatted text, nothing else.
type Hover struct {
	N      int                   `json:"n"`
	Times  []string              `json:"t"`
	Charts map[string]HoverChart `json:"c"`
	Route  []HoverRoute          `json:"r"`
}

// HoverChart is one chart's series and per-bucket notes.
type HoverChart struct {
	Series []HoverSeries `json:"s"`
	Notes  []string      `json:"n"`
}

// HoverSeries is one series: its y positions, in percent from the top, and
// its values as text.
type HoverSeries struct {
	Name   string     `json:"name"`
	Colour int        `json:"k"`
	Y      []*float64 `json:"y"`
	V      []string   `json:"v"`
}

// HoverRoute is one TV's exit-route state in each bucket.
type HoverRoute struct {
	TV     string   `json:"tv"`
	Text   []string `json:"s"`
	Levels []Level  `json:"l"`
}

type series struct {
	name   string
	colour int
	vals   []*float64
	now    *float64
	format func(*float64) string
}

func yOf(v, lo, hi float64) float64 {
	return clamp(boxH-(v-lo)/(hi-lo)*boxH, 0, boxH)
}

func xOf(i, n int) float64 {
	if n < 2 {
		return 0
	}
	return float64(i) / float64(n-1) * boxW
}

func pathOf(vals []*float64, lo, hi float64) string {
	var b strings.Builder
	pen := false
	for i, v := range vals {
		if v == nil {
			pen = false
			continue
		}
		cmd := "M"
		if pen {
			cmd = "L"
		}
		fmt.Fprintf(&b, "%s%.1f %.2f ", cmd, xOf(i, len(vals)), yOf(*v, lo, hi))
		pen = true
	}
	return strings.TrimSpace(b.String())
}

func areaOf(vals []*float64, lo, hi float64) string {
	var b strings.Builder
	var run [][2]float64
	flush := func() {
		if len(run) > 1 {
			fmt.Fprintf(&b, "M%.1f %.0f ", run[0][0], boxH)
			for _, p := range run {
				fmt.Fprintf(&b, "L%.1f %.2f ", p[0], p[1])
			}
			fmt.Fprintf(&b, "L%.1f %.0f Z ", run[len(run)-1][0], boxH)
		}
		run = run[:0]
	}
	for i, v := range vals {
		if v == nil {
			flush()
			continue
		}
		run = append(run, [2]float64{xOf(i, len(vals)), yOf(*v, lo, hi)})
	}
	flush()
	return strings.TrimSpace(b.String())
}

func bandsOf(flags []bool) string {
	n := len(flags)
	if n < 2 {
		return ""
	}
	var b strings.Builder
	half := boxW / float64(n-1) / 2
	for i := 0; i < n; {
		if !flags[i] {
			i++
			continue
		}
		j := i
		for j+1 < n && flags[j+1] {
			j++
		}
		x0 := math.Max(0, xOf(i, n)-half)
		x1 := math.Min(boxW, xOf(j, n)+half)
		fmt.Fprintf(&b, "M%.1f 0 H%.1f V%.0f H%.1f Z ", x0, x1, boxH, x0)
		i = j + 1
	}
	return strings.TrimSpace(b.String())
}

func yAxis(lo, hi, step float64) ([]YTick, string) {
	var ticks []YTick
	var grid []string
	for v := lo; v <= hi+1e-9; v += step {
		y := yOf(v, lo, hi)
		ticks = append(ticks, YTick{fmt.Sprintf("%.0f", v), fmt.Sprintf("%.2f", y)})
		if v > lo {
			grid = append(grid, fmt.Sprintf("M0 %.2f H%.0f", y, boxW))
		}
	}
	return ticks, strings.Join(grid, " ")
}

// niceTop picks a step from 1, 2, 5, 10, 20, 25, 50 and up, the smallest that
// covers top in at most four steps, and returns top rounded up to it.
func niceTop(top float64) (float64, float64) {
	for mag := 1.0; ; mag *= 10 {
		for _, s := range []float64{1, 2, 2.5, 5} {
			step := s * mag
			if s == 2.5 && mag < 10 {
				continue
			}
			if math.Ceil(top/step) <= 4 {
				return math.Ceil(top/step) * step, step
			}
		}
	}
}

func maxOf(ss []series) float64 {
	m := 0.0
	for _, s := range ss {
		for _, v := range s.vals {
			if v != nil && *v > m {
				m = *v
			}
		}
	}
	return m
}

func minOf(ss []series, floor float64) float64 {
	m := floor
	for _, s := range ss {
		for _, v := range s.vals {
			if v != nil && *v < m {
				m = *v
			}
		}
	}
	return m
}

func (st *state) charts() ([]Chart, Hover) {
	in := st.in
	n := in.Window.N
	h := in.Hist
	multi := len(st.gateways) > 1

	col := func(x *pi, f func(model.Agg) *float64) []*float64 {
		out := make([]*float64, n)
		for i := range out {
			out[i] = f(h.Bucket(x.Host, i))
		}
		return out
	}
	// Current values come only from a current answer.
	now := func(x *pi, f func(model.Sample) *float64) *float64 {
		if !x.Current {
			return nil
		}
		return f(x.Latest)
	}
	perGateway := func(f func(model.Agg) *float64, g func(model.Sample) *float64, format func(*float64) string) []series {
		var out []series
		for _, x := range st.gateways {
			name := x.Name
			if !multi {
				name = "Now"
			}
			out = append(out, series{name, x.Colour, col(x, f), now(x, g), format})
		}
		return out
	}
	// Gateway first, then the exit, then the other gateways, as the mockup has them.
	perPi := func(f func(model.Agg) *float64, g func(model.Sample) *float64, format func(*float64) string) []series {
		var out []series
		order := append([]*pi(nil), st.gateways...)
		if st.exit != nil {
			if len(order) > 0 {
				order = append(order[:1], append([]*pi{st.exit}, order[1:]...)...)
			} else {
				order = []*pi{st.exit}
			}
		}
		for _, x := range order {
			out = append(out, series{x.Name, x.Colour, col(x, f), now(x, g), format})
		}
		return out
	}

	relayed := make([]bool, n)
	var relayedBy, ownBy [][]string
	relayedBy, ownBy = make([][]string, n), make([][]string, n)
	for i := 0; i < n; i++ {
		for _, g := range st.gateways {
			a := h.Bucket(g.Host, i)
			if a.Relayed > 0 {
				relayed[i] = true
				relayedBy[i] = append(relayedBy[i], g.Name)
			}
			if a.RouteOwn > 0 {
				ownBy[i] = append(ownBy[i], g.TV)
			}
		}
	}

	hover := Hover{N: n, Charts: map[string]HoverChart{}}
	for i := 0; i < n; i++ {
		hover.Times = append(hover.Times, stamp(in.Window, i, in.Loc))
	}

	mk := func(key, title, sub string, lo, hi, step float64, ss []series, area, bands bool, ref float64, refLabel string) Chart {
		c := Chart{Key: key, Title: title, Sub: sub, BandKey: bands}
		c.YTicks, c.Grid = yAxis(lo, hi, step)
		for _, s := range ss {
			c.Legend = append(c.Legend, LegendItem{Name: s.name, Value: s.format(s.now), Colour: s.colour, Swatch: len(ss) > 1})
		}
		for i := len(ss) - 1; i >= 0; i-- {
			c.Lines = append(c.Lines, Line{pathOf(ss[i].vals, lo, hi), ss[i].colour})
		}
		if area && len(ss) == 1 {
			c.Area, c.AreaColour = areaOf(ss[0].vals, lo, hi), ss[0].colour
		}
		if bands {
			c.Bands = bandsOf(relayed)
		}
		if refLabel != "" {
			y := fmt.Sprintf("%.2f", yOf(ref, lo, hi))
			c.Ref, c.RefLabel, c.RefTop = "M0 "+y+" H1000", refLabel, y
		}

		hc := HoverChart{Notes: make([]string, n)}
		for _, s := range ss {
			hs := HoverSeries{Name: s.name, Colour: s.colour, Y: make([]*float64, n), V: make([]string, n)}
			for i, v := range s.vals {
				hs.V[i] = s.format(v)
				if v != nil {
					y := math.Round(yOf(*v, lo, hi)*100) / 100
					hs.Y[i] = &y
				}
			}
			hc.Series = append(hc.Series, hs)
		}
		for i := 0; i < n; i++ {
			switch {
			case len(ownBy[i]) > 0:
				hc.Notes[i] = "Tunnel down, " + joinAnd(ownBy[i]) + " on own connection"
			case bands && len(relayedBy[i]) > 0:
				hc.Notes[i] = "Relayed: " + joinAnd(relayedBy[i])
			}
		}
		hover.Charts[key] = hc
		return c
	}

	traffic := perGateway(func(a model.Agg) *float64 { return a.Traffic.Value() }, func(s model.Sample) *float64 { return s.TrafficMbps }, mbit)
	tHi, tStep := niceTop(math.Max(5, maxOf(traffic)))
	latency := perGateway(func(a model.Agg) *float64 { return a.Latency.Value() }, func(s model.Sample) *float64 { return s.LatencyMS }, ms)
	lHi, lStep := niceTop(math.Max(20, maxOf(latency)))
	temps := perPi(func(a model.Agg) *float64 { return a.Temp.Value() }, func(s model.Sample) *float64 { return s.TempC }, degC)
	tLo := math.Min(30, math.Floor(minOf(temps, 30)/10)*10)
	tTop := math.Max(70, math.Ceil(maxOf(temps)/10)*10)
	cpus := perPi(func(a model.Agg) *float64 { return clampPct(a.CPU.Value()) }, func(s model.Sample) *float64 { return clampPct(s.CPUPct) }, pctText)
	cHi, cStep := 100.0, 25.0
	if maxOf(cpus) < 45 {
		cHi, cStep = 50, 10
	}

	trafficSub, latencySub := "Pi B to Pi A, Mbit/s", "Round trip between the Pis, ms"
	if len(st.gateways) == 1 {
		trafficSub = st.gateways[0].Name + " to " + st.exitName() + ", Mbit/s"
	}
	if multi {
		trafficSub = "Each gateway to " + st.exitName() + ", Mbit/s"
		latencySub = "Round trip from each gateway to " + st.exitName() + ", ms"
	}
	charts := []Chart{
		mk("traffic", "Tunnel traffic", trafficSub, 0, tHi, tStep, traffic, true, true, 0, ""),
		mk("latency", "Tunnel latency", latencySub, 0, lHi, lStep, latency, false, true, 0, ""),
		mk("temp", "Temperature", "CPU, °C", tLo, tTop, 10, temps, false, false, TempClock, deg(TempClock)+", the Pi lowers its clock"),
		mk("cpu", "CPU", "All four cores, %", 0, cHi, cStep, cpus, false, false, 0, ""),
	}

	for _, g := range st.gateways {
		hr := HoverRoute{TV: g.TV, Text: make([]string, n), Levels: make([]Level, n)}
		for i := 0; i < n; i++ {
			hr.Levels[i], hr.Text[i] = routeText(routeAt(h.Bucket(g.Host, i)))
		}
		hover.Route = append(hover.Route, hr)
	}
	return charts, hover
}
