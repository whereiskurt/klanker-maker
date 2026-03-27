// Package main — email-create-handler Lambda
// Processes SES-delivered emails stored in S3, validates KM-AUTH safe phrase,
// parses YAML SandboxProfile, and dispatches a SandboxCreate EventBridge event.
//
// Flow:
//  1. SES delivers email to S3 bucket under mail/ prefix
//  2. S3 notification triggers this Lambda via EventBridge or direct invocation
//  3. Lambda fetches raw MIME email from S3
//  4. Parses MIME to extract sender, body text, and YAML profile attachment
//  5. Validates KM-AUTH safe phrase against SSM parameter
//  6. Validates YAML via profile.Parse
//  7. Generates sandbox ID, uploads profile to S3
//  8. Publishes SandboxCreate EventBridge event
//  9. Sends acknowledgment email to operator
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
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
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

// EmailCreateS3API is the narrow S3 interface for fetching and storing email-create artifacts.
type EmailCreateS3API interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// SSMClientAPI is the narrow SSM interface for reading safe phrase parameters.
type SSMClientAPI interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// SESEmailAPI is the narrow SES interface needed by this handler (send only).
// Note: narrower than pkg/aws.SESV2API which includes identity lifecycle methods.
type SESEmailAPI interface {
	SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// SandboxCreateDetail holds the EventBridge detail payload for a SandboxCreate event.
type SandboxCreateDetail struct {
	SandboxID      string `json:"sandbox_id"`
	ArtifactBucket string `json:"artifact_bucket"`
	ArtifactPrefix string `json:"artifact_prefix"`
	OperatorEmail  string `json:"operator_email"`
	OnDemand       bool   `json:"on_demand"`
}

// putSandboxCreateEvent publishes a SandboxCreate event to EventBridge.
// Note: This will be consolidated with pkg/aws/eventbridge.go in Plan 03.
func putSandboxCreateEvent(ctx context.Context, client awspkg.EventBridgeAPI, detail SandboxCreateDetail) error {
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal SandboxCreate detail: %w", err)
	}

	input := &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:       awssdk.String("km.sandbox"),
				DetailType:   awssdk.String("SandboxCreate"),
				Detail:       awssdk.String(string(detailBytes)),
				EventBusName: awssdk.String("default"),
			},
		},
	}

	if _, err := client.PutEvents(ctx, input); err != nil {
		return fmt.Errorf("publish SandboxCreate event for sandbox %s: %w", detail.SandboxID, err)
	}
	return nil
}

// ---- handler ----

// EmailCreateHandler processes SES-delivered create emails.
type EmailCreateHandler struct {
	S3Client          EmailCreateS3API
	SSMClient         SSMClientAPI
	EventBridgeClient awspkg.EventBridgeAPI
	SESClient         SESEmailAPI
	ArtifactBucket    string
	Domain            string
	SafePhraseSSMKey  string
}

