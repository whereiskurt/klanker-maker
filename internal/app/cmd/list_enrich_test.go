package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// km list used to make 4–5 serial AWS calls PER sandbox — three of them the
// identical DescribeInstances-by-tag — so six boxes cost ~30 round trips and
// 6–12 s. These tests pin the shape that makes it flat: one DescribeInstances
// for the whole list (per launch account), and everything left over fanned
// out concurrently.

// fakeEC2Describe answers DescribeInstances from a fixed instance set, filtering
// on the tag:km:sandbox-id values it is asked for, and records every call.
type fakeEC2Describe struct {
	mu        sync.Mutex
	calls     []*ec2.DescribeInstancesInput
	instances []ec2types.Instance
	err       error
}

func (f *fakeEC2Describe) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	want := map[string]bool{}
	runningOnly := false
	for _, flt := range in.Filters {
		switch awssdk.ToString(flt.Name) {
		case "tag:km:sandbox-id":
			for _, v := range flt.Values {
				want[v] = true
			}
		case "instance-state-name":
			runningOnly = true
		}
	}
	var out []ec2types.Instance
	for _, inst := range f.instances {
		if !want[sandboxIDTag(inst)] {
			continue
		}
		if runningOnly && (inst.State == nil || inst.State.Name != ec2types.InstanceStateNameRunning) {
			continue
		}
		out = append(out, inst)
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: out}}}, nil
}

func taggedInstance(id, sandboxID string, state ec2types.InstanceStateName, hibernate bool, launched time.Time) ec2types.Instance {
	return ec2types.Instance{
		InstanceId:         awssdk.String(id),
		State:              &ec2types.InstanceState{Name: state},
		LaunchTime:         awssdk.Time(launched),
		HibernationOptions: &ec2types.HibernationOptions{Configured: awssdk.Bool(hibernate)},
		Tags:               []ec2types.Tag{{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String(sandboxID)}},
	}
}

func TestDescribeSandboxInstances_OneCallGroupedByTag(t *testing.T) {
	now := time.Now()
	f := &fakeEC2Describe{instances: []ec2types.Instance{
		taggedInstance("i-1", "sb-a", ec2types.InstanceStateNameRunning, true, now),
		taggedInstance("i-2", "sb-b", ec2types.InstanceStateNameStopped, false, now),
		taggedInstance("i-3", "sb-b", ec2types.InstanceStateNameTerminated, false, now.Add(-time.Hour)),
		taggedInstance("i-9", "sb-other", ec2types.InstanceStateNameRunning, false, now),
	}}
	got, err := describeSandboxInstances(context.Background(), f, []string{"sb-a", "sb-b", "sb-none"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("want exactly 1 DescribeInstances for 3 sandboxes, got %d", len(f.calls))
	}
	if len(got["sb-a"]) != 1 || len(got["sb-b"]) != 2 {
		t.Errorf("grouping wrong: sb-a=%d sb-b=%d", len(got["sb-a"]), len(got["sb-b"]))
	}
	// A queried id with no instances is PRESENT with an empty slice: the caller
	// must be able to tell "described, nothing there" (reconcile may downgrade a
	// running claim) from "the call failed" (keep the stored status).
	if _, ok := got["sb-none"]; !ok {
		t.Error("queried id with no instances must still be present in the result")
	}
	if _, ok := got["sb-other"]; ok {
		t.Error("an id we did not ask for leaked into the result")
	}
}

// The EC2 filter accepts at most 200 values; a bigger fleet must chunk, not fail.
func TestDescribeSandboxInstances_ChunksAtFilterLimit(t *testing.T) {
	ids := make([]string, 0, 450)
	for i := 0; i < 450; i++ {
		ids = append(ids, fmt.Sprintf("sb-%03d", i))
	}
	f := &fakeEC2Describe{}
	if _, err := describeSandboxInstances(context.Background(), f, ids); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("450 ids should take 3 calls of ≤200 values, got %d", len(f.calls))
	}
	for _, c := range f.calls {
		if n := len(c.Filters[0].Values); n > describeFilterValueMax {
			t.Errorf("a call carried %d filter values (max %d)", n, describeFilterValueMax)
		}
	}
}

