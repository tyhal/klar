package klar

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/stretchr/testify/assert"
)

func TestLogEntry_UnmarshalJSON(t *testing.T) {
	testcase := []struct {
		Name     string
		JSON     string
		Expected logEntry
		Err      bool
	}{
		{
			Name: "basic",
			JSON: `{"time":"2011-05-15T12:00:00+02:00","level":"info","msg":"Redistribution scheduled"}`,
			Expected: logEntry{
				Time:  time.Date(2011, time.May, 15, 12, 0, 0, 0, time.FixedZone("", 60*60*2)),
				Level: log.InfoLevel,
				Msg:   "Redistribution scheduled",
			},
		},
		{
			Name: "keeps field order and drops empty values",
			JSON: `{"msg":"hi","z":1,"empty":"","nil":null,"obj":{},"arr":[],"a":"x","zero":0,"f":false}`,
			Expected: logEntry{
				Msg:     "hi",
				Keyvals: []any{"z", float64(1), "a", "x", "zero", float64(0), "f", false},
			},
		},
		{
			Name: "epoch seconds",
			JSON: `{"ts":1700000000.5,"msg":"hi"}`,
			Expected: logEntry{
				Time: time.Unix(1700000000, 5e8),
				Msg:  "hi",
			},
		},
		{
			Name: "epoch millis",
			JSON: `{"ts":1700000000123,"msg":"hi"}`,
			Expected: logEntry{
				Time: time.Unix(1700000000, 123e6),
				Msg:  "hi",
			},
		},
		{
			Name: "unparseable level kept as field",
			JSON: `{"level":"loud","msg":"hi"}`,
			Expected: logEntry{
				Msg:     "hi",
				Keyvals: []any{"level", "loud"},
			},
		},
		{Name: "not an object", JSON: `[1,2]`, Err: true},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			var le logEntry
			err := le.UnmarshalJSON([]byte(tc.JSON))
			if tc.Err {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.True(t, tc.Expected.Time.Equal(le.Time), "time %v != %v", tc.Expected.Time, le.Time)
			tc.Expected.Time, le.Time = time.Time{}, time.Time{}
			assert.Equal(t, tc.Expected, le)
		})
	}
}

