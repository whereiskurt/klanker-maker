package compiler

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func boolp(b bool) *bool { return &b }

func TestEnableBedrock(t *testing.T) {
	cases := []struct {
		name   string
		use    bool
		allow  *bool
		expect bool
	}{
		{"neither", false, nil, false},
		{"useBedrock only", true, nil, true},
		{"allowBedrock only", false, boolp(true), true},
		{"allow false", false, boolp(false), false},
		{"both", true, boolp(true), true},
	}
	for _, c := range cases {
		p := &profile.SandboxProfile{}
		p.Spec.Execution.UseBedrock = c.use
		p.Spec.IAM.AllowBedrock = c.allow
		if got := enableBedrock(p); got != c.expect {
			t.Errorf("%s: enableBedrock=%v want %v", c.name, got, c.expect)
		}
	}
}
