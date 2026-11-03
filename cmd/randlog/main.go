// Package main emits a short, randomised but realistic stream of JSON logs,
// sized to fit a small terminal when piped through klar for a demo.
package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	charm "charm.land/log/v2"
)

// step is one line of the demo story. gap is the time since the previous line.
type step struct {
	gap     time.Duration
	level   charm.Level
	msg     string
	keyvals []any
	raw     bool // emit msg as a plain, non-JSON line
}

func line(gap time.Duration, level charm.Level, msg string, keyvals ...any) step {
	return step{gap: gap, level: level, msg: msg, keyvals: keyvals}
}

func raw(gap time.Duration, msg string) step {
	return step{gap: gap, msg: msg, raw: true}
}

func jitter(base time.Duration) time.Duration {
	return base/2 + rand.N(base)
}

func story() []step {
	worker := rand.IntN(8) + 1
	user := rand.IntN(9000) + 1000
	req := fmt.Sprintf("%08x", rand.Uint32())
	paths := []string{"/api/users", "/api/orders", "/api/cart", "/healthz"}
	path := func() string { return paths[rand.IntN(len(paths))] }

	return []step{
		// Boot, a couple of hours ago.
		line(0, charm.InfoLevel, "starting server", "version", "v1.4.2", "addr", ":8080", "tags", []string{}),
		line(jitter(2*time.Millisecond), charm.DebugLevel, "loaded config", "path", "/etc/app.yaml", "overrides", map[string]any{}),
		line(jitter(40*time.Millisecond), charm.InfoLevel, "connected to database", "host", "db-1", "pool", 10),
		line(jitter(300*time.Microsecond), charm.InfoLevel, "ready"),

		// Traffic, a few minutes ago.
		line(2*time.Hour+jitter(10*time.Minute), charm.InfoLevel, "request", "method", "GET", "path", path(), "status", 200, "dur", "12ms"),
		// One request's span: repeated leading context collapses to key,key=〃.
		line(jitter(80*time.Millisecond), charm.InfoLevel, "request", "req", req, "method", "POST", "path", "/api/orders"),
		line(jitter(2*time.Second), charm.WarnLevel, "slow query", "req", req, "method", "POST", "path", "/api/orders", "table", "orders", "dur", "1.8s", "trace", ""),
		line(jitter(15*time.Millisecond), charm.ErrorLevel, "payment failed", "req", req, "method", "POST", "path", "/api/orders", "err", "card declined", "user", user, "retry", nil),
		line(jitter(time.Millisecond), charm.InfoLevel, "response", "req", req, "status", 402, "dur", "2.1s"),
		line(jitter(5*time.Second), charm.DebugLevel, "cache miss", "key", fmt.Sprintf("user:%d", user)),

		// Something just went wrong.
		line(8*time.Minute+jitter(4*time.Minute), charm.InfoLevel, "request", "method", "GET", "path", path(), "status", 200, "dur", "9ms"),
		raw(jitter(20*time.Microsecond), "panic: index out of range [3]"),
		line(jitter(50*time.Microsecond), charm.ErrorLevel, "worker crashed", "err", "index out of range", "worker", worker),
		line(jitter(time.Second), charm.InfoLevel, "worker restarted", "worker", worker, "attempt", 1),
	}
}

func main() {
	steps := story()

	var total time.Duration
	for _, s := range steps {
		total += s.gap
	}
	// Anchor the story so the last line lands a few seconds ago.
	t := time.Now().Add(-total - jitter(10*time.Second))

	logger := charm.NewWithOptions(os.Stdout, charm.Options{
		Formatter:       charm.JSONFormatter,
		Level:           charm.DebugLevel,
		ReportTimestamp: true,
		TimeFormat:      time.RFC3339Nano,
	})

	for _, s := range steps {
		t = t.Add(s.gap)
		if s.raw {
			fmt.Println(s.msg)
			continue
		}
		at := t
		logger.SetTimeFunction(func(time.Time) time.Time { return at })
		logger.Log(s.level, s.msg, s.keyvals...)
	}
}
