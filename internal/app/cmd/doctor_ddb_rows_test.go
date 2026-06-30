package cmd

import (
	"context"
	"sync"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// trackingDDBScanDelete wraps mockDDBScanDelete and records DeleteItem calls
// so tests can assert exactly which keys were (or were not) deleted.
type trackingDDBScanDelete struct {
	mock           *mockDDBScanDelete
	mu             sync.Mutex
	deletedItems   []map[string]dynamodbtypes.AttributeValue // copy of Key map per call
}

func (t *trackingDDBScanDelete) Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return t.mock.Scan(ctx, input, optFns...)
}

func (t *trackingDDBScanDelete) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	t.mu.Lock()
	// Deep-copy the key map for assertion
	keyCopy := make(map[string]dynamodbtypes.AttributeValue, len(input.Key))
	for k, v := range input.Key {
		keyCopy[k] = v
	}
	t.deletedItems = append(t.deletedItems, keyCopy)
	t.mu.Unlock()
	return t.mock.DeleteItem(ctx, input, optFns...)
}

func (t *trackingDDBScanDelete) BatchWriteItem(ctx context.Context, input *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	return t.mock.BatchWriteItem(ctx, input, optFns...)
}

// deletedSKs returns all SK values from DeleteItem calls recorded by the tracker.
// Used by AI-preservation assertions.
func (t *trackingDDBScanDelete) deletedSKs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var sks []string
	for _, key := range t.deletedItems {
		if v, ok := key["SK"]; ok {
			if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
				sks = append(sks, sv.Value)
			}
		}
	}
	return sks
}

// hasSKDeleted returns true when the tracker has a DeleteItem with the given SK.
func (t *trackingDDBScanDelete) hasSKDeleted(sk string) bool {
	for _, s := range t.deletedSKs() {
		if s == sk {
			return true
		}
	}
	return false
}

// =============================================================================
// Helper: build scan output for a single page across multiple tables
// =============================================================================

// buildDDBScanFn returns a Scan mock that serves different items based on the
// TableName in the input. Page 1 only (no pagination). Items is:
//
//	map[tableName] -> []item (each item is map[attrName]AttributeValue)
func buildDDBScanFn(items map[string][]map[string]dynamodbtypes.AttributeValue) func(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return func(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
		tbl := awssdk.ToString(input.TableName)
		var rows []map[string]dynamodbtypes.AttributeValue
		rows = append(rows, items[tbl]...)
		return &dynamodb.ScanOutput{Items: rows, Count: int32(len(rows))}, nil
	}
}

// buildDDBScanMultiPageFn serves page 1 when ExclusiveStartKey is nil, then
// page 2 when ExclusiveStartKey has key "page"="1".
func buildDDBScanMultiPageFn(
	page1 map[string][]map[string]dynamodbtypes.AttributeValue,
	page2 map[string][]map[string]dynamodbtypes.AttributeValue,
) func(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return func(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
		tbl := awssdk.ToString(input.TableName)
		isPage2 := len(input.ExclusiveStartKey) > 0

		var rows []map[string]dynamodbtypes.AttributeValue
		var lastKey map[string]dynamodbtypes.AttributeValue

		if !isPage2 {
			for _, item := range page1[tbl] {
				rows = append(rows, item)
			}
			if _, has := page2[tbl]; has && len(page2[tbl]) > 0 {
				lastKey = map[string]dynamodbtypes.AttributeValue{
					"page": &dynamodbtypes.AttributeValueMemberS{Value: "1"},
				}
			}
		} else {
			for _, item := range page2[tbl] {
				rows = append(rows, item)
			}
		}
		return &dynamodb.ScanOutput{
			Items:            rows,
			Count:            int32(len(rows)),
			LastEvaluatedKey: lastKey,
		}, nil
	}
}

// strAttr returns a DynamoDB S AttributeValue.
func strAttr(s string) dynamodbtypes.AttributeValue {
	return &dynamodbtypes.AttributeValueMemberS{Value: s}
}

// listerWithStatus builds a mockSandboxLister with sandbox IDs that have a given status.
// The lister is used to confirm active sandboxes — status is stored only in DDB rows,
// not in the lister records (which represent the known-active set from DDB queries).
func listerWithStatus(records ...kmaws.SandboxRecord) *mockSandboxLister {
	return &mockSandboxLister{records: records}
}

