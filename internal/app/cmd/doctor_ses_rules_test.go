// Package cmd — doctor_ses_rules_test.go
// Phase 84 Wave 2 — W0-08: SES receipt rule check tests (build-tag removed).
//
// Defines mockSESReceiptRuleAPI implementing the SESReceiptRuleAPI interface
// declared in doctor.go. The mock implements DescribeReceiptRuleSet using the
// real aws-sdk-go-v2/service/ses types (added to go.mod by Plan 84-07).
package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// mockSESReceiptRuleAPI implements SESReceiptRuleAPI for testing.
type mockSESReceiptRuleAPI struct {
	// ruleNames are the rule names returned by DescribeReceiptRuleSet.
	// Set by individual tests to exercise different scenarios.
	ruleNames []string
	// err is the error returned by DescribeReceiptRuleSet (nil by default).
	err error

	// activeRuleSet is the name returned by DescribeActiveReceiptRuleSet.
	// Empty string models "no active rule set" (SES returns nil Metadata).
	activeRuleSet string
	// activeErr is the error returned by DescribeActiveReceiptRuleSet.
	activeErr error
}

// DescribeActiveReceiptRuleSet satisfies SESReceiptRuleAPI.
func (m *mockSESReceiptRuleAPI) DescribeActiveReceiptRuleSet(_ context.Context, _ *ses.DescribeActiveReceiptRuleSetInput, _ ...func(*ses.Options)) (*ses.DescribeActiveReceiptRuleSetOutput, error) {
	if m.activeErr != nil {
		return nil, m.activeErr
	}
	if m.activeRuleSet == "" {
		// SES returns an empty response when no rule set is active.
		return &ses.DescribeActiveReceiptRuleSetOutput{}, nil
	}
	return &ses.DescribeActiveReceiptRuleSetOutput{
		Metadata: &sestypes.ReceiptRuleSetMetadata{Name: awssdk.String(m.activeRuleSet)},
	}, nil
}

// DescribeReceiptRuleSet satisfies SESReceiptRuleAPI.
func (m *mockSESReceiptRuleAPI) DescribeReceiptRuleSet(_ context.Context, _ *ses.DescribeReceiptRuleSetInput, _ ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	rules := make([]sestypes.ReceiptRule, 0, len(m.ruleNames))
	for _, name := range m.ruleNames {
		n := name
		rules = append(rules, sestypes.ReceiptRule{Name: awssdk.String(n)})
	}
	return &ses.DescribeReceiptRuleSetOutput{Rules: rules}, nil
}

// TestCheckSESRules (W0-08) is the umbrella test for SES receipt rule checks.
// It exercises the happy path (all rules own prefix) and the orphan path
// (rules from a foreign prefix present) via sub-tests.
func TestCheckSESRules(t *testing.T) {
	t.Run("AllOwnRules_ReturnsOK", func(t *testing.T) {
		mock := &mockSESReceiptRuleAPI{
			ruleNames: []string{"kph-operator-inbound", "kph-sandbox-catchall"},
		}
		result := checkSESRules(context.Background(), mock, "kph", nil)
		if result.Status != CheckOK {
			t.Errorf("expected CheckOK when all rules belong to prefix kph, got %s: %s",
				result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "2 rules") {
			t.Errorf("expected message to mention '2 rules', got: %s", result.Message)
		}
		if !strings.Contains(result.Message, "kph") {
			t.Errorf("expected message to mention 'kph', got: %s", result.Message)
		}
	})

	t.Run("OrphanRule_ReturnsWarn", func(t *testing.T) {
		mock := &mockSESReceiptRuleAPI{
			ruleNames: []string{
				"kph-operator-inbound",
				"kph-sandbox-catchall",
				"xx-operator-inbound", // foreign prefix — orphan
			},
		}
		result := checkSESRules(context.Background(), mock, "kph", nil)
		if result.Status != CheckWarn {
			t.Errorf("expected CheckWarn when orphan rule xx-operator-inbound present, got %s: %s",
				result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "xx-operator-inbound") {
			t.Errorf("expected message to contain 'xx-operator-inbound', got: %s", result.Message)
		}
	})

	t.Run("NilClient_DoesNotPanic", func(t *testing.T) {
		// nil client — production code returns CheckSkipped.
		result := checkSESRules(context.Background(), nil, "kph", nil)
		if result.Status != CheckSkipped {
			t.Errorf("expected CheckSkipped for nil client, got %s", result.Status)
		}
	})
}

// SES allows exactly one active receipt rule set per account/region, so a
// sibling install activating its own set silently redirects every inbound
// message away from this install. checkSESRules cannot see it — it inspects the
// CONTENTS of sandbox-email-shared and reports "SES rules healthy" regardless.
//
// Regression: on 2026-09-03 an install had correct, enabled rules in
// sandbox-email-shared while a sibling's "kmv-email" set was active. Outbound
// mail kept working (sending never consults receipt rules), so it presented as
// "km-send works, km-recv never receives" with a green km doctor.
func TestCheckSESActiveRuleSet(t *testing.T) {
	for _, tc := range []struct {
		name       string
		active     string
		activeErr  error
		wantStatus CheckStatus
		wantIn     string
	}{
		{
			name:       "shared set is active",
			active:     "sandbox-email-shared",
			wantStatus: CheckOK,
			wantIn:     "is active",
		},
		{
			name:       "a sibling install's set is active",
			active:     "kmv-email",
			wantStatus: CheckError,
			wantIn:     "kmv-email",
		},
		{
			name:       "no active rule set at all",
			active:     "",
			wantStatus: CheckError,
			wantIn:     "ALL inbound email is dropped",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSESReceiptRuleAPI{activeRuleSet: tc.active, activeErr: tc.activeErr}
			got := checkSESActiveRuleSet(context.Background(), mock)

			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (message: %s)", got.Status, tc.wantStatus, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantIn) {
				t.Errorf("message %q does not contain %q", got.Message, tc.wantIn)
			}
			if tc.wantStatus == CheckError && got.Remediation == "" {
				t.Error("a failing active-rule-set check must carry remediation — this is a total inbound outage")
			}
		})
	}
}

// A nil client is skipped, never reported as an outage.
func TestCheckSESActiveRuleSet_NilClientIsSkipped(t *testing.T) {
	got := checkSESActiveRuleSet(context.Background(), nil)
	if got.Status != CheckSkipped {
		t.Errorf("status = %q, want %q", got.Status, CheckSkipped)
	}
}

// The remediation for a foreign active set must not be a bare
// set-active-receipt-rule-set: switching stops SES evaluating the other set's
// rules, which would break whichever install owns it.
func TestCheckSESActiveRuleSet_ForeignSetRemediationWarnsAboutTheOtherInstall(t *testing.T) {
	mock := &mockSESReceiptRuleAPI{activeRuleSet: "kmv-email"}
	got := checkSESActiveRuleSet(context.Background(), mock)

	for _, want := range []string{"set-active-receipt-rule-set", "kmv-email", "migrate"} {
		if !strings.Contains(got.Remediation, want) {
			t.Errorf("remediation %q does not mention %q", got.Remediation, want)
		}
	}
}
