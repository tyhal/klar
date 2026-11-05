package klar

import "slices"

// field is one key/value pair of an entry, in the order the logger wrote it.
type field struct {
	key string
	val any
}

func (f field) equal(g field) bool { return f == g }

// fieldKey is a key handed to charm. charm renders the plain strings "level",
// "time", "msg", "caller" and "prefix" specially, dropping a "level" that
// isn't one of its own; a distinct type makes every field print as key=value.
type fieldKey string

// keyvals flattens fields into the alternating key/value list charm logs.
// Keys can come from data (a flattened header map), so they're escaped too.
func keyvals(fs []field) []any {
	kv := make([]any, 0, 2*len(fs))
	for _, f := range fs {
		kv = append(kv, fieldKey(escapeControl(f.key)), f.val)
	}
	return kv
}

// ditto stands in for a value repeated from the line above.
type ditto struct{}

func (ditto) String() string { return "〃" }

func isDitto(v any) bool {
	_, ok := v.(ditto)
	return ok
}

// carry remembers the fields of the line above, so the ones it repeats can be
// shown as 〃 and only what changed is spelled out.
//
// Fields are matched by key and value, wherever they sit on either line, so
// a field that changes every line (a caller, a span id) or an encoder that
// sorts keys can't stop the unchanged context from collapsing. Only the line
// above counts, as that is where a reader looks to resolve a 〃; a field that
// skipped a line is spelled out again. A line from another source (another
// container in docker compose) shares nothing with it, so the chain restarts.
type carry struct {
	source string
	prev   []field
}

// next returns fs, from source, with the values the previous line repeats
// replaced by ditto, and remembers fs as the previous line.
func (c *carry) next(source string, fs []field) []field {
	if source != c.source {
		c.prev = nil
	}
	out := slices.Clone(fs)
	for i, f := range fs {
		if slices.ContainsFunc(c.prev, f.equal) {
			out[i].val = ditto{}
		}
	}
	c.source, c.prev = source, fs
	return out
}

// reset forgets the previous line, so the next one is printed in full.
func (c *carry) reset() { c.prev = nil }

// dittoKeysWidth caps the key list of the 〃 field. Past a few keys the list
// says no more than "same as above"; the first ones are kept as loggers emit
// context (service, trace) first.
const dittoKeysWidth = 30

// merge joins keys that share a value into one "a,b=v" field, placed where
// its first key was. Dittoed keys always form the last field, so what changed
// leads the line and the 〃 trails it: "e=5 a,b,c=〃".
func merge(fs []field) []field {
	var out []field
	var repeated []string
	for _, f := range fs {
		if isDitto(f.val) {
			repeated = append(repeated, f.key)
			continue
		}
		i := slices.IndexFunc(out, func(g field) bool { return g.val == f.val })
		if i >= 0 {
			out[i].key += "," + f.key
			continue
		}
		out = append(out, f)
	}
	if len(repeated) > 0 {
		out = append(out, field{joinCapped(repeated, dittoKeysWidth), ditto{}})
	}
	return out
}

// joinCapped joins keys with commas, ending in "…" rather than passing width.
// The first key is always kept.
func joinCapped(keys []string, width int) string {
	s := keys[0]
	for _, k := range keys[1:] {
		if len(s)+len(","+k) > width {
			return s + ",…"
		}
		s += "," + k
	}
	return s
}
