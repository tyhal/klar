// Package main emits a randomised but realistic stream of JSON logs: a short
// story (boot, a failing request, a crash) with about -n lines of busy traffic
// in between. -n 0 fits a small terminal, for the demo.
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
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

func pick[T any](xs ...T) T { return xs[rand.IntN(len(xs))] }

var paths = []string{"/api/users", "/api/orders", "/api/cart", "/healthz"}

func story(extra int) []step {
	worker := rand.IntN(8) + 1
	user := rand.IntN(9000) + 1000
	req := fmt.Sprintf("%08x", rand.Uint32())

	boot := []step{
		// Boot, a couple of hours ago.
		line(0, charm.InfoLevel, "starting server", "version", "v1.4.2", "addr", ":8080", "tags", []string{}),
		line(jitter(2*time.Millisecond), charm.DebugLevel, "loaded config", "path", "/etc/app.yaml", "overrides", map[string]any{}),
		line(jitter(40*time.Millisecond), charm.InfoLevel, "connected to database", "host", "db-1", "pool", 10),
		line(jitter(300*time.Microsecond), charm.InfoLevel, "ready"),
	}
	traffic := []step{
		// Traffic, a few minutes ago.
		line(2*time.Hour+jitter(10*time.Minute), charm.InfoLevel, "request", "method", "GET", "path", pick(paths...), "status", 200, "dur", "12ms"),
		// One request's span: repeated context collapses to key,key=〃.
		line(jitter(80*time.Millisecond), charm.InfoLevel, "request", "req", req, "method", "POST", "path", "/api/orders"),
		line(jitter(2*time.Second), charm.WarnLevel, "slow query", "req", req, "method", "POST", "path", "/api/orders", "table", "orders", "dur", "1.8s", "trace", ""),
		line(jitter(15*time.Millisecond), charm.ErrorLevel, "payment failed", "req", req, "method", "POST", "path", "/api/orders", "err", "card declined", "user", user, "retry", nil),
		line(jitter(time.Millisecond), charm.InfoLevel, "response", "req", req, "status", 402, "dur", "2.1s"),
		line(jitter(5*time.Second), charm.DebugLevel, "cache miss", "key", fmt.Sprintf("user:%d", user)),
	}
	crash := []step{
		// Something just went wrong.
		line(8*time.Minute+jitter(4*time.Minute), charm.InfoLevel, "request", "method", "GET", "path", pick(paths...), "status", 200, "dur", "9ms"),
		raw(jitter(20*time.Microsecond), "panic: index out of range [3]"),
		line(jitter(50*time.Microsecond), charm.ErrorLevel, "worker crashed", "err", "index out of range", "worker", worker),
		line(jitter(time.Second), charm.InfoLevel, "worker restarted", "worker", worker, "attempt", 1),
	}
	return slices.Concat(boot, traffic, busy(extra), crash)
}

// span is one request's lines, each led by the request's own context the way
// a logger.With / span scope emits it.
func span() []step {
	req := fmt.Sprintf("%08x", rand.Uint32())
	method := pick("GET", "GET", "GET", "POST", "PUT", "DELETE")
	path := pick(paths[:3]...)
	ctx := []any{"req", req, "method", method, "path", path}
	with := func(kv ...any) []any { return append(slices.Clone(ctx), kv...) }
	user := rand.IntN(9000) + 1000

	steps := []step{line(jitter(40*time.Millisecond), charm.InfoLevel, "request", with()...)}
	for range rand.IntN(4) + 1 {
		gap := jitter(8 * time.Millisecond)
		steps = append(steps, pick(
			line(gap, charm.DebugLevel, "query", with("table", pick("users", "orders", "carts"), "rows", rand.IntN(50))...),
			line(gap, charm.DebugLevel, "cache hit", with("key", fmt.Sprintf("user:%d", user))...),
			line(gap, charm.DebugLevel, "cache miss", with("key", fmt.Sprintf("user:%d", user))...),
			line(gap, charm.InfoLevel, "calling upstream", with("service", pick("billing", "inventory", "auth"), "user", user)...),
		))
	}
	status, level := 200, charm.InfoLevel
	switch r := rand.IntN(20); {
	case r == 0:
		status, level = 500, charm.ErrorLevel
		steps = append(steps, line(jitter(time.Millisecond), charm.ErrorLevel, "upstream failed", with("err", "connection reset", "service", "billing")...))
	case r < 3:
		status, level = pick(400, 404, 409), charm.WarnLevel
	}
	return append(steps, line(jitter(time.Millisecond), level, "response", "req", req, "status", status, "dur", fmt.Sprintf("%dms", rand.IntN(300)+5)))
}

// interleave merges spans line by line, as concurrent requests share a log.
func interleave(a, b []step) []step {
	var out []step
	for len(a) > 0 || len(b) > 0 {
		if len(a) > 0 {
			out, a = append(out, a[0]), a[1:]
		}
		if len(b) > 0 {
			out, b = append(out, b[0]), b[1:]
		}
	}
	return out
}

// busy is about n lines of mixed traffic: request spans, some interleaved,
// health checks, stray raw lines and the occasional quiet minute or two.
func busy(n int) []step {
	var out []step
	for len(out) < n {
		switch r := rand.IntN(20); {
		case r < 11:
			out = append(out, span()...)
		case r < 14:
			out = append(out, interleave(span(), span())...)
		case r < 17:
			out = append(out, line(jitter(200*time.Millisecond), charm.DebugLevel, "health check", "path", "/healthz", "status", 200))
		case r < 18:
			out = append(out,
				raw(jitter(time.Millisecond), fmt.Sprintf("goroutine %d [running]:", rand.IntN(500))),
				raw(jitter(10*time.Microsecond), "\tmain.handler(0xc000123450)"),
			)
		default:
			out = append(out, line(time.Minute+jitter(2*time.Minute), charm.InfoLevel, "gc", "heap", fmt.Sprintf("%dMB", rand.IntN(400)+50), "pause", "1.2ms"))
		}
	}
	return out
}

func main() {
	extra := flag.Int("n", 100, "about how many lines of busy traffic to add (0 for just the short story)")
	flag.Parse()
	steps := story(*extra)

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
