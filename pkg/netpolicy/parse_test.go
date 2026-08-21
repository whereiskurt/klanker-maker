package netpolicy_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantOK  bool
		comment string
	}{
		{name: "plain host", in: "evil.example.com", want: "evil.example.com", wantOK: true},
		{name: "surrounding whitespace", in: "  evil.example.com\t", want: "evil.example.com", wantOK: true},
		{name: "uppercase normalized", in: "EVIL.Example.COM", want: "evil.example.com", wantOK: true},
		{name: "trailing dot stripped", in: "evil.example.com.", want: "evil.example.com", wantOK: true},
		{name: "leading dot preserved", in: ".tracker.net", want: ".tracker.net", wantOK: true},
		{name: "wildcard", in: "*", want: "*", wantOK: true},

		{name: "blank", in: "", wantOK: false},
		{name: "whitespace only", in: "   ", wantOK: false},
		{name: "comment", in: "# a note", wantOK: false},
		{name: "indented comment", in: "   # a note", wantOK: false},

		// A malformed line must be dropped, never accepted as a pattern that
		// silently matches nothing (or worse, everything).
		{name: "embedded space", in: "evil.example.com extra", wantOK: false},
		{name: "url scheme", in: "https://evil.example.com", wantOK: false},
		{name: "path component", in: "evil.example.com/payload", wantOK: false},
		{name: "port suffix", in: "evil.example.com:443", wantOK: false},
		{name: "control character", in: "evil\x00.example.com", wantOK: false},
		{name: "bare dot", in: ".", wantOK: false},
		{name: "double wildcard", in: "**", wantOK: false},
		{name: "wildcard with suffix", in: "*.example.com", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := netpolicy.ParseLine(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ParseLine(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("ParseLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseLine_RejectsOverlongName(t *testing.T) {
	long := ""
	for len(long) < 300 {
		long += "a"
	}
	if _, ok := netpolicy.ParseLine(long); ok {
		t.Error("a name longer than the DNS maximum must be rejected")
	}
}
