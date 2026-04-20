// Package main — operator email handler Lambda
// Processes SES-delivered emails to operator@sandboxes.{domain}, dispatches
// commands based on the email Subject line or AI interpretation:
//
//   - YAML attachment + "create" subject → fast-path create sandbox
//   - "status" subject + sandbox ID → reply with sandbox status
//   - Free-form email + BedrockClient set → AI interpretation via Haiku
//   - Unrecognized (no Bedrock) → reply with help text
//
// All commands require KM-AUTH safe phrase validation.
//
// Flow:
//  1. SES delivers email to S3 bucket under mail/create/ prefix
//  2. S3 notification triggers this Lambda
//  3. Lambda fetches raw MIME email from S3
//  4. Parses MIME to extract sender, subject, body text, and attachments
//  5. Validates KM-AUTH safe phrase against SSM parameter
//  6. Dispatches to the appropriate command handler:
//     a. Existing conversation thread → handleConversationReply
//     b. YAML attachment + "create" subject → handleCreate fast-path
//     c. "status" subject → handleStatus
//     d. BedrockClient set → handleAIInterpretation
//     e. Else → sendHelp
//  7. Sends reply email with results
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/version"
	"gopkg.in/yaml.v3"
)

// ---- S3 event record types ----

// S3EventRecord represents the SES → S3 notification event payload.
type S3EventRecord struct {
	Records []S3Record `json:"Records"`
}

// S3Record holds a single record from the S3 event notification.
type S3Record struct {
	S3 S3Detail `json:"s3"`
}

// S3Detail holds the bucket and object information for a single event.
type S3Detail struct {
	Bucket S3Bucket `json:"bucket"`
	Object S3Object `json:"object"`
}

// S3Bucket holds the name of the S3 bucket.
type S3Bucket struct {
	Name string `json:"name"`
}

// S3Object holds the key of the S3 object.
type S3Object struct {
	Key string `json:"key"`
}

// ---- dependency interfaces ----

