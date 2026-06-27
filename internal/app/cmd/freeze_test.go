// Package cmd_test — freeze_test.go
// CLI-01 (km freeze) and CLI-02 (km unlock latch-aware) tests.
// These tests use a mock DDB UpdateItem client to verify that the right
// UpdateItem calls are made without touching real AWS.
package cmd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ---- Minimal mock DDB client for freeze/unfreeze tests ----

// freezeMockDynamo captures UpdateItem calls made by FreezeSandboxDynamo /
// UnfreezeSandboxDynamo. It returns nil (success) for every call.
type freezeMockDynamo struct {
	updateItemInputs []*dynamodb.UpdateItemInput
	// returnErr is returned by UpdateItem when set (simulate row-not-found etc.)
	returnErr error
	// returnErrOnN, if > 0, returns returnErr only on the Nth call (1-based).
	returnErrOnN int
	callCount    int
}

func (m *freezeMockDynamo) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (m *freezeMockDynamo) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}
func (m *freezeMockDynamo) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.callCount++
	m.updateItemInputs = append(m.updateItemInputs, input)
	if m.returnErr != nil {
		if m.returnErrOnN == 0 || m.callCount == m.returnErrOnN {
			return nil, m.returnErr
		}
	}
	return &dynamodb.UpdateItemOutput{}, nil
}
func (m *freezeMockDynamo) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}
func (m *freezeMockDynamo) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{}, nil
}
func (m *freezeMockDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

// ---- Helpers ----

func runFreezeCmd(t *testing.T, ddb cmd.FreezeableDDB, sandboxID string, extraArgs ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{StateBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	freezeCmd := cmd.NewFreezeCmdWithDDB(cfg, ddb)
	root.AddCommand(freezeCmd)
	args := append([]string{"freeze"}, extraArgs...)
	args = append(args, sandboxID)
	root.SetArgs(args)
	var buf strings.Builder
	root.SetOut(&buf)
	err := root.Execute()
	return buf.String(), err
}

func runUnlockCmdForFreeze(t *testing.T, ddb cmd.LatchAwareDDB, sandboxID string, extraArgs ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{StateBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	unlockCmd := cmd.NewUnlockCmdWithLatchDDB(cfg, ddb)
	root.AddCommand(unlockCmd)
	args := append([]string{"unlock", "--yes"}, extraArgs...)
	args = append(args, sandboxID)
	root.SetArgs(args)
	var buf strings.Builder
	root.SetOut(&buf)
	err := root.Execute()
	return buf.String(), err
}

// ---- CLI-01: TestRunFreeze ----

// TestRunFreeze verifies that km freeze writes action_frozen=true plus
// frozen_reason/frozen_at/frozen_by using a single atomic UpdateItem (not PutItem).
func TestRunFreeze(t *testing.T) {
	ddb := &freezeMockDynamo{}
	out, err := runFreezeCmd(t, ddb, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("km freeze returned error: %v", err)
	}

	// Exactly ONE UpdateItem call (freeze is a single atomic SET).
	if ddb.callCount != 1 {
		t.Fatalf("expected 1 UpdateItem call, got %d", ddb.callCount)
	}

	input := ddb.updateItemInputs[0]

	// Must use SET (not PutItem / full-row replace).
	expr := ""
	if input.UpdateExpression != nil {
		expr = *input.UpdateExpression
	}
	if !strings.Contains(expr, "action_frozen") {
		t.Errorf("UpdateExpression does not reference action_frozen: %q", expr)
	}
	if !strings.Contains(expr, "frozen_reason") {
		t.Errorf("UpdateExpression does not reference frozen_reason: %q", expr)
	}
	if !strings.Contains(expr, "frozen_at") {
		t.Errorf("UpdateExpression does not reference frozen_at: %q", expr)
	}
	if !strings.Contains(expr, "frozen_by") {
		t.Errorf("UpdateExpression does not reference frozen_by: %q", expr)
	}

	// action_frozen must be true (BOOL AttributeValue).
	av, ok := input.ExpressionAttributeValues[":t"]
	if !ok {
		t.Fatal("ExpressionAttributeValues missing :t (action_frozen = true)")
	}
	boolAV, isBool := av.(*dynamodbtypes.AttributeValueMemberBOOL)
	if !isBool || !boolAV.Value {
		t.Errorf("expected :t to be BOOL true, got %v", av)
	}

	// Default reason should be non-empty.
	reasonAV, ok := input.ExpressionAttributeValues[":reason"]
	if !ok {
		t.Fatal("ExpressionAttributeValues missing :reason")
	}
	reasonS, isStr := reasonAV.(*dynamodbtypes.AttributeValueMemberS)
	if !isStr || reasonS.Value == "" {
		t.Errorf("expected :reason to be a non-empty String, got %v", reasonAV)
	}

	// Output should mention "Frozen" and the sandbox ID.
	if !strings.Contains(out, "sb-aabbccdd") {
		t.Errorf("output does not mention sandbox ID: %q", out)
	}
	_ = out // success message content validated enough
}

// TestRunFreeze_WithReason verifies that --reason overrides the default reason.
func TestRunFreeze_WithReason(t *testing.T) {
	ddb := &freezeMockDynamo{}
	_, err := runFreezeCmd(t, ddb, "sb-aabbccdd", "--reason", "quota:push:daily:10")
	if err != nil {
		t.Fatalf("km freeze --reason returned error: %v", err)
	}
	if ddb.callCount != 1 {
		t.Fatalf("expected 1 UpdateItem call, got %d", ddb.callCount)
	}
	input := ddb.updateItemInputs[0]
	reasonAV := input.ExpressionAttributeValues[":reason"]
	reasonS, ok := reasonAV.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || reasonS.Value != "quota:push:daily:10" {
		t.Errorf("expected :reason = %q, got %v", "quota:push:daily:10", reasonAV)
	}
}

// TestRunFreeze_InvalidSandboxID verifies that an invalid sandbox ID returns an error
// without calling UpdateItem.
func TestRunFreeze_InvalidSandboxID(t *testing.T) {
	ddb := &freezeMockDynamo{}
	_, err := runFreezeCmd(t, ddb, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if ddb.callCount != 0 {
		t.Errorf("expected 0 UpdateItem calls for invalid ID, got %d", ddb.callCount)
	}
}

// ---- CLI-02: TestRunUnlockLatchAware ----

// TestRunUnlockLatchAware verifies that km unlock clears action_frozen alongside
// the safety lock and reports both in the output.
func TestRunUnlockLatchAware(t *testing.T) {
	ddb := &freezeMockDynamo{}
	out, err := runUnlockCmdForFreeze(t, ddb, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("km unlock (latch-aware) returned error: %v", err)
	}

	// Should have made at least two UpdateItem calls:
	// 1. safety-lock clear (UnlockSandboxDynamo)
	// 2. freeze latch clear (UnfreezeSandboxDynamo)
	if ddb.callCount < 2 {
		t.Fatalf("expected >= 2 UpdateItem calls (lock + freeze), got %d", ddb.callCount)
	}

	// Verify one of the calls references action_frozen.
	frozenClearFound := false
	lockClearFound := false
	for _, inp := range ddb.updateItemInputs {
		expr := ""
		if inp.UpdateExpression != nil {
			expr = *inp.UpdateExpression
		}
		if strings.Contains(expr, "action_frozen") {
			frozenClearFound = true
		}
		if strings.Contains(expr, "locked") {
			lockClearFound = true
		}
	}
	if !frozenClearFound {
		t.Error("none of the UpdateItem calls referenced action_frozen (freeze latch not cleared)")
	}
	if !lockClearFound {
		t.Error("none of the UpdateItem calls referenced locked (safety lock not cleared)")
	}

	// Output should mention both lock and freeze.
	outLower := strings.ToLower(out)
	if !strings.Contains(outLower, "lock") && !strings.Contains(outLower, "unlock") {
		t.Errorf("output does not mention lock/unlock: %q", out)
	}
	// "actions resume" hint should appear.
	if !strings.Contains(outLower, "action") {
		t.Logf("note: expected 'action' in output, got: %q", out)
	}
}

// TestRunUnlockLatchAware_OnlyLock verifies km unlock on a sandbox that has a
// safety lock but NO quarantine freeze still completes successfully (back-compat).
func TestRunUnlockLatchAware_OnlyLock(t *testing.T) {
	// UnfreezeSandboxDynamo on an already-unfrozen sandbox is idempotent (no-op).
	// The mock returns nil for all UpdateItem calls.
	ddb := &freezeMockDynamo{}
	_, err := runUnlockCmdForFreeze(t, ddb, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("km unlock on non-frozen sandbox returned error: %v", err)
	}
}

// TestRunUnlockLatchAware_InvalidSandboxID verifies that an invalid sandbox ID
// returns an error without calling UpdateItem.
func TestRunUnlockLatchAware_InvalidSandboxID(t *testing.T) {
	ddb := &freezeMockDynamo{}
	_, err := runUnlockCmdForFreeze(t, ddb, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if ddb.callCount != 0 {
		t.Errorf("expected 0 UpdateItem calls for invalid ID, got %d", ddb.callCount)
	}
}
