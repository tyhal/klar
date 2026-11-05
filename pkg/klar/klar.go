// Package klar turns structured JSON logs into clear, human-readable output.
package klar

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

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
	// KeyframeLines is how many printed lines apart relative-time keyframes
	// ("3h ago") are, so any screenful has an absolute time to anchor the
	// deltas on. Defaults to 12, half a 24-row terminal.
	KeyframeLines int
	// Full prints every key/value, instead of merging keys that share a value
	// into "key,key=value", where values repeated from the line above share
	// the value "〃".
	Full bool
}

const relWidth = 10

// absLayout always writes milliseconds and a numeric offset, so the absolute
// time column keeps one width and a burst within a second stays readable.
const absLayout = "2006-01-02T15:04:05.000-07:00"

var (
	keyframeStyle = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Bold(true).Foreground(lipgloss.Color("111"))
	deltaStyle    = lipgloss.NewStyle().Width(relWidth).Align(lipgloss.Right).Faint(true)
	absStyle      = lipgloss.NewStyle().Width(len(absLayout)).Faint(true)
	dittoStyle    = lipgloss.NewStyle().Faint(true)
)

// Logger is a wrapper around the charm log package with json log parsing
type Logger struct {
	*log.Logger
	w      io.Writer
	out    *colorprofile.Writer // for what klar writes itself, in w's colours
	styles *log.Styles
	// column renders the time column, padded so untimed and raw lines align;
	// nil without one.
	column func(time.Time) string
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
		w:      w,
		out:    colorprofile.NewWriter(w, os.Environ()),
	}
	l.SetLevel(opts.Level)
	switch opts.Time {
	case TimeAbsolute:
		l.column = absColumn
	case TimeRelative:
		if opts.KeyframeLines <= 0 {
			opts.KeyframeLines = defaultKeyframeLines
		}
		l.clock = &relClock{now: opts.Now, every: opts.KeyframeLines}
		l.column = l.relColumn
	}
	if !opts.Full {
		l.carry = &carry{}
	}

	styles := log.DefaultStyles()
	styles.Levels[noLevel] = lipgloss.NewStyle().SetString(" ").Width(5)
	// Errors stand out from the rest of the line, however the logger names them.
	for _, k := range []string{"err", "error", "error.message", "error.type", "exception"} {
		styles.Keys[k] = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
		styles.Values[k] = lipgloss.NewStyle().Bold(true)
	}
	l.SetStyles(styles)
	l.styles = styles
	return l
}

// Decode reads structured JSON logs and writes them as human-readable logs.
// Lines may be any length, so one huge line can't end the stream.
//
// Output is buffered, as a write per line made a large dump crawl, and
// flushed whenever the input runs dry, so a live stream (kubectl logs -f)
// still shows each line as it arrives.
func (l Logger) Decode(ctx context.Context, r io.Reader) error {
	bw := bufio.NewWriter(l.w)
	defer bw.Flush()
	l.SetOutput(bw)
	defer l.SetOutput(l.w)
	out := *l.out // keep the colours detected for w
	out.Forward = bw
	l.out = &out

	br := bufio.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// No complete line left to read: the next read may wait on the
		// producer, which may have flushed mid-line.
		if buffered, _ := br.Peek(br.Buffered()); bytes.IndexByte(buffered, '\n') < 0 {
			if err := bw.Flush(); err != nil {
				return err
			}
		}
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			l.print(parseLine(line))
		}
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
	}
}

// parseLine parses a JSON log line. Text before the object, as docker compose
// ("api-1  | {...}") and kubectl --prefix ("[pod/api/app] {...}") add, names
// the source. Anything else is raw text.
func parseLine(line []byte) logEntry {
	var entry logEntry
	if json.Unmarshal(line, &entry, lenient) == nil {
		return entry
	}
	if i := bytes.IndexByte(line, '{'); i > 0 && json.Unmarshal(line[i:], &entry, lenient) == nil {
		if p, ok := splitPrefix(string(line[:i])); ok {
			entry.source = p.source
			if entry.Time.IsZero() {
				entry.Time = p.time
			}
			if entry.Level == noLevel {
				entry.Level = p.level
			}
			return entry
		}
	}
	return logEntry{Msg: string(line), raw: true}
}