func TestLogger_Decode(t *testing.T) {
	now := time.Date(2011, time.May, 15, 15, 30, 0, 0, time.UTC)
	testcase := []struct {
		Name string
		Opts Options
		In   string
		Out  string
	}{
		{
			Name: "absolute",
			Opts: Options{Time: TimeAbsolute},
			In: `
{"time":"2011-05-15T12:00:00+02:00","level":"info","msg":"Redistribution scheduled"}
`,
			Out: "2011-05-15T12:00:00+02:00 INFO  Redistribution scheduled\n",
		},
		{
			Name: "none",
			Opts: Options{Time: TimeNone},
			In:   `{"time":"2011-05-15T12:00:00Z","level":"info","msg":"hi","empty":{}}`,
			Out:  "INFO  hi\n",
		},
		{
			Name: "relative keyframes and deltas",
			In: strings.Join([]string{
				`{"time":"2011-05-15T12:00:00Z","level":"info","msg":"a"}`,
				`{"time":"2011-05-15T12:00:00.0015Z","level":"info","msg":"b"}`,
				`{"time":"2011-05-15T12:00:02Z","level":"info","msg":"c"}`,
				`{"time":"2011-05-15T12:00:02Z","level":"info","msg":"d"}`,
				`{"time":"2011-05-15T15:29:58Z","level":"info","msg":"e"}`,
				`plain text`,
			}, "\n"),
			Out: "" +
				" 3h30m ago INFO  a\n" +
				"    +1.5ms INFO  b\n" +
				"       +2s INFO  c\n" +
				"        +0 INFO  d\n" +
				"    2s ago INFO  e\n" +
				"           WARN  plain text err=\"jsontext: invalid character 'p' at start of value\"\n",
		},
		{
			Name: "elides repeated leading keyvals",
			Opts: Options{Time: TimeNone},
			In: strings.Join([]string{
				`{"level":"info","msg":"start","trace":"t1","n":1}`,
				`{"level":"info","msg":"enter","trace":"t1","span":"s1","n":2}`,
				`{"level":"info","msg":"work","trace":"t1","span":"s1","n":2}`,
				`{"level":"info","msg":"leave","trace":"t1","status":200}`,
				`{"level":"info","msg":"other","trace":"t2","status":200}`,
				`plain`,
				`plain`,
				`{"level":"info","trace":"t2"}`,
			}, "\n"),
			Out: "" +
				"INFO  start trace=t1 n=1\n" +
				"INFO  enter span=s1 n=2 trace=〃\n" +
				"INFO  work trace,span,n=〃\n" +
				"INFO  leave status=200 trace=〃\n" +
				"INFO  other trace=t2 status=200\n" +
				"WARN  plain err=\"jsontext: invalid character 'p' at start of value\"\n" +
				"WARN  plain err=\"jsontext: invalid character 'p' at start of value\"\n" +
				"INFO  trace=t2\n",
		},
		{
			Name: "merges keys sharing a value",
			Opts: Options{Time: TimeNone},
			In: `{"level":"info","msg":"a","trace":"t1","start":5,"x":"y","end":5}
{"level":"info","msg":"b","trace":"t1","start":7,"end":7}`,
			Out: "INFO  a trace=t1 x=y start,end=5\n" +
				"INFO  b start,end=7 trace=〃\n",
		},
		{
			Name: "keyframe resets elision",
			In: strings.Join([]string{
				`{"time":"2011-05-15T12:00:00Z","level":"info","msg":"a","trace":"t1"}`,
				`{"time":"2011-05-15T12:00:01Z","level":"info","msg":"b","trace":"t1"}`,
				`{"time":"2011-05-15T15:29:58Z","level":"info","msg":"c","trace":"t1"}`,
			}, "\n"),
			Out: "" +
				" 3h30m ago INFO  a trace=t1\n" +
				"       +1s INFO  b trace=〃\n" +
				"    2s ago INFO  c trace=t1\n",
		},
		{
			Name: "full",
			Opts: Options{Time: TimeNone, Full: true},
			In: `{"level":"info","msg":"a","trace":"t1"}
{"level":"info","msg":"b","trace":"t1"}`,
			Out: "INFO  a trace=t1\nINFO  b trace=t1\n",
		},
		{
			Name: "level filter",
			Opts: Options{Level: log.WarnLevel},
			In: `{"time":"2011-05-15T15:29:00Z","level":"info","msg":"hidden"}
{"time":"2011-05-15T15:29:00Z","level":"error","msg":"shown"}`,
			Out: "    1m ago ERROR shown\n",
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.Opts.Now = func() time.Time { return now }
			l := New(&buf, tc.Opts)
			err := l.Decode(t.Context(), strings.NewReader(tc.In))
			assert.NoError(t, err)
			assert.Equal(t, tc.Out, buf.String())
		})
	}
}

func TestFormatAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		500 * time.Millisecond:        "now",
		12 * time.Second:              "12s ago",
		5*time.Minute + 3*time.Second: "5m3s ago",
		time.Hour + 30*time.Second:    "1h ago",
		50 * time.Hour:                "2d2h ago",
		15 * 24 * time.Hour:           "2w1d ago",
		400 * 24 * time.Hour:          "1y5w ago",
		-4 * time.Second:              "in 4s",
	} {
		assert.Equal(t, want, formatAgo(d), d.String())
	}
}

func TestFormatDelta(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                         "+0",
		850:                       "+850ns",
		12300 * time.Nanosecond:   "+12.3µs",
		time.Millisecond:          "+1ms",
		123456 * time.Microsecond: "+123ms",
		42100 * time.Millisecond:  "+42.1s",
	} {
		assert.Equal(t, want, formatDelta(d), d.String())
	}
}
