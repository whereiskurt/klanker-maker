// Package main — operator email handler Lambda
// Processes SES-delivered emails to operator@sandboxes.{domain}, dispatches
// commands based on the email Subject line:
//
//   - Subject contains "create" → create sandbox from YAML attachment
//   - Subject contains "status" + sandbox ID → reply with sandbox status
//   - Unrecognized → reply with help text
//
// All commands require KM-AUTH safe phrase validation.
//
// Flow:
//  1. SES delivers email to S3 bucket under mail/create/ prefix
//  2. S3 notification triggers this Lambda
//  3. Lambda fetches raw MIME email from S3
//  4. Parses MIME to extract sender, subject, body text, and attachments
//  5. Validates KM-AUTH safe phrase against SSM parameter
//  6. Dispatches to the appropriate command handler based on subject
//  7. Sends reply email with results
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
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
	SSMClient         SSMClientAPI
	EventBridgeClient awspkg.EventBridgeAPI
	SESClient         SESEmailAPI
	ArtifactBucket    string
	StateBucket       string
	Domain            string
	SafePhraseSSMKey  string
}

// sandboxIDPattern matches sandbox IDs like "sb-abc123de" or just hex strings.
var sandboxIDPattern = regexp.MustCompile(`(?i)\b(?:sb-)?([0-9a-f]{8,16})\b`)

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

	// Step 3: Extract sender and subject
	senderFrom := msg.Header.Get("From")
	senderEmail := extractEmail(senderFrom)
	subject := msg.Header.Get("Subject")

	// Step 4: Extract body text and YAML profile
	bodyText, yamlBytes, err := extractBodyAndYAML(msg)
	if err != nil {
		return fmt.Errorf("extract body and YAML: %w", err)
	}

	// Step 5: Extract and validate KM-AUTH phrase
	phrase := extractKMAuth(bodyText)
	if phrase == "" {
		phrase = extractKMAuth(string(rawEmail))
	}
	if phrase == "" {
		return h.sendReply(ctx, senderEmail, "Command rejected", "Missing KM-AUTH phrase. Include KM-AUTH: <your-phrase> in the email body.\n")
	}

	paramOut, err := h.SSMClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(h.SafePhraseSSMKey),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("fetch safe phrase from SSM (%s): %w", h.SafePhraseSSMKey, err)
	}
	if subtle.ConstantTimeCompare([]byte(phrase), []byte(awssdk.ToString(paramOut.Parameter.Value))) != 1 {
		return h.sendReply(ctx, senderEmail, "Command rejected", "Invalid KM-AUTH phrase.\n")
	}

	// Step 6: Dispatch based on subject
	subjectLower := strings.ToLower(subject)
	switch {
	case strings.Contains(subjectLower, "create"):
		return h.handleCreate(ctx, senderEmail, yamlBytes)
	case strings.Contains(subjectLower, "status"):
		return h.handleStatus(ctx, senderEmail, subject)
	default:
		return h.sendHelp(ctx, senderEmail)
	}
}

// handleCreate processes a sandbox creation request.
func (h *OperatorEmailHandler) handleCreate(ctx context.Context, senderEmail string, yamlBytes []byte) error {
	if len(yamlBytes) == 0 {
		return h.sendReply(ctx, senderEmail, "Create failed",
			"No YAML profile found. Attach a .yaml file or include the profile in the email body.\n")
	}

	if _, err := profile.Parse(yamlBytes); err != nil {
		return h.sendReply(ctx, senderEmail, "Create failed",
			fmt.Sprintf("Profile validation failed: %v\n", err))
	}

	sandboxID, err := generateSandboxID()
	if err != nil {
		return fmt.Errorf("generate sandbox ID: %w", err)
	}

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
			"No sandbox ID found in subject. Use: Subject: status sb-<id>\n")
	}

	// Ensure sb- prefix
	if !strings.HasPrefix(sandboxID, "sb-") {
		sandboxID = "sb-" + sandboxID
	}

	// Read metadata from S3
	bucket := h.StateBucket
	if bucket == "" {
		bucket = h.ArtifactBucket
	}
	metaKey := "tf-km/sandboxes/" + sandboxID + "/metadata.json"
	metaOut, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(metaKey),
	})
	if err != nil {
		return h.sendReply(ctx, senderEmail, fmt.Sprintf("Status: %s", sandboxID),
			fmt.Sprintf("Sandbox %s not found or metadata unavailable.\n", sandboxID))
	}
	defer metaOut.Body.Close()

	metaBytes, err := io.ReadAll(metaOut.Body)
	if err != nil {
		return h.sendReply(ctx, senderEmail, fmt.Sprintf("Status: %s", sandboxID),
			fmt.Sprintf("Could not read metadata for %s.\n", sandboxID))
	}

	var meta awspkg.SandboxMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return h.sendReply(ctx, senderEmail, fmt.Sprintf("Status: %s", sandboxID),
			fmt.Sprintf("Could not parse metadata for %s.\n", sandboxID))
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
		"  status <sandbox-id>  — Get sandbox status (e.g. \"status sb-abc123de\")\n\n" +
		"All commands require KM-AUTH: <phrase> in the email body.\n"
	return h.sendReply(ctx, senderEmail, "Operator Help", body)
}

// sendReply sends a formatted reply email.
func (h *OperatorEmailHandler) sendReply(ctx context.Context, to, subject, body string) error {
	from := fmt.Sprintf("operator@sandboxes.%s", h.Domain)
	if _, err := h.SESClient.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{to},
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
	}); err != nil {
		return fmt.Errorf("send reply to %s: %w", to, err)
	}
	return nil
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

// generateSandboxID produces a random 8-byte hex string for use as a sandbox ID.
func generateSandboxID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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

	h := &OperatorEmailHandler{
		S3Client:          s3.NewFromConfig(cfg),
		SSMClient:         ssm.NewFromConfig(cfg),
		EventBridgeClient: eventbridge.NewFromConfig(cfg),
		SESClient:         sesv2.NewFromConfig(cfg),
		ArtifactBucket:    artifactBucket,
		StateBucket:       stateBucket,
		Domain:            domain,
		SafePhraseSSMKey:  safePhraseKey,
	}

	lambda.Start(h.Handle)
}
