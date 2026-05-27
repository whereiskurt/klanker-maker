package cmd

// Phase 89 Plan 04 — bootstrap_secrets_test.go
//
// Task 1 (RED): Tests for runBootstrapSharedSecretsKey + --all chain + mutex.
//
// RED contract: `go test ./internal/app/cmd/ -run TestRunBootstrapSharedSecretsKey|...`
// fails until Task 2 lands the implementation in bootstrap.go.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// =============================================================================
// Fakes / mocks
// =============================================================================

// fakeKMSAliasLister implements KMSAliasLister for testing.
type fakeKMSAliasLister struct {
	// aliases to return on ListAliases
	aliases []kmstypes.AliasListEntry
	// err to return
	err error
}

func (f *fakeKMSAliasLister) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &kms.ListAliasesOutput{
		Aliases: f.aliases,
	}, nil
}

// =============================================================================
// Helper
// =============================================================================

func bootstrapSecretsCfg() *config.Config {
	return &config.Config{
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
		Domain:                "test.example.com",
		PrimaryRegion:         "us-east-1",
		ArtifactsBucket:       "km-artifacts-12345",
		ResourcePrefix:        "km",
		EmailSubdomain:        "sandboxes",
	}
}

// =============================================================================
// Task 1: RED tests — runBootstrapSharedSecretsKey
// =============================================================================

