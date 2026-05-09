package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// fakeIAMCleanup implements IAMCleanupAPI for stale-role teardown tests.
type fakeIAMCleanup struct {
	roles                  []string
	inlinePolicies         map[string][]string
	deleteRolePolicyErr    error
	deleteRoleErrs         map[string]error
	deletedRoles           []string
	deleteRoleCallCount    int
	deletePolicyCallCount  int
}

func (f *fakeIAMCleanup) ListRoles(_ context.Context, _ *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	out := &iam.ListRolesOutput{}
	for _, r := range f.roles {
		name := r
		out.Roles = append(out.Roles, iamtypes.Role{RoleName: awssdk.String(name)})
	}
	return out, nil
}

func (f *fakeIAMCleanup) ListRolePolicies(_ context.Context, in *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	rn := awssdk.ToString(in.RoleName)
	return &iam.ListRolePoliciesOutput{PolicyNames: f.inlinePolicies[rn]}, nil
}

func (f *fakeIAMCleanup) DeleteRolePolicy(_ context.Context, _ *iam.DeleteRolePolicyInput, _ ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error) {
	f.deletePolicyCallCount++
	if f.deleteRolePolicyErr != nil {
		return nil, f.deleteRolePolicyErr
	}
	return &iam.DeleteRolePolicyOutput{}, nil
}

func (f *fakeIAMCleanup) DeleteRole(_ context.Context, in *iam.DeleteRoleInput, _ ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	f.deleteRoleCallCount++
	rn := awssdk.ToString(in.RoleName)
	if err, ok := f.deleteRoleErrs[rn]; ok {
		return nil, err
	}
	f.deletedRoles = append(f.deletedRoles, rn)
	return &iam.DeleteRoleOutput{}, nil
}

func (f *fakeIAMCleanup) ListAttachedRolePolicies(_ context.Context, _ *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return &iam.ListAttachedRolePoliciesOutput{}, nil
}

func (f *fakeIAMCleanup) DetachRolePolicy(_ context.Context, _ *iam.DetachRolePolicyInput, _ ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	return &iam.DetachRolePolicyOutput{}, nil
}

func (f *fakeIAMCleanup) ListInstanceProfilesForRole(_ context.Context, _ *iam.ListInstanceProfilesForRoleInput, _ ...func(*iam.Options)) (*iam.ListInstanceProfilesForRoleOutput, error) {
	return &iam.ListInstanceProfilesForRoleOutput{}, nil
}

func (f *fakeIAMCleanup) RemoveRoleFromInstanceProfile(_ context.Context, _ *iam.RemoveRoleFromInstanceProfileInput, _ ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error) {
	return &iam.RemoveRoleFromInstanceProfileOutput{}, nil
}

func (f *fakeIAMCleanup) DeleteInstanceProfile(_ context.Context, _ *iam.DeleteInstanceProfileInput, _ ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error) {
	return &iam.DeleteInstanceProfileOutput{}, nil
}

// TestCheckStaleIAMRoles_PrecursorFailure_ReportedInline asserts that when
// DeleteRolePolicy fails for a stale role, the role is NOT passed to
// DeleteRole (which would just emit DeleteConflict and mask the real
// failure), and the result message includes the failing role + step.
func TestCheckStaleIAMRoles_PrecursorFailure_ReportedInline(t *testing.T) {
	staleRole := "km-budget-enforcer-sb-deadbeef"
	iamFake := &fakeIAMCleanup{
		roles: []string{staleRole},
		inlinePolicies: map[string][]string{
			staleRole: {"inline-pol-1"},
		},
		deleteRolePolicyErr: errors.New("AccessDenied: not allowed to delete inline policy"),
	}
	// No live sandboxes — so staleRole gets classified stale.
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{}}

	// dryRun=false to trigger the teardown branch.
	r := checkStaleIAMRoles(context.Background(), iamFake, lister, false, "km")

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	// DeleteRole must NOT have been invoked — precursor failure should short-circuit.
	if iamFake.deleteRoleCallCount != 0 {
		t.Errorf("DeleteRole should NOT be called when DeleteRolePolicy fails; got %d calls", iamFake.deleteRoleCallCount)
	}
	if iamFake.deletePolicyCallCount != 1 {
		t.Errorf("expected 1 DeleteRolePolicy call, got %d", iamFake.deletePolicyCallCount)
	}
	// Message should mention the role + the failing step + the underlying error.
	if !strings.Contains(r.Message, staleRole) {
		t.Errorf("message missing role name: %q", r.Message)
	}
	if !strings.Contains(r.Message, "DeleteRolePolicy") {
		t.Errorf("message missing failing step: %q", r.Message)
	}
	if !strings.Contains(r.Message, "AccessDenied") {
		t.Errorf("message missing underlying error: %q", r.Message)
	}
	if !strings.Contains(r.Message, "0 deleted") {
		t.Errorf("message should report 0 deleted: %q", r.Message)
	}
}

// TestCheckStaleIAMRoles_HappyPath confirms a clean role tears down end-to-end
// with no errors emitted in the message.
func TestCheckStaleIAMRoles_HappyPath(t *testing.T) {
	staleRole := "km-budget-enforcer-sb-deadbeef"
	iamFake := &fakeIAMCleanup{
		roles:          []string{staleRole},
		inlinePolicies: map[string][]string{staleRole: {}},
	}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{}}

	r := checkStaleIAMRoles(context.Background(), iamFake, lister, false, "km")

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn (always emits warn when stale found), got %s: %s", r.Status, r.Message)
	}
	if iamFake.deleteRoleCallCount != 1 {
		t.Errorf("expected 1 DeleteRole call, got %d", iamFake.deleteRoleCallCount)
	}
	if !strings.Contains(r.Message, "1 deleted") {
		t.Errorf("expected '1 deleted' in message, got: %q", r.Message)
	}
	if strings.Contains(r.Message, "first failures") {
		t.Errorf("happy path should not mention failures: %q", r.Message)
	}
}