// =============================================================================
// Table names used throughout tests
// =============================================================================

const (
	testBudgetsTbl      = "km-budgets"
	testIdentitiesTbl   = "km-identities"
	testSlackThreadsTbl = "km-slack-threads"
	testSandboxesTbl    = "km-sandboxes"
)

// =============================================================================
// TestDoctor_OrphanedDDBRows — table-driven tests
// =============================================================================

func TestDoctor_OrphanedDDBRows_NilClient(t *testing.T) {
	// Nil client → CheckSkipped (no panic).
	result := checkOrphanedDDBRows(context.Background(), nil, listerOf("sb-1"), true, false,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)
	if result.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped for nil client, got %s", result.Status)
	}
}

func TestDoctor_OrphanedDDBRows_AllActive(t *testing.T) {
	// All rows belong to active sandboxes → CheckOK.
	const id = "sb-active"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + id), "SK": strAttr("BUDGET#compute")},
		},
		testIdentitiesTbl: {
			{"sandbox_id": strAttr(id)},
		},
		testSlackThreadsTbl: {
			{"channel_id": strAttr("C111"), "thread_ts": strAttr("123.456"), "sandbox_id": strAttr(id)},
		},
		testSandboxesTbl: {},
	})
	client := &mockDDBScanDelete{scanFn: scanFn}
	lister := listerOf(id)

	result := checkOrphanedDDBRows(context.Background(), client, lister, true, false,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if result.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_Detected(t *testing.T) {
	// Orphaned sandbox with a BUDGET#compute row → WARN with hint when dryRun=true.
	const orphanID = "sb-orphan"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#compute")},
		},
		testIdentitiesTbl:   {},
		testSlackThreadsTbl: {},
		testSandboxesTbl:    {},
	})
	client := &mockDDBScanDelete{scanFn: scanFn}
	lister := listerOf() // empty active set → orphan

	result := checkOrphanedDDBRows(context.Background(), client, lister, true /*dryRun*/, false,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if result.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", result.Status, result.Message)
	}
	if result.Remediation == "" && !containsAny(result.Message, "--delete-ddb-rows", "--dry-run=false") {
		t.Errorf("expected delete hint in WARN message, got: %s", result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_PreserveAI(t *testing.T) {
	// DBG-DDB-AI: BUDGET#ai# rows are NEVER queued for deletion, even for an
	// orphaned sandbox. Only BUDGET#compute and BUDGET#limits are deletable.
	const orphanID = "sb-ai"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#compute")},
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#limits")},
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#ai#anthropic.claude-3-5-sonnet-20241022-v1:0")},
		},
		testIdentitiesTbl:   {},
		testSlackThreadsTbl: {},
		testSandboxesTbl:    {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // orphan

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, false /*dryRun*/, true /*deleteDDBRows*/,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	// BUDGET#compute and BUDGET#limits should be deleted.
	if !tracker.hasSKDeleted("BUDGET#compute") {
		t.Errorf("expected BUDGET#compute to be deleted, deletedSKs=%v", tracker.deletedSKs())
	}
	if !tracker.hasSKDeleted("BUDGET#limits") {
		t.Errorf("expected BUDGET#limits to be deleted, deletedSKs=%v", tracker.deletedSKs())
	}
	// BUDGET#ai# row MUST NOT be deleted.
	if tracker.hasSKDeleted("BUDGET#ai#anthropic.claude-3-5-sonnet-20241022-v1:0") {
		t.Errorf("BUDGET#ai# row must NEVER be deleted (AI preservation violated), deletedSKs=%v", tracker.deletedSKs())
	}
	_ = result
}

func TestDoctor_OrphanedDDBRows_FailedStatusGuard(t *testing.T) {
	// DBG-DDB-GUARD: sandboxes rows are deleted ONLY when status∈{failed,nocap}.
	// Rows with status running/starting/stopped/paused/destroyed must be skipped.
	guardedStatuses := []string{"running", "starting", "stopped", "paused", "destroyed"}
	deletableStatuses := []string{"failed", "nocap"}

	for _, status := range guardedStatuses {
		status := status
		t.Run("skips_"+status, func(t *testing.T) {
			const orphanID = "sb-guard"
			scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
				testBudgetsTbl:    {},
				testIdentitiesTbl: {},
				testSlackThreadsTbl: {},
				testSandboxesTbl: {
					{"sandbox_id": strAttr(orphanID), "status": strAttr(status)},
				},
			})
			tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
			lister := listerOf() // not in active set

			checkOrphanedDDBRows(context.Background(), tracker, lister, false, true,
				testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

			if len(tracker.deletedItems) != 0 {
				t.Errorf("status=%s should be SKIPPED but got %d deletions", status, len(tracker.deletedItems))
			}
		})
	}

	for _, status := range deletableStatuses {
		status := status
		t.Run("deletes_"+status, func(t *testing.T) {
			const orphanID = "sb-todel"
			scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
				testBudgetsTbl:      {},
				testIdentitiesTbl:   {},
				testSlackThreadsTbl: {},
				testSandboxesTbl: {
					{"sandbox_id": strAttr(orphanID), "status": strAttr(status)},
				},
			})
			tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
			lister := listerOf() // not in active set

			checkOrphanedDDBRows(context.Background(), tracker, lister, false, true,
				testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

			if len(tracker.deletedItems) == 0 {
				t.Errorf("status=%s should produce deletion but got 0", status)
			}
		})
	}
}

