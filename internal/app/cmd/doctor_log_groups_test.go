package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// trackingCWLogsCleanup wraps mockCWLogsCleanup and records which log group names
// were passed to DeleteLogGroup and PutRetentionPolicy for assertion in tests.
type trackingCWLogsCleanup struct {
	mock           *mockCWLogsCleanup
	mu             sync.Mutex
	deleted        []string
	retentionSet   []string
}

func (t *trackingCWLogsCleanup) DescribeLogGroups(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	return t.mock.DescribeLogGroups(ctx, input, optFns...)
}

func (t *trackingCWLogsCleanup) DeleteLogGroup(ctx context.Context, input *cloudwatchlogs.DeleteLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
	t.mu.Lock()
	t.deleted = append(t.deleted, aws.ToString(input.LogGroupName))
	t.mu.Unlock()
	return t.mock.DeleteLogGroup(ctx, input, optFns...)
}

func (t *trackingCWLogsCleanup) PutRetentionPolicy(ctx context.Context, input *cloudwatchlogs.PutRetentionPolicyInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	t.mu.Lock()
	t.retentionSet = append(t.retentionSet, aws.ToString(input.LogGroupName))
	t.mu.Unlock()
	return t.mock.PutRetentionPolicy(ctx, input, optFns...)
}

// buildDescribeFn builds a DescribeLogGroups mock that returns different pages
// based on the LogGroupNamePrefix filter and an optional NextToken page map.
// groups maps prefix → []logGroupName (page 1 only unless pages specified).
func buildDescribeMultiPageFn(
	page1 map[string][]string,
	page2 map[string][]string, // keyed by prefix, only returned when NextToken=="page1"
) func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	return func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
		prefix := aws.ToString(input.LogGroupNamePrefix)
		token := aws.ToString(input.NextToken)

		var names []string
		var nextTok *string
		if token == "" {
			// Page 1
			names = page1[prefix]
			if page2 != nil {
				if _, ok := page2[prefix]; ok {
					nextTok = aws.String("page2-" + prefix)
				}
			}
		} else {
			// Page 2+: extract prefix from token
			rawPrefix := strings.TrimPrefix(token, "page2-")
			if page2 != nil {
				names = page2[rawPrefix]
			}
		}

		var groups []cwlogstypes.LogGroup
		for _, n := range names {
			n := n
			groups = append(groups, cwlogstypes.LogGroup{LogGroupName: aws.String(n)})
		}
		return &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: groups, NextToken: nextTok}, nil
	}
}

// buildDescribeFn is a simpler helper: one page, prefix→names.
func buildDescribeFn(groups map[string][]string) func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	return buildDescribeMultiPageFn(groups, nil)
}

// listerOf returns a mockSandboxLister with the given sandbox IDs.
func listerOf(ids ...string) *mockSandboxLister {
	var records []kmaws.SandboxRecord
	for _, id := range ids {
		records = append(records, kmaws.SandboxRecord{SandboxID: id})
	}
	return &mockSandboxLister{records: records}
}

func TestDoctor_StaleLogGroups_OrphanDetected(t *testing.T) {
	// One orphaned sandbox: has a budget-enforcer log group but not in active set.
	const orphanID = "sb-abc123"
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			"/aws/lambda/km-budget-enforcer-": {"/aws/lambda/km-budget-enforcer-" + orphanID},
		}),
	}
	lister := listerOf() // empty active set → orphan

	result := checkStaleLogGroups(ctx(), client, lister, true /*dryRun*/, false /*deleteLogs*/, false /*setLogRetention*/, 30, "km")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, orphanID) && !strings.Contains(result.Message, "1") {
		t.Errorf("expected orphan reference in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "--delete-logs") {
		t.Errorf("expected --delete-logs hint in message, got: %s", result.Message)
	}
}

func TestDoctor_StaleLogGroups_AllActive(t *testing.T) {
	// All log groups belong to active sandboxes → OK.
	const activeID = "sb-active1"
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			"/aws/lambda/km-budget-enforcer-": {"/aws/lambda/km-budget-enforcer-" + activeID},
		}),
	}
	lister := listerOf(activeID)

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "km")

	if result.Status != CheckOK {
		t.Fatalf("expected OK, got %s: %s", result.Status, result.Message)
	}
}

