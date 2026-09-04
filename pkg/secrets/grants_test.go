package secrets_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func TestResolve(t *testing.T) {
	all := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DB_PASSWORD"}

	tests := []struct {
		name    string
		grants  secrets.Grants
		as      string
		only    []string
		want    []string
		wantErr error
	}{
		{
			// Migration case: today's behaviour, scoped to one process.
			name: "no grants and no identity yields the whole bundle",
			want: []string{"ANTHROPIC_API_KEY", "DB_PASSWORD", "OPENAI_API_KEY"},
		},
		{
			name: "no grants means an identity is not narrowing",
			as:   "claude",
			want: []string{"ANTHROPIC_API_KEY", "DB_PASSWORD", "OPENAI_API_KEY"},
		},
		{
			name:   "a granted identity receives only its keys",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:     "claude",
			want:   []string{"ANTHROPIC_API_KEY"},
		},
		{
			// An identity nobody granted is an error, not a silent full bundle.
			name:    "an ungranted identity is rejected once grants exist",
			grants:  secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:      "codex",
			wantErr: secrets.ErrUnknownConsumer,
		},
		{
			name:   "only narrows within a grant",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY", "DB_PASSWORD"}},
			as:     "claude",
			only:   []string{"DB_PASSWORD"},
			want:   []string{"DB_PASSWORD"},
		},
		{
			// The load-bearing property: --only intersects, it never widens.
			name:   "only cannot widen a grant",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:     "claude",
			only:   []string{"DB_PASSWORD"},
			want:   []string{},
		},
		{
			name: "only narrows the full bundle when no grants exist",
			only: []string{"DB_PASSWORD"},
			want: []string{"DB_PASSWORD"},
		},
		{
			// A grant may name a key a later bundle revision dropped.
			name:   "a granted key absent from the bundle is dropped",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY", "GONE"}},
			as:     "claude",
			want:   []string{"ANTHROPIC_API_KEY"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := secrets.Resolve(all, tc.grants, tc.as, tc.only)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Resolve() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveIsSorted(t *testing.T) {
	// Deterministic order keeps audit events diffable across unseals.
	got, err := secrets.Resolve([]string{"Z", "A", "M"}, nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"A", "M", "Z"}) {
		t.Errorf("Resolve() = %v, want sorted", got)
	}
}
