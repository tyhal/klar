package klar

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/stretchr/testify/assert"
)

// now is the fixed "present" every relative time is measured from.
var now = time.Date(2011, time.May, 15, 15, 30, 0, 0, time.UTC)

// render decodes the in lines with opts and returns the printed lines.
func render(t *testing.T, opts Options, in ...string) []string {
	t.Helper()
	var buf bytes.Buffer
	opts.Now = func() time.Time { return now }
	err := New(&buf, opts).Decode(t.Context(), strings.NewReader(strings.Join(in, "\n")))
	assert.NoError(t, err)
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

type renderCase struct {
	Name string
	Opts Options
	In   []string
	Out  []string
}

func runRender(t *testing.T, cases []renderCase) {
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Equal(t, tc.Out, render(t, tc.Opts, tc.In...))
		})
	}
}

func TestParse(t *testing.T) {
	testcase := []struct {
		Name     string
		JSON     string
		Expected logEntry
		Err      bool
	}{
		{
			// Field order is what the logger chose (context first, call-site
			// after); a map would shuffle it every line. Empty values are
			// noise, but zero and false are real data.
			Name: "keeps field order, drops only empty values",
			JSON: `{"msg":"hi","z":1,"empty":"","nil":null,"obj":{},"arr":[],"a":"x","zero":0,"f":false}`,
			Expected: logEntry{
				Level:  noLevel,
				Msg:    "hi",
				Fields: []field{{"z", number("1")}, {"a", "x"}, {"zero", number("0")}, {"f", false}},
			},
		},
		{
			// Through a float64, ids print as 1.2345678e+07 and integers
			// past 2^53 silently change.
			Name: "numbers are kept as written",
			JSON: `{"id":12345678,"big":9007199254740993,"ratio":0.5}`,
			Expected: logEntry{
				Level:  noLevel,
				Fields: []field{{"id", number("12345678")}, {"big", number("9007199254740993")}, {"ratio", number("0.5")}},
			},
		},
		{
			// Each nested value becomes its own field, so it can repeat as 〃
			// on its own; arrays read like the rest of the line.
			Name: "nested objects flatten, arrays stay inline",
			JSON: `{"http":{"method":"GET","req":{"id":"r1"},"none":{}},"tags":["a","b",1]}`,
			Expected: logEntry{
				Level:  noLevel,
				Fields: []field{{"http.method", "GET"}, {"http.req.id", "r1"}, {"tags", "[a,b,1]"}},
			},
		},
		{
			// Loggers disagree on epoch units; values too large to be seconds
			// (past year ~33658) must be milliseconds.
			Name:     "epoch seconds",
			JSON:     `{"ts":1700000000.5}`,
			Expected: logEntry{Level: noLevel, Time: time.Unix(1700000000, 5e8)},
		},
		{
			Name:     "epoch millis",
			JSON:     `{"ts":1700000000123}`,
			Expected: logEntry{Level: noLevel, Time: time.Unix(1700000000, 123e6)},
		},
		{
			// zerolog's nanosecond epochs don't fit a float64 exactly.
			Name:     "epoch nanos",
			JSON:     `{"time":1700000000123456789}`,
			Expected: logEntry{Level: noLevel, Time: time.Unix(1700000000, 123456789)},
		},
		{
			Name:     "ECS @timestamp, with a space as Python writes it",
			JSON:     `{"@timestamp":"2023-11-14 22:13:20.5Z"}`,
			Expected: logEntry{Level: noLevel, Time: time.Unix(1700000000, 5e8)},
		},
		{
			// A level misread as info would hide under --level warn, so the
			// common names and keys are all recognised.
			Name:     "level aliases",
			JSON:     `{"log.level":"WARNING","msg":"hi"}`,
			Expected: logEntry{Level: log.WarnLevel, Msg: "hi"},
		},
		{
			// pino and bunyan write levels as numbers: 30 info … 50 error.
			Name:     "numeric level",
			JSON:     `{"level":50}`,
			Expected: logEntry{Level: log.ErrorLevel},
		},
		{
			// logback and log4j write offsets without a colon.
			Name:     "+0000 offset",
			JSON:     `{"@timestamp":"2023-11-14T22:13:20.000+0000"}`,
			Expected: logEntry{Level: noLevel, Time: time.Unix(1700000000, 0)},
		},
		{
			// A "level" we can't parse is somebody's data, not ours to drop.
			Name: "unrecognised level stays a field",
			JSON: `{"level":"loud","msg":"hi"}`,
			Expected: logEntry{
				Level:  noLevel,
				Msg:    "hi",
				Fields: []field{{"level", "loud"}},
			},
		},
		{
			// Valid JSON that isn't an object must error so Decode shows it raw.
			Name: "not an object",
			JSON: `[1,2]`,
			Err:  true,
		},
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
			if len(le.Fields) == 0 {
				le.Fields = nil // empty and nil print the same
			}
			assert.Equal(t, tc.Expected, le)
		})
	}
}