func TestHibernationFromInstances(t *testing.T) {
	now := time.Now()
	if !hibernationFromInstances([]ec2types.Instance{taggedInstance("i", "s", ec2types.InstanceStateNameRunning, true, now)}) {
		t.Error("configured hibernation not reported")
	}
	if hibernationFromInstances(nil) {
		t.Error("no instances reported as hibernating")
	}
	if hibernationFromInstances([]ec2types.Instance{{InstanceId: awssdk.String("i")}}) {
		t.Error("absent HibernationOptions reported as hibernating")
	}
}

// ---- enrichRecords: the orchestrator ----

type fakeCW struct {
	calls  int32
	events []cwtypes.OutputLogEvent
}

func (f *fakeCW) GetLogEvents(context.Context, *cloudwatchlogs.GetLogEventsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	atomic.AddInt32(&f.calls, 1)
	return &cloudwatchlogs.GetLogEventsOutput{Events: f.events}, nil
}

type fakeSSMSessions struct {
	calls int32
	gate  chan struct{} // when non-nil, every call blocks until closed — proves concurrency
}

func (f *fakeSSMSessions) DescribeSessions(_ context.Context, _ *ssm.DescribeSessionsInput, _ ...func(*ssm.Options)) (*ssm.DescribeSessionsOutput, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return &ssm.DescribeSessionsOutput{}, nil
}

type fakeDDBQuery struct{ calls int32 }

func (f *fakeDDBQuery) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	atomic.AddInt32(&f.calls, 1)
	return &dynamodb.QueryOutput{Count: 2}, nil
}

func sixRunningRecords() []kmaws.SandboxRecord {
	var recs []kmaws.SandboxRecord
	for i := 0; i < 6; i++ {
		recs = append(recs, kmaws.SandboxRecord{
			SandboxID: fmt.Sprintf("sb-%d", i), Status: "running", Substrate: "ec2spot",
			IdleTimeout: "30m", CreatedAt: time.Now().Add(-time.Hour),
			SlackChannelID: fmt.Sprintf("C%d", i), SlackInboundQueueURL: "https://sqs/q",
		})
	}
	return recs
}

func instancesFor(recs []kmaws.SandboxRecord) []ec2types.Instance {
	var out []ec2types.Instance
	for i, r := range recs {
		out = append(out, taggedInstance(fmt.Sprintf("i-%d", i), r.SandboxID, ec2types.InstanceStateNameRunning, i%2 == 0, time.Now().Add(-time.Hour)))
	}
	return out
}

func TestEnrichRecords_OneDescribeForTheWholeList(t *testing.T) {
	recs := sixRunningRecords()
	ec2f := &fakeEC2Describe{instances: instancesFor(recs)}
	cw := &fakeCW{}
	sess := &fakeSSMSessions{}
	ddb := &fakeDDBQuery{}
	enrichRecords(context.Background(), listEnrichDeps{ec2: ec2f, cw: cw, ssm: sess, ddb: ddb}, recs, "km", "km-slack-threads", true)

	if len(ec2f.calls) != 1 {
		t.Fatalf("6 running ec2 sandboxes must cost ONE DescribeInstances, got %d", len(ec2f.calls))
	}
	for i, r := range recs {
		if r.Status != "running" {
			t.Errorf("%s: status %q after reconcile", r.SandboxID, r.Status)
		}
		if r.Hibernation != (i%2 == 0) {
			t.Errorf("%s: hibernation %v", r.SandboxID, r.Hibernation)
		}
		if r.IdleRemaining == "" {
			t.Errorf("%s: idle not computed", r.SandboxID)
		}
		if r.ActiveThreads != 2 {
			t.Errorf("%s: thread count %d", r.SandboxID, r.ActiveThreads)
		}
	}
	if int(cw.calls) != 6 || int(sess.calls) != 6 || int(ddb.calls) != 6 {
		t.Errorf("per-row lookups: cw=%d ssm=%d ddb=%d (want 6 each)", cw.calls, sess.calls, ddb.calls)
	}
}

