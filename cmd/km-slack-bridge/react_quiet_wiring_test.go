package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestWireReactQuiet(t *testing.T) {
	cases := []struct {
		env      string
		want     int
		wantWarn bool
	}{
		{"", 180, false},
		{"0", 0, false},
		{"600", 600, false},
		{" 90 ", 90, false},
		{"abc", 180, true},
		{"-5", 180, true},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			var buf bytes.Buffer
			orig := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(orig) })

			h := &bridge.EventsHandler{}
			WireReactQuiet(h, func(k string) string {
				if k == "KM_SLACK_REACT_QUIET_SECONDS" {
					return tc.env
				}
				return ""
			})
			if h.ReactQuietSeconds != tc.want {
				t.Errorf("ReactQuietSeconds = %d, want %d", h.ReactQuietSeconds, tc.want)
			}
			if got := strings.Contains(buf.String(), "level=WARN"); got != tc.wantWarn {
				t.Errorf("warn logged = %v, want %v; log: %s", got, tc.wantWarn, buf.String())
			}
			if !strings.Contains(buf.String(), "react_quiet_seconds=") {
				t.Errorf("the effective value must be logged; log: %s", buf.String())
			}
		})
	}
}