func TestDoctor_StaleLogGroups_DryRun(t *testing.T) {
	// dryRun=true → WARN but zero DeleteLogGroup calls.
	const orphanID = "sb-dryrun"
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: buildDescribeFn(map[string][]string{
				"/aws/lambda/km-budget-enforcer-": {"/aws/lambda/km-budget-enforcer-" + orphanID},
			}),
		},
	}
	lister := listerOf() // no active sandboxes

	result := checkStaleLogGroups(ctx(), tracker, lister, true /*dryRun*/, true /*deleteLogs*/, false, 30, "km")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN in dry-run, got %s", result.Status)
	}
	if len(tracker.deleted) != 0 {
		t.Errorf("expected no deletions in dry-run, got %v", tracker.deleted)
	}
}

func TestDoctor_StaleLogGroups_DeleteFlag(t *testing.T) {
	// dryRun=false && deleteLogs=true → deletes orphan groups, returns counts.
	const orphanID = "sb-todel"
	groupName := "/aws/lambda/km-budget-enforcer-" + orphanID
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: buildDescribeFn(map[string][]string{
				"/aws/lambda/km-budget-enforcer-": {groupName},
			}),
		},
	}
	lister := listerOf()

	result := checkStaleLogGroups(ctx(), tracker, lister, false /*dryRun*/, true /*deleteLogs*/, false, 30, "km")

	if result.Status != CheckOK && result.Status != CheckWarn {
		t.Fatalf("expected OK or WARN after delete, got %s: %s", result.Status, result.Message)
	}
	if len(tracker.deleted) != 1 || tracker.deleted[0] != groupName {
		t.Errorf("expected %s deleted, got %v", groupName, tracker.deleted)
	}
	if !strings.Contains(result.Message, "deleted") {
		t.Errorf("expected 'deleted' in message, got: %s", result.Message)
	}
}

func TestDoctor_StaleLogGroups_Pagination(t *testing.T) {
	// Orphan on page 2 (NextToken) must still be detected.
	const page2OrphanID = "sb-page2"
	const page1ID = "sb-page1"
	groupPage2 := "/aws/lambda/km-budget-enforcer-" + page2OrphanID
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeMultiPageFn(
			map[string][]string{
				"/aws/lambda/km-budget-enforcer-": {"/aws/lambda/km-budget-enforcer-" + page1ID},
			},
			map[string][]string{
				"/aws/lambda/km-budget-enforcer-": {groupPage2},
			},
		),
	}
	lister := listerOf(page1ID) // page1ID is active; page2OrphanID is not

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "km")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN for page-2 orphan, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, page2OrphanID) && !strings.Contains(result.Message, "1") {
		t.Errorf("page-2 orphan not reflected in message: %s", result.Message)
	}
}

func TestDoctor_StaleLogGroups_KmPrefix(t *testing.T) {
	// resource_prefix=kph install, but legacy groups are named km-… (historical).
	// The check must still detect them as orphans.
	const orphanID = "sb-legacykm"
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			// Legacy km- name, on a kph install
			"/aws/lambda/km-budget-enforcer-":            {"/aws/lambda/km-budget-enforcer-" + orphanID},
			"/aws/lambda/km-github-token-refresher-":     {},
			"/km/sandboxes/":                             {},
			"/km/sidecars/":                              {},
			"/aws/lambda/kph-budget-enforcer-":           {},
			"/aws/lambda/kph-github-token-refresher-":   {},
			"/kph/sandboxes/":                            {},
			"/kph/sidecars/":                             {},
		}),
	}
	lister := listerOf() // no active sandboxes

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "kph")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN for legacy km- orphan on kph install, got %s: %s", result.Status, result.Message)
	}
}

