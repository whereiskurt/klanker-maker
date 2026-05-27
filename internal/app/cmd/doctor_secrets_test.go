// Package cmd — doctor_secrets_test.go
// Phase 89 — SOPS-18-DOCTOR-CHECK: table-driven tests for checkSharedSecretsKey.
//
// Five subtests covering OK, WARN-missing-own, WARN-orphans, WARN-missing-with-orphan
// (precedence: missing-own wins), and nil-client-skip (WARNING 5).
package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// doctorFakeKMSAliasLister is a test double for KMSAliasLister used by
// doctor_secrets_test.go. It returns a canned single-page alias list
// (Truncated=false). Any error set in err is returned instead.
// Named distinctly to avoid conflict with fakeKMSAliasLister in bootstrap_secrets_test.go.
type doctorFakeKMSAliasLister struct {
	aliasNames []string // AliasName strings; converted to AliasListEntry in ListAliases
	err        error
}

func (f *doctorFakeKMSAliasLister) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	entries := make([]kmstypes.AliasListEntry, 0, len(f.aliasNames))
	for _, a := range f.aliasNames {
		name := a
		entries = append(entries, kmstypes.AliasListEntry{AliasName: awssdk.String(name)})
	}
	return &kms.ListAliasesOutput{Aliases: entries, Truncated: false}, nil
}

// TestCheckSharedSecretsKey is the umbrella for Phase 89 doctor check tests.
func TestCheckSharedSecretsKey(t *testing.T) {

	// OK — own alias present, no siblings.
	// Expect: CheckOK, Message contains "healthy".
	t.Run("OK", func(t *testing.T) {
		fake := &doctorFakeKMSAliasLister{
			aliasNames: []string{"alias/km-sandbox-secrets"},
		}
		result := checkSharedSecretsKey(context.Background(), fake, "km")
		if result.Status != CheckOK {
			t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "healthy") {
			t.Errorf("expected message to contain 'healthy', got: %s", result.Message)
		}
	})

	// MissingOwn — empty alias list, own alias absent.
	// Expect: CheckWarn, Message contains "not found", Remediation contains
	// "km bootstrap --shared-secrets-key".
	t.Run("MissingOwn", func(t *testing.T) {
		fake := &doctorFakeKMSAliasLister{
			aliasNames: []string{}, // nothing — own alias missing
		}
		result := checkSharedSecretsKey(context.Background(), fake, "km")
		if result.Status != CheckWarn {
			t.Errorf("expected CheckWarn for missing own alias, got %s: %s", result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "not found") {
			t.Errorf("expected message to contain 'not found', got: %s", result.Message)
		}
		if !strings.Contains(result.Remediation, "km bootstrap --shared-secrets-key") {
			t.Errorf("expected Remediation to contain 'km bootstrap --shared-secrets-key', got: %s", result.Remediation)
		}
	})

	// OrphansPresent — own alias present, plus a sibling alias.
	// Expect: CheckWarn, Message contains "orphan" and the sibling alias name,
	// Remediation hints at sibling install being expected.
	t.Run("OrphansPresent", func(t *testing.T) {
		fake := &doctorFakeKMSAliasLister{
			aliasNames: []string{
				"alias/km-sandbox-secrets",  // own — healthy
				"alias/km2-sandbox-secrets", // sibling — orphan
			},
		}
		result := checkSharedSecretsKey(context.Background(), fake, "km")
		if result.Status != CheckWarn {
			t.Errorf("expected CheckWarn for sibling alias, got %s: %s", result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "orphan") {
			t.Errorf("expected message to contain 'orphan', got: %s", result.Message)
		}
		if !strings.Contains(result.Message, "alias/km2-sandbox-secrets") {
			t.Errorf("expected message to contain 'alias/km2-sandbox-secrets', got: %s", result.Message)
		}
		if !strings.Contains(result.Remediation, "sibling") {
			t.Errorf("expected Remediation to mention 'sibling', got: %s", result.Remediation)
		}
	})

	// OrphansWithoutOwn — sibling alias present, own absent.
	// Expect: CheckWarn for missing-own (takes precedence over orphan list).
	// Message contains "not found" (more actionable than listing orphans).
	t.Run("OrphansWithoutOwn", func(t *testing.T) {
		fake := &doctorFakeKMSAliasLister{
			aliasNames: []string{
				"alias/km2-sandbox-secrets", // only sibling, own is missing
			},
		}
		result := checkSharedSecretsKey(context.Background(), fake, "km")
		if result.Status != CheckWarn {
			t.Errorf("expected CheckWarn, got %s: %s", result.Status, result.Message)
		}
		// Missing-own takes precedence — message should say "not found" (not just "orphan").
		if !strings.Contains(result.Message, "not found") {
			t.Errorf("expected 'not found' to take precedence over orphan message, got: %s", result.Message)
		}
		if !strings.Contains(result.Remediation, "km bootstrap --shared-secrets-key") {
			t.Errorf("expected Remediation to contain 'km bootstrap --shared-secrets-key', got: %s", result.Remediation)
		}
	})

	// NilClientIsSkipped (WARNING 5) — nil client must not panic and must return
	// a skip status. Mirrors the existing checkSESRules nil guard.
	t.Run("NilClientIsSkipped", func(t *testing.T) {
		result := checkSharedSecretsKey(context.Background(), nil, "km")
		if result.Status != CheckSkipped {
			t.Errorf("expected CheckSkipped for nil client, got %s: %s", result.Status, result.Message)
		}
		if !strings.Contains(strings.ToLower(result.Message), "skipped") {
			t.Errorf("expected message to contain 'skipped', got: %s", result.Message)
		}
	})
}
