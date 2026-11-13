package view

import (
	"fmt"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// Range is one of the page's four time ranges (D2).
type Range struct {
	Key   string // the query value
	Label string // the switch's label
	Word  string // "last <word>"
	// N buckets of Width each; the last one holds now.
	N     int
	Width time.Duration
	// Raw ranges read raw samples; the others read 10-minute rollups.
	Raw bool
}

// Ranges are the four ranges, in the switch's order.
var Ranges = []Range{
	{Key: "1h", Label: "1 h", Word: "hour", N: 120, Width: model.Interval, Raw: true},
	{Key: "24h", Label: "24 h", Word: "24 hours", N: 144, Width: 10 * time.Minute},
	{Key: "7d", Label: "7 d", Word: "7 days", N: 168, Width: time.Hour},
	{Key: "30d", Label: "30 d", Word: "30 days", N: 180, Width: 4 * time.Hour},
}

// DefaultRange is what anything but a known range falls back to (D3).
var DefaultRange = Ranges[1]

// ParseRange reads the page's one input. Anything but 1h, 24h, 7d or 30d,
// exactly, means 24h.
func ParseRange(q string) Range {
	for _, r := range Ranges {
		if q == r.Key {
			return r
		}
	}
	return DefaultRange
}

func init() {
	for _, r := range Ranges {
		if !r.Raw && r.Width%model.RollupWidth != 0 {
			panic(fmt.Sprintf("view: range %s isn't a whole number of rollups", r.Key))
		}
	}
}

// Window is a range placed at a moment: bucket i starts at Start + i*Width.
type Window struct {
	Range
	Start time.Time
}

// WindowAt places r so its last bucket holds now.
func WindowAt(r Range, now time.Time) Window {
	return Window{Range: r, Start: now.Truncate(r.Width).Add(-time.Duration(r.N-1) * r.Width)}
}

// End is the end of the last bucket.
func (w Window) End() time.Time { return w.Start.Add(time.Duration(w.N) * w.Width) }

// Index is the bucket holding t, or -1 outside the window.
func (w Window) Index(t time.Time) int {
	if t.Before(w.Start) || !t.Before(w.End()) {
		return -1
	}
	return int(t.Sub(w.Start) / w.Width)
}

// History is each Pi's samples summed into the window's buckets.
type History struct {
	Window
	Hosts map[string][]model.Agg
	Roles map[string]model.Role
}

func newHistory(w Window) History {
	return History{Window: w, Hosts: map[string][]model.Agg{}, Roles: map[string]model.Role{}}
}

func (h History) add(host string, role model.Role, t time.Time, a model.Agg) {
	i := h.Index(t)
	if i < 0 {
		return
	}
	b, ok := h.Hosts[host]
	if !ok {
		b = make([]model.Agg, h.N)
		h.Hosts[host] = b
	}
	b[i].Merge(a)
	h.Roles[host] = role
}

// FromSamples buckets raw samples.
func FromSamples(w Window, samples []model.Sample) History {
	h := newHistory(w)
	for _, s := range samples {
		h.add(s.Host, s.Role, s.TS, model.AggOf(s))
	}
	return h
}

// FromRollups buckets 10-minute rollups.
func FromRollups(w Window, rollups []model.Rollup) History {
	h := newHistory(w)
	for _, r := range rollups {
		h.add(r.Host, r.Role, r.Bucket, r.Agg)
	}
	return h
}

// Bucket returns a host's bucket i, empty if the host has none.
func (h History) Bucket(host string, i int) model.Agg {
	if b, ok := h.Hosts[host]; ok {
		return b[i]
	}
	return model.Agg{}
}

// Tick is a label under the time axis.
type Tick struct {
	Label string
	Left  string // percent
	Align string // a CSS class: tk-start, tk-mid or tk-end
}

// xTicks puts labels at round times in loc: every 15 minutes for 1 h, every
// 6 hours for 24 h, each midnight for 7 d, and each Monday for 30 d.
func xTicks(w Window, loc *time.Location) []Tick {
	first := w.Start
	last := w.Start.Add(time.Duration(w.N-1) * w.Width)
	span := last.Sub(first).Seconds()
	var out []Tick
	add := func(t time.Time, label string) {
		if t.Before(first) || t.After(last) {
			return
		}
		left := t.Sub(first).Seconds() / span * 100
		align := "tk-mid"
		if left < 3 {
			align = "tk-start"
		} else if left > 97 {
			align = "tk-end"
		}
		out = append(out, Tick{Label: label, Left: fmt.Sprintf("%.2f", left), Align: align})
	}
	lf := first.In(loc)
	switch w.Key {
	case "1h":
		for t := lf.Truncate(15 * time.Minute); !t.After(last); t = t.Add(15 * time.Minute) {
			add(t, t.In(loc).Format("15:04"))
		}
	case "24h":
		for d := 0; d <= 2; d++ {
			for hour := 0; hour < 24; hour += 6 {
				t := time.Date(lf.Year(), lf.Month(), lf.Day()+d, hour, 0, 0, 0, loc)
				add(t, t.Format("15:04"))
			}
		}
	case "7d":
		day := time.Date(lf.Year(), lf.Month(), lf.Day(), 0, 0, 0, 0, loc)
		for t := day; !t.After(last); t = day {
			add(t, t.Format("Mon"))
			day = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		}
	case "30d":
		day := time.Date(lf.Year(), lf.Month(), lf.Day(), 0, 0, 0, 0, loc)
		for day.Weekday() != time.Monday {
			day = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
		}
		for t := day; !t.After(last); t = day {
			add(t, t.Format("2 Jan"))
			day = time.Date(t.Year(), t.Month(), t.Day()+7, 0, 0, 0, 0, loc)
		}
	default:
		panic("view: unknown range " + w.Key)
	}
	return out
}

// stamp labels bucket i for the hover tooltip.
func stamp(w Window, i int, loc *time.Location) string {
	from := w.Start.Add(time.Duration(i) * w.Width).In(loc)
	to := from.Add(w.Width)
	switch w.Key {
	case "1h":
		return from.Format("15:04:05")
	case "24h":
		return from.Format("15:04") + "–" + to.Format("15:04")
	default:
		return from.Format("Mon 2 Jan, 15:04") + "–" + to.Format("15:04")
	}
}
