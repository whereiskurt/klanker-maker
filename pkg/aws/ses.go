// Package aws — ses.go
// ProvisionSandboxEmail, SendLifecycleNotification, and CleanupSandboxEmail wrap
// the SES v2 SDK for per-sandbox email identity lifecycle and operator notifications.
package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/whereiskurt/klankrmkr/pkg/version"
)

// SESV2API is the minimal SES v2 interface required by ProvisionSandboxEmail,
// SendLifecycleNotification, and CleanupSandboxEmail.
// Implemented by *sesv2.Client.
type SESV2API interface {
	CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error)
	DeleteEmailIdentity(ctx context.Context, input *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error)
	SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// sandboxEmailAddress returns the email address for a given sandboxID and domain.
func sandboxEmailAddress(sandboxID, domain string) string {
	return fmt.Sprintf("%s@%s", sandboxID, domain)
}

// ProvisionSandboxEmail returns the full email address for {sandboxID}@{domain}.
//
// The verified domain identity (sandboxes.{domain}) covers all addresses at that
// domain for sending — no per-address CreateEmailIdentity call is needed.
func ProvisionSandboxEmail(ctx context.Context, client SESV2API, sandboxID, domain string) (string, error) {
	return sandboxEmailAddress(sandboxID, domain), nil
}

// SendLifecycleNotification sends an operator notification email for a sandbox lifecycle event.
//
// Events: "destroyed", "idle-timeout", "spot-interruption", "error".
// From address is always notifications@{domain}.
// Subject format: "km sandbox {event}: {sandboxID}".
func SendLifecycleNotification(ctx context.Context, client SESV2API, operatorEmail, sandboxID, event, domain string) error {
	from := fmt.Sprintf("notifications@%s", domain)
	subject := fmt.Sprintf("km sandbox %s: %s", event, sandboxID)
	body := fmt.Sprintf("Sandbox lifecycle event: %s\nSandbox ID: %s\nDomain: %s\n\n— %s\n", event, sandboxID, domain, version.Header())

	_, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{operatorEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(subject),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(body),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send lifecycle notification for sandbox %s (event: %s): %w", sandboxID, event, err)
	}
	return nil
}

// NotificationDetail holds rich context for a detailed lifecycle notification email.
type NotificationDetail struct {
	SandboxID   string
	Event       string // e.g. "ttl-expired", "idle-timeout", "destroyed"
	ProfileName string
	Substrate   string
	Region      string
	CreatedAt   time.Time
	TTLExpiry   *time.Time
	IdleTimeout string

	// Artifact capture results
	ArtifactsUploaded int
	ArtifactsSkipped  int
	ArtifactPaths     []string // configured paths from profile
}

// SendDetailedNotification sends a rich operator notification with sandbox status
// details and artifact capture results — similar to `km status` output.
func SendDetailedNotification(ctx context.Context, client SESV2API, operatorEmail, domain string, detail NotificationDetail) error {
	from := fmt.Sprintf("notifications@%s", domain)
	subject := fmt.Sprintf("km sandbox %s: %s", detail.Event, detail.SandboxID)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Sandbox Lifecycle Event: %s\n", detail.Event))
	b.WriteString(fmt.Sprintf("Time: %s\n", time.Now().UTC().Format("2006-01-02 3:04:05 PM UTC")))
	b.WriteString("\n")
	b.WriteString("─── Sandbox Details ───────────────────────────\n")
	b.WriteString(fmt.Sprintf("  Sandbox ID:  %s\n", detail.SandboxID))
	if detail.ProfileName != "" {
		b.WriteString(fmt.Sprintf("  Profile:     %s\n", detail.ProfileName))
	}
	if detail.Substrate != "" {
		b.WriteString(fmt.Sprintf("  Substrate:   %s\n", detail.Substrate))
	}
	if detail.Region != "" {
		b.WriteString(fmt.Sprintf("  Region:      %s\n", detail.Region))
	}
	if !detail.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  Created At:  %s\n", detail.CreatedAt.Format("2006-01-02 3:04:05 PM UTC")))
	}
	if detail.TTLExpiry != nil {
		b.WriteString(fmt.Sprintf("  TTL Expiry:  %s\n", detail.TTLExpiry.Format("2006-01-02 3:04:05 PM UTC")))
	}
	if detail.IdleTimeout != "" {
		b.WriteString(fmt.Sprintf("  Idle Timeout: %s\n", detail.IdleTimeout))
	}
	if !detail.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  Lifetime:    %s\n", time.Since(detail.CreatedAt).Round(time.Minute)))
	}

	b.WriteString("\n")
	b.WriteString("─── Artifact Capture ─────────────────────────\n")
	if len(detail.ArtifactPaths) == 0 {
		b.WriteString("  No artifact paths configured.\n")
	} else {
		b.WriteString(fmt.Sprintf("  Configured paths: %s\n", strings.Join(detail.ArtifactPaths, ", ")))
		b.WriteString(fmt.Sprintf("  Uploaded: %d file(s)\n", detail.ArtifactsUploaded))
		if detail.ArtifactsSkipped > 0 {
			b.WriteString(fmt.Sprintf("  Skipped:  %d file(s) (oversized or inaccessible)\n", detail.ArtifactsSkipped))
		}
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Domain: %s\n", domain))
	b.WriteString(fmt.Sprintf("\n— %s\n", version.Header()))

	_, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{operatorEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(subject),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(b.String()),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send detailed notification for sandbox %s (event: %s): %w", detail.SandboxID, detail.Event, err)
	}
	return nil
}

