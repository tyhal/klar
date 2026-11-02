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
}

const relWidth = 10

var (
	keyframeStyle = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Bold(true).Foreground(lipgloss.Color("111"))
	deltaStyle    = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Faint(true)
)

// Logger is a wrapper around the charm log package with json log parsing
type Logger struct {
	*log.Logger
	out   io.Writer
	clock *relClock
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

	styles := log.DefaultStyles()
	for _, k := range errKeys {
		styles.Keys[k] = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
		styles.Values[k] = lipgloss.NewStyle().Bold(true)
	}
	l.SetStyles(styles)
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
		_, _ = io.WriteString(l.out, l.relColumn(entry.Time)+" ")
	}
	l.SetTimeFunction(func(time.Time) time.Time { return entry.Time })
	l.Log(entry.Level, entry.Msg, entry.Keyvals...)
}

func (l Logger) relColumn(t time.Time) string {
	if t.IsZero() {
		return deltaStyle.Render("")
	}
	label, keyframe := l.clock.next(t)
	if keyframe {
		return keyframeStyle.Render(label)
	}
	return deltaStyle.Render(label)
}
