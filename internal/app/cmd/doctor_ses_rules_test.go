// Package cmd — doctor_ses_rules_test.go
// Phase 84 Wave 0 — W0-08: SES receipt rule check test infrastructure.
//
// Defines mockSESReceiptRuleAPI implementing the SESReceiptRuleAPI interface
// that doctor.go exposes. At Wave 0 the interface is an empty stub (Plan 84-07
// will add DescribeReceiptRuleSet once go.mod includes aws-sdk-go-v2/service/ses).
//
// Build tag note: the plan specified //go:build phase84_doctor to guard against
// importing the classic SES SDK before Plan 84-07. Since the current interface
// is empty (no SDK import required), this file compiles without a build tag.
// Plan 84-07 will add the tag when wiring the real SDK method.
//
// RED at end of Plan 84-01 — checkSESRules returns CheckSkipped; full assertion
// suite turns GREEN when Plan 84-07 implements the real check.
package cmd

import (
	"context"
	"strings"
	"testing"
)

// mockSESReceiptRuleAPI implements SESReceiptRuleAPI for testing.
// At Wave 0 the interface is empty; Plan 84-07 will extend it with
// DescribeReceiptRuleSet and update this mock accordingly.
type mockSESReceiptRuleAPI struct {
	// ruleNames are the rule names returned by DescribeReceiptRuleSet.
	// Set by individual tests to exercise different scenarios.
	ruleNames []string
	// err is the error returned by DescribeReceiptRuleSet (nil by default).
	err error
}

// TestCheckSESRules (W0-08) is the umbrella test for SES receipt rule checks.
// It exercises both the happy path (all rules own prefix) and the orphan path
// (rules from a foreign prefix present) via sub-tests.
//
// RED at Wave 0 because checkSESRules always returns CheckSkipped.
// GREEN when Plan 84-07 replaces the stub with the real implementation.
func TestCheckSESRules(t *testing.T) {
	t.Run("AllOwnRules_ReturnsOK", func(t *testing.T) {
		mock := &mockSESReceiptRuleAPI{
			ruleNames: []string{"kph-operator-inbound", "kph-sandbox-catchall"},
		}
		result := checkSESRules(context.Background(), mock, "kph")
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
		result := checkSESRules(context.Background(), mock, "kph")
		if result.Status != CheckWarn {
			t.Errorf("expected CheckWarn when orphan rule xx-operator-inbound present, got %s: %s",
				result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "xx-operator-inbound") {
			t.Errorf("expected message to contain 'xx-operator-inbound', got: %s", result.Message)
		}
	})

	t.Run("NilClient_DoesNotPanic", func(t *testing.T) {
		// nil client — production code should handle gracefully (e.g. CheckSkipped).
		result := checkSESRules(context.Background(), nil, "kph")
		// At Wave 0 this returns CheckSkipped; Plan 84-07 may return CheckSkipped or CheckError.
		_ = result
	})
}