// prefix is what the text before a JSON object said about the line.
type prefix struct {
	source string
	time   time.Time
	level  log.Level
}

// splitPrefix reads the text before a JSON object: timestamps, as
// `docker compose logs -t` and `kubectl logs --timestamps` add (the first
// wins), a level word ("INFO {...}"), and at most one source name. Anything
// more, or a word ending in ':' ("panic: {...}"), is prose that happens to end
// in JSON, which is better left raw.
func splitPrefix(s string) (prefix, bool) {
	p := prefix{level: noLevel}
	for _, word := range strings.Fields(strings.NewReplacer("|", " ", "[", " ", "]", " ").Replace(s)) {
		if t, ok := parseTime(word); ok {
			if p.time.IsZero() {
				p.time = t
			}
			continue
		}
		if lvl, ok := parseLevel(word); ok && p.level == noLevel {
			p.level = lvl
			continue
		}
		if p.source != "" || strings.HasSuffix(word, ":") {
			return prefix{}, false
		}
		p.source = word
	}
	return p, true
}

func (l Logger) print(entry logEntry) {
	// A raw line (a panic, a stack trace, a printf) has no level to filter on,
	// and is often what explains the error above it, so it is always shown.
	if !entry.raw && entry.Level < l.GetLevel() {
		return
	}
	if l.column != nil {
		_, _ = io.WriteString(l.out, l.column(entry.Time)+" ")
	}
	if entry.raw {
		_, _ = fmt.Fprintln(l.out, entry.Msg)
		if l.carry != nil {
			l.carry.reset() // nothing for the next line to repeat
		}
		return
	}
	msg, more := message(entry.Msg)
	fields := entry.Fields
	if more != "" {
		fields = append(slices.Clip(fields), field{"msg", more})
	}
	if l.carry != nil {
		fields = merge(l.carry.next(entry.source, fields))
	}
	l.SetPrefix(entry.source)
	defer l.styleDitto(fields)()
	l.Log(entry.Level, msg, keyvals(fields)...)
}

// message prepares msg for the line. Control characters are escaped: they
// came from inside a JSON string, so they are data, not meant to retitle or
// clear the terminal. Lines past the first (a stack trace) become a "msg"
// field, which renders indented below the line instead of breaking its
// alignment.
func message(msg any) (any, string) {
	s, ok := msg.(string)
	if !ok {
		return msg, ""
	}
	first, more, _ := strings.Cut(escapeControl(s), "\n")
	return first, strings.ReplaceAll(more, "\t", "    ") // charm would print \t
}

// escapeControl shows control characters other than newline and tab as \xNN.
// charm quotes such values, but not messages or keys.
func escapeControl(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isControl(r) {
			fmt.Fprintf(&b, `\x%02x`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isControl(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }

// styleDitto makes the 〃 (merge puts it last) faint like the keys for one log
// call, so the field recedes behind what changed, and returns a func restoring
// the previous style. charm only styles values per key, and quotes values
// containing escape codes, so the 〃 can't be pre-rendered.
func (l Logger) styleDitto(fs []field) func() {
	if len(fs) == 0 || !isDitto(fs[len(fs)-1].val) {
		return func() {}
	}
	k := fs[len(fs)-1].key
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

func absColumn(t time.Time) string {
	if t.IsZero() {
		return absStyle.Render("")
	}
	return absStyle.Render(t.Format(absLayout))
}

func (l Logger) relColumn(t time.Time) string {
	label, keyframe := l.clock.next(t)
	if keyframe {
		return keyframeStyle.Render(label)
	}
	return deltaStyle.Render(label)
}