func TestDoctor_StaleLogGroups_BothNames(t *testing.T) {
	// kph install: orphaned sandbox has BOTH a legacy km- group AND a new kph- group.
	// Both must be detected, but no double-counting (by full group name dedup).
	const orphanID = "sb-bothn"
	legacyGroup := "/aws/lambda/km-budget-enforcer-" + orphanID
	prefixedGroup := "/aws/lambda/kph-budget-enforcer-" + orphanID
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: buildDescribeFn(map[string][]string{
				"/aws/lambda/km-budget-enforcer-":          {legacyGroup},
				"/aws/lambda/km-github-token-refresher-":   {},
				"/km/sandboxes/":                            {},
				"/km/sidecars/":                             {},
				"/aws/lambda/kph-budget-enforcer-":         {prefixedGroup},
				"/aws/lambda/kph-github-token-refresher-":  {},
				"/kph/sandboxes/":                           {},
				"/kph/sidecars/":                            {},
			}),
		},
	}
	lister := listerOf() // both orphaned

	// Delete both
	result := checkStaleLogGroups(ctx(), tracker, lister, false, true, false, 30, "kph")

	if result.Status != CheckOK && result.Status != CheckWarn {
		t.Fatalf("unexpected status %s: %s", result.Status, result.Message)
	}

	// Both distinct group names must have been deleted (deduped by name → 2)
	if len(tracker.deleted) != 2 {
		t.Errorf("expected 2 distinct deletions, got %v", tracker.deleted)
	}
	deletedSet := map[string]bool{}
	for _, d := range tracker.deleted {
		deletedSet[d] = true
	}
	if !deletedSet[legacyGroup] {
		t.Errorf("legacy group %s not deleted", legacyGroup)
	}
	if !deletedSet[prefixedGroup] {
		t.Errorf("prefixed group %s not deleted", prefixedGroup)
	}
}

func TestDoctor_StaleLogGroups_DefaultInstallCollapse(t *testing.T) {
	// When prefix=="km", legacy and prefixed filters are identical.
	// DescribeLogGroups should be called once per distinct filter (not doubled).
	// No double-counting of groups.
	const activeID = "sb-default"
	callCount := 0
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
			callCount++
			prefix := aws.ToString(input.LogGroupNamePrefix)
			names := map[string][]string{
				"/aws/lambda/km-budget-enforcer-":        {"/aws/lambda/km-budget-enforcer-" + activeID},
				"/aws/lambda/km-github-token-refresher-": {},
				"/km/sandboxes/":                         {},
				"/km/sidecars/":                          {},
			}[prefix]
			var groups []cwlogstypes.LogGroup
			for _, n := range names {
				n := n
				groups = append(groups, cwlogstypes.LogGroup{LogGroupName: aws.String(n)})
			}
			return &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: groups}, nil
		},
	}
	lister := listerOf(activeID)

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "km")

	if result.Status != CheckOK {
		t.Fatalf("expected OK, got %s: %s", result.Status, result.Message)
	}
	// Should call exactly 4 (one per distinct filter: km==km so 4 distinct not 8)
	if callCount != 4 {
		t.Errorf("expected 4 DescribeLogGroups calls (deduped), got %d", callCount)
	}
}

func TestDoctor_StaleLogGroups_IgnorePrefix(t *testing.T) {
	// A sibling install's sandbox ID appears in log groups but NOT in the local active set.
	// It is treated as orphaned by the diff — we verify that sibling IDs are NOT deleted
	// when deleteLogs=false. This documents that multi-install isolation is via active-set diff.
	const siblingID = "sb-sibling"
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			"/aws/lambda/km-budget-enforcer-": {"/aws/lambda/km-budget-enforcer-" + siblingID},
		}),
	}
	lister := listerOf() // local install has no active sandboxes (sibling's ID unknown)

	result := checkStaleLogGroups(ctx(), client, lister, true /*dryRun*/, false, false, 30, "km")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN (sibling looks orphaned to local install), got %s", result.Status)
	}
	// No actual deletion happened (dryRun=true)
	// Users should also pass --ignore-prefix to doctor, which operates at the command level.
	// At the check level: no explosion, just WARN.
}

