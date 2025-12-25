package klar

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
)

func TestLogEntry_UnmarshalJSON(t *testing.T) {
	testcase := []struct {
		Name     string
		JSON     []byte
		Expected logEntry
	}{
		{
			Name: "basic",
			JSON: []byte(`{"time":"2011-05-15T12:00:00+02:00","level":"info","msg":"Redistribution scheduled"}`),
			Expected: logEntry{
				Time:  time.Date(2011, time.May, 15, 12, 0, 0, 0, time.FixedZone("", 60*60*2)),
				Level: log.InfoLevel,
				Msg:   "Redistribution scheduled",
			},
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			var le logEntry
			err := le.UnmarshalJSON(tc.JSON)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, le)
		})
	}
}

func TestLogger_Decode(t *testing.T) {
	testcase := []struct {
		Name string
		In   string
		Out  string
	}{
		{
			Name: "basic",
			In: `
{"time":"2011-05-15T12:00:00+02:00","level":"info","msg":"Redistribution scheduled"}
`,
			Out: `2011-05-15T12:00:00+02:00 INFO Redistribution scheduled
`,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf)
			err := l.Decode(context.Background(), strings.NewReader(tc.In))
			assert.NoError(t, err)
			assert.Equal(t, tc.Out, buf.String())
		})
	}
}
