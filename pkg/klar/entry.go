package klar

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"time"

	"charm.land/log/v2"
)

var timeKeys = []string{"time", "timestamp", "ts", "eventTime"}
var levelKeys = []string{"level", "severity"}
var msgKeys = []string{"msg", "message"}
var errKeys = []string{"err", "error"}

// logEntry represents a single log entry
type logEntry struct {
	Time    time.Time
	Level   log.Level
	Msg     any
	Keyvals []any
	// raw marks a line that failed to parse and is shown verbatim.
	raw bool
}

type field struct {
	key string
	val any
}

// UnmarshalJSON parses the log entry from a JSON object, preserving field order
// and dropping empty values (null, "", {} and []).
func (l *logEntry) UnmarshalJSON(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("expected a JSON object, got %s", tok.Kind())
	}

	var fields []field
	for dec.PeekKind() != '}' {
		keyTok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		key := keyTok.String() // must be read before the decoder advances
		var v any
		if err := json.UnmarshalDecode(dec, &v); err != nil {
			return err
		}
		fields = append(fields, field{key, v})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}

	*l = logEntry{}
	fields = l.take(fields, timeKeys, func(v any) bool {
		t, ok := parseTime(v)
		l.Time = t
		return ok
	})
	fields = l.take(fields, levelKeys, func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		lvl, err := log.ParseLevel(s)
		l.Level = lvl
		return err == nil
	})
	fields = l.take(fields, msgKeys, func(v any) bool {
		l.Msg = v
		return true
	})

	for _, f := range fields {
		if isEmpty(f.val) {
			continue
		}
		l.Keyvals = append(l.Keyvals, f.key, f.val)
	}
	return nil
}

// take finds the first field matching one of keys (in priority order) that
// accept successfully consumes, and removes it from fields.
func (l *logEntry) take(fields []field, keys []string, accept func(any) bool) []field {
	for _, k := range keys {
		i := slices.IndexFunc(fields, func(f field) bool { return f.key == k })
		if i >= 0 && accept(fields[i].val) {
			return slices.Delete(fields, i, i+1)
		}
	}
	return fields
}

// parseTime accepts RFC3339 strings or numeric unix epochs (seconds, or
// milliseconds when the value is too large to be seconds).
func parseTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, t)
		return parsed, err == nil
	case float64:
		unit := float64(time.Second)
		if t > 1e12 {
			unit = float64(time.Millisecond)
		}
		whole, frac := math.Modf(t)
		return time.Unix(0, int64(whole)*int64(unit)+int64(math.Round(frac*unit))), true
	}
	return time.Time{}, false
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}
