package klar

import (
	"strconv"
	"strings"
	"time"
)

// keyframeGap is the largest gap between consecutive entries that is still
// shown as a delta; anything larger (or going backwards) starts a new keyframe.
const keyframeGap = time.Minute

// relClock tracks the previous entry time so each entry can be rendered either
// as a keyframe ("3h12m ago") or as a delta from the previous entry ("+1.5ms").
type relClock struct {
	now  func() time.Time
	prev time.Time
}

// next returns the label for t and whether it is a keyframe.
func (c *relClock) next(t time.Time) (string, bool) {
	prev := c.prev
	c.prev = t
	d := t.Sub(prev)
	if prev.IsZero() || d < 0 || d >= keyframeGap {
		return formatAgo(c.now().Sub(t)), true
	}
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
		d = -d
	}
	if d < time.Second {
		return "now"
	}
	var b strings.Builder
	parts := 0
	for _, u := range agoUnits {
		n := d / u.d
		if parts > 0 || n > 0 {
			if n > 0 {
				b.WriteString(strconv.FormatInt(int64(n), 10))
				b.WriteString(u.name)
			}
			d -= n * u.d
			if parts++; parts == 2 {
				break
			}
		}
	}
	if future {
		return "in " + b.String()
	}
	return b.String() + " ago"
}

// formatDelta renders d with ~3 significant figures in the most fitting unit,
// e.g. "+0", "+850ns", "+12.3µs", "+1.5ms", "+42.1s".
func formatDelta(d time.Duration) string {
	if d == 0 {
		return "+0"
	}
	var v float64
	var name string
	switch {
	case d < time.Microsecond:
		return "+" + strconv.FormatInt(int64(d), 10) + "ns"
	case d < time.Millisecond:
		v, name = float64(d)/float64(time.Microsecond), "µs"
	case d < time.Second:
		v, name = float64(d)/float64(time.Millisecond), "ms"
	default:
		v, name = d.Seconds(), "s"
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
	return "+" + s + name
}