// TestRunBootstrapSharedSecretsKeyDryRun_NoExistingAlias: alias list is empty;
// dryRun=true. Expect dry-run message mentioning sandbox-secrets-key path and
// KM_REGISTER_SECRETS_KEY set to "true".
func TestRunBootstrapSharedSecretsKeyDryRun_NoExistingAlias(t *testing.T) {
	// Empty alias list → alias does NOT exist → register=true
	mock := &fakeKMSAliasLister{aliases: nil}
	cfg := bootstrapSecretsCfg()

	var buf bytes.Buffer
	err := runBootstrapSharedSecretsKey(context.Background(), cfg, true /* dryRun */, &buf, mock)
	if err != nil {
		t.Fatalf("expected nil error for dry-run, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "would run: terragrunt apply") {
		t.Errorf("dry-run output missing 'would run: terragrunt apply'; got:\n%s", out)
	}
	if !strings.Contains(out, "sandbox-secrets-key") {
		t.Errorf("dry-run output should mention 'sandbox-secrets-key'; got:\n%s", out)
	}

	if got := getTestEnv("KM_REGISTER_SECRETS_KEY"); got != "true" {
		t.Errorf("KM_REGISTER_SECRETS_KEY = %q, want %q (no existing alias → should register)", got, "true")
	}
}

// TestRunBootstrapSharedSecretsKeyDryRun_ExistingAlias: alias list contains
// the own-prefix alias; dryRun=true. Expect dry-run message and
// KM_REGISTER_SECRETS_KEY set to "false".
func TestRunBootstrapSharedSecretsKeyDryRun_ExistingAlias(t *testing.T) {
	aliasName := "alias/km-sandbox-secrets"
	mock := &fakeKMSAliasLister{
		aliases: []kmstypes.AliasListEntry{
			{AliasName: &aliasName},
		},
	}
	cfg := bootstrapSecretsCfg()

	var buf bytes.Buffer
	err := runBootstrapSharedSecretsKey(context.Background(), cfg, true /* dryRun */, &buf, mock)
	if err != nil {
		t.Fatalf("expected nil error for dry-run, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "would run: terragrunt apply") {
		t.Errorf("dry-run output missing 'would run: terragrunt apply'; got:\n%s", out)
	}

	if got := getTestEnv("KM_REGISTER_SECRETS_KEY"); got != "false" {
		t.Errorf("KM_REGISTER_SECRETS_KEY = %q, want %q (existing alias → skip create)", got, "false")
	}
}

// TestRunBootstrapSharedSecretsKeyDryRun_AWSUnavailableGraceful: kmsListerOverride
// is nil AND AWS is effectively unavailable (bad profile). dryRun=true.
// Expect: graceful degradation — returns nil, prints dry-run message, no panic.
func TestRunBootstrapSharedSecretsKeyDryRun_AWSUnavailableGraceful(t *testing.T) {
	// Force AWS config loading to fail by setting a bad profile.
	t.Setenv("AWS_PROFILE", "does-not-exist-phase-89-test")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")

	cfg := bootstrapSecretsCfg()

	var buf bytes.Buffer
	// nil override → will try real AWS → fails → should degrade gracefully with dryRun=true
	err := runBootstrapSharedSecretsKey(context.Background(), cfg, true /* dryRun */, &buf, nil)
	if err != nil {
		t.Fatalf("dry-run with unavailable AWS should return nil (graceful degrade), got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "would run: terragrunt apply") && !strings.Contains(out, "Dry run") {
		t.Errorf("graceful-degrade dry-run output missing apply message; got:\n%s", out)
	}
}

// =============================================================================
// Task 1: RED tests — runBootstrapAll chain order
// =============================================================================

// TestRunBootstrapAllChain_Phase89 verifies that runBootstrapAll chains:
//   foundation → shared-ses → shared-secrets-key  (exact order).
//
// Stubs RunBootstrapFunc, RunBootstrapSharedSESFunc, RunBootstrapSharedSecretsKeyFunc
// to record their call order in a shared slice.
func TestRunBootstrapAllChain_Phase89(t *testing.T) {
	cfg := &config.Config{PrimaryRegion: "us-east-1"}

	var order []string

	// Save and restore seams.
	origFoundation := RunBootstrapFunc
	origSES := RunBootstrapSharedSESFunc
	// RunBootstrapSharedSecretsKeyFunc is not yet defined — this reference is RED.
	origSecretsKey := RunBootstrapSharedSecretsKeyFunc
	t.Cleanup(func() {
		RunBootstrapFunc = origFoundation
		RunBootstrapSharedSESFunc = origSES
		RunBootstrapSharedSecretsKeyFunc = origSecretsKey
	})

	RunBootstrapFunc = func(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer) error {
		order = append(order, "foundation")
		return nil
	}
	RunBootstrapSharedSESFunc = func(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, _ SESIdentityLister) error {
		order = append(order, "shared-ses")
		return nil
	}
	RunBootstrapSharedSecretsKeyFunc = func(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, _ KMSAliasLister) error {
		order = append(order, "shared-secrets-key")
		return nil
	}

	var buf bytes.Buffer
	if err := runBootstrapAll(context.Background(), cfg, true /* dryRun */, false, false, &buf); err != nil {
		t.Fatalf("runBootstrapAll: %v", err)
	}

	want := []string{"foundation", "shared-ses", "shared-secrets-key"}
	if len(order) != len(want) {
		t.Fatalf("invocation order: got %v (len=%d), want %v (len=%d)", order, len(order), want, len(want))
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("invocation[%d]: got %q, want %q", i, order[i], v)
		}
	}
}

// =============================================================================
// Task 1: RED tests — --all ↔ --shared-secrets-key mutex
// =============================================================================

// TestBootstrapMutex_AllAndSharedSecretsKey verifies that cobra RunE rejects
// the combination of --all + --shared-secrets-key with a clear error message.
func TestBootstrapMutex_AllAndSharedSecretsKey(t *testing.T) {
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	var buf bytes.Buffer
	c := NewBootstrapCmdWithWriter(cfg, &buf)
	c.SetArgs([]string{"--all", "--shared-secrets-key"})

	err := c.Execute()
	if err == nil {
		t.Fatal("expected error for --all + --shared-secrets-key, got nil")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("error %q should mention --all", err.Error())
	}
	if !strings.Contains(err.Error(), "--shared-secrets-key") {
		t.Errorf("error %q should mention --shared-secrets-key", err.Error())
	}
}

// =============================================================================
// Task 3 (RED): deleteOwnSecretsKMSAlias tests
// =============================================================================

// fakeKMSAliasDeleter implements KMSAliasDeleter for testing.
type fakeKMSAliasDeleter struct {
	// listAliasesOutput to return
	aliases []kmstypes.AliasListEntry
	// describeKeyFn allows per-test customisation
	describeKeyFn func(ctx context.Context, params *kms.DescribeKeyInput) (*kms.DescribeKeyOutput, error)
	// listKeysOutput for orphan-recovery path
	listKeys []kmstypes.KeyListEntry
	// listResourceTagsFn to return tags per key
	listResourceTagsFn func(keyID string) ([]kmstypes.Tag, error)

	// Recorded calls for assertions
	deleteAliasCalls         []string
	scheduleKeyDeletionCalls []string
}

func (f *fakeKMSAliasDeleter) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	return &kms.ListAliasesOutput{Aliases: f.aliases}, nil
}

func (f *fakeKMSAliasDeleter) DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	if f.describeKeyFn != nil {
		return f.describeKeyFn(ctx, params)
	}
	return nil, fmt.Errorf("DescribeKey not configured in fake")
}

