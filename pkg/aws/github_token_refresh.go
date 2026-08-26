// Package aws — github_token_refresh.go
//
// Forces an out-of-band re-mint of a sandbox's GitHub App installation token.
//
// Every sandbox with sourceAccess.github gets a per-sandbox refresher Lambda on
// a rate(45 minutes) EventBridge schedule (infra/modules/github-token/v1.0.0).
// That schedule keeps ticking while the instance is stopped or hibernated —
// only destroy removes it — so in the healthy case a resumed sandbox already
// has a token less than 45 minutes old.
//
// The failure this guards against is the unhealthy case: a refresher that has
// been erroring on every tick, or whose schedule was removed out from under the
// sandbox. Nothing surfaces that today — the errors land in CloudWatch and the
// operator discovers them later as a 401 from git. Invoking the refresher
// synchronously on resume both re-mints the token and turns a silent failure
// into an error the operator sees at the moment they wake the box.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// ErrNoGitHubRefresher reports that the sandbox has no github-token refresher
// schedule. The common cause is a profile without sourceAccess.github, so
// callers treat it as "skipped", not as a failure.
var ErrNoGitHubRefresher = errors.New("no github-token refresher schedule for sandbox")

// ScheduleGetter is the narrow EventBridge Scheduler read used here.
// Implemented by *scheduler.Client.
type ScheduleGetter interface {
	GetSchedule(ctx context.Context, params *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
}

// LambdaInvoker is the narrow Lambda invoke used here.
// Implemented by *lambda.Client.
type LambdaInvoker interface {
	Invoke(ctx context.Context, params *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// GitHubTokenScheduleName returns the per-sandbox refresher schedule name.
func GitHubTokenScheduleName(resourcePrefix, sandboxID string) string {
	return resourcePrefix + "-github-token-" + sandboxID
}

// GitHubTokenRefresherFunctionName returns the per-sandbox refresher Lambda name.
func GitHubTokenRefresherFunctionName(resourcePrefix, sandboxID string) string {
	return resourcePrefix + "-github-token-refresher-" + sandboxID
}

// ForceGitHubTokenRefresh re-mints the sandbox's GitHub installation token now,
// rather than waiting for the next scheduled tick.
//
// It reads the refresher's payload back off its own EventBridge schedule and
// forwards it to the Lambda verbatim. That payload carries the installation id,
// the per-sandbox KMS key ARN, the allowed repos and the compiled permissions
// map — all decided at create time. Reconstructing it here would duplicate
// create-time knowledge, which is precisely how this feature stayed broken for
// its entire lifetime once already (see pkg/github/token.go TokenRefreshEvent).
//
// Returns ErrNoGitHubRefresher when the sandbox has no such schedule.
func ForceGitHubTokenRefresh(ctx context.Context, sched ScheduleGetter, invoker LambdaInvoker, resourcePrefix, sandboxID string) error {
	scheduleName := GitHubTokenScheduleName(resourcePrefix, sandboxID)

	out, err := sched.GetSchedule(ctx, &scheduler.GetScheduleInput{
		Name:      awssdk.String(scheduleName),
		GroupName: awssdk.String("default"),
	})
	if err != nil {
		// Only a definitive not-found means "this sandbox has no GitHub access".
		// A throttle or a transient API error must stay a real error — reporting
		// it as absence would silently skip the refresh on exactly the runs where
		// something is already wrong.
		var notFound *schedulertypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("%w: %s", ErrNoGitHubRefresher, scheduleName)
		}
		return fmt.Errorf("read github-token schedule %s: %w", scheduleName, err)
	}

	if out == nil || out.Target == nil || awssdk.ToString(out.Target.Input) == "" {
		return fmt.Errorf("github-token schedule %s has no target input payload", scheduleName)
	}
	payload := awssdk.ToString(out.Target.Input)

	functionName := GitHubTokenRefresherFunctionName(resourcePrefix, sandboxID)
	invokeOut, err := invoker.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   awssdk.String(functionName),
		InvocationType: "RequestResponse",
		Payload:        []byte(payload),
	})
	if err != nil {
		return fmt.Errorf("invoke %s: %w", functionName, err)
	}
	if invokeOut != nil && awssdk.ToString(invokeOut.FunctionError) != "" {
		return fmt.Errorf("%s failed: %s", functionName, lambdaErrorDetail(invokeOut.Payload))
	}
	return nil
}

// lambdaErrorDetail extracts the errorMessage from a Lambda error payload,
// falling back to the raw body when it is not the expected shape. The message
// is the operator's only clue about why the refresher has been failing, so it
// is never swallowed.
func lambdaErrorDetail(payload []byte) string {
	if len(payload) == 0 {
		return "(no error payload)"
	}
	var body struct {
		ErrorMessage string `json:"errorMessage"`
		ErrorType    string `json:"errorType"`
	}
	if err := json.Unmarshal(payload, &body); err == nil && body.ErrorMessage != "" {
		if body.ErrorType != "" {
			return body.ErrorType + ": " + body.ErrorMessage
		}
		return body.ErrorMessage
	}
	return string(payload)
}
