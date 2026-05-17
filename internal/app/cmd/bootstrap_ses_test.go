package cmd_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// mockSESIdentityLister implements cmd.SESIdentityLister for testing.
type mockSESIdentityLister struct {
	// ruleSetNames is the list of existing receipt rule set names.
	ruleSetNames []string
	// domainIdentities is the list of existing domain identity names.
	domainIdentities []string
	// listRuleSetsErr is returned by ListReceiptRuleSets when non-nil.
	listRuleSetsErr error
	// listIdentitiesErr is returned by ListEmailIdentities when non-nil.
	listIdentitiesErr error
}

func (m *mockSESIdentityLister) ListReceiptRuleSets(_ context.Context, _ *ses.ListReceiptRuleSetsInput, _ ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error) {
	if m.listRuleSetsErr != nil {
		return nil, m.listRuleSetsErr
	}
	rsets := make([]sestypes.ReceiptRuleSetMetadata, 0, len(m.ruleSetNames))
	for _, name := range m.ruleSetNames {
		n := name
		rsets = append(rsets, sestypes.ReceiptRuleSetMetadata{Name: aws.String(n)})
	}
	return &ses.ListReceiptRuleSetsOutput{RuleSets: rsets}, nil
}

func (m *mockSESIdentityLister) ListEmailIdentities(_ context.Context, _ *sesv2.ListEmailIdentitiesInput, _ ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error) {
	if m.listIdentitiesErr != nil {
		return nil, m.listIdentitiesErr
	}
	ids := make([]sesv2types.IdentityInfo, 0, len(m.domainIdentities))
	for _, name := range m.domainIdentities {
		n := name
		ids = append(ids, sesv2types.IdentityInfo{
			IdentityName: aws.String(n),
			IdentityType: sesv2types.IdentityTypeDomain,
		})
	}
	return &sesv2.ListEmailIdentitiesOutput{EmailIdentities: ids}, nil
}

// TestDetectSharedSESState exercises the three key scenarios for detectSharedSESState.
func TestDetectSharedSESState(t *testing.T) {
	const ruleSetName = "sandbox-email-shared"
	const emailDomain = "sandboxes.example.com"

	t.Run("BothAbsent_BothTrue", func(t *testing.T) {
		// Neither rule set nor identity exists → should create both.
		mock := &mockSESIdentityLister{}
		registerRS, registerID, err := cmd.DetectSharedSESState(context.Background(), mock, ruleSetName, emailDomain)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !registerRS {
			t.Error("expected registerSharedRuleSet=true (rule set absent)")
		}
		if !registerID {
			t.Error("expected registerDomainIdentity=true (identity absent)")
		}
	})

	t.Run("RuleSetExists_IdentityAbsent", func(t *testing.T) {
		// Rule set already exists; identity does not → reuse rule set, create identity.
		mock := &mockSESIdentityLister{
			ruleSetNames: []string{ruleSetName},
		}
		registerRS, registerID, err := cmd.DetectSharedSESState(context.Background(), mock, ruleSetName, emailDomain)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if registerRS {
			t.Error("expected registerSharedRuleSet=false (rule set present)")
		}
		if !registerID {
			t.Error("expected registerDomainIdentity=true (identity absent)")
		}
	})

	t.Run("BothPresent_BothFalse", func(t *testing.T) {
		// Both already exist → reuse both.
		mock := &mockSESIdentityLister{
			ruleSetNames:     []string{ruleSetName},
			domainIdentities: []string{emailDomain},
		}
		registerRS, registerID, err := cmd.DetectSharedSESState(context.Background(), mock, ruleSetName, emailDomain)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if registerRS {
			t.Error("expected registerSharedRuleSet=false (rule set present)")
		}
		if registerID {
			t.Error("expected registerDomainIdentity=false (identity present)")
		}
	})

	t.Run("IdentityExists_RuleSetAbsent", func(t *testing.T) {
		// Identity exists, rule set does not → create rule set, reuse identity.
		mock := &mockSESIdentityLister{
			domainIdentities: []string{emailDomain},
		}
		registerRS, registerID, err := cmd.DetectSharedSESState(context.Background(), mock, ruleSetName, emailDomain)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !registerRS {
			t.Error("expected registerSharedRuleSet=true (rule set absent)")
		}
		if registerID {
			t.Error("expected registerDomainIdentity=false (identity present)")
		}
	})
}

// =============================================================================
// Phase 84.1-04 Task 1: FoundationStateReader-aware auto-detect (GAP-2, GAP-3)
// =============================================================================
//
// These tests lock in the Phase 84.1 semantic change: register_* flags mean
// "manage this resource", not "create on first apply only". The auto-detect
// MUST prefer foundation state ownership over AWS reality polling. If the
// foundation already manages a resource in tfstate, register_* stays true so
// terragrunt keeps managing it (no destroy planned, no prevent_destroy violation).