func (f *fakeKMSAliasDeleter) ListKeys(_ context.Context, _ *kms.ListKeysInput, _ ...func(*kms.Options)) (*kms.ListKeysOutput, error) {
	return &kms.ListKeysOutput{Keys: f.listKeys}, nil
}

func (f *fakeKMSAliasDeleter) ListResourceTags(_ context.Context, params *kms.ListResourceTagsInput, _ ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error) {
	keyID := ""
	if params.KeyId != nil {
		keyID = *params.KeyId
	}
	var tags []kmstypes.Tag
	if f.listResourceTagsFn != nil {
		var err error
		tags, err = f.listResourceTagsFn(keyID)
		if err != nil {
			return nil, err
		}
	}
	return &kms.ListResourceTagsOutput{Tags: tags}, nil
}

func (f *fakeKMSAliasDeleter) DeleteAlias(_ context.Context, params *kms.DeleteAliasInput, _ ...func(*kms.Options)) (*kms.DeleteAliasOutput, error) {
	if params.AliasName != nil {
		f.deleteAliasCalls = append(f.deleteAliasCalls, *params.AliasName)
	}
	return &kms.DeleteAliasOutput{}, nil
}

func (f *fakeKMSAliasDeleter) ScheduleKeyDeletion(_ context.Context, params *kms.ScheduleKeyDeletionInput, _ ...func(*kms.Options)) (*kms.ScheduleKeyDeletionOutput, error) {
	if params.KeyId != nil {
		f.scheduleKeyDeletionCalls = append(f.scheduleKeyDeletionCalls, *params.KeyId)
	}
	return &kms.ScheduleKeyDeletionOutput{}, nil
}

// TestDeleteOwnSecretsKMSAlias_DeletesOwnPrefix: mock returns both own and sibling
// alias. Invoke with resourcePrefix="km". Expect exactly one DeleteAlias for own
// alias and one ScheduleKeyDeletion; sibling untouched.
func TestDeleteOwnSecretsKMSAlias_DeletesOwnPrefix(t *testing.T) {
	keyID := "arn:aws:kms:us-east-1:111111111111:key/test-key-id-km"

	mock := &fakeKMSAliasDeleter{
		// DescribeKey via alias → returns own key
		describeKeyFn: func(_ context.Context, params *kms.DescribeKeyInput) (*kms.DescribeKeyOutput, error) {
			if params.KeyId != nil && *params.KeyId == "alias/km-sandbox-secrets" {
				return &kms.DescribeKeyOutput{
					KeyMetadata: &kmstypes.KeyMetadata{KeyId: aws.String(keyID)},
				}, nil
			}
			return nil, &kmstypes.NotFoundException{}
		},
	}

	err := deleteOwnSecretsKMSAlias(context.Background(), mock, "km")
	if err != nil {
		t.Fatalf("deleteOwnSecretsKMSAlias: %v", err)
	}

	if len(mock.deleteAliasCalls) != 1 || mock.deleteAliasCalls[0] != "alias/km-sandbox-secrets" {
		t.Errorf("deleteAlias calls: %v, want [alias/km-sandbox-secrets]", mock.deleteAliasCalls)
	}
	if len(mock.scheduleKeyDeletionCalls) != 1 || mock.scheduleKeyDeletionCalls[0] != keyID {
		t.Errorf("scheduleKeyDeletion calls: %v, want [%s]", mock.scheduleKeyDeletionCalls, keyID)
	}
}

// TestDeleteOwnSecretsKMSAlias_NoOwnAliasIsNoOp: mock returns only sibling alias
// AND orphan scan returns nothing. Expect zero Delete/ScheduleKeyDeletion calls.
func TestDeleteOwnSecretsKMSAlias_NoOwnAliasIsNoOp(t *testing.T) {
	mock := &fakeKMSAliasDeleter{
		// DescribeKey for "alias/km-sandbox-secrets" → NotFoundException
		describeKeyFn: func(_ context.Context, params *kms.DescribeKeyInput) (*kms.DescribeKeyOutput, error) {
			return nil, &kmstypes.NotFoundException{}
		},
		// No keys in orphan scan
		listKeys: nil,
	}

	err := deleteOwnSecretsKMSAlias(context.Background(), mock, "km")
	if err != nil {
		t.Fatalf("deleteOwnSecretsKMSAlias: %v", err)
	}

	if len(mock.deleteAliasCalls) != 0 {
		t.Errorf("expected zero DeleteAlias calls (no-op), got: %v", mock.deleteAliasCalls)
	}
	if len(mock.scheduleKeyDeletionCalls) != 0 {
		t.Errorf("expected zero ScheduleKeyDeletion calls (no-op), got: %v", mock.scheduleKeyDeletionCalls)
	}
}

