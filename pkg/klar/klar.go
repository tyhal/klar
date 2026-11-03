// Package klar turns structured JSON logs into clear, human-readable output.
package klar

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
)

// TimeMode controls how entry timestamps are displayed.
type TimeMode string

// Available time modes.
const (
	// TimeRelative shows "3h12m ago" keyframes followed by "+1.5ms" deltas.
	TimeRelative TimeMode = "relative"
	// TimeAbsolute shows the original RFC3339 timestamp.
	TimeAbsolute TimeMode = "absolute"
	// TimeNone hides timestamps.
	TimeNone TimeMode = "none"
)

// TimeModes lists every valid TimeMode.
var TimeModes = []TimeMode{TimeRelative, TimeAbsolute, TimeNone}

// ParseTimeMode validates s as a TimeMode.
func ParseTimeMode(s string) (TimeMode, error) {
	for _, m := range TimeModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("invalid time mode %q, expected one of %v", s, TimeModes)
}

// Options configures a Logger.
type Options struct {
	// Level is the minimum level to print.
	Level log.Level
	// Time controls how timestamps are rendered. Defaults to TimeRelative.
	Time TimeMode
	// Now is used to compute relative keyframes. Defaults to time.Now.
	Now func() time.Time
	// Full prints every key/value, instead of merging keys that share a value
	// into "key,key=value" (moved to the end of the line), where a leading run
	// repeating the previous line shares the value "〃".
	Full bool
}

const relWidth = 10

var (
	keyframeStyle = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Bold(true).Foreground(lipgloss.Color("111"))
	deltaStyle    = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Faint(true)
	dittoStyle    = lipgloss.NewStyle().Faint(true)
)

// Logger is a wrapper around the charm log package with json log parsing
type Logger struct {
	*log.Logger
	out    io.Writer
	styles *log.Styles
	clock  *relClock
	carry  *carry
}

// New creates a new Logger with some opinionated defaults
func New(w io.Writer, opts Options) Logger {
	if opts.Time == "" {
		opts.Time = TimeRelative
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	l := Logger{
		Logger: log.New(w),
		out:    colorprofile.NewWriter(w, os.Environ()),
	}
	l.SetLevel(opts.Level)
	switch opts.Time {
	case TimeAbsolute:
		l.SetReportTimestamp(true)
		l.SetTimeFormat(time.RFC3339)
	case TimeRelative:
		l.clock = &relClock{now: opts.Now}
	}
	if !opts.Full {
		l.carry = &carry{}
	}

	styles := log.DefaultStyles()
	for _, k := range errKeys {
		styles.Keys[k] = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
		styles.Values[k] = lipgloss.NewStyle().Bold(true)
	}
	l.SetStyles(styles)
	l.styles = styles
	return l
}

// Decode reads structured JSON logs and writes them as human-readable logs
func (l Logger) Decode(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry logEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Failed to decode - print raw line
			entry = logEntry{
				Level:   log.WarnLevel,
				Msg:     string(line),
				Keyvals: []any{errKeys[0], err.Error()},
				raw:     true,
			}
		}
		l.print(entry)
	}
	return scanner.Err()
}

func (l Logger) print(entry logEntry) {
	if entry.Level < l.GetLevel() {
		return
	}
	if l.clock != nil {
		col, keyframe := l.relColumn(entry.Time)
		_, _ = io.WriteString(l.out, col+" ")
		// A keyframe marks a section break; start it with full context.
		if keyframe && l.carry != nil {
			l.carry.reset()
		}
	}
	kv := entry.Keyvals
	if l.carry != nil {
		if entry.raw {
			// Unparseable lines are shown verbatim and break the chain.
			l.carry.reset()
		} else {
			kv = mergeEqual(dittoPrefix(kv, l.carry.next(kv)))
		}
	}
	l.SetTimeFunction(func(time.Time) time.Time { return entry.Time })
	defer l.styleDitto(kv)()
	l.Log(entry.Level, entry.Msg, kv...)
}

// styleDitto makes the value of kv's ditto pair faint for one log call, and
// returns a func restoring the previous style. charm only styles values per
// key, and quotes values containing escape codes, so it can't be pre-rendered.
func (l Logger) styleDitto(kv []any) func() {
	if len(kv) < 2 || kv[len(kv)-1] != (ditto{}) {
		return func() {}
	}
	k := kv[len(kv)-2].(string)
	prev, had := l.styles.Values[k]
	l.styles.Values[k] = dittoStyle
	return func() {
		if had {
			l.styles.Values[k] = prev
		} else {
			delete(l.styles.Values, k)
		}
	}
}

func (l Logger) relColumn(t time.Time) (string, bool) {
	if t.IsZero() {
		return deltaStyle.Render(""), false
	}
	label, keyframe := l.clock.next(t)
	if keyframe {
		return keyframeStyle.Render(label), true
	}
	return deltaStyle.Render(label), false
}
