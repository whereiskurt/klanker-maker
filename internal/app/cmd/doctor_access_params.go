package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkOrphanAccessParams reports /{prefix}/access/<id>/* parameters (the
// shared SSH key + desktop password, pkg/aws/access.go) whose sandbox has no
// row — a remote create that died after key generation, or a TTL expiry that
// ran before the ttl-handler's delete grant was deployed. WARN only; nothing
// here deletes. Silent SKIP when the namespace is empty so an install that
// never used the feature sees nothing.
func checkOrphanAccessParams(ctx context.Context, ssmRead SSMReadAPI, lister SandboxLister, resourcePrefix string) CheckResult {
	const name = "Shared access credentials"
	if ssmRead == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "SSM or sandbox lister not available"}
	}
	prefix := kmaws.AccessParamPrefix(resourcePrefix)
	ids := map[string]bool{}
	var next *string
	for {
		out, err := ssmRead.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      awssdk.String(prefix),
			Recursive: awssdk.Bool(true),
			NextToken: next,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list %s: %v", prefix, err)}
		}
		for _, p := range out.Parameters {
			rel := strings.TrimPrefix(awssdk.ToString(p.Name), prefix)
			if id, _, ok := strings.Cut(rel, "/"); ok && id != "" {
				ids[id] = true
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		next = out.NextToken
	}
	if len(ids) == 0 {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "none published"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	known := map[string]bool{}
	for _, r := range records {
		known[r.SandboxID] = true
	}
	var orphans []string
	for id := range ids {
		if !known[id] {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d sandbox(es) have shared credentials in SSM but no record: %s", len(orphans), strings.Join(orphans, ", ")),
			Remediation: fmt.Sprintf("aws ssm delete-parameters --names %s<id>/ssh-key %s<id>/desktop-cred", prefix, prefix),
			Details:     orphans,
		}
	}
	return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("%d sandbox(es) published, all known", len(ids))}
}
