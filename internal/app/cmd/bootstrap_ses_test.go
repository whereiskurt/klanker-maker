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
