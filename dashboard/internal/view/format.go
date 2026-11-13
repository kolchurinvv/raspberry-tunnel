package view

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/kolchurinvv/raspberry-tunnel/dashboard/internal/model"
)

// Names are how the page calls a Pi and what hangs off it. Apartments are
// named after their Pi (D2): pi-b is Pi B, in apartment B, with TV B.
type Names struct {
	Name, TV, Apt string
}

var piHost = regexp.MustCompile(`^pi[-_]([a-z])$`)

// NamesOf derives the names from a Pi's host name, which is the one name of
// it the page may show.
func NamesOf(host string) Names {
	if m := piHost.FindStringSubmatch(host); m != nil {
		l := strings.ToUpper(m[1])
		return Names{Name: "Pi " + l, TV: "TV " + l, Apt: "apartment " + l}
	}
	return Names{Name: host, TV: "the TV behind " + host, Apt: host + "'s apartment"}
}

const dash = "—"

func f0(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.0f", *v)
}

func f1(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.1f", *v)
}

func mbit(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.1f Mbit/s", *v)
}

func ms(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.0f ms", *v)
}

func degC(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.0f °C", *v)
}

func pctText(v *float64) string {
	if v == nil {
		return dash
	}
	return fmt.Sprintf("%.0f%%", *v)
}

// clock is a time as the page shows it: "21:31" today, "Thu 03:14" this
// week, "12 Nov 03:14" before.
func clock(t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	default:
		return t.Format("2 Jan 15:04")
	}
}

// ago is how long before now t was: "just now", "9 min ago", "3 h ago".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "Just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
}

// span is a duration as "1 d 18 h", "4 h 12 min" or "9 min".
func span(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d d %d h", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d h %d min", hours, mins)
	default:
		return fmt.Sprintf("%d min", mins)
	}
}

// failureLabel is the fixed wording for how a poll failed. Nothing a Pi or
// the network said is shown, only one of these.
func failureLabel(o model.Outcome) string {
	switch o {
	case model.OutcomeOffline:
		return "offline"
	case model.OutcomeTimeout:
		return "timed out"
	case model.OutcomeUnreachable:
		return "no answer"
	case model.OutcomeHTTPError:
		return "error from the Pi"
	case model.OutcomeInvalid:
		return "bad data"
	case model.OutcomeSchema:
		return "unsupported schema"
	case model.OutcomeOK:
		return "answering"
	default:
		panic("view: unknown outcome " + string(o))
	}
}

// pathWord is how the page names a tunnel path.
func pathWord(p model.Path) string {
	switch p {
	case model.PathDirect:
		return "direct"
	case model.PathDERP:
		return "relayed (DERP)"
	case model.PathPeerRelay:
		return "relayed (peer relay)"
	case model.PathIdle:
		return "idle"
	case model.PathOffline:
		return "down"
	case model.PathNone:
		return "no exit node set"
	case model.PathUnknown:
		return "unknown"
	default:
		panic("view: unknown path " + string(p))
	}
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// joinAnd joins "a", "a and b", "a, b and c".
func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	default:
		return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
	}
}
