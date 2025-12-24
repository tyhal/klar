package klar

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
)

var timeKeys = []string{"time", "timestamp"}
var levelKeys = []string{"level", "severity"}
var msgKeys = []string{"msg", "message"}
var errKeys = []string{"err", "error"}

// logEntry represents a single log entry
type logEntry struct {
	Time    time.Time
	Level   log.Level
	Msg     any
	Keyvals []any
}

// UnmarshalJSON parses the log entry from JSON
func (l *logEntry) UnmarshalJSON(data []byte) error {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	for _, k := range timeKeys {
		if t, ok := obj[k].(string); ok {
			parsed, err := time.Parse(time.RFC3339, t)
			if err == nil {
				l.Time = parsed
				delete(obj, k)
				break
			}
		}
	}

	for _, k := range levelKeys {
		if lvl, ok := obj[k].(string); ok {
			level, err := log.ParseLevel(lvl)
			if err == nil {
				l.Level = level
				delete(obj, k)
				break
			}
		}
	}

	for _, k := range msgKeys {
		if msg, ok := obj[k]; ok {
			l.Msg = msg
			delete(obj, k)
		}
	}

	for k, v := range obj {
		l.Keyvals = append(l.Keyvals, k, v)
	}

	return nil
}

func (l *logEntry) time(time.Time) time.Time {
	return l.Time
}

// Logger is a wrapper around the charm log package with json log parsing
type Logger struct {
	*log.Logger
}

// New creates a new Logger with some opinionated defaults
func New(w io.Writer) Logger {
	l := Logger{
		log.New(w),
	}
	l.SetReportTimestamp(true)
	l.SetTimeFormat(time.RFC3339)
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
	dec := jsontext.NewDecoder(r)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var entry logEntry

			err := json.UnmarshalDecode(dec, &entry)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}

			l.SetTimeFunction(entry.time)
			l.Log(entry.Level, entry.Msg, entry.Keyvals...)
		}
	}
}
