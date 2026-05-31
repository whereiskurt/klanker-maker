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