// OperatorS3API is the narrow S3 interface for fetching and storing artifacts.
type OperatorS3API interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// SSMClientAPI is the narrow SSM interface for reading safe phrase parameters.
type SSMClientAPI interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// SESEmailAPI is the narrow SES interface needed by this handler (send only).
type SESEmailAPI interface {
	SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// ---- handler ----

// OperatorEmailHandler processes SES-delivered emails to operator@sandboxes.{domain}.
type OperatorEmailHandler struct {
	S3Client          OperatorS3API
	DynamoClient      awspkg.SandboxMetadataAPI
	SandboxTableName  string
	SSMClient         SSMClientAPI
	EventBridgeClient awspkg.EventBridgeAPI
	SESClient         SESEmailAPI
	ArtifactBucket    string
	StateBucket       string
	Domain            string
	SafePhraseSSMKey  string
	// BedrockClient enables AI interpretation path. If nil, falls back to keyword dispatch.
	BedrockClient  BedrockRuntimeAPI
	BedrockModelID string
	// VerboseErrors when true sends rejection replies for missing/invalid KM-AUTH.
	// Default false: silently drop unauthenticated emails to prevent SES quota abuse.
	VerboseErrors bool

	// replyCC holds CC addresses extracted from the current inbound email.
	// Set at the start of Handle(), used by sendReply to preserve CC on responses.
	// Per-request field — safe because Lambda processes one event at a time.
	replyCC []string
}

// sandboxIDPattern matches sandbox IDs: {prefix}-{8hex} (e.g. sb-abc123de, claude-abc123de).
var sandboxIDPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]{0,11}-[0-9a-f]{8})\b`)

// Handle processes a single S3 event record containing an SES-delivered email.
func (h *OperatorEmailHandler) Handle(ctx context.Context, event S3EventRecord) error {
	if len(event.Records) == 0 {
		return fmt.Errorf("operator-email-handler: no records in event")
	}
	rec := event.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key

	// Step 1: Fetch raw MIME email from S3
	out, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return fmt.Errorf("fetch email from s3 (bucket=%s, key=%s): %w", bucket, key, err)
	}
	defer out.Body.Close()

	rawEmail, err := io.ReadAll(out.Body)
	if err != nil {
		return fmt.Errorf("read email body: %w", err)
	}

	// Step 2: Parse MIME email
	msg, err := mail.ReadMessage(bytes.NewReader(rawEmail))
	if err != nil {
		return fmt.Errorf("parse MIME message: %w", err)
	}

	// Step 3: Extract sender, subject, and CC
	senderFrom := msg.Header.Get("From")
	senderEmail := extractEmail(senderFrom)
	subject := msg.Header.Get("Subject")

	// Preserve CC addresses from the inbound message so replies include them.
	h.replyCC = nil
	if ccHeader := msg.Header.Get("Cc"); ccHeader != "" {
		for _, addr := range strings.Split(ccHeader, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" && addr != senderEmail {
				h.replyCC = append(h.replyCC, extractEmail(addr))
			}
		}
	}

	// Step 4: Extract body text and YAML profile
	bodyText, yamlBytes, err := extractBodyAndYAML(msg)
	if err != nil {
		return fmt.Errorf("extract body and YAML: %w", err)
	}

	// Step 5: Extract and validate KM-AUTH phrase.
	// When VerboseErrors is false (default), silently drop unauthenticated emails
	// without replying. This prevents attackers from discovering the operator address
	// and flooding it to generate reply traffic that consumes SES quota.
	phrase := extractKMAuth(bodyText)
	if phrase == "" {
		phrase = extractKMAuth(string(rawEmail))
	}
	if phrase == "" {
		if h.VerboseErrors {
			return h.sendReply(ctx, senderEmail, "Command rejected", "Missing KM-AUTH phrase. Include KM-AUTH: <your-phrase> in the email body.\n")
		}
		fmt.Fprintf(os.Stderr, "[operator-email] silently dropping email from %s (subject: %s): missing KM-AUTH phrase\n", senderEmail, subject)
		return nil
	}

	paramOut, err := h.SSMClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(h.SafePhraseSSMKey),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("fetch safe phrase from SSM (%s): %w", h.SafePhraseSSMKey, err)
	}
	if subtle.ConstantTimeCompare([]byte(phrase), []byte(awssdk.ToString(paramOut.Parameter.Value))) != 1 {
		if h.VerboseErrors {
			return h.sendReply(ctx, senderEmail, "Command rejected", "Invalid KM-AUTH phrase.\n")
		}
		fmt.Fprintf(os.Stderr, "[operator-email] silently dropping email from %s (subject: %s): invalid KM-AUTH phrase\n", senderEmail, subject)
		return nil
	}

	// Step 6: Dispatch
	subjectLower := strings.ToLower(subject)

	// Check for an existing conversation thread.
	// Try multiple thread IDs: the reply's In-Reply-To may point to the confirmation
	// email (not the original), so we try all candidate IDs from the MIME headers.
	candidateIDs := extractAllThreadIDs(msg)
	fmt.Fprintf(os.Stderr, "[operator-email] candidates=%v sender=%s subject=%q\n", candidateIDs, senderEmail, subject)
	var threadID string
	for _, cid := range candidateIDs {
		conv, loadErr := loadConversation(ctx, h.S3Client, h.ArtifactBucket, cid)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "[operator-email] conversation load %q: %v\n", cid, loadErr)
			continue
		}
		if conv != nil {
			fmt.Fprintf(os.Stderr, "[operator-email] conversation found: state=%s threadID=%s via=%s\n", conv.State, conv.ThreadID, cid)
			if conv.State == "awaiting_confirmation" {
				return h.handleConversationReply(ctx, senderEmail, bodyText, conv)
			}
		}
	}
	// Use primary thread ID for new conversations.
	if len(candidateIDs) > 0 {
		threadID = candidateIDs[0]
	} else {
		threadID = extractThreadID(msg)
	}

	// YAML attachment + "create" subject → fast-path (no Haiku).
	// Only trigger if there's actually a YAML attachment AND the subject is not a reply.
	if strings.Contains(subjectLower, "create") && len(yamlBytes) > 0 && !strings.HasPrefix(subjectLower, "re:") {
		return h.handleCreate(ctx, senderEmail, yamlBytes)
	}

	// "status" subject → fast-path (no Haiku).
	if strings.Contains(subjectLower, "status") {
		return h.handleStatus(ctx, senderEmail, subject)
	}

	// Free-form email with BedrockClient → AI interpretation path.
	if h.BedrockClient != nil {
		return h.handleAIInterpretation(ctx, senderEmail, bodyText, threadID)
	}

	// No Bedrock configured → help text.
	return h.sendHelp(ctx, senderEmail)
}

// handleCreate processes a sandbox creation request.
func (h *OperatorEmailHandler) handleCreate(ctx context.Context, senderEmail string, yamlBytes []byte) error {
	if len(yamlBytes) == 0 {
		return h.sendReply(ctx, senderEmail, "Create failed",
			"No YAML profile found. Attach a .yaml file or include the profile in the email body.\n")
	}

	parsedProfile, err := profile.Parse(yamlBytes)
	if err != nil {
		return h.sendReply(ctx, senderEmail, "Create failed",
			fmt.Sprintf("Profile validation failed: %v\n", err))
	}

	sandboxID := compiler.GenerateSandboxID(parsedProfile.Metadata.Prefix)

	artifactPrefix := fmt.Sprintf("remote-create/%s", sandboxID)
	profileKey := fmt.Sprintf("%s/.km-profile.yaml", artifactPrefix)

	if _, err := h.S3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(h.ArtifactBucket),
		Key:         awssdk.String(profileKey),
		Body:        bytes.NewReader(yamlBytes),
		ContentType: awssdk.String("text/yaml"),
	}); err != nil {
		return fmt.Errorf("upload profile to S3 (%s): %w", profileKey, err)
	}

	if err := awspkg.PutSandboxCreateEvent(ctx, h.EventBridgeClient, awspkg.SandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: h.ArtifactBucket,
		ArtifactPrefix: artifactPrefix,
		OperatorEmail:  senderEmail,
		OnDemand:       false,
	}); err != nil {
		return fmt.Errorf("publish SandboxCreate event: %w", err)
	}

	body := fmt.Sprintf(
		"Sandbox creation request received.\n\n"+
			"Sandbox ID:  %s\n"+
			"Profile:     uploaded to S3\n"+
			"Status:      provisioning via EventBridge\n\n"+
			"You will receive another notification when provisioning completes.\n",
		sandboxID,
	)
	return h.sendReply(ctx, senderEmail, fmt.Sprintf("Sandbox create: %s", sandboxID), body)
}

// handleStatus looks up sandbox metadata and replies with status details.
func (h *OperatorEmailHandler) handleStatus(ctx context.Context, senderEmail, subject string) error {
	sandboxID := extractSandboxID(subject)
	if sandboxID == "" {
		return h.sendReply(ctx, senderEmail, "Status failed",
			"No sandbox ID found in subject. Use: Subject: status <sandbox-id>\n")
	}


	// Read metadata from DynamoDB.
	meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID)
	if err != nil {
		return h.sendReply(ctx, senderEmail, fmt.Sprintf("Status: %s", sandboxID),
			fmt.Sprintf("Sandbox %s not found or metadata unavailable.\n", sandboxID))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "─── Sandbox Status ───────────────────────────\n")
	fmt.Fprintf(&b, "  Sandbox ID:  %s\n", meta.SandboxID)
	if meta.ProfileName != "" {
		fmt.Fprintf(&b, "  Profile:     %s\n", meta.ProfileName)
	}
	if meta.Substrate != "" {
		fmt.Fprintf(&b, "  Substrate:   %s\n", meta.Substrate)
	}
	if meta.Region != "" {
		fmt.Fprintf(&b, "  Region:      %s\n", meta.Region)
	}
	if !meta.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "  Created At:  %s\n", meta.CreatedAt.Format("2006-01-02 3:04:05 PM UTC"))
		fmt.Fprintf(&b, "  Lifetime:    %s\n", time.Since(meta.CreatedAt).Round(time.Minute))
	}
	if meta.TTLExpiry != nil {
		fmt.Fprintf(&b, "  TTL Expiry:  %s\n", meta.TTLExpiry.Format("2006-01-02 3:04:05 PM UTC"))
		if meta.TTLExpiry.After(time.Now()) {
			fmt.Fprintf(&b, "  TTL Left:    %s\n", time.Until(*meta.TTLExpiry).Round(time.Minute))
		} else {
			fmt.Fprintf(&b, "  TTL Left:    expired\n")
		}
	}
	if meta.IdleTimeout != "" {
		fmt.Fprintf(&b, "  Idle Timeout: %s\n", meta.IdleTimeout)
	}

	return h.sendReply(ctx, senderEmail, fmt.Sprintf("Status: %s", sandboxID), b.String())
}

// sendHelp replies with available commands.
func (h *OperatorEmailHandler) sendHelp(ctx context.Context, senderEmail string) error {
	body := "Unrecognized command. Available commands (use as email Subject):\n\n" +
		"  create    — Attach a YAML profile to create a new sandbox\n" +
		"  status <sandbox-id>  — Get sandbox status (e.g. \"status sb-abc123de\" or \"status claude-abc123de\")\n\n" +
		"Or send a free-form description and I'll interpret it.\n\n" +
		"All commands require KM-AUTH: <phrase> in the email body.\n"
	return h.sendReply(ctx, senderEmail, "Operator Help", body)
}

// handleAIInterpretation calls Haiku to interpret a free-form email and either:
//   - sends a clarifying question (confidence < 0.7)
//   - executes info commands immediately (list, status)
//   - sends a confirmation template for action commands
func (h *OperatorEmailHandler) handleAIInterpretation(ctx context.Context, senderEmail, bodyText, threadID string) error {
	profiles := profile.ListBuiltins()

	// List running sandboxes for context. If DynamoClient is nil, skip gracefully.
	var sandboxIDs []string
	if h.DynamoClient != nil {
		records, err := awspkg.ListAllSandboxesByDynamo(ctx, h.DynamoClient, h.SandboxTableName)
		if err == nil {
			for _, r := range records {
				sandboxIDs = append(sandboxIDs, r.SandboxID)
			}
		}
	}

	systemPrompt := buildSystemPrompt(profiles, sandboxIDs)
	cmd, err := callHaiku(ctx, h.BedrockClient, h.BedrockModelID, systemPrompt, bodyText)
	if err != nil {
		return h.sendReply(ctx, senderEmail, "AI interpretation error",
			fmt.Sprintf("Failed to interpret your request: %v\nPlease try rephrasing or use a specific subject line.\n", err))
	}

	if cmd.Confidence < 0.7 {
		// Save conversation state as "new" and ask for clarification.
		conv := &ConversationState{
			ThreadID: threadID,
			Sender:   senderEmail,
			Started:  time.Now().UTC(),
			State:    "new",
			Messages: []ConversationMsg{
				{Role: "operator", Content: bodyText, At: time.Now().UTC()},
			},
		}
		_ = saveConversation(ctx, h.S3Client, h.ArtifactBucket, conv)
		return h.sendReply(ctx, senderEmail, "Could you clarify?",
			fmt.Sprintf("I wasn't sure what you wanted (confidence: %.0f%%).\n\n"+
				"Could you be more specific? For example:\n"+
				"  - \"Create an open-dev sandbox with 2h TTL\"\n"+
				"  - \"Destroy sandbox sb-abc12345\"\n"+
				"  - \"List running sandboxes\"\n\n"+
				"Haiku's reasoning: %s\n", cmd.Confidence*100, cmd.Reasoning))
	}

	// Info commands: execute immediately, no confirmation.
	if cmd.Type == "info" {
		return h.handleInfoCommand(ctx, senderEmail, cmd)
	}

	// Action commands: send confirmation template, save conversation state.
	return h.sendActionConfirmation(ctx, senderEmail, bodyText, threadID, cmd)
}

// handleInfoCommand executes info commands (list, status) and replies immediately.
func (h *OperatorEmailHandler) handleInfoCommand(ctx context.Context, senderEmail string, cmd *InterpretedCommand) error {
	switch cmd.Command {
	case "list":
		var sb strings.Builder
		sb.WriteString("─── Running Sandboxes ────────────────────────\n")
		if h.DynamoClient != nil {
			records, err := awspkg.ListAllSandboxesByDynamo(ctx, h.DynamoClient, h.SandboxTableName)
			if err != nil {
				sb.WriteString(fmt.Sprintf("  (error listing sandboxes: %v)\n", err))
			} else if len(records) == 0 {
				sb.WriteString("  No sandboxes currently running.\n")
			} else {
				fmt.Fprintf(&sb, "  %-20s %-12s %-14s %s\n", "Sandbox ID", "Profile", "Status", "TTL Remaining")
				fmt.Fprintf(&sb, "  %-20s %-12s %-14s %s\n", strings.Repeat("-", 20), strings.Repeat("-", 12), strings.Repeat("-", 14), strings.Repeat("-", 12))
				for _, r := range records {
					ttl := r.TTLRemaining
					if ttl == "" {
						ttl = "—"
					}
					fmt.Fprintf(&sb, "  %-20s %-12s %-14s %s\n", r.SandboxID, r.Profile, r.Status, ttl)
				}
			}
		} else {
			sb.WriteString("  (DynamoDB not configured)\n")
		}
		return h.sendReply(ctx, senderEmail, "Sandbox List", sb.String())

	case "status":
		// Resolve sandbox ID from profile field or overrides.
		sandboxID := cmd.Profile
		if sandboxID == "" {
			if v, ok := cmd.Overrides["sandbox_id"]; ok {
				sandboxID = fmt.Sprintf("%v", v)
			}
		}
		if sandboxID == "" {
			return h.sendReply(ctx, senderEmail, "Status failed",
				"Could not determine which sandbox to check. Please specify a sandbox ID.\n")
		}
		return h.handleStatus(ctx, senderEmail, "status "+sandboxID)

	default:
		return h.sendReply(ctx, senderEmail, "Info command",
			fmt.Sprintf("Executed info command: %s\nReasoning: %s\n", cmd.Command, cmd.Reasoning))
	}
}

// sendActionConfirmation builds and sends the confirmation template for an action command,
// then saves the conversation state as "awaiting_confirmation".
func (h *OperatorEmailHandler) sendActionConfirmation(ctx context.Context, senderEmail, originalBody, threadID string, cmd *InterpretedCommand) error {
	var sb strings.Builder
	sb.WriteString("I'll run:\n")
	sb.WriteString(fmt.Sprintf("  km %s", cmd.Command))
	if cmd.Profile != "" {
		sb.WriteString(fmt.Sprintf(" profiles/%s", cmd.Profile))
	}
	sb.WriteString("\n")
	if len(cmd.Overrides) > 0 {
		sb.WriteString("\nWith overrides:\n")
		for k, v := range cmd.Overrides {
			sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
	}
	sb.WriteString(fmt.Sprintf("\nConfidence: %.0f%%\n", cmd.Confidence*100))
	sb.WriteString(fmt.Sprintf("Reasoning: %s\n", cmd.Reasoning))
	sb.WriteString("\nReply YES to proceed, CANCEL to abort, or describe changes.\n")
	sb.WriteString("(Include your KM-AUTH phrase in the reply body.)\n")

	conv := &ConversationState{
		ThreadID:    threadID,
		Sender:      senderEmail,
		Started:     time.Now().UTC(),
		State:       "awaiting_confirmation",
		ResolvedCmd: cmd,
		Messages: []ConversationMsg{
			{Role: "operator", Content: originalBody, At: time.Now().UTC()},
		},
	}
	if err := saveConversation(ctx, h.S3Client, h.ArtifactBucket, conv); err != nil {
		fmt.Fprintf(os.Stderr, "[operator-email] save conversation error (primary %s): %v\n", threadID, err)
	}

	// Send confirmation reply and capture the SES Message-ID.
	confirmMsgID, sendErr := h.sendReplyGetID(ctx, senderEmail,
		fmt.Sprintf("Confirm: km %s", cmd.Command),
		sb.String())
	if sendErr != nil {
		return sendErr
	}

	// Save conversation under the confirmation's Message-ID too.
	// Gmail's reply will have In-Reply-To pointing to this ID, not the original.
	if confirmMsgID != "" {
		convCopy := *conv
		convCopy.ThreadID = confirmMsgID
		if err := saveConversation(ctx, h.S3Client, h.ArtifactBucket, &convCopy); err != nil {
			fmt.Fprintf(os.Stderr, "[operator-email] save conversation error (confirm %s): %v\n", confirmMsgID, err)
		}
		fmt.Fprintf(os.Stderr, "[operator-email] conversation saved under both %s and %s\n", threadID, confirmMsgID)
	}

	return nil
}

// handleConversationReply processes a reply to an existing conversation in "awaiting_confirmation".
func (h *OperatorEmailHandler) handleConversationReply(ctx context.Context, senderEmail, bodyText string, conv *ConversationState) error {
	// Check each non-empty line for yes/cancel — KM-AUTH and other lines may precede the reply word.
	intent := replyIntent(bodyText)

	switch intent {
	case "yes":
		return h.executeConfirmedCommand(ctx, senderEmail, conv)

	case "cancel":
		conv.State = "cancelled"
		conv.Messages = append(conv.Messages, ConversationMsg{Role: "operator", Content: bodyText, At: time.Now().UTC()})
		_ = saveConversation(ctx, h.S3Client, h.ArtifactBucket, conv)
		return h.sendReply(ctx, senderEmail, "Cancelled",
			fmt.Sprintf("Command cancelled. No action was taken.\n\nOriginal command: km %s\n", conv.ResolvedCmd.Command))

	default:
		// Revision: call Haiku with original context + new user message.
		return h.handleRevision(ctx, senderEmail, bodyText, conv)
	}
}

// replyIntent scans the body lines (skipping KM-AUTH and blank lines) to determine intent.
// Returns "yes", "cancel", or "" (revision).
func replyIntent(bodyText string) string {
	for _, line := range strings.Split(bodyText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		// Skip KM-AUTH lines — they are not the reply intent.
		if strings.HasPrefix(lower, "km-auth:") {
			continue
		}
		// First non-empty non-auth line determines intent.
		// Accept common affirmatives: yes, yep, yup, yeah, y, sure, ok, approve, confirm, looks good, lgtm
		firstWord := strings.Fields(lower)[0]
		switch {
		case strings.HasPrefix(firstWord, "yes"), firstWord == "y", firstWord == "yep",
			firstWord == "yup", firstWord == "yeah", firstWord == "sure",
			firstWord == "ok", firstWord == "okay", firstWord == "approve",
			firstWord == "approved", firstWord == "confirm", firstWord == "confirmed",
			firstWord == "lgtm", strings.HasPrefix(lower, "looks good"):
			return "yes"
		case strings.HasPrefix(firstWord, "cancel"), firstWord == "no",
			firstWord == "nope", firstWord == "abort", firstWord == "stop":
			return "cancel"
		}
		// Any other content = revision.
		return ""
	}
	return ""
}

// executeConfirmedCommand dispatches the appropriate EventBridge event for a confirmed command.
func (h *OperatorEmailHandler) executeConfirmedCommand(ctx context.Context, senderEmail string, conv *ConversationState) error {
	cmd := conv.ResolvedCmd
	if cmd == nil {
		return h.sendReply(ctx, senderEmail, "Execution error", "No resolved command found in conversation state.\n")
	}

	var execErr error
	var execDetail string

	switch cmd.Command {
	case "create":
		// Load builtin profile, serialize to YAML, upload to S3, dispatch EventBridge.
		var sandboxID string
		var profileYAML []byte
		if profile.IsBuiltin(cmd.Profile) {
			p, err := profile.LoadBuiltin(cmd.Profile)
			if err != nil {
				return h.sendReply(ctx, senderEmail, "Execution error",
					fmt.Sprintf("Could not load profile %q: %v\n", cmd.Profile, err))
			}
			// Apply known overrides.
			if ttl, ok := cmd.Overrides["ttl"]; ok {
				p.Spec.Lifecycle.TTL = fmt.Sprintf("%v", ttl)
			}
			// Note: `alias` override is forwarded to km create --alias via
			// SandboxCreateDetail.Alias below — it's a display/metadata alias,
			// not the dot-notation spec.email.alias which requires [a-z]+\.[a-z]+.
			sandboxID = compiler.GenerateSandboxID(p.Metadata.Prefix)
			profileYAML, err = yaml.Marshal(p)
			if err != nil {
				return fmt.Errorf("serialize profile: %w", err)
			}
		} else if cmd.Profile != "" {
			return h.sendReply(ctx, senderEmail, "Execution error",
				fmt.Sprintf("Profile %q is not a known built-in profile. Available profiles: %s\n",
					cmd.Profile, strings.Join(profile.ListBuiltins(), ", ")))
		} else {
			return h.sendReply(ctx, senderEmail, "Execution error",
				"No profile specified. Please include a profile name (e.g. open-dev, restricted-dev).\n")
		}

		artifactPrefix := fmt.Sprintf("remote-create/%s", sandboxID)
		profileKey := fmt.Sprintf("%s/.km-profile.yaml", artifactPrefix)
		if _, err := h.S3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      awssdk.String(h.ArtifactBucket),
			Key:         awssdk.String(profileKey),
			Body:        bytes.NewReader(profileYAML),
			ContentType: awssdk.String("text/yaml"),
		}); err != nil {
			return fmt.Errorf("upload profile to S3: %w", err)
		}

		onDemand := false
		if v, ok := cmd.Overrides["on_demand"]; ok {
			if b, ok := v.(bool); ok {
				onDemand = b
			} else if fmt.Sprintf("%v", v) == "true" {
				onDemand = true
			}
		}

		// If schedule_time is set, relay to TTL handler via EventBridge.
		// The TTL handler has scheduler:CreateSchedule + iam:PassRole permissions (no SCP block).
		if schedTime, ok := cmd.Overrides["schedule_time"]; ok && fmt.Sprintf("%v", schedTime) != "" {
			alias := ""
			if a, ok := cmd.Overrides["alias"]; ok {
				alias = fmt.Sprintf("%v", a)
			}

			// Publish a SandboxIdle event with event_type "schedule-create".
			// The km-sandbox-idle EventBridge rule routes this to the TTL handler,
			// which has scheduler:CreateSchedule + iam:PassRole (no SCP block).
			execErr = awspkg.PublishSandboxCommand(ctx, h.EventBridgeClient, sandboxID, "schedule-create",
				"schedule_time", fmt.Sprintf("%v", schedTime),
				"artifact_bucket", h.ArtifactBucket,
				"artifact_prefix", artifactPrefix,
				"operator_email_event", senderEmail,
				"on_demand", fmt.Sprintf("%v", onDemand),
				"alias", alias,
				"profile_name", cmd.Profile,
			)
			execDetail = fmt.Sprintf("Sandbox ID: %s\nProfile: %s\nScheduled: %v\nOn-demand: %v\nAlias: %s\n",
				sandboxID, cmd.Profile, schedTime, onDemand, alias)
		} else {
		// Immediate create: dispatch via EventBridge.
			aliasOverride := ""
			if a, ok := cmd.Overrides["alias"]; ok {
				aliasOverride = fmt.Sprintf("%v", a)
			}
			execErr = awspkg.PutSandboxCreateEvent(ctx, h.EventBridgeClient, awspkg.SandboxCreateDetail{
				SandboxID:      sandboxID,
				ArtifactBucket: h.ArtifactBucket,
				ArtifactPrefix: artifactPrefix,
				OperatorEmail:  senderEmail,
				OnDemand:       onDemand,
				Alias:          aliasOverride,
			})
			execDetail = fmt.Sprintf("Sandbox ID: %s\nProfile: %s\nOn-demand: %v\nAlias: %s\n", sandboxID, cmd.Profile, onDemand, aliasOverride)
		}

	case "destroy", "extend", "pause", "resume":
		// For non-create actions, dispatch a generic command event via EventBridge.
		// The sandbox ID may be in cmd.Profile or cmd.Overrides["sandbox_id"].
		sandboxID := cmd.Profile
		if sandboxID == "" {
			if v, ok := cmd.Overrides["sandbox_id"]; ok {
				sandboxID = fmt.Sprintf("%v", v)
			}
		}
		if sandboxID == "" {
			return h.sendReply(ctx, senderEmail, "Execution error",
				fmt.Sprintf("No sandbox ID found for %s command.\n", cmd.Command))
		}

		execErr = awspkg.PublishSandboxCommand(ctx, h.EventBridgeClient, sandboxID, cmd.Command)
		execDetail = fmt.Sprintf("Sandbox ID: %s\nCommand: %s\n", sandboxID, cmd.Command)

	default:
		return h.sendReply(ctx, senderEmail, "Execution error",
			fmt.Sprintf("Unknown command: %s\n", cmd.Command))
	}

	if execErr != nil {
		return fmt.Errorf("dispatch %s event: %w", cmd.Command, execErr)
	}

	conv.State = "confirmed"
	conv.Messages = append(conv.Messages, ConversationMsg{Role: "system", Content: "confirmed", At: time.Now().UTC()})
	_ = saveConversation(ctx, h.S3Client, h.ArtifactBucket, conv)

	return h.sendReply(ctx, senderEmail,
		fmt.Sprintf("Executing: km %s", cmd.Command),
		fmt.Sprintf("Command accepted and dispatched.\n\n%s\nYou will receive a notification when the operation completes.\n", execDetail))
}

// handleRevision calls Haiku with the original command context plus the revision request,
// then sends an updated confirmation template.
func (h *OperatorEmailHandler) handleRevision(ctx context.Context, senderEmail, bodyText string, conv *ConversationState) error {
	if conv.ResolvedCmd == nil {
		return h.sendReply(ctx, senderEmail, "Revision error", "No previous command to revise.\n")
	}

	origJSON, _ := json.Marshal(conv.ResolvedCmd)
	revisionMessage := fmt.Sprintf(
		"Original request was interpreted as: %s\n\nThe operator now says: %s\n\nPlease revise the command accordingly.",
		string(origJSON), bodyText,
	)

	profiles := profile.ListBuiltins()
	var sandboxIDs []string
	if h.DynamoClient != nil {
		records, _ := awspkg.ListAllSandboxesByDynamo(ctx, h.DynamoClient, h.SandboxTableName)
		for _, r := range records {
			sandboxIDs = append(sandboxIDs, r.SandboxID)
		}
	}

	cmd, err := callHaiku(ctx, h.BedrockClient, h.BedrockModelID, buildSystemPrompt(profiles, sandboxIDs), revisionMessage)
	if err != nil {
		return h.sendReply(ctx, senderEmail, "Revision error",
			fmt.Sprintf("Could not process revision: %v\n", err))
	}

	conv.ResolvedCmd = cmd
	conv.State = "awaiting_confirmation"
	conv.Messages = append(conv.Messages, ConversationMsg{Role: "operator", Content: bodyText, At: time.Now().UTC()})
	_ = saveConversation(ctx, h.S3Client, h.ArtifactBucket, conv)

	// Send updated confirmation.
	return h.sendActionConfirmation(ctx, senderEmail, bodyText, conv.ThreadID, cmd)
}

// sendReply sends a formatted reply email.
func (h *OperatorEmailHandler) sendReply(ctx context.Context, to, subject, body string) error {
	_, err := h.sendReplyGetID(ctx, to, subject, body)
	return err
}

// sendReplyGetID sends a reply and returns the SES Message-ID (for conversation threading).
func (h *OperatorEmailHandler) sendReplyGetID(ctx context.Context, to, subject, body string) (string, error) {
	from := fmt.Sprintf("\"operator\" <operator@%s>", h.Domain)
	fullBody := body + "\n— " + version.Header() + "\n"
	dest := &sesv2types.Destination{
		ToAddresses: []string{to},
	}
	if len(h.replyCC) > 0 {
		dest.CcAddresses = h.replyCC
	}
	out, err := h.SESClient.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination:      dest,
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(subject),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(fullBody),
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("send reply to %s: %w", to, err)
	}
	msgID := ""
	if out != nil && out.MessageId != nil {
		msgID = *out.MessageId
	}
	return msgID, nil
}

// extractSandboxID finds a sandbox ID in the subject string.
func extractSandboxID(subject string) string {
	if m := sandboxIDPattern.FindStringSubmatch(subject); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// extractKMAuth extracts the KM-AUTH phrase from a string using the exported pattern.
func extractKMAuth(text string) string {
	if matches := awspkg.KMAuthPattern.FindStringSubmatch(text); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

// extractEmail returns the bare email address from an RFC 5322 address string.
func extractEmail(addr string) string {
	a, err := mail.ParseAddress(addr)
	if err != nil {
		return strings.TrimSpace(addr)
	}
	return a.Address
}

// extractBodyAndYAML parses the MIME message body and returns:
//   - bodyText: the text/plain parts concatenated (used for KM-AUTH extraction)
//   - yamlBytes: the YAML profile bytes (from text/yaml attachment or from text/plain body)
func extractBodyAndYAML(msg *mail.Message) (bodyText string, yamlBytes []byte, err error) {
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", nil, fmt.Errorf("parse Content-Type %q: %w", ct, err)
	}

	rawBody, readErr := io.ReadAll(msg.Body)
	if readErr != nil {
		return "", nil, fmt.Errorf("read message body: %w", readErr)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
		var textParts []string
		for {
			part, partErr := mr.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				return "", nil, fmt.Errorf("read multipart: %w", partErr)
			}
			partCT := part.Header.Get("Content-Type")
			partMT, _, _ := mime.ParseMediaType(partCT)
			partDisp := part.Header.Get("Content-Disposition")
			_, dispParams, _ := mime.ParseMediaType(partDisp)
			filename := dispParams["filename"]

			data, _ := io.ReadAll(part)
			part.Close()

			switch {
			case partMT == "text/yaml" || partMT == "application/x-yaml" ||
				strings.HasSuffix(strings.ToLower(filename), ".yaml"):
				yamlBytes = data
			case partMT == "text/plain" || partMT == "":
				textParts = append(textParts, string(data))
			}
		}
		bodyText = strings.Join(textParts, "\n")
		return bodyText, yamlBytes, nil
	}

	// Single-part message
	switch mediaType {
	case "text/yaml", "application/x-yaml":
		return "", rawBody, nil
	default:
		return string(rawBody), rawBody, nil
	}
}


// ---- Lambda entrypoint ----

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(fmt.Sprintf("load AWS config: %v", err))
	}

	artifactBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	stateBucket := os.Getenv("KM_STATE_BUCKET")
	domain := os.Getenv("KM_EMAIL_DOMAIN")
	safePhraseKey := os.Getenv("KM_SAFE_PHRASE_SSM_KEY")
	if safePhraseKey == "" {
		safePhraseKey = "/km/config/remote-create/safe-phrase"
	}
	sandboxTableName := os.Getenv("SANDBOX_TABLE_NAME")
	if sandboxTableName == "" {
		sandboxTableName = "km-sandboxes"
	}

	bedrockModelID := os.Getenv("BEDROCK_MODEL_ID")
	var bedrockClient BedrockRuntimeAPI
	if bedrockModelID != "" {
		bedrockClient = bedrockruntime.NewFromConfig(cfg)
	}

	h := &OperatorEmailHandler{
		S3Client:          s3.NewFromConfig(cfg),
		DynamoClient:      dynamodbpkg.NewFromConfig(cfg),
		SandboxTableName:  sandboxTableName,
		SSMClient:         ssm.NewFromConfig(cfg),
		EventBridgeClient: eventbridge.NewFromConfig(cfg),
		SESClient:         sesv2.NewFromConfig(cfg),
		ArtifactBucket:    artifactBucket,
		StateBucket:       stateBucket,
		Domain:            domain,
		SafePhraseSSMKey:  safePhraseKey,
		BedrockClient:  bedrockClient,
		BedrockModelID: bedrockModelID,
		VerboseErrors:  os.Getenv("KM_VERBOSE_EMAIL_ERRORS") == "true",
	}

	lambda.Start(h.Handle)
}
