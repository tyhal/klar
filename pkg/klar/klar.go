package klar

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"github.com/charmbracelet/log"
	"io"
	"time"
)

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

	if t, ok := obj["time"].(string); ok {
		parsed, err := time.Parse(time.RFC3339, t)
		if err == nil {
			l.Time = parsed
		}
	}

	if lvl, ok := obj["level"].(string); ok {
		level, err := log.ParseLevel(lvl)
		if err == nil {
			l.Level = level
		}
	}

	if msg, ok := obj["msg"]; ok {
		l.Msg = msg
	}

	for k, v := range obj {
		if k != "time" && k != "level" && k != "msg" {
			l.Keyvals = append(l.Keyvals, k, v)
		}
	}

	return nil
}

func (l *logEntry) time(time.Time) time.Time {
	return l.Time
}

// Stream reads structured JSON logs and writes them as human-readable logs
func Stream(ctx context.Context, r io.Reader, w io.Writer) error {
	dec := jsontext.NewDecoder(r)
	enc := log.New(w)
	enc.SetReportTimestamp(true)
	enc.SetTimeFormat(time.RFC3339)

	for {
		select {
		case <-ctx.Done():
		default:
			var entry logEntry

			err := json.UnmarshalDecode(dec, &entry)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}

			enc.SetTimeFunction(entry.time)
			enc.Log(entry.Level, entry.Msg, entry.Keyvals...)
		}
	}
}