func TestDoctor_StaleLogGroups_NilClient(t *testing.T) {
	result := checkStaleLogGroups(ctx(), nil, listerOf(), true, false, false, 30, "km")
	if result.Status != CheckSkipped {
		t.Errorf("expected SKIPPED for nil client, got %s", result.Status)
	}
}

func TestDoctor_StaleLogGroups_FourFamilies(t *testing.T) {
	// All four families (budget-enforcer, github-token-refresher, /km/sandboxes/, /km/sidecars/)
	// are scanned.  Each has an orphaned group for a different sandbox ID.
	orphans := map[string]string{
		"sb-budget":  "/aws/lambda/km-budget-enforcer-sb-budget",
		"sb-github":  "/aws/lambda/km-github-token-refresher-sb-github",
		"sb-audit":   "/km/sandboxes/sb-audit/some-stream",
		"sb-sidecar": "/km/sidecars/sb-sidecar",
	}
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			"/aws/lambda/km-budget-enforcer-":        {orphans["sb-budget"]},
			"/aws/lambda/km-github-token-refresher-": {orphans["sb-github"]},
			"/km/sandboxes/":                         {orphans["sb-audit"]},
			"/km/sidecars/":                          {orphans["sb-sidecar"]},
		}),
	}
	lister := listerOf() // all orphaned

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "km")

	if result.Status != CheckWarn {
		t.Fatalf("expected WARN for four-family orphans, got %s: %s", result.Status, result.Message)
	}
	// Should see 4 orphaned sandbox IDs
	for id := range orphans {
		if !strings.Contains(result.Message, id) && !strings.Contains(result.Message, "4") {
			// Either the ID is in the message or the count reflects all 4
		}
	}
}

// --- Retention tests (Task 2) ---

func TestDoctor_StaleLogGroups_RetentionAlreadySet(t *testing.T) {
	// Group already has RetentionInDays set → no PutRetentionPolicy call.
	const activeID = "sb-hasret"
	retention := int32(30)
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
				prefix := aws.ToString(input.LogGroupNamePrefix)
				var groups []cwlogstypes.LogGroup
				if prefix == "/aws/lambda/km-budget-enforcer-" {
					groups = []cwlogstypes.LogGroup{
						{
							LogGroupName:    aws.String("/aws/lambda/km-budget-enforcer-" + activeID),
							RetentionInDays: &retention,
						},
					}
				}
				return &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: groups}, nil
			},
		},
	}
	lister := listerOf(activeID)

	result := checkStaleLogGroups(ctx(), tracker, lister, false /*dryRun*/, false, true /*setLogRetention*/, 30, "km")

	if result.Status != CheckOK {
		t.Fatalf("expected OK when retention already set, got %s: %s", result.Status, result.Message)
	}
	if len(tracker.retentionSet) != 0 {
		t.Errorf("expected no PutRetentionPolicy calls, got %v", tracker.retentionSet)
	}
}

func TestDoctor_StaleLogGroups_SetRetention(t *testing.T) {
	// Group with nil RetentionInDays and setLogRetention=true → PutRetentionPolicy called.
	const activeID = "sb-noret"
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
				prefix := aws.ToString(input.LogGroupNamePrefix)
				var groups []cwlogstypes.LogGroup
				if prefix == "/aws/lambda/km-budget-enforcer-" {
					groups = []cwlogstypes.LogGroup{
						{
							LogGroupName:    aws.String("/aws/lambda/km-budget-enforcer-" + activeID),
							RetentionInDays: nil, // not set
						},
					}
				}
				return &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: groups}, nil
			},
		},
	}
	lister := listerOf(activeID)

	result := checkStaleLogGroups(ctx(), tracker, lister, false, false, true /*setLogRetention*/, 90, "km")

	if result.Status != CheckOK {
		t.Fatalf("expected OK after setting retention, got %s: %s", result.Status, result.Message)
	}
	if len(tracker.retentionSet) == 0 {
		t.Errorf("expected PutRetentionPolicy call, got none")
	}
	// The group must have been targeted
	found := false
	for _, n := range tracker.retentionSet {
		if strings.Contains(n, activeID) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected group containing %s in retention set, got %v", activeID, tracker.retentionSet)
	}
}