// The relative time column shows an "ago" keyframe to anchor on, then deltas
// from the line above, which are what matter when reading a burst. Keyframes
// depend on lines on screen, never on time.
func TestTimeColumn(t *testing.T) {
	runRender(t, []renderCase{
		{
			Name: "a burst reads as deltas from the line above",
			In: []string{
				`{"time":"2011-05-15T15:29:00Z","msg":"a"}`,
				`{"time":"2011-05-15T15:29:00.0015Z","msg":"b"}`,
				`{"time":"2011-05-15T15:29:02Z","msg":"c"}`,
			},
			Out: []string{
				"    1m ago       a",
				"    +1.5ms       b",
				"       +2s       c",
			},
		},
		{
			// A gap is just a bigger delta; it doesn't make a keyframe.
			Name: "a long gap is still a delta",
			In: []string{
				`{"time":"2011-05-15T12:00:00Z","msg":"a"}`,
				`{"time":"2011-05-15T15:29:58Z","msg":"b"}`,
			},
			Out: []string{
				" 3h30m ago       a",
				"    +3h29m       b",
			},
		},
		{
			// Interleaved sources go back in time; say how far.
			Name: "going backwards is a negative delta",
			In: []string{
				`{"time":"2011-05-15T15:29:30Z","msg":"a"}`,
				`{"time":"2011-05-15T15:29:20Z","msg":"b"}`,
			},
			Out: []string{
				"   30s ago       a",
				"      -10s       b",
			},
		},
		{
			// Otherwise the last keyframe scrolls off screen, leaving deltas
			// with nothing to add up from. N defaults to half a 24-row
			// terminal, so every screenful has one.
			Name: "a keyframe is forced every N lines",
			Opts: Options{KeyframeLines: 2},
			In: []string{
				`{"time":"2011-05-15T15:29:00Z","msg":"a"}`,
				`{"time":"2011-05-15T15:29:01Z","msg":"b"}`,
				`{"time":"2011-05-15T15:29:02Z","msg":"c"}`,
				`{"msg":"untimed"}`,
				`{"time":"2011-05-15T15:29:03Z","msg":"d"}`,
			},
			Out: []string{
				"    1m ago       a",       // first line
				"       +1s       b",       //
				"   58s ago       c",       // forced: 2 lines since a
				"                 untimed", // still takes up a line...
				"   57s ago       d",       // ...so forced: 2 lines since c
			},
		},
		{
			// Lines without a time still pad the column so messages align.
			Name: "untimed lines keep the column",
			In: []string{
				`{"time":"2011-05-15T15:29:00Z","msg":"a"}`,
				`plain text`,
			},
			Out: []string{
				"    1m ago       a",
				"           plain text",
			},
		},
		{
			// Converting to local time would hide which zone the source runs
			// in; milliseconds keep a burst apart; a fixed width keeps the
			// untimed line aligned.
			Name: "absolute keeps the source's offset, at a fixed width",
			Opts: Options{Time: TimeAbsolute},
			In: []string{
				`{"time":"2011-05-15T12:00:00+02:00","msg":"hi"}`,
				`{"time":"2011-05-15T12:00:00.25Z","msg":"utc"}`,
				`{"msg":"untimed"}`,
			},
			Out: []string{
				"2011-05-15T12:00:00.000+02:00       hi",
				"2011-05-15T12:00:00.250+00:00       utc",
				"                                    untimed",
			},
		},
	})
}

