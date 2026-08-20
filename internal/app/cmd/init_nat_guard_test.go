package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestNATDisableGuard exercises the pure natDisableGuard function (Phase
// 125-06 Task 3 / T-125-17) across the six behaviour cases from the plan.
func TestNATDisableGuard(t *testing.T) {
	tests := []struct {
		name           string
		desiredEnabled bool
		sandboxes      []awspkg.SandboxMetadata
		wantErr        bool
		wantContains   []string // substrings the error message must contain
	}{
		{
			name:           "disabling with two running private sandboxes names both ids",
			desiredEnabled: false,
			sandboxes: []awspkg.SandboxMetadata{
				{SandboxID: "sb-alpha", NetworkPlacement: "private", Status: "running"},
				{SandboxID: "sb-beta", NetworkPlacement: "private", Status: "running"},
			},
			wantErr:      true,
			wantContains: []string{"sb-alpha", "sb-beta", "destroy"},
		},
		{
			name:           "disabling with private sandboxes stopped/destroyed does not block",
			desiredEnabled: false,
			sandboxes: []awspkg.SandboxMetadata{
				{SandboxID: "sb-alpha", NetworkPlacement: "private", Status: "stopped"},
				{SandboxID: "sb-beta", NetworkPlacement: "private", Status: "killed"},
			},
			wantErr: false,
		},
		{
			name:           "disabling with zero private sandboxes does not block",
			desiredEnabled: false,
			sandboxes: []awspkg.SandboxMetadata{
				{SandboxID: "sb-pub-1", NetworkPlacement: "public", Status: "running"},
			},
			wantErr: false,
		},
		{
			name:           "enabling never blocks regardless of sandbox state",
			desiredEnabled: true,
			sandboxes: []awspkg.SandboxMetadata{
				{SandboxID: "sb-alpha", NetworkPlacement: "private", Status: "running"},
				{SandboxID: "sb-beta", NetworkPlacement: "private", Status: "running"},
			},
			wantErr: false,
		},
		{
			name:           "pre-125 empty NetworkPlacement treated as public — does not block",
			desiredEnabled: false,
			sandboxes: []awspkg.SandboxMetadata{
				{SandboxID: "sb-old", NetworkPlacement: "", Status: "running"},
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := natDisableGuard(tc.desiredEnabled, tc.sandboxes)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestNATDisableGuard_NilListerDegradesGracefully is behaviour Test 6: a nil
// lister (no DynamoDB access wired) must degrade to a WARN-and-proceed, never
// panic and never silently block a disabling apply.
func TestNATDisableGuard_NilListerDegradesGracefully(t *testing.T) {
	err := natDisableGuardPreApply(context.Background(), false /* desiredEnabled */, true /* currentlyEnabled */, nil /* lister */)
	if err != nil {
		t.Fatalf("expected nil lister to degrade gracefully (WARN + proceed), got error: %v", err)
	}
}

// TestNATDisableGuard_PreApply_ListerErrorDegradesGracefully covers the
// sibling case: a lister that itself errors (e.g. a transient DynamoDB
// failure) must also WARN and proceed rather than blocking the apply.
func TestNATDisableGuard_PreApply_ListerErrorDegradesGracefully(t *testing.T) {
	failingLister := func(_ context.Context) ([]awspkg.SandboxMetadata, error) {
		return nil, errors.New("simulated DynamoDB failure")
	}
	err := natDisableGuardPreApply(context.Background(), false, true, failingLister)
	if err != nil {
		t.Fatalf("expected a lister error to degrade gracefully, got: %v", err)
	}
}

// TestNATDisableGuard_PreApply_NotCurrentlyEnabledSkipsListerEntirely verifies
// that when NAT was never on (or already off), the guard never even calls the
// lister — disabling an already-off NAT has nothing to protect.
func TestNATDisableGuard_PreApply_NotCurrentlyEnabledSkipsListerEntirely(t *testing.T) {
	called := false
	lister := func(_ context.Context) ([]awspkg.SandboxMetadata, error) {
		called = true
		return nil, nil
	}
	err := natDisableGuardPreApply(context.Background(), false /* desiredEnabled */, false /* currentlyEnabled */, lister)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if called {
		t.Error("expected the lister NOT to be called when NAT was never currently enabled")
	}
}

// TestNATDisableGuard_PreApply_GenuineDisableListsAndBlocks is an end-to-end
// wiring check: desiredEnabled=false + currentlyEnabled=true + a lister
// returning a running private sandbox must produce the blocking error.
func TestNATDisableGuard_PreApply_GenuineDisableListsAndBlocks(t *testing.T) {
	lister := func(_ context.Context) ([]awspkg.SandboxMetadata, error) {
		return []awspkg.SandboxMetadata{
			{SandboxID: "sb-live", NetworkPlacement: "private", Status: "running"},
		}, nil
	}
	err := natDisableGuardPreApply(context.Background(), false, true, lister)
	if err == nil {
		t.Fatal("expected an error for a genuine disable with a running private sandbox")
	}
	if !strings.Contains(err.Error(), "sb-live") {
		t.Errorf("error %q does not name sb-live", err.Error())
	}
}