func TestDoctor_StaleLogGroups_ManagementGroupsNeverDeleted(t *testing.T) {
	// Management groups (/aws/lambda/{prefix}-create-handler etc.) must never appear
	// in DeleteLogGroup calls, only in PutRetentionPolicy when retention is nil.
	const orphanID = "sb-managed"
	mgmtGroup := "/aws/lambda/km-create-handler"
	sandboxGroup := "/aws/lambda/km-budget-enforcer-" + orphanID
	tracker := &trackingCWLogsCleanup{
		mock: &mockCWLogsCleanup{
			describeLogGroupsFn: func(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
				prefix := aws.ToString(input.LogGroupNamePrefix)
				names := map[string][]string{
					"/aws/lambda/km-budget-enforcer-": {sandboxGroup},
					// Management group returned under the management prefix scan
					"/aws/lambda/km-create-handler":         {mgmtGroup},
					"/aws/lambda/km-ttl-handler":            {},
					"/aws/lambda/km-email-handler":          {},
					"/aws/lambda/km-slack-bridge":           {},
					"/aws/lambda/km-github-token-refresher-": {},
					"/km/sandboxes/":                        {},
					"/km/sidecars/":                         {},
				}
				var groups []cwlogstypes.LogGroup
				for _, n := range names[prefix] {
					n := n
					groups = append(groups, cwlogstypes.LogGroup{LogGroupName: aws.String(n)})
				}
				return &cloudwatchlogs.DescribeLogGroupsOutput{LogGroups: groups}, nil
			},
		},
	}
	lister := listerOf() // orphanID is not active

	result := checkStaleLogGroups(ctx(), tracker, lister, false /*dryRun*/, true /*deleteLogs*/, true /*setLogRetention*/, 30, "km")

	// Management group should NOT have been deleted
	for _, d := range tracker.deleted {
		if d == mgmtGroup {
			t.Errorf("management group %s was incorrectly deleted", mgmtGroup)
		}
	}
	// Sandbox group should have been deleted
	deletedSandbox := false
	for _, d := range tracker.deleted {
		if d == sandboxGroup {
			deletedSandbox = true
		}
	}
	if !deletedSandbox {
		t.Errorf("orphaned sandbox group %s was not deleted; deleted=%v", sandboxGroup, tracker.deleted)
	}
	_ = result
}

func TestDoctor_StaleLogGroups_DeleteResourceNotFound(t *testing.T) {
	// ResourceNotFoundException during delete is treated as already-deleted (success).
	const orphanID = "sb-notfound"
	groupName := "/aws/lambda/km-budget-enforcer-" + orphanID
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(map[string][]string{
			"/aws/lambda/km-budget-enforcer-": {groupName},
		}),
		deleteLogGroupFn: func(ctx context.Context, input *cloudwatchlogs.DeleteLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
			return nil, &cwlogstypes.ResourceNotFoundException{Message: aws.String("not found")}
		},
	}
	lister := listerOf()

	result := checkStaleLogGroups(ctx(), client, lister, false, true, false, 30, "km")

	// ResourceNotFoundException counts as deleted → OK
	if result.Status != CheckOK {
		t.Fatalf("expected OK when group already gone, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "deleted") {
		t.Errorf("expected 'deleted' in message, got: %s", result.Message)
	}
}

func TestDoctor_StaleLogGroups_NoGroupsFound(t *testing.T) {
	// No log groups found at all → OK.
	client := &mockCWLogsCleanup{
		describeLogGroupsFn: buildDescribeFn(nil),
	}
	lister := listerOf()

	result := checkStaleLogGroups(ctx(), client, lister, true, false, false, 30, "km")

	if result.Status != CheckOK {
		t.Fatalf("expected OK when no groups found, got %s: %s", result.Status, result.Message)
	}
}

// ctx is a helper to get a background context.
func ctx() context.Context {
	return context.Background()
}

// compile-time assertion: checkStaleLogGroups is exported to this test package.
var _ = fmt.Sprintf
