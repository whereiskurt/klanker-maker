// Package cmd — doctor_stale_kms_test.go
// Regression test for the stale-KMS-key sweeper. Phase 89's shared
// sandbox-secrets alias must NOT be classified as stale just because it
// carries no sandbox-ID token — otherwise every `km doctor --dry-run=false`
// run schedules the install's secrets key for 7-day deletion.
package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// fakeKMSCleanupSweeper satisfies KMSCleanupAPI for sweeper tests.
// Only ListAliases needs realistic behavior because the dry-run branch
// short-circuits before DescribeKey / ScheduleKeyDeletion / DeleteAlias fire.
type fakeKMSCleanupSweeper struct {
	aliases []string
}

func (f *fakeKMSCleanupSweeper) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	entries := make([]kmstypes.AliasListEntry, 0, len(f.aliases))
	for _, n := range f.aliases {
		name := n
		entries = append(entries, kmstypes.AliasListEntry{
			AliasName:   awssdk.String(name),
			TargetKeyId: awssdk.String("key-" + name),
		})
	}
	return &kms.ListAliasesOutput{Aliases: entries, Truncated: false}, nil
}

func (f *fakeKMSCleanupSweeper) DescribeKey(_ context.Context, _ *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	return &kms.DescribeKeyOutput{}, nil
}

func (f *fakeKMSCleanupSweeper) ScheduleKeyDeletion(_ context.Context, _ *kms.ScheduleKeyDeletionInput, _ ...func(*kms.Options)) (*kms.ScheduleKeyDeletionOutput, error) {
	return &kms.ScheduleKeyDeletionOutput{}, nil
}

func (f *fakeKMSCleanupSweeper) DeleteAlias(_ context.Context, _ *kms.DeleteAliasInput, _ ...func(*kms.Options)) (*kms.DeleteAliasOutput, error) {
	return &kms.DeleteAliasOutput{}, nil
}

// TestCheckStaleKMSKeys_ExemptsSharedSecretsAlias is the regression for the
// Phase 89 sweeper interaction: the install-singleton sandbox-secrets alias
// (no sandbox-ID token in its name) used to be flagged as stale and scheduled
// for 7-day deletion by every `km doctor --dry-run=false` run.
func TestCheckStaleKMSKeys_ExemptsSharedSecretsAlias(t *testing.T) {
	ctx := context.Background()

	// Subtest A: only the shared secrets alias exists, no active sandboxes.
	// Before the fix this returned WARN with 1 stale key. Now it must report OK.
	t.Run("OnlySecretsAlias", func(t *testing.T) {
		kmsFake := &fakeKMSCleanupSweeper{
			aliases: []string{"alias/km-sandbox-secrets"},
		}
		lister := &mockSandboxLister{}
		result := checkStaleKMSKeys(ctx, kmsFake, lister, true /* dryRun */, "km")

		if result.Status != CheckOK {
			t.Fatalf("expected CheckOK (secrets alias must be exempt), got %s: %s",
				result.Status, result.Message)
		}
		if strings.Contains(result.Message, "stale") {
			t.Errorf("message should not mention stale keys; got: %s", result.Message)
		}
	})

	// Subtest B: secrets alias + a genuinely orphaned alias coexist.
	// Only the orphan should be classified stale (count=1, not 2).
	t.Run("SecretsPlusGenuineOrphan", func(t *testing.T) {
		kmsFake := &fakeKMSCleanupSweeper{
			aliases: []string{
				"alias/km-sandbox-secrets",
				"alias/km-github-token-doomed-deadbeef",
			},
		}
		lister := &mockSandboxLister{}
		result := checkStaleKMSKeys(ctx, kmsFake, lister, true /* dryRun */, "km")

		if result.Status != CheckWarn {
			t.Fatalf("expected CheckWarn (1 genuine stale), got %s: %s",
				result.Status, result.Message)
		}
		if !strings.Contains(result.Message, "1 stale") {
			t.Errorf("expected message to report exactly 1 stale key, got: %s", result.Message)
		}
	})
}
