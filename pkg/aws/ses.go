// Package aws — ses.go
// ProvisionSandboxEmail, SendLifecycleNotification, and CleanupSandboxEmail wrap
// the SES v2 SDK for per-sandbox email identity lifecycle and operator notifications.
package aws

import (
	"context"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
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

// ProvisionSandboxEmail creates an SES email identity for {sandboxID}@{domain}
// and returns the full email address.
//
// This is called during km create to register the sandbox's email address with SES.
func ProvisionSandboxEmail(ctx context.Context, client SESV2API, sandboxID, domain string) (string, error) {
	addr := sandboxEmailAddress(sandboxID, domain)
	_, err := client.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{
		EmailIdentity: awssdk.String(addr),
	})
	if err != nil {
		return "", fmt.Errorf("provision sandbox email %s: %w", addr, err)
	}
	return addr, nil
}

// SendLifecycleNotification sends an operator notification email for a sandbox lifecycle event.
//
// Events: "destroyed", "idle-timeout", "spot-interruption", "error".
// From address is always notifications@{domain}.
// Subject format: "km sandbox {event}: {sandboxID}".
func SendLifecycleNotification(ctx context.Context, client SESV2API, operatorEmail, sandboxID, event, domain string) error {
	from := fmt.Sprintf("notifications@%s", domain)
	subject := fmt.Sprintf("km sandbox %s: %s", event, sandboxID)
	body := fmt.Sprintf("Sandbox lifecycle event: %s\nSandbox ID: %s\nDomain: %s\n", event, sandboxID, domain)

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
	body := fmt.Sprintf("Sandbox %s requests approval for action: %s\n\n%s\n\nReply with APPROVED to authorize or DENIED to reject.\n", sandboxID, action, description)

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