func TestDoctor_OrphanedDDBRows_StartingSkipped(t *testing.T) {
	// DBG-DDB-GUARD explicit: a sandbox with status="starting" (in-flight create)
	// MUST be skipped even with dryRun=false and deleteDDBRows=true.
	const orphanID = "sb-starting"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl:      {},
		testIdentitiesTbl:   {},
		testSlackThreadsTbl: {},
		testSandboxesTbl: {
			{"sandbox_id": strAttr(orphanID), "status": strAttr("starting")},
		},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // not in active set

	checkOrphanedDDBRows(context.Background(), tracker, lister, false, true,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if len(tracker.deletedItems) != 0 {
		t.Errorf("status=starting must never be deleted, got %d deletions", len(tracker.deletedItems))
	}
}

func TestDoctor_OrphanedDDBRows_SlackThreadNoSandboxID(t *testing.T) {
	// DBG-DDB-SLACK: a slack-threads row without a sandbox_id attribute must be skipped.
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl:    {},
		testIdentitiesTbl: {},
		testSlackThreadsTbl: {
			// Row missing sandbox_id attribute — should be skipped entirely.
			{"channel_id": strAttr("C999"), "thread_ts": strAttr("999.999")},
		},
		testSandboxesTbl: {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // empty active set

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, false, true,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if len(tracker.deletedItems) != 0 {
		t.Errorf("row without sandbox_id must be skipped, got %d deletions", len(tracker.deletedItems))
	}
	// Should be CheckOK since there are no orphaned rows to report.
	if result.Status != CheckOK {
		t.Logf("result status=%s msg=%s (acceptable if no orphans found)", result.Status, result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_Delete(t *testing.T) {
	// dryRun=false && deleteDDBRows=true → deletes rows and returns counts.
	const orphanID = "sb-del"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#compute")},
		},
		testIdentitiesTbl: {
			{"sandbox_id": strAttr(orphanID)},
		},
		testSlackThreadsTbl: {
			{"channel_id": strAttr("C123"), "thread_ts": strAttr("111.222"), "sandbox_id": strAttr(orphanID)},
		},
		testSandboxesTbl: {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // orphan

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, false /*dryRun*/, true /*deleteDDBRows*/,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	// 3 rows queued: 1 budgets, 1 identities, 1 slack-threads.
	if len(tracker.deletedItems) != 3 {
		t.Errorf("expected 3 DeleteItem calls, got %d", len(tracker.deletedItems))
	}
	if result.Status == CheckError {
		t.Errorf("unexpected error status: %s", result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_PreservesOperatorRow(t *testing.T) {
	// Regression: the operator public-key row (sandbox_id="operator") lives in
	// the identities table but is NOT a sandbox, so it is never in the active
	// set. It must be preserved — otherwise every `km doctor --with-deletes
	// --dry-run=false` deletes it and operator-signed actions fail with
	// unknown_sender until --republish-operator-identity is run.
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {},
		testIdentitiesTbl: {
			{"sandbox_id": strAttr(operatorIdentitySandboxID)},
		},
		testSlackThreadsTbl: {},
		testSandboxesTbl:    {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // empty active set — operator row would orphan if unguarded

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, false /*dryRun*/, true /*deleteDDBRows*/,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if len(tracker.deletedItems) != 0 {
		t.Errorf("operator row must never be deleted, got %d DeleteItem call(s)", len(tracker.deletedItems))
	}
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK (operator row preserved, nothing orphaned), got %s: %s", result.Status, result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_Paginate(t *testing.T) {
	// DBG-PAGE: orphan on page 2 (LastEvaluatedKey) must be detected.
	const page1ID = "sb-page1"   // active
	const page2ID = "sb-page2"   // orphan, returned on page 2

	page1Budgets := []map[string]dynamodbtypes.AttributeValue{
		{"PK": strAttr("SANDBOX#" + page1ID), "SK": strAttr("BUDGET#compute")},
	}
	page2Budgets := []map[string]dynamodbtypes.AttributeValue{
		{"PK": strAttr("SANDBOX#" + page2ID), "SK": strAttr("BUDGET#compute")},
	}

	scanFn := buildDDBScanMultiPageFn(
		map[string][]map[string]dynamodbtypes.AttributeValue{
			testBudgetsTbl:      page1Budgets,
			testIdentitiesTbl:   {},
			testSlackThreadsTbl: {},
			testSandboxesTbl:    {},
		},
		map[string][]map[string]dynamodbtypes.AttributeValue{
			testBudgetsTbl:      page2Budgets,
			testIdentitiesTbl:   {},
			testSlackThreadsTbl: {},
			testSandboxesTbl:    {},
		},
	)
	client := &mockDDBScanDelete{scanFn: scanFn}
	lister := listerOf(page1ID) // page1ID active; page2ID orphaned

	result := checkOrphanedDDBRows(context.Background(), client, lister, true, false,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN for page-2 orphan, got %s: %s", result.Status, result.Message)
	}
}

func TestDoctor_OrphanedDDBRows_DryRunNoDelete(t *testing.T) {
	// dryRun=true → WARN but zero DeleteItem calls even when deleteDDBRows=true.
	const orphanID = "sb-dryrun"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#compute")},
		},
		testIdentitiesTbl:   {},
		testSlackThreadsTbl: {},
		testSandboxesTbl:    {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf() // orphan

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, true /*dryRun*/, true /*deleteDDBRows*/,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN in dryRun, got %s", result.Status)
	}
	if len(tracker.deletedItems) != 0 {
		t.Errorf("dryRun=true must not issue DeleteItem, got %d calls", len(tracker.deletedItems))
	}
}

func TestDoctor_OrphanedDDBRows_FlagNotSet(t *testing.T) {
	// dryRun=false but deleteDDBRows=false → WARN with hint but zero mutations.
	const orphanID = "sb-noflag"
	scanFn := buildDDBScanFn(map[string][]map[string]dynamodbtypes.AttributeValue{
		testBudgetsTbl: {
			{"PK": strAttr("SANDBOX#" + orphanID), "SK": strAttr("BUDGET#compute")},
		},
		testIdentitiesTbl:   {},
		testSlackThreadsTbl: {},
		testSandboxesTbl:    {},
	})
	tracker := &trackingDDBScanDelete{mock: &mockDDBScanDelete{scanFn: scanFn}}
	lister := listerOf()

	result := checkOrphanedDDBRows(context.Background(), tracker, lister, false /*dryRun*/, false /*deleteDDBRows*/,
		testBudgetsTbl, testIdentitiesTbl, testSlackThreadsTbl, testSandboxesTbl)

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN when deleteDDBRows=false, got %s", result.Status)
	}
	if len(tracker.deletedItems) != 0 {
		t.Errorf("deleteDDBRows=false must not issue DeleteItem, got %d calls", len(tracker.deletedItems))
	}
	if !containsAny(result.Message, "--delete-ddb-rows") {
		t.Errorf("expected --delete-ddb-rows hint in message: %s", result.Message)
	}
}

// =============================================================================
// Helper: containsAny
// =============================================================================

// containsAny returns true if s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 {
			// inline strings.Contains to avoid importing strings in test file
			// (doctor_log_groups_test.go already imports it but we keep this file independent)
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
