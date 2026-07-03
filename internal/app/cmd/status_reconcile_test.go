package cmd

import (
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func rcInst(state ec2types.InstanceStateName, reasonCode string, launch time.Time) ec2types.Instance {
	i := ec2types.Instance{State: &ec2types.InstanceState{Name: state}}
	if reasonCode != "" {
		i.StateReason = &ec2types.StateReason{Code: awssdk.String(reasonCode)}
	}
	if !launch.IsZero() {
		i.LaunchTime = awssdk.Time(launch)
	}
	return i
}

// TestReconcileStatusFromInstances is the core of the drift fix: the displayed
// status is reconciled against the live EC2 instance state in BOTH directions,
// so a box the DDB believes is "stopped" but which is actually running (idle-stop
// then restart, or a resume whose status write raced) shows "running" — while
// "paused" (hibernated → EC2 stopped) and "failed" are preserved.
func TestReconcileStatusFromInstances(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	cases := []struct {
		name      string
		stored    string
		instances []ec2types.Instance
		want      string
	}{
		// THE bug: DDB says stopped, instance is actually running.
		{"stale-stopped-but-running", "stopped", []ec2types.Instance{rcInst("running", "", t0)}, "running"},
		{"running-but-actually-stopped", "running", []ec2types.Instance{rcInst("stopped", "", t0)}, "stopped"},
		// paused (hibernated) shows as EC2 "stopped" — keep the richer label.
		{"paused-preserved-when-stopped", "paused", []ec2types.Instance{rcInst("stopped", "", t0)}, "paused"},
		{"paused-preserved-when-stopping", "paused", []ec2types.Instance{rcInst("stopping", "", t0)}, "paused"},
		{"running-to-stopping", "running", []ec2types.Instance{rcInst("stopping", "", t0)}, "stopped"},
		{"stopped-to-starting", "stopped", []ec2types.Instance{rcInst("pending", "", t0)}, "starting"},
		// Termination is authoritative regardless of stored (except failed).
		{"running-gone-terminated", "running", []ec2types.Instance{rcInst("terminated", "", t0)}, "killed"},
		{"stopped-gone-terminated", "stopped", []ec2types.Instance{rcInst("terminated", "", t0)}, "killed"},
		{"spot-reaped", "running", []ec2types.Instance{rcInst("terminated", "Server.SpotInstanceTermination", t0)}, "reaped"},
		// No instances at all (empty describe): only a "running" claim downgrades.
		{"running-no-instances", "running", nil, "killed"},
		{"stopped-no-instances", "stopped", nil, "stopped"},
		{"paused-no-instances", "paused", nil, "paused"},
		// failed short-circuits — never overridden by instance state.
		{"failed-preserved", "failed", []ec2types.Instance{rcInst("running", "", t0)}, "failed"},
		// Most-recent non-terminated instance wins over a lingering terminated one.
		{"replace-picks-newest-running", "running", []ec2types.Instance{
			rcInst("terminated", "", t0), rcInst("running", "", t1),
		}, "running"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reconcileStatusFromInstances(c.stored, c.instances)
			if got != c.want {
				t.Errorf("reconcileStatusFromInstances(%q, …) = %q; want %q", c.stored, got, c.want)
			}
		})
	}
}