// The per-row lookups must overlap. Every SSM call blocks on a gate; if the
// loop were serial the first call would never return and this test would hang,
// so we wait for ALL six to be in flight before opening the gate.
func TestEnrichRecords_PerRowLookupsRunConcurrently(t *testing.T) {
	recs := sixRunningRecords()
	ec2f := &fakeEC2Describe{instances: instancesFor(recs)}
	sess := &fakeSSMSessions{gate: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		enrichRecords(context.Background(), listEnrichDeps{ec2: ec2f, cw: &fakeCW{}, ssm: sess}, recs, "km", "", false)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&sess.calls) < 6 {
		select {
		case <-deadline:
			t.Fatalf("only %d of 6 idle lookups in flight after 5s — the fan-out is serial", sess.calls)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	close(sess.gate)
	<-done
}

// A failed batch describe must keep the stored status — never mislabel a live
// box on a transient EC2 error — and must not stop the idle lookups either.
func TestEnrichRecords_DescribeFailureKeepsStoredStatus(t *testing.T) {
	recs := sixRunningRecords()
	recs[1].Status = "stopped"
	ec2f := &fakeEC2Describe{err: errors.New("throttled")}
	enrichRecords(context.Background(), listEnrichDeps{ec2: ec2f, cw: &fakeCW{}, ssm: &fakeSSMSessions{}}, recs, "km", "", false)
	if recs[0].Status != "running" || recs[1].Status != "stopped" {
		t.Errorf("statuses changed on describe failure: %q %q", recs[0].Status, recs[1].Status)
	}
	if recs[0].IdleRemaining == "" {
		t.Error("idle lookup skipped because the describe failed")
	}
}

// A successful describe that finds nothing for a "running" sandbox downgrades it
// (the instance is gone) — the pre-batch behaviour, preserved.
func TestEnrichRecords_MissingInstanceDowngradesRunning(t *testing.T) {
	recs := sixRunningRecords()[:1]
	ec2f := &fakeEC2Describe{}
	enrichRecords(context.Background(), listEnrichDeps{ec2: ec2f, cw: &fakeCW{}, ssm: &fakeSSMSessions{}}, recs, "km", "", false)
	if recs[0].Status != "killed" {
		t.Errorf("running sandbox with no instance should read killed, got %q", recs[0].Status)
	}
}

// Cross-account sandboxes are described where they live: one call per launch
// account, the home client never asked about a linked box.
func TestEnrichRecords_LinkedAccountsGetTheirOwnDescribe(t *testing.T) {
	recs := sixRunningRecords()[:3]
	recs[2].LaunchAccount = "gpu-link"
	home := &fakeEC2Describe{instances: instancesFor(recs[:2])}
	link := &fakeEC2Describe{instances: instancesFor(recs)[2:]}
	deps := listEnrichDeps{
		ec2: home, cw: &fakeCW{}, ssm: &fakeSSMSessions{},
		linkEC2: func(_ context.Context, name string) ec2DescribeInstancesAPI {
			if name == "gpu-link" {
				return link
			}
			return nil
		},
	}
	enrichRecords(context.Background(), deps, recs, "km", "", false)
	if len(home.calls) != 1 || len(link.calls) != 1 {
		t.Fatalf("want 1 home + 1 link describe, got home=%d link=%d", len(home.calls), len(link.calls))
	}
	for _, v := range home.calls[0].Filters[0].Values {
		if v == "sb-2" {
			t.Error("linked sandbox was described in the home account")
		}
	}
	if recs[2].Status != "running" {
		t.Errorf("linked box reconciled to %q — a RUNNING GPU box reported dead is the expensive direction", recs[2].Status)
	}
}
