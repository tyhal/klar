package klar

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// carry tracks the key/values of the previously printed entry so that a
// repeated leading run can be elided.
//
// Only a common *prefix* is elided, never arbitrary matching pairs. Context
// attached via spans or logger.With is emitted before call-site fields by
// slog, zap, zerolog, etc., so it forms the leading run of every line in that
// scope; entering a span appends to it and leaving truncates it. Matching by
// prefix keeps call-site fields that merely happen to repeat (status=200)
// visible. Repeated values become 〃, and keys sharing a value merge, so they
// collapse to a single "key,key=〃" naming what is still in scope; a dropped
// key is simply absent.
type carry struct {
	prev []any
}

// next returns how many leading key/value pairs of kv repeat the previous
// entry, and records kv as the new previous entry.
func (c *carry) next(kv []any) int {
	n := 0
	for i := 0; i+1 < len(kv) && i+1 < len(c.prev); i += 2 {
		if kv[i] != c.prev[i] || !reflect.DeepEqual(kv[i+1], c.prev[i+1]) {
			break
		}
		n++
	}
	c.prev = kv
	return n
}

// reset forgets the previous entry, so the next one is printed in full.
func (c *carry) reset() { c.prev = nil }

// ditto stands in for a value repeated from the line above.
type ditto struct{}

func (ditto) String() string { return "〃" }

// dittoPrefix returns kv with the values of its first n pairs replaced by ditto.
func dittoPrefix(kv []any, n int) []any {
	if n == 0 {
		return kv
	}
	out := slices.Clone(kv)
	for i := 1; i < 2*n; i += 2 {
		out[i] = ditto{}
	}
	return out
}

// mergeEqual merges keys that share a value into one "a,b=v" pair. Single
// pairs keep their order and come first, so what is new on the line leads;
// merged pairs follow in order of first appearance, with ditto always last.
func mergeEqual(kv []any) []any {
	type group struct {
		keys []string
		val  any
	}
	var groups []*group
	for i := 0; i+1 < len(kv); i += 2 {
		k := fmt.Sprint(kv[i])
		j := slices.IndexFunc(groups, func(g *group) bool { return reflect.DeepEqual(g.val, kv[i+1]) })
		if j >= 0 {
			groups[j].keys = append(groups[j].keys, k)
		} else {
			groups = append(groups, &group{keys: []string{k}, val: kv[i+1]})
		}
	}
	rank := func(g *group) int {
		switch {
		case g.val == ditto{}:
			return 2
		case len(g.keys) > 1:
			return 1
		}
		return 0
	}
	slices.SortStableFunc(groups, func(a, b *group) int { return rank(a) - rank(b) })
	out := make([]any, 0, 2*len(groups))
	for _, g := range groups {
		out = append(out, strings.Join(g.keys, ","), g.val)
	}
	return out
}
