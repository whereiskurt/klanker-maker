package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// initFailure is what the bootstrap records when /tmp/km-init.sh aborts:
// the failing initCommand, where it sat in the list, and how many later
// commands were never run. The sandbox still comes up (the failure is
// deliberately non-fatal — see the 7.5 block in pkg/compiler/userdata.go),
// which is exactly why it has to be visible from off the box: `km list` is
// green, SANDBOX_READY fired, and nothing else says a word.
type initFailure struct {
	ExitCode string `json:"exit_code"`
	Step     string `json:"step"`
	Total    string `json:"total"`
	Skipped  string `json:"skipped"`
	Command  string `json:"command"`
	At       time.Time
}

// String renders the one-line form used by km status and km doctor.
func (f initFailure) String() string {
	cmd := f.Command
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}
	return fmt.Sprintf("FAILED (exit %s) at step %s/%s: %s — %s later initCommand(s) not run",
		f.ExitCode, f.Step, f.Total, cmd, f.Skipped)
}

// initFailedFilterPattern is CloudWatch Logs JSON metric-filter syntax; the
// text form ("event_type":"init_failed") is rejected as InvalidParameterException.
const initFailedFilterPattern = `{ $.event_type = "init_failed" }`

// lookupInitFailure reads a sandbox's audit stream for an init_failed event.
// No StartTime: userdata runs exactly once per instance, so the event is as
// old as the sandbox, and a box that has been up for a week is the one an
// operator most needs told. (nil, nil) means no failure recorded; an error
// means the stream could not be read (a pre-fix sandbox has no such event and
// reads clean, which is the honest answer — it never reported).
func lookupInitFailure(ctx context.Context, cw CWLogsFilterAPI, logGroup string) (*initFailure, error) {
	out, err := cw.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  awssdk.String(logGroup),
		FilterPattern: awssdk.String(initFailedFilterPattern),
		Limit:         awssdk.Int32(1),
	})
	if err != nil {
		return nil, err
	}
	if out == nil || len(out.Events) == 0 || out.Events[0].Message == nil {
		return nil, nil
	}
	var ev struct {
		Detail initFailure `json:"detail"`
	}
	if err := json.Unmarshal([]byte(*out.Events[0].Message), &ev); err != nil {
		return nil, fmt.Errorf("parse init_failed event: %w", err)
	}
	f := ev.Detail
	if out.Events[0].Timestamp != nil {
		f.At = time.UnixMilli(*out.Events[0].Timestamp)
	}
	return &f, nil
}

// checkInitFailed is the km doctor check: WARN naming every running sandbox
// whose profile init aborted. WARN not ERROR — the box is up and usable; what
// is wrong is that part of its profile never applied. Skipped with zero AWS
// calls when either dependency is nil, like the presence check it mirrors.
func checkInitFailed(ctx context.Context, cw CWLogsFilterAPI, lister runningSandboxLister, logGroupPrefix string) CheckResult {
	const name = "Sandbox profile init"
	if cw == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "no CloudWatch client or sandbox lister"}
	}
	ids, err := lister.ListRunningSandboxIDs(ctx)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list running sandboxes: %v", err)}
	}
	if len(ids) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no running sandboxes to check"}
	}
	var failed []string
	for _, id := range ids {
		f, lookErr := lookupInitFailure(ctx, cw, logGroupPrefix+id+"/")
		if lookErr != nil || f == nil {
			continue // unreadable stream or no event: nothing to report
		}
		failed = append(failed, fmt.Sprintf("%s: %s", id, f.String()))
	}
	if len(failed) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("no init failures recorded on %d running sandbox(es)", len(ids))}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d sandbox(es) booted with an aborted profile init:\n    %s", len(failed), strings.Join(failed, "\n    ")),
		Remediation: "The sandbox is up but every initCommand after the failing one was skipped. Fix the command in the profile (or the network/registry it needs) and 'km destroy && km create'; the full [km-init] FAILED line is in /var/log/cloud-init-output.log and /var/lib/km/init-failed on the box.",
	}
}

// statusInitFailureLookup is km status's seam: it resolves its own CloudWatch
// client so printSandboxStatus's signature (and its callers) stay put. Tests
// swap it. nil ⇒ nothing printed, including on any AWS error — status must
// never fail over an observability lookup.
var statusInitFailureLookup = func(ctx context.Context, sandboxID, resourcePrefix string) *initFailure {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil
	}
	f, err := lookupInitFailure(ctx, cloudwatchlogs.NewFromConfig(awsCfg), "/"+resourcePrefix+"/sandboxes/"+sandboxID+"/")
	if err != nil {
		return nil
	}
	return f
}
