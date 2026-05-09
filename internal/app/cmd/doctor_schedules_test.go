// Tests for checkStaleSchedules — specifically the regression where every
// km-at-* schedule was unconditionally exempted from the sandbox-ID
// containment check, leaking orphan at-schedules forever after km destroy
// (which doesn't sweep per-sandbox at-schedules).
package cmd

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedtypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// fakeSchedulerForDoctor is a minimal kmaws.SchedulerAPI implementation
// used by checkStaleSchedules tests. Returns the configured names from
// ListSchedules and records every DeleteSchedule call.
type fakeSchedulerForDoctor struct {
	scheduleNames []string
	listErr       error
	deleteErr     error
	deleted       []string
}

var _ kmaws.SchedulerAPI = (*fakeSchedulerForDoctor)(nil)

func (f *fakeSchedulerForDoctor) ListSchedules(_ context.Context, _ *scheduler.ListSchedulesInput, _ ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := &scheduler.ListSchedulesOutput{}
	for _, n := range f.scheduleNames {
		out.Schedules = append(out.Schedules, schedtypes.ScheduleSummary{Name: awssdk.String(n)})
	}
	return out, nil
}

func (f *fakeSchedulerForDoctor) CreateSchedule(_ context.Context, _ *scheduler.CreateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	return &scheduler.CreateScheduleOutput{}, nil
}

func (f *fakeSchedulerForDoctor) DeleteSchedule(_ context.Context, in *scheduler.DeleteScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	if in != nil && in.Name != nil {
		f.deleted = append(f.deleted, *in.Name)
	}
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &scheduler.DeleteScheduleOutput{}, nil
}

func (f *fakeSchedulerForDoctor) GetSchedule(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return &scheduler.GetScheduleOutput{}, nil
}

// TestCheckStaleSchedules_KmAtForDestroyedSandbox_FlaggedStale verifies the
// regression fix: a per-sandbox km-at-* schedule whose sandbox is gone from
// DDB must be detected as stale. The previous code exempted every km-at-*
// schedule from the sandbox-ID containment check, so destroying a sandbox
// left its at-schedules behind — they kept firing against an instance ID
// that didn't exist anymore.
func TestCheckStaleSchedules_KmAtForDestroyedSandbox_FlaggedStale(t *testing.T) {
	sched := &fakeSchedulerForDoctor{
		scheduleNames: []string{
			"km-at-extend-sb-ghost-tomorrow-at-9am",
			"km-at-kill-sb-ghost-in-1h",
		},
	}
	// Empty active set — sandbox is destroyed.
	r := checkStaleSchedules(context.Background(), sched, &fakeSandboxLister{}, true, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn (orphan km-at-* schedules), got %s: %s", r.Status, r.Message)
	}
	if len(sched.deleted) != 0 {
		t.Errorf("dryRun=true must not delete; saw %v", sched.deleted)
	}
}

// TestCheckStaleSchedules_KmAtForActiveSandbox_NotStale verifies that
// km-at-* schedules whose sandbox is still alive are NOT flagged. The
// nameContainsSandboxIDToken matcher recognises sb-alive as a hyphen-
// delimited token embedded mid-name (between `-extend-` and `-tomorrow`).
func TestCheckStaleSchedules_KmAtForActiveSandbox_NotStale(t *testing.T) {
	sched := &fakeSchedulerForDoctor{
		scheduleNames: []string{
			"km-at-extend-sb-alive-tomorrow-at-9am",
		},
	}
	lister := &fakeSandboxLister{
		records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}},
	}
	r := checkStaleSchedules(context.Background(), sched, lister, true, "km")
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK when km-at-* references a live sandbox, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleSchedules_KmAtCreate_AlwaysPreserved verifies the carve-out
// for km-at-create-* schedules: these provision a NEW sandbox at fire time,
// so there's no pre-existing sandbox ID to embed. They must not be swept
// by sandbox-staleness logic — EventBridge auto-deletes one-shot at()
// schedules after firing, so a lingering km-at-create-* is by definition
// still pending future work.
func TestCheckStaleSchedules_KmAtCreate_AlwaysPreserved(t *testing.T) {
	sched := &fakeSchedulerForDoctor{
		scheduleNames: []string{
			"km-at-create-tomorrow-at-9am",
			"km-at-create-2026-05-08T17-00-00",
		},
	}
	// Empty active set — but km-at-create-* must still be preserved.
	r := checkStaleSchedules(context.Background(), sched, &fakeSandboxLister{}, false, "km")
	if r.Status != CheckOK {
		t.Fatalf("km-at-create-* must never be flagged stale, got %s: %s", r.Status, r.Message)
	}
	if len(sched.deleted) != 0 {
		t.Errorf("km-at-create-* schedules must NEVER be deleted, but DeleteSchedule was called for: %v", sched.deleted)
	}
}

// TestCheckStaleSchedules_DeletesStaleKmAtOnDryRunFalse verifies the
// destructive path: with dryRun=false, stale km-at-* schedules are deleted
// (closing the leak from km destroy not sweeping at-schedules).
func TestCheckStaleSchedules_DeletesStaleKmAtOnDryRunFalse(t *testing.T) {
	sched := &fakeSchedulerForDoctor{
		scheduleNames: []string{
			"km-at-extend-sb-ghost-tomorrow",
			"km-at-create-tomorrow",          // must NOT be deleted
			"km-at-kill-sb-alive-in-2h",      // must NOT be deleted (sandbox alive)
			"km-budget-enforcer-sb-ghost",    // generic km- schedule, also stale
		},
	}
	lister := &fakeSandboxLister{
		records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}},
	}
	r := checkStaleSchedules(context.Background(), sched, lister, false, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	deletedSet := make(map[string]bool, len(sched.deleted))
	for _, n := range sched.deleted {
		deletedSet[n] = true
	}
	if !deletedSet["km-at-extend-sb-ghost-tomorrow"] {
		t.Errorf("expected km-at-extend-sb-ghost-tomorrow to be deleted, got: %v", sched.deleted)
	}
	if !deletedSet["km-budget-enforcer-sb-ghost"] {
		t.Errorf("expected km-budget-enforcer-sb-ghost to be deleted, got: %v", sched.deleted)
	}
	if deletedSet["km-at-create-tomorrow"] {
		t.Errorf("km-at-create-* schedules must NEVER be deleted, but it was: %v", sched.deleted)
	}
	if deletedSet["km-at-kill-sb-alive-in-2h"] {
		t.Errorf("schedule for live sandbox must NOT be deleted, but it was: %v", sched.deleted)
	}
}