// Values repeated from the line above become 〃, so a line spells out only
// what changed; read up to find the rest.
func TestDitto(t *testing.T) {
	runRender(t, []renderCase{
		{
			// What changed leads; everything else collapses into one 〃 at the
			// end, and keeps collapsing for as long as each line repeats the
			// one above. Matching is by key, not position: e moves in line 3.
			// Only the line above counts: c skipped line 3, so it's spelled
			// out again in line 4.
			Name: "only what changed is spelled out",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"a":1,"b":2,"c":3,"d":4}`,
				`{"a":1,"b":2,"c":3,"e":5}`,
				`{"a":1,"b":2,"e":5}`,
				`{"a":1,"b":2,"c":3}`,
			},
			Out: []string{
				"      a=1 b=2 c=3 d=4",
				"      e=5 a,b,c=〃",
				"      a,b,e=〃",
				"      c=3 a,b=〃",
			},
		},
		{
			// Loggers often put a field that differs every line (caller,
			// source) ahead of the context; it must not stop the rest
			// collapsing.
			Name: "a field changing every line doesn't block the rest",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"line":10,"trace":"t1","span":"s1"}`,
				`{"line":22,"trace":"t1","span":"s1"}`,
			},
			Out: []string{
				"      line=10 trace=t1 span=s1",
				"      line=22 trace,span=〃",
			},
		},
		{
			// Traced loggers carry many context keys; listing them all every
			// line says no more than "same as above" and pushes the line wide.
			// The first few (service, trace) are kept, the rest become "…".
			Name: "a long 〃 key list is cut short",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"service":"api","trace_id":"t1","parent_id":"p1","source":"otelhttp","kind":"client","n":1}`,
				`{"service":"api","trace_id":"t1","parent_id":"p1","source":"otelhttp","kind":"client","n":2}`,
			},
			Out: []string{
				"      service=api trace_id=t1 parent_id=p1 source=otelhttp kind=client n=1",
				"      n=2 service,trace_id,parent_id,…=〃",
			},
		},
		{
			// Same idea within a line: keys with one value read once, in the
			// place of the first.
			Name: "keys sharing a value merge",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"trace":"t1","start":5,"x":"y","end":5}`,
				`{"trace":"t1","start":7,"end":7}`,
			},
			Out: []string{
				"      trace=t1 start,end=5 x=y",
				"      start,end=7 trace=〃",
			},
		},
		{
			// A raw line (a panic, a printf) is shown exactly as written, and
			// has no fields, so the line after it has nothing above to repeat.
			Name: "an unparseable line breaks the chain",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"trace":"t1"}`,
				`plain`,
				`plain`,
				`{"trace":"t1"}`,
			},
			Out: []string{
				"      trace=t1",
				"plain",
				"plain",
				"      trace=t1",
			},
		},
		{
			// docker compose interleaves containers; the line above from
			// another one shares nothing with this one.
			Name: "a different source restarts the chain",
			Opts: Options{Time: TimeNone},
			In: []string{
				`api-1  | {"trace":"t1"}`,
				`api-1  | {"trace":"t1"}`,
				`db-1   | {"trace":"t1"}`,
			},
			Out: []string{
				"      api-1: trace=t1",
				"      api-1: trace=〃",
				"      db-1: trace=t1",
			},
		},
		{
			// 〃 points at the line above, however long ago it happened.
			Name: "a time gap doesn't break the chain",
			In: []string{
				`{"time":"2011-05-15T12:00:00Z","trace":"t1"}`,
				`{"time":"2011-05-15T15:29:58Z","trace":"t1"}`,
			},
			Out: []string{
				" 3h30m ago       trace=t1",
				"    +3h29m       trace=〃",
			},
		},
	})
}

func TestLevelFilter(t *testing.T) {
	runRender(t, []renderCase{
		{
			// Deltas and 〃 both refer to the line on screen above, so a
			// filtered-out line must not advance either.
			Name: "filtered lines are invisible to deltas and ditto",
			Opts: Options{Level: log.WarnLevel},
			In: []string{
				`{"time":"2011-05-15T15:29:00Z","level":"error","msg":"a","trace":"t1"}`,
				`{"time":"2011-05-15T15:29:00.5Z","level":"info","msg":"hidden","trace":"t2"}`,
				`{"time":"2011-05-15T15:29:01Z","level":"error","msg":"b","trace":"t1"}`,
			},
			Out: []string{
				"    1m ago ERROR a trace=t1",
				"       +1s ERROR b trace=〃",
			},
		},
		{
			// An unrecognised level (slog's ERROR+4) or none at all could be an
			// error; hiding it as "info" would lose it, so it shows unlabelled.
			Name: "lines without a known level always show",
			Opts: Options{Level: log.WarnLevel, Time: TimeNone},
			In: []string{
				`{"level":"ERROR+4","msg":"custom"}`,
				`{"msg":"none"}`,
			},
			Out: []string{
				"      custom level=ERROR+4",
				"      none",
			},
		},
		{
			// Raw text has no level to filter on, and a panic or stack trace
			// is usually what explains the error before it.
			Name: "raw lines always show",
			Opts: Options{Level: log.ErrorLevel, Time: TimeNone},
			In: []string{
				`{"level":"error","msg":"worker crashed"}`,
				`panic: boom`,
				`{"level":"info","msg":"hidden"}`,
			},
			Out: []string{
				"ERROR worker crashed",
				"panic: boom",
			},
		},
	})
}

func TestInput(t *testing.T) {
	runRender(t, []renderCase{
		{
			// docker compose and kubectl --prefix put the source before the
			// JSON; it names the line rather than making it unreadable.
			Name: "a prefix before the JSON names the source",
			Opts: Options{Time: TimeNone},
			In: []string{
				`api-1  | {"msg":"compose"}`,
				`[pod/api-7f/app] {"msg":"kubectl"}`,
			},
			Out: []string{
				"      api-1: compose",
				"      pod/api-7f/app: kubectl",
			},
		},
		{
			// kubectl logs --timestamps and docker compose logs -t put the
			// time before the JSON; it's the line's time, not its source.
			Name: "a timestamp prefix is the line's time",
			In: []string{
				`2011-05-15T15:29:00Z {"msg":"a","svc":"api"}`,
				`api-1  | 2011-05-15T15:29:00.1Z {"msg":"b","svc":"api"}`,
			},
			Out: []string{
				"    1m ago       a svc=api",
				"    +100ms       api-1: b svc=api",
			},
		},
		{
			// Text ending in JSON is prose, not a source label: several words,
			// or one ending in ':'.
			Name: "a sentence before JSON stays raw",
			Opts: Options{Time: TimeNone},
			In:   []string{`ERROR payment failed {"order":42}`, `panic: {"oops":1}`},
			Out:  []string{`ERROR payment failed {"order":42}`, `panic: {"oops":1}`},
		},
		{
			// Some formats write the level as text before the JSON.
			Name: "a level word before the JSON is the level",
			Opts: Options{Time: TimeNone},
			In:   []string{`ERROR {"code":500}`},
			Out:  []string{"ERROR code=500"},
		},
		{
			// One oversized line (a dumped payload) mustn't end the stream.
			Name: "a huge line doesn't stop the rest",
			Opts: Options{Time: TimeNone},
			In: []string{
				`{"msg":"huge","blob":"` + strings.Repeat("x", 2<<20) + `"}`,
				`{"msg":"after"}`,
			},
			Out: []string{
				"      huge blob=" + strings.Repeat("x", 2<<20),
				"      after",
			},
		},
		{
			// zap can repeat a key and a logged byte string may not be UTF-8;
			// neither should demote the line to raw text.
			Name: "duplicate keys and invalid UTF-8 still parse",
			Opts: Options{Time: TimeNone},
			In:   []string{`{"msg":"dup","a":1,"a":2}`, "{\"msg\":\"bytes\",\"b\":\"x\xff\"}"},
			Out:  []string{"      dup a=1 a=2", "      bytes b=\"x\ufffd\""},
		},
		{
			Name: "windows line endings",
			Opts: Options{Time: TimeNone},
			In:   []string{"{\"msg\":\"a\"}\r", "plain\r"},
			Out:  []string{"      a", "plain"},
		},
	})
}

func TestMessage(t *testing.T) {
	runRender(t, []renderCase{
		{
			// Escapes inside a JSON string are data; passed through, they could
			// retitle or clear the reader's terminal. Keys too: a flattened
			// header map puts data in keys.
			Name: "control characters are escaped",
			Opts: Options{Time: TimeNone},
			In:   []string{`{"msg":"a\u001b]0;title\u0007b","h":{"\u001b[2J":1}}`},
			Out:  []string{`      a\x1b]0;title\x07b h.\x1b[2J=1`},
		},
		{
			// A stack trace in the message would start each line at column 0;
			// below the line, indented, it reads as part of the entry.
			Name: "a multi-line message continues below",
			Opts: Options{Time: TimeNone},
			In:   []string{`{"level":"error","msg":"failed\n\tat Foo.bar\n\tat Main.run","k":1}`},
			Out: []string{
				"ERROR failed k=1",
				"  msg=",
				"  │     at Foo.bar",
				"  │     at Main.run",
			},
		},
	})
}

// charm renders the keys level, time, msg, caller and prefix specially and
// drops a "level" that isn't its own; every field must still print as data.
func TestReservedKeysStillPrint(t *testing.T) {
	out := render(t, Options{Time: TimeNone},
		`{"msg":"m","level":"loud","caller":"x.go:1","prefix":"p","message":"other"}`)
	assert.Equal(t, []string{"      m level=loud caller=x.go:1 prefix=p message=other"}, out)
}

// The time column is a fixed relWidth; a longer label would push the level
// and message out of alignment for that line.
func TestRelLabelsFitColumn(t *testing.T) {
	for d := time.Nanosecond; d < 99*365*24*time.Hour; d = d*3/2 + 1 {
		assert.LessOrEqual(t, len([]rune(formatAgo(d))), relWidth, d.String())
		assert.LessOrEqual(t, len([]rune(formatAgo(-d))), relWidth, d.String())
		assert.LessOrEqual(t, len([]rune(formatDelta(d))), relWidth, d.String())
		assert.LessOrEqual(t, len([]rune(formatDelta(-d))), relWidth, d.String())
	}
	// A nonsense timestamp (year 1700) saturates the gap at the extremes.
	for _, d := range []time.Duration{math.MinInt64, math.MaxInt64} {
		assert.LessOrEqual(t, len([]rune(formatDelta(d))), relWidth, d.String())
		assert.LessOrEqual(t, len([]rune(formatAgo(d))), relWidth, d.String())
	}
}

func TestFormatAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		// Sub-second precision is meaningless at keyframe scale.
		500 * time.Millisecond: "now",
		// Two most significant units; a zero second unit is dropped, not padded.
		5*time.Minute + 3*time.Second: "5m3s ago",
		time.Hour + 30*time.Second:    "1h ago",
		400 * 24 * time.Hour:          "1y5w ago",
		// Clock skew puts sources in the future; say so rather than lie.
		-4 * time.Second: "in 4s",
	} {
		assert.Equal(t, want, formatAgo(d), d.String())
	}
}

func TestFormatDelta(t *testing.T) {
	for d, want := range map[time.Duration]string{
		// Lines in the same instant are common; "+0" not "+0ns".
		0: "+0",
		// ~3 significant figures, trailing zeros trimmed.
		12300 * time.Nanosecond:   "+12.3µs",
		time.Millisecond:          "+1ms",
		123456 * time.Microsecond: "+123ms",
		// From a minute up, two units like the keyframes.
		3*time.Minute + 54*time.Second: "+3m54s",
		// Interleaved sources go back in time.
		-10 * time.Second: "-10s",
	} {
		assert.Equal(t, want, formatDelta(d), d.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Output is buffered for speed, but with kubectl logs -f a line must show as
// it arrives, not when the next one does, even if a producer that flushes in
// blocks has already sent part of that next one.
func TestLiveStreamShowsEachLine(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	got := make(chan string, 10)
	w := writerFunc(func(p []byte) (int, error) { got <- string(p); return len(p), nil })
	go func() { _ = New(w, Options{Time: TimeNone}).Decode(t.Context(), pr) }()

	_, _ = io.WriteString(pw, `{"msg":"a"}`+"\n"+`{"ms`)
	select {
	case s := <-got:
		assert.Equal(t, "      a\n", s)
	case <-time.After(time.Second):
		t.Fatal("line held back until more input arrived")
	}
}