// Handle processes a single S3 event record containing an SES-delivered email.
// Returns nil for both successful dispatch and politely-rejected emails (wrong/missing auth).
// Returns non-nil only for unexpected infrastructure errors.
func (h *EmailCreateHandler) Handle(ctx context.Context, event S3EventRecord) error {
	if len(event.Records) == 0 {
		return fmt.Errorf("email-create-handler: no records in event")
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

	// Step 3: Extract sender
	senderFrom := msg.Header.Get("From")
	senderEmail := extractEmail(senderFrom)

	// Step 4: Extract body text and YAML profile
	bodyText, yamlBytes, err := extractBodyAndYAML(msg)
	if err != nil {
		return fmt.Errorf("extract body and YAML: %w", err)
	}

	// Step 5: Extract KM-AUTH phrase — check body text first, then entire raw email
	phrase := extractKMAuth(bodyText)
	if phrase == "" {
		phrase = extractKMAuth(string(rawEmail))
	}

	// Step 6: Reject if no KM-AUTH phrase
	if phrase == "" {
		return h.sendRejection(ctx, senderEmail, "Sandbox creation rejected: missing KM-AUTH phrase")
	}

	// Step 7: Fetch expected phrase from SSM
	paramOut, err := h.SSMClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(h.SafePhraseSSMKey),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("fetch safe phrase from SSM (%s): %w", h.SafePhraseSSMKey, err)
	}
	expectedPhrase := awssdk.ToString(paramOut.Parameter.Value)

	// Step 8: Constant-time phrase comparison
	if subtle.ConstantTimeCompare([]byte(phrase), []byte(expectedPhrase)) != 1 {
		return h.sendRejection(ctx, senderEmail, "Sandbox creation rejected: invalid KM-AUTH phrase")
	}

	// Step 9: Parse and validate YAML profile
	if _, err := profile.Parse(yamlBytes); err != nil {
		return h.sendRejection(ctx, senderEmail,
			fmt.Sprintf("Sandbox creation rejected: profile validation failed: %v", err))
	}

	// Step 10: Generate sandbox ID
	sandboxID, err := generateSandboxID()
	if err != nil {
		return fmt.Errorf("generate sandbox ID: %w", err)
	}

	// Step 11: Upload profile to S3
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

	// Step 12: Publish SandboxCreate EventBridge event
	if err := putSandboxCreateEvent(ctx, h.EventBridgeClient, SandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: h.ArtifactBucket,
		ArtifactPrefix: artifactPrefix,
		OperatorEmail:  senderEmail,
		OnDemand:       false,
	}); err != nil {
		return fmt.Errorf("publish SandboxCreate event: %w", err)
	}

	// Step 13: Send acknowledgment email
	from := fmt.Sprintf("create@sandboxes.%s", h.Domain)
	if _, err := h.SESClient.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{senderEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String(fmt.Sprintf("Sandbox creation request received: %s", sandboxID)),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(fmt.Sprintf(
							"Your sandbox creation request has been received.\nSandbox ID: %s\nYou will receive another notification when provisioning completes.\n",
							sandboxID,
						)),
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("send acknowledgment email to %s: %w", senderEmail, err)
	}

	return nil
}

// sendRejection sends a rejection email to the operator and returns nil
// (a rejected email is not an infrastructure error).
func (h *EmailCreateHandler) sendRejection(ctx context.Context, senderEmail, reason string) error {
	from := fmt.Sprintf("create@sandboxes.%s", h.Domain)
	if _, err := h.SESClient.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{senderEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{
					Data: awssdk.String("Sandbox creation rejected"),
				},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{
						Data: awssdk.String(reason + "\n"),
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("send rejection email to %s: %w", senderEmail, err)
	}
	return nil
}

// extractKMAuth extracts the KM-AUTH phrase from a string using the exported pattern.
func extractKMAuth(text string) string {
	if matches := awspkg.KMAuthPattern.FindStringSubmatch(text); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

// extractEmail returns the bare email address from an RFC 5322 address string.
// e.g. "Alice <alice@example.com>" → "alice@example.com"
func extractEmail(addr string) string {
	a, err := mail.ParseAddress(addr)
	if err != nil {
		// Return addr as-is if it cannot be parsed (may already be bare)
		return strings.TrimSpace(addr)
	}
	return a.Address
}

// extractBodyAndYAML parses the MIME message body and returns:
//   - bodyText: the text/plain parts concatenated (used for KM-AUTH extraction)
//   - yamlBytes: the YAML profile bytes (from text/yaml attachment or from text/plain body)
//
// For multipart messages:
//   - text/yaml or application/x-yaml parts (or filename *.yaml) → yamlBytes
//   - text/plain parts → bodyText
//
// For single-part messages:
//   - text/yaml: entire body is yamlBytes; bodyText is empty
//   - text/plain: entire body is both bodyText and yamlBytes
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
	default: // text/plain and everything else
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
	domain := os.Getenv("KM_EMAIL_DOMAIN")
	safePhraseKey := os.Getenv("KM_SAFE_PHRASE_SSM_KEY")
	if safePhraseKey == "" {
		safePhraseKey = "/km/config/remote-create/safe-phrase"
	}

	h := &EmailCreateHandler{
		S3Client:          s3.NewFromConfig(cfg),
		SSMClient:         ssm.NewFromConfig(cfg),
		EventBridgeClient: eventbridge.NewFromConfig(cfg),
		SESClient:         sesv2.NewFromConfig(cfg),
		ArtifactBucket:    artifactBucket,
		Domain:            domain,
		SafePhraseSSMKey:  safePhraseKey,
	}

	lambda.Start(h.Handle)
}
