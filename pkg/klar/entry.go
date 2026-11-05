package klar

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
)

// Keys each part of an entry is found under, in priority order, covering
// slog, zap, zerolog, logrus, pino, bunyan, python-json-logger, GCP and ECS.
var (
	timeKeys  = []string{"time", "timestamp", "ts", "@timestamp", "eventTime"}
	levelKeys = []string{"level", "lvl", "severity", "log.level", "levelname"}
	msgKeys   = []string{"msg", "message"}
)

// noLevel marks an entry without a level we recognise. Labelling it INFO
// would hide it under --level warn, though it may well be an error, so it is
// shown unlabelled and passes every filter.
const noLevel = log.Level(math.MaxInt32)

// logEntry represents a single log entry
type logEntry struct {
	Time   time.Time
	Level  log.Level
	Msg    any
	Fields []field
	// raw marks a line that isn't a JSON object, shown verbatim.
	raw bool
	// source is the prefix before the object, naming where the line came from.
	source string
}

// lenient accepts what loggers really write: zap can repeat a key, and a
// logged byte string may not be valid UTF-8. Either would otherwise turn a
// structured line into a raw one.
var lenient = json.JoinOptions(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))

// number is a JSON number kept as written: float64 would print an id as
// 1.2345678e+07 and round integers past 2^53.
type number string

// UnmarshalJSON parses the log entry from a JSON object, preserving field
// order. Nested objects flatten into dotted keys (http.method), so each part
// can repeat as 〃 on its own; empty values (null, "", {} and []) are dropped.
func (l *logEntry) UnmarshalJSON(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data), lenient)
	if k := dec.PeekKind(); k != '{' {
		return fmt.Errorf("expected a JSON object, got %s", k)
	}
	fields, err := readObject(dec, "", nil)
	if err != nil {
		return err
	}

	*l = logEntry{Level: noLevel}
	fields = take(fields, timeKeys, func(v any) (ok bool) {
		l.Time, ok = parseTime(v)
		return ok
	})
	fields = take(fields, levelKeys, func(v any) bool {
		lvl, ok := parseLevel(v)
		if ok {
			l.Level = lvl
		}
		return ok
	})
	fields = take(fields, msgKeys, func(v any) bool {
		l.Msg = v
		return true
	})
	l.Fields = fields
	return nil
}

// readObject appends the fields of the object dec is at to out, prefixing
// keys of nested objects with their parent's.
func readObject(dec *jsontext.Decoder, prefix string, out []field) ([]field, error) {
	if _, err := dec.ReadToken(); err != nil { // {
		return nil, err
	}
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		key := prefix + tok.String()
		if dec.PeekKind() == '{' {
			if out, err = readObject(dec, key+".", out); err != nil {
				return nil, err
			}
			continue
		}
		raw, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		if v := scalar(raw); v != nil {
			out = append(out, field{key, v})
		}
	}
	_, err := dec.ReadToken() // }
	return out, err
}

// scalar converts a non-object JSON value for display, or nil when empty.
func scalar(raw jsontext.Value) any {
	switch raw.Kind() {
	case 't', 'f':
		return raw.Kind() == 't'
	case '0':
		return number(raw)
	case '"':
		var s string
		if json.Unmarshal(raw, &s, lenient) != nil || s == "" {
			return nil
		}
		return s
	case '[':
		if s := array(raw); s != "[]" {
			return s
		}
	}
	return nil
}

// array renders a JSON array as [a,b], unquoting strings so it reads like the
// rest of the line.
func array(raw jsontext.Value) string {
	var parts []string
	dec := jsontext.NewDecoder(bytes.NewReader(raw), lenient)
	_, _ = dec.ReadToken() // [
	for dec.PeekKind() != ']' {
		v, err := dec.ReadValue()
		if err != nil {
			break
		}
		var s string
		if v.Kind() != '"' || json.Unmarshal(v, &s, lenient) != nil {
			_ = v.Compact()
			s = string(v)
		}
		parts = append(parts, s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// take finds the first field matching one of keys (in priority order) that
// accept successfully consumes, and removes it from fields.
func take(fields []field, keys []string, accept func(any) bool) []field {
	for _, k := range keys {
		i := slices.IndexFunc(fields, func(f field) bool { return f.key == k })
		if i >= 0 && accept(fields[i].val) {
			return slices.Delete(fields, i, i+1)
		}
	}
	return fields
}

// timeLayouts are tried in order: RFC3339, with a space instead of the T as
// Python writes it, and with a +0000 offset as logback and log4j write it. Go
// also accepts a comma before fractional seconds.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z0700",
}

// parseTime accepts RFC3339 strings (with a T or a space) or numeric unix
// epochs, whose unit (s, ms, µs or ns) is inferred from their size.
func parseTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case string:
		for _, layout := range timeLayouts {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	case number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return time.Time{}, false
		}
		var unit int64
		switch a := math.Abs(f); {
		case a < 1e11:
			unit = int64(time.Second)
		case a < 1e14:
			unit = int64(time.Millisecond)
		case a < 1e17:
			unit = int64(time.Microsecond)
		default:
			unit = 1
		}
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return time.Unix(0, i*unit), true // exact, nanoseconds don't fit a float64
		}
		whole, frac := math.Modf(f)
		return time.Unix(0, int64(whole)*unit+int64(math.Round(frac*float64(unit)))), true
	}
	return time.Time{}, false
}

// levelNames maps the level names loggers use onto charm's five levels.
var levelNames = map[string]log.Level{
	"trace": log.DebugLevel, "debug": log.DebugLevel, "dbug": log.DebugLevel, "verbose": log.DebugLevel,
	"info": log.InfoLevel, "information": log.InfoLevel, "notice": log.InfoLevel, "default": log.InfoLevel,
	"warn": log.WarnLevel, "warning": log.WarnLevel,
	"error": log.ErrorLevel, "err": log.ErrorLevel, "eror": log.ErrorLevel,
	"fatal": log.FatalLevel, "panic": log.FatalLevel, "dpanic": log.FatalLevel, "crit": log.FatalLevel,
	"critical": log.FatalLevel, "alert": log.FatalLevel, "emerg": log.FatalLevel, "emergency": log.FatalLevel,
}

// ParseLevel parses a level name as loggers write them, in any case: trace,
// warning, critical, … mapped onto charm's five levels.
func ParseLevel(s string) (log.Level, error) {
	if lvl, ok := parseLevel(s); ok {
		return lvl, nil
	}
	return 0, fmt.Errorf("unknown level %q", s)
}

// parseLevel accepts a level name in any case, or pino/bunyan's numeric
// levels (10 trace … 60 fatal). Anything unknown stays a field.
func parseLevel(v any) (log.Level, bool) {
	switch t := v.(type) {
	case string:
		lvl, ok := levelNames[strings.ToLower(t)]
		return lvl, ok
	case number:
		n, err := strconv.Atoi(string(t))
		switch {
		case err != nil || n < 10:
			return 0, false
		case n >= 60:
			return log.FatalLevel, true
		case n >= 50:
			return log.ErrorLevel, true
		case n >= 40:
			return log.WarnLevel, true
		case n >= 30:
			return log.InfoLevel, true
		}
		return log.DebugLevel, true
	}
	return 0, false
}