// mockFoundationStateReader implements cmd.FoundationStateReader for testing.
// owned is the set of resource addresses (e.g. "aws_ses_receipt_rule_set.shared[0]")
// the foundation tfstate is asserted to own.
type mockFoundationStateReader struct {
	owned map[string]bool
	err   error
}

func (m *mockFoundationStateReader) StateOwns(_ context.Context, addr string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.owned[addr], nil
}

// TestDetectSharedSESState_FoundationStateOwnership_PrefersStateOverAWS verifies
// that when foundation tfstate already lists aws_ses_domain_identity.sandbox[0]
// as a managed resource, DetectSharedSESState returns registerDomainIdentity=true
// (NOT false), because foundation must keep managing what is in its state.
// Closes GAP-2 + GAP-3.
func TestDetectSharedSESState_FoundationStateOwnership_PrefersStateOverAWS(t *testing.T) {
	const ruleSetName = "sandbox-email-shared"
	const emailDomain = "sandboxes.example.com"

	// Foundation state owns BOTH shared resources.
	stateReader := &mockFoundationStateReader{
		owned: map[string]bool{
			"aws_ses_receipt_rule_set.shared[0]":   true,
			"aws_ses_domain_identity.sandbox[0]":   true,
		},
	}

	// AWS reality says: rule set + identity already exist. Old behaviour would
	// have set both flags to false → terraform destroy → prevent_destroy block.
	// New behaviour: state ownership wins, flags stay TRUE.
	mock := &mockSESIdentityLister{
		ruleSetNames:     []string{ruleSetName},
		domainIdentities: []string{emailDomain},
	}

	registerRS, registerID, err := cmd.DetectSharedSESStateWithStateReader(
		context.Background(), mock, stateReader, ruleSetName, emailDomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !registerRS {
		t.Error("expected registerSharedRuleSet=true (foundation state owns it; must keep managing)")
	}
	if !registerID {
		t.Error("expected registerDomainIdentity=true (foundation state owns it; must keep managing)")
	}
}

// TestDetectSharedSESState_FoundationStateAbsent_FallsBackToAWSReality verifies
// that when the foundation tfstate does NOT contain a resource, the auto-detect
// falls through to the existing AWS-reality check. With the new semantics, AWS
// presence still keeps the flag TRUE (relying on Task 2's import blocks to bring
// the resource under foundation management). This preserves fresh-install
// behaviour AND closes GAP-3: pre-existing AWS resources NOT in foundation
// state are imported, not skipped.
func TestDetectSharedSESState_FoundationStateAbsent_FallsBackToAWSReality(t *testing.T) {
	const ruleSetName = "sandbox-email-shared"
	const emailDomain = "sandboxes.example.com"

	// Foundation state owns NOTHING (fresh install OR pre-Phase-84.1 install).
	stateReader := &mockFoundationStateReader{owned: map[string]bool{}}

	// AWS reality says: rule set + identity exist (legacy v1.0.0 owned them).
	// GAP-3 scenario: previously this would have set both flags to FALSE and
	// the regional cutover would have destroyed the resources. New behaviour:
	// flags stay TRUE → foundation imports them via Task 2's import blocks.
	mock := &mockSESIdentityLister{
		ruleSetNames:     []string{ruleSetName},
		domainIdentities: []string{emailDomain},
	}

	registerRS, registerID, err := cmd.DetectSharedSESStateWithStateReader(
		context.Background(), mock, stateReader, ruleSetName, emailDomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !registerRS {
		t.Error("expected registerSharedRuleSet=true (Phase 84.1 semantic: manage, not create-once)")
	}
	if !registerID {
		t.Error("expected registerDomainIdentity=true (Phase 84.1 semantic: manage, not create-once)")
	}
}

// TestDetectSharedSESState_FoundationStateMissing_FreshInstall verifies that
// passing a nil FoundationStateReader (truly fresh account, no state at all,
// or test bypass) results in both register flags defaulting to TRUE (create
// everything). Matches the pre-84.1 fresh-install behaviour exactly.
func TestDetectSharedSESState_FoundationStateMissing_FreshInstall(t *testing.T) {
	const ruleSetName = "sandbox-email-shared"
	const emailDomain = "sandboxes.example.com"

	// No state reader at all (the nil-mode bypass for tests / fresh accounts).
	mock := &mockSESIdentityLister{} // AWS reality: nothing exists either.

	registerRS, registerID, err := cmd.DetectSharedSESStateWithStateReader(
		context.Background(), mock, nil, ruleSetName, emailDomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !registerRS {
		t.Error("expected registerSharedRuleSet=true (fresh install)")
	}
	if !registerID {
		t.Error("expected registerDomainIdentity=true (fresh install)")
	}
}
