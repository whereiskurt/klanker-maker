package cmd

import (
	"strings"
	"testing"
)

// TestResolveCreateArgs covers the positional-argument disambiguation for
// `km create`. The one-arg form must stay byte-identical to the pre-change
// behaviour; the two-arg form infers which positional is the profile from the
// .yaml/.yml extension so operators can type either order.
func TestResolveCreateArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		aliasFlag   string
		wantProfile string
		wantAlias   string
		wantErr     string
	}{
		{
			name:        "one arg, no alias flag",
			args:        []string{"profiles/dc34.yaml"},
			wantProfile: "profiles/dc34.yaml",
		},
		{
			name:        "one arg with alias flag",
			args:        []string{"profiles/dc34.yaml"},
			aliasFlag:   "orc",
			wantProfile: "profiles/dc34.yaml",
			wantAlias:   "orc",
		},
		{
			name:        "one arg that is not yaml is still the profile",
			args:        []string{"myprofile"},
			wantProfile: "myprofile",
		},
		{
			name:        "two args, profile first",
			args:        []string{"profiles/dc34.yaml", "orc"},
			wantProfile: "profiles/dc34.yaml",
			wantAlias:   "orc",
		},
		{
			name:        "two args, alias first",
			args:        []string{"orc", "profiles/dc34.yaml"},
			wantProfile: "profiles/dc34.yaml",
			wantAlias:   "orc",
		},
		{
			name:        "two args, .yml extension",
			args:        []string{"wrkr", "profiles/dc34.yml"},
			wantProfile: "profiles/dc34.yml",
			wantAlias:   "wrkr",
		},
		{
			name:        "extension match is case-insensitive",
			args:        []string{"orc", "profiles/DC34.YAML"},
			wantProfile: "profiles/DC34.YAML",
			wantAlias:   "orc",
		},
		{
			name:    "two args, both yaml is ambiguous",
			args:    []string{"a.yaml", "b.yaml"},
			wantErr: "ambiguous",
		},
		{
			name:    "two args, neither yaml",
			args:    []string{"orc", "wrkr"},
			wantErr: "no profile",
		},
		{
			name:      "two args plus --alias conflicts",
			args:      []string{"profiles/dc34.yaml", "orc"},
			aliasFlag: "wrkr",
			wantErr:   "--alias",
		},
		{
			name:    "no args",
			args:    nil,
			wantErr: "profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProfile, gotAlias, err := resolveCreateArgs(tt.args, tt.aliasFlag)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (profile=%q alias=%q)", tt.wantErr, gotProfile, gotAlias)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotProfile != tt.wantProfile {
				t.Errorf("profile = %q, want %q", gotProfile, tt.wantProfile)
			}
			if gotAlias != tt.wantAlias {
				t.Errorf("alias = %q, want %q", gotAlias, tt.wantAlias)
			}
		})
	}
}
