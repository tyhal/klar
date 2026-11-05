package klar

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// defaultKeyframeLines is how often a keyframe is forced: half a classic
// 24-row terminal, leaving room for lines that wrap, so a screenful always
// has one.
const defaultKeyframeLines = 12

// relClock tracks the previous entry time so each entry can be rendered either
// as a keyframe ("3h12m ago") or as a delta from the previous entry ("+1.5ms").
//
// Keyframes depend on lines, not time: deltas only add up from an absolute
// time, so one is forced every so many lines, and scrolling through any part
// of the output leaves one on screen. Between them, every gap is a delta,
// however long, or negative when interleaved sources go back in time.
type relClock struct {
	now   func() time.Time
	prev  time.Time
	every int // lines between keyframes
	since int // lines printed since the last keyframe
}

// next returns the label for t and whether it is a keyframe. A zero t (an
// untimed line) gets an empty label but still occupies a line on screen.
func (c *relClock) next(t time.Time) (string, bool) {
	if t.IsZero() {
		c.since++
		return "", false
	}
	first := c.prev.IsZero()
	d := t.Sub(c.prev)
	c.prev = t
	if first || c.since >= c.every {
		c.since = 1
		return formatAgo(c.now().Sub(t)), true
	}
	c.since++
	return formatDelta(d), false
}

type unit struct {
	d    time.Duration
	name string
}

var agoUnits = []unit{
	{365 * 24 * time.Hour, "y"},
	{7 * 24 * time.Hour, "w"},
	{24 * time.Hour, "d"},
	{time.Hour, "h"},
	{time.Minute, "m"},
	{time.Second, "s"},
}

// formatAgo renders d using its two most significant units, e.g. "3h12m ago",
// "2w ago" or "in 4s" for timestamps in the future.
func formatAgo(d time.Duration) string {
	future := d < 0
	if future {
		d = abs(d)
	}
	if d < time.Second {
		return "now"
	}
	label := func(parts int) string {
		if future {
			return "in " + units(d, parts)
		}
		return units(d, parts) + " ago"
	}
	if s := label(2); len(s) <= relWidth {
		return s
	}
	return label(1) // a century or more: "292y ago"
}

// units renders d (at least a second) in its most significant units, up to
// parts of them, dropping a zero trailing unit: "5m3s", "1h", "2w1d".
func units(d time.Duration, parts int) string {
	var b strings.Builder
	for _, u := range agoUnits {
		n := d / u.d
		if b.Len() == 0 && n == 0 {
			continue
		}
		if n > 0 {
			b.WriteString(strconv.FormatInt(int64(n), 10))
			b.WriteString(u.name)
		}
		d -= n * u.d
		if parts--; parts == 0 {
			break
		}
	}
	return b.String()
}

// formatDelta renders d with ~3 significant figures in the most fitting unit,
// e.g. "+0", "+850ns", "+12.3µs", "+1.5ms", "+42.1s", and in two units from a
// minute up, "+3m54s", "+2h25m". Going back in time gives "-10s".
func formatDelta(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign, d = "-", abs(d)
	}
	var v float64
	var name string
	switch {
	case d == 0:
		return "+0"
	case d < time.Microsecond:
		return sign + strconv.FormatInt(int64(d), 10) + "ns"
	case d < time.Millisecond:
		v, name = float64(d)/float64(time.Microsecond), "µs"
	case d < time.Second:
		v, name = float64(d)/float64(time.Millisecond), "ms"
	case d < time.Minute:
		v, name = d.Seconds(), "s"
	default:
		return sign + units(d, 2)
	}
	prec := 2
	switch {
	case v >= 100:
		prec = 0
	case v >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return sign + s + name
}

// abs is |d|. Subtracting far-apart times saturates at the minimum duration,
// which has no positive counterpart, so it is clamped.
func abs(d time.Duration) time.Duration {
	if d == math.MinInt64 {
		return math.MaxInt64
	}
	return max(d, -d)
}
