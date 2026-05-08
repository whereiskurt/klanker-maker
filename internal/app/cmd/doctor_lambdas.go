// Package cmd — orphaned-Lambda detection for `km doctor`.
//
// Two km-managed Lambdas are provisioned per sandbox:
//
//   - {prefix}-budget-enforcer-{sandbox-id}
//   - {prefix}-github-token-refresher-{sandbox-id}
//
// km destroy cleans them up via cleanupBudgetEnforcerResources +
// terragrunt destroy of the github-token module — but if a destroy is
// interrupted (Ctrl-C, SCP block on IAM access, region failover) or the
// terragrunt state is broken, those Lambdas linger. The function itself
// has near-zero idle cost, but leaving them around (a) clutters the
// console, (b) keeps their EventBridge invoke targets alive, and (c)
// becomes load-bearing on real cleanup if billing alarms ever start
// firing on stale invocations.
//
// checkStaleLambdas mirrors checkStaleSchedules: enumerate everything
// with the {resource_prefix}- name prefix, match against a known set of
// per-sandbox component prefixes, extract the sandbox ID, and flag (or
// delete, with --dry-run=false --delete-lambdas) those whose sandbox
// is gone from DynamoDB.
package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// LambdaCleanupAPI is the narrow interface used by checkStaleLambdas:
// ListFunctions for enumeration, DeleteFunction for cleanup. The real
// *lambda.Client satisfies this directly.
type LambdaCleanupAPI interface {
	ListFunctions(ctx context.Context, params *lambda.ListFunctionsInput, optFns ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	DeleteFunction(ctx context.Context, params *lambda.DeleteFunctionInput, optFns ...func(*lambda.Options)) (*lambda.DeleteFunctionOutput, error)
}

// perSandboxLambdaComponents lists the component substrings that identify
// per-sandbox Lambdas. Anything not matching one of these is treated as a
// platform-level Lambda and left alone — this is intentionally an
// allowlist rather than a denylist so a future platform Lambda doesn't
// accidentally get classified as orphan and deleted on the first doctor
// run after deploy.
//
// Format of per-sandbox Lambda names: {resource_prefix}-{component}-{sandbox-id}
// — see infra/modules/budget-enforcer/v1.0.0/main.tf and
// infra/modules/github-token/v1.0.0/main.tf.
var perSandboxLambdaComponents = []string{
	"budget-enforcer",
	"github-token-refresher",
}

// checkStaleLambdas lists all Lambda functions whose name starts with
// "{resource_prefix}-{component}-" for a known per-sandbox component, and
// flags any whose embedded sandbox ID has no matching DynamoDB record.
// Deletion is gated on dryRun==false AND deleteLambdas==true.
func checkStaleLambdas(
	ctx context.Context,
	client LambdaCleanupAPI,
	lister SandboxLister,
	dryRun bool,
	deleteLambdas bool,
	resourcePrefix string,
) CheckResult {
	name := "Stale Lambdas"
	if client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "Lambda client not available"}
	}
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	// Enumerate every Lambda; filter to per-sandbox component prefixes below.
	// ListFunctions has no name-filter parameter — we paginate the full set.
	var perSandbox []perSandboxLambda
	var nextMarker *string
	for {
		out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{
			Marker: nextMarker,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list Lambdas: %v", err)}
		}
		for _, fn := range out.Functions {
			fnName := awssdk.ToString(fn.FunctionName)
			if l, ok := classifyLambda(fnName, resourcePrefix); ok {
				perSandbox = append(perSandbox, l)
			}
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}

	if len(perSandbox) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no per-sandbox km Lambdas found"}
	}

	// Build the live-sandbox set.
	activeSandboxes := make(map[string]bool)
	if lister != nil {
		records, err := lister.ListSandboxes(ctx, false)
		if err == nil {
			for _, r := range records {
				activeSandboxes[r.SandboxID] = true
			}
		}
	}

	var stale []perSandboxLambda
	for _, l := range perSandbox {
		if !activeSandboxes[l.sandboxID] {
			stale = append(stale, l)
		}
	}
	if len(stale) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d per-sandbox km Lambdas, all active", len(perSandbox)),
		}
	}

	// Report-only: dryRun OR opt-in missing. Two-flavored remediation, same
	// shape as --delete-ebs / --delete-sqs / --delete-s3.
	if dryRun || !deleteLambdas {
		var sb strings.Builder
		fmt.Fprintf(&sb, "found %d stale per-sandbox Lambda(s) (no DynamoDB record):", len(stale))
		for _, l := range stale {
			fmt.Fprintf(&sb, "\n  %s (component=%s, sandbox=%s)", l.functionName, l.component, l.sandboxID)
		}
		remediation := "Re-run with --dry-run=false --delete-lambdas to delete the orphan Lambda functions"
		if !dryRun && !deleteLambdas {
			remediation = "Add --delete-lambdas to also delete the orphan Lambda functions"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     sb.String(),
			Remediation: remediation,
		}
	}

	// Destructive path. Best-effort per Lambda; failures don't abort the loop.
	deleted, failed := 0, 0
	failures := make(map[string]error)
	for _, l := range stale {
		_, err := client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: awssdk.String(l.functionName),
		})
		if err != nil {
			failed++
			failures[l.functionName] = err
			continue
		}
		deleted++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "found %d stale per-sandbox Lambda(s); %d deleted, %d failed:", len(stale), deleted, failed)
	for _, l := range stale {
		marker := " [deleted]"
		if e, present := failures[l.functionName]; present {
			marker = fmt.Sprintf(" [delete failed: %v]", e)
		}
		fmt.Fprintf(&sb, "\n  %s%s", l.functionName, marker)
	}
	remediation := ""
	if failed > 0 {
		remediation = "Re-run after resolving the listed delete failures (typically transient throttling or IAM)."
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: remediation,
	}
}

// perSandboxLambda is the parsed representation of a per-sandbox Lambda
// function name, used by checkStaleLambdas to track sandbox-id linkage.
type perSandboxLambda struct {
	functionName string // full Lambda function name as returned by ListFunctions
	component    string // e.g. "budget-enforcer", "github-token-refresher"
	sandboxID    string // extracted suffix after the component segment
}

// classifyLambda reports whether fnName matches one of the known
// per-sandbox component patterns. When it does, returns the parsed
// (component, sandboxID) pair; otherwise returns ok=false (platform
// Lambda — leave alone).
func classifyLambda(fnName, resourcePrefix string) (perSandboxLambda, bool) {
	prefix := resourcePrefix + "-"
	if !strings.HasPrefix(fnName, prefix) {
		return perSandboxLambda{}, false
	}
	rest := strings.TrimPrefix(fnName, prefix)
	for _, comp := range perSandboxLambdaComponents {
		compPrefix := comp + "-"
		if strings.HasPrefix(rest, compPrefix) {
			sid := strings.TrimPrefix(rest, compPrefix)
			if sid == "" {
				return perSandboxLambda{}, false
			}
			return perSandboxLambda{
				functionName: fnName,
				component:    comp,
				sandboxID:    sid,
			}, true
		}
	}
	return perSandboxLambda{}, false
}
