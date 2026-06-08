// Package cmd — doctor_inbound_dlq.go
// Phase 99.1 Plan 04: km doctor visibility on poison messages stranded in the
// shared per-install FIFO dead-letter queues.
//
// checkInboundDLQDepth probes the two shared DLQs
// ({prefix}-github-inbound-dlq.fifo / {prefix}-slack-inbound-dlq.fifo) — created
// by the sqs-inbound-dlq Terraform module (Plan 03) and targeted by the
// per-sandbox inbound FIFO queues' RedrivePolicy (Plan 01). A non-zero depth
// means a poison envelope dead-lettered after maxReceiveCount=3 instead of
// head-of-line-blocking its FIFO group.
//
// States (RESEARCH Finding 6):
//   - CheckSkipped: nil SQS client, OR neither DLQ exists (dormant — inbound
//     integrations never configured / DLQs never provisioned).
//   - CheckOK:      both DLQs resolvable and empty.
//   - CheckWarn:    at least one DLQ holds >0 messages (count + remediation).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	sqssvc "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkInboundDLQDepth reports the depth of the shared GitHub/Slack inbound DLQs.
// Mirrors checkSlackInboundQueueExists (doctor_slack.go): resolve each DLQ URL via
// a ListQueues prefix match, then read ApproximateNumberOfMessages via QueueDepth.
func checkInboundDLQDepth(ctx context.Context, sqsClient kmaws.SQSClient, resourcePrefix string) CheckResult {
	name := "Inbound DLQ depth"
	if sqsClient == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "inbound DLQ deps not configured",
		}
	}
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	dlqs := []struct {
		label string
		qName string
	}{
		{"github", kmaws.GitHubInboundDLQName(resourcePrefix)},
		{"slack", kmaws.SlackInboundDLQName(resourcePrefix)},
	}

	resolved := 0
	var nonEmpty []string
	total := int64(0)

	for _, d := range dlqs {
		qURL, ok, err := resolveQueueURL(ctx, sqsClient, d.qName)
		if err != nil {
			// Treat resolution errors (other than not-exist, handled below) as
			// non-fatal — never fail km doctor on a DLQ probe.
			return CheckResult{
				Name:    name,
				Status:  CheckWarn,
				Message: fmt.Sprintf("could not resolve %s inbound DLQ %s: %v", d.label, d.qName, err),
			}
		}
		if !ok {
			// DLQ not provisioned — dormant for this integration.
			continue
		}
		depth, dErr := kmaws.QueueDepth(ctx, sqsClient, qURL)
		if dErr != nil {
			// QueueDoesNotExist between ListQueues and GetQueueAttributes ⇒ dormant.
			var notFound *sqstypes.QueueDoesNotExist
			if errors.As(dErr, &notFound) {
				continue
			}
			return CheckResult{
				Name:    name,
				Status:  CheckWarn,
				Message: fmt.Sprintf("could not read %s inbound DLQ depth: %v", d.label, dErr),
			}
		}
		resolved++
		if depth > 0 {
			nonEmpty = append(nonEmpty, fmt.Sprintf("%s-inbound-dlq=%d", d.label, depth))
			total += depth
		}
	}

	if resolved == 0 {
		// Neither DLQ exists — feature dormant, byte-identical to pre-99.1.
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "no shared inbound DLQs found (inbound integrations not provisioned)",
		}
	}

	if len(nonEmpty) > 0 {
		return CheckResult{
			Name:   name,
			Status: CheckWarn,
			Message: fmt.Sprintf(
				"%d poison message(s) in shared inbound DLQ(s): %s",
				total, strings.Join(nonEmpty, ", ")),
			Remediation: "Inspect with 'aws sqs receive-message --queue-url <dlq-url>'; once triaged, redrive or 'aws sqs purge-queue --queue-url <dlq-url>'. A poison message indicates an agent turn that failed 3x — check the source poller logs.",
		}
	}

	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("%d shared inbound DLQ(s) present, all empty", resolved),
	}
}

// resolveQueueURL resolves a queue URL from its name via a ListQueues prefix
// match (the SQSClient interface has no GetQueueUrl). Returns ok=false when no
// queue with that exact name exists — the caller treats this as dormant.
func resolveQueueURL(ctx context.Context, c kmaws.SQSClient, qName string) (string, bool, error) {
	out, err := c.ListQueues(ctx, &sqssvc.ListQueuesInput{
		QueueNamePrefix: awssdk.String(qName),
	})
	if err != nil {
		// A not-exist style error on ListQueues is treated as dormant.
		var notFound *sqstypes.QueueDoesNotExist
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, u := range out.QueueUrls {
		if strings.HasSuffix(u, "/"+qName) {
			return u, true, nil
		}
	}
	return "", false, nil
}
