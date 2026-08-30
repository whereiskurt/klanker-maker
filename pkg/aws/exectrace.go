package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// execTracePollInterval is the delay between GetCommandInvocation polls in
// SaveExecTraceOverSSM. A package variable rather than a hardcoded
// time.Sleep so tests can zero it out and finish instantly instead of
// paying real wall-clock time for every poll tick.
var execTracePollInterval = 1 * time.Second

// execTraceSaveCommand is the shell command run on the sandbox. It reads its
// own S3 bucket / sandbox id / exec-store directory from /etc/km/netpolicy.env
// via km-netpolicy's existing pick() fallback (see the km-netpolicy execs save
// env fix) — no environment variables need to travel with the SSM command.
const execTraceSaveCommand = "/opt/km/bin/km-netpolicy execs save"

// SSMCommandRunner is the narrow SSM interface SaveExecTraceOverSSM needs:
// SendCommand to launch the save, GetCommandInvocation to wait for it to
// finish. Implemented by *ssm.Client.
type SSMCommandRunner interface {
	SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

// SaveExecTraceOverSSM runs `km-netpolicy execs save` on instanceID via SSM
// RunShellScript and waits for it to finish, so the caller can be sure the
// upload has completed before proceeding to tear the instance's networking
// or the instance itself down.
//
// Why this exists rather than relying on km-execlog.service's own
// ExecStopPost=-/opt/km/bin/km-netpolicy execs save: that hook only fires
// during a graceful shutdown (`km stop`, verified live — two S3 objects, the
// larger one catching the shutdown's own execs). It cannot be relied on for
// `km destroy`: TerminateInstances gives a shorter grace window than
// StopInstances, and/or terraform destroy removes the instance's networking
// (security group / ENI / route) alongside it, so the shutdown-time upload
// has nowhere to send to — verified live: a box with a 1,672,598-byte local
// trace and confirmed-correct unit ordering still uploaded nothing after
// `km destroy`. This function sidesteps the shutdown window entirely by
// running the save while the instance is still fully alive and networked,
// before any teardown begins.
//
// Bounded and best-effort BY CONTRACT, not just by convention: callers MUST
// treat every returned error as non-fatal to whatever teardown they are
// about to perform — losing a diagnostic trace is far cheaper than leaking a
// running, billing instance because a log upload stalled. The wait for
// completion is governed entirely by ctx; pass a context with a short
// deadline (10-20s is plenty for a ~1.6MB upload from inside AWS).
func SaveExecTraceOverSSM(ctx context.Context, client SSMCommandRunner, instanceID string) error {
	if client == nil || instanceID == "" {
		return nil
	}

	sendOut, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {execTraceSaveCommand},
		},
		TimeoutSeconds: aws.Int32(30),
	})
	if err != nil {
		return fmt.Errorf("send exec-trace save command to instance %s: %w", instanceID, err)
	}
	commandID := aws.ToString(sendOut.Command.CommandId)

	for {
		inv, invErr := client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		})
		if invErr == nil {
			switch string(inv.Status) {
			case "Success":
				return nil
			case "Failed", "Cancelled", "TimedOut":
				return fmt.Errorf("exec-trace save command %s on %s finished %s: %s",
					commandID, instanceID, inv.Status, aws.ToString(inv.StandardErrorContent))
			}
			// Pending / InProgress / Delayed — keep waiting.
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("exec-trace save command %s on %s did not finish before the deadline: %w",
				commandID, instanceID, ctx.Err())
		case <-time.After(execTracePollInterval):
		}
	}
}