// TestDeleteOwnSecretsKMSAlias_PrefixCollisionGuard: alias "alias/kmilk-sandbox-secrets"
// starts with same letters as prefix "km" but is a different install. Expect zero calls.
func TestDeleteOwnSecretsKMSAlias_PrefixCollisionGuard(t *testing.T) {
	mock := &fakeKMSAliasDeleter{
		// DescribeKey for the exact alias "alias/km-sandbox-secrets" → NotFoundException
		// (only "alias/kmilk-sandbox-secrets" exists, not "alias/km-sandbox-secrets")
		describeKeyFn: func(_ context.Context, params *kms.DescribeKeyInput) (*kms.DescribeKeyOutput, error) {
			// The impl calls DescribeKey("alias/km-sandbox-secrets") — should not find it.
			return nil, &kmstypes.NotFoundException{}
		},
		// No keys tagged for prefix "km" in orphan scan
		listKeys: nil,
	}

	err := deleteOwnSecretsKMSAlias(context.Background(), mock, "km")
	if err != nil {
		t.Fatalf("deleteOwnSecretsKMSAlias: %v", err)
	}

	if len(mock.deleteAliasCalls) != 0 {
		t.Errorf("prefix collision guard: expected zero DeleteAlias calls, got: %v", mock.deleteAliasCalls)
	}
	if len(mock.scheduleKeyDeletionCalls) != 0 {
		t.Errorf("prefix collision guard: expected zero ScheduleKeyDeletion calls, got: %v", mock.scheduleKeyDeletionCalls)
	}
}

// TestDeleteOwnSecretsKMSAlias_OrphanedRecovery: alias was deleted manually but
// key still exists, tagged with km:component=sandbox-secrets-key + km:resource_prefix=km.
// Expect: ScheduleKeyDeletion called for recovered key; no DeleteAlias call.
func TestDeleteOwnSecretsKMSAlias_OrphanedRecovery(t *testing.T) {
	orphanKeyID := "arn:aws:kms:us-east-1:111111111111:key/orphan-key-id"

	mock := &fakeKMSAliasDeleter{
		// DescribeKey → NotFoundException (alias is missing)
		describeKeyFn: func(_ context.Context, params *kms.DescribeKeyInput) (*kms.DescribeKeyOutput, error) {
			return nil, &kmstypes.NotFoundException{}
		},
		// One key in the scan
		listKeys: []kmstypes.KeyListEntry{
			{KeyId: aws.String(orphanKeyID)},
		},
		// That key has the expected tags
		listResourceTagsFn: func(keyID string) ([]kmstypes.Tag, error) {
			if keyID == orphanKeyID {
				return []kmstypes.Tag{
					{TagKey: aws.String("km:component"), TagValue: aws.String("sandbox-secrets-key")},
					{TagKey: aws.String("km:resource_prefix"), TagValue: aws.String("km")},
				}, nil
			}
			return nil, nil
		},
	}

	err := deleteOwnSecretsKMSAlias(context.Background(), mock, "km")
	if err != nil {
		t.Fatalf("deleteOwnSecretsKMSAlias orphan recovery: %v", err)
	}

	// No alias to delete (alias was already gone)
	if len(mock.deleteAliasCalls) != 0 {
		t.Errorf("orphan recovery: expected zero DeleteAlias calls, got: %v", mock.deleteAliasCalls)
	}
	// Key should be scheduled for deletion
	if len(mock.scheduleKeyDeletionCalls) != 1 || mock.scheduleKeyDeletionCalls[0] != orphanKeyID {
		t.Errorf("orphan recovery: expected ScheduleKeyDeletion for %s, got: %v", orphanKeyID, mock.scheduleKeyDeletionCalls)
	}
}

// =============================================================================
// Helper: getTestEnv reads an env var after tests set it via os.Setenv.
// =============================================================================

func getTestEnv(key string) string {
	return getenvForTest(key)
}

// getenvForTest wraps os.Getenv so if we want to swap implementation later we can.
func getenvForTest(key string) string {
	return os.Getenv(key)
}