// SendApprovalRequest sends a structured approval request email to the operator.
//
// The From address is set to {sandboxID}@sandboxes.{domain} so that when the
// operator replies, the reply routes back to the sandbox's own SES mailbox via
// the existing SES receipt rule.
//
// Subject format: "[KM-APPROVAL-REQUEST] {sandboxID} {action}"
// Body: structured text with action details and APPROVED/DENIED reply instructions.
func SendApprovalRequest(ctx context.Context, client SESV2API, sandboxID, domain, operatorEmail, action, description string) error {
	from := sandboxEmailAddress(sandboxID, domain)
	subject := fmt.Sprintf("[KM-APPROVAL-REQUEST] %s %s", sandboxID, action)
	body := fmt.Sprintf("Sandbox %s requests approval for action: %s\n\n%s\n\nReply with APPROVED to authorize or DENIED to reject.\n\n— %s\n", sandboxID, action, description, version.Header())

	_, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{operatorEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(subject),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(body),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send approval request for sandbox %s (action: %s): %w", sandboxID, action, err)
	}
	return nil
}

// SendLimitNotification sends an operator notification when sandbox creation is
// rejected because the active sandbox count has reached the configured maximum.
//
// From address: notifications@{domain}
// Subject: "km sandbox limit-reached: {sandboxID}"
// Body: includes attempted sandbox ID, current/max counts, and remediation hint.
func SendLimitNotification(ctx context.Context, client SESV2API, operatorEmail, sandboxID, domain string, currentCount, maxCount int) error {
	from := fmt.Sprintf("notifications@%s", domain)
	subject := fmt.Sprintf("km sandbox limit-reached: %s", sandboxID)
	body := fmt.Sprintf(
		"Sandbox creation rejected: limit reached.\nAttempted sandbox: %s\nActive sandboxes: %d/%d\nTo increase, set max_sandboxes in km-config.yaml.\n\n— %s\n",
		sandboxID, currentCount, maxCount, version.Header(),
	)

	_, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{operatorEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(subject),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(body),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send limit notification for sandbox %s (%d/%d): %w", sandboxID, currentCount, maxCount, err)
	}
	return nil
}

// CleanupSandboxEmail deletes the SES email identity for {sandboxID}@{domain}.
//
// This is called during km destroy. The function is idempotent: if the identity
// does not exist (NotFoundException), nil is returned so that retried destroy
// commands succeed without error.
func CleanupSandboxEmail(ctx context.Context, client SESV2API, sandboxID, domain string) error {
	addr := sandboxEmailAddress(sandboxID, domain)
	_, err := client.DeleteEmailIdentity(ctx, &sesv2.DeleteEmailIdentityInput{
		EmailIdentity: awssdk.String(addr),
	})
	if err != nil {
		var notFound *sesv2types.NotFoundException
		if errors.As(err, &notFound) {
			// Identity does not exist — idempotent, treat as success.
			return nil
		}
		return fmt.Errorf("cleanup sandbox email %s: %w", addr, err)
	}
	return nil
}
