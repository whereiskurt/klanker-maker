// Package cmd — email.go
// Implements the "km email" command group with "km email send" and "km email read" subcommands.
// send: compose and send a signed (optionally encrypted) email between sandboxes.
// read: list and display messages from a sandbox mailbox, auto-decrypting when keys are available.
package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// EmailSSMAPI is the SSM interface needed by the email commands.
// Matches kmaws.IdentitySSMAPI to satisfy SendSignedEmail (which needs PutParameter,
// GetParameter, DeleteParameter). Only GetParameter is actually called at runtime
// for send (signing key) and read (encryption key), but the full interface is
// required by SendSignedEmail's signature.
// Implemented by *ssm.Client.
type EmailSSMAPI interface {
	kmaws.IdentitySSMAPI
}

// EmailS3API is the narrow S3 interface needed by the email read command.
// Implemented by *s3.Client.
type EmailS3API interface {
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// EmailSendDeps holds injectable dependencies for the email send command.
type EmailSendDeps struct {
	SES      kmaws.SESV2API
	SSMParam EmailSSMAPI
	Identity kmaws.IdentityTableAPI
}

// EmailReadDeps holds injectable dependencies for the email read command.
type EmailReadDeps struct {
	S3Client EmailS3API
	SSMParam EmailSSMAPI
	Identity kmaws.IdentityTableAPI
}

// NewEmailCmd creates the "km email" parent command.
func NewEmailCmd(cfg *config.Config) *cobra.Command {
	return newEmailCmdInternal(cfg, nil, nil)
}

// NewEmailCmdWithDeps creates a testable "km email" command with injected dependencies.
func NewEmailCmdWithDeps(cfg *config.Config, sendDeps *EmailSendDeps, readDeps *EmailReadDeps) *cobra.Command {
	return newEmailCmdInternal(cfg, sendDeps, readDeps)
}

// newEmailCmdInternal builds the email command tree.
func newEmailCmdInternal(cfg *config.Config, sendDeps *EmailSendDeps, readDeps *EmailReadDeps) *cobra.Command {
	email := &cobra.Command{
		Use:          "email",
		Short:        "Send and read signed sandbox email",
		SilenceUsage: true,
	}

	email.AddCommand(newEmailSendCmd(cfg, sendDeps))
	email.AddCommand(newEmailReadCmd(cfg, readDeps))

	return email
}

// emailDomain returns the configured email domain (e.g. "sandboxes.klankermaker.ai").
func emailDomain(cfg *config.Config) string {
	if cfg.Domain != "" {
		return "sandboxes." + cfg.Domain
	}
	return "sandboxes.klankermaker.ai"
}

// ============================================================
// km email send
// ============================================================

// newEmailSendCmd creates the "km email send" subcommand.
func newEmailSendCmd(cfg *config.Config, deps *EmailSendDeps) *cobra.Command {
	var fromFlag string
	var toFlag string
	var subjectFlag string
	var bodyFlag string
	var attachFlag string

	send := &cobra.Command{
		Use:          "send",
		Short:        "Send a signed email from one sandbox to another",
		Long:         "Send a signed (and optionally encrypted) email from a sender sandbox to a recipient sandbox.\nBody can be a file path or - for stdin. Attachments are comma-separated file paths.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runEmailSend(ctx, cfg, deps, fromFlag, toFlag, subjectFlag, bodyFlag, attachFlag, cmd.OutOrStdout())
		},
	}

	send.Flags().StringVar(&fromFlag, "from", "", "Sender sandbox ID (required)")
	send.Flags().StringVar(&toFlag, "to", "", "Recipient sandbox ID (required)")
	send.Flags().StringVar(&subjectFlag, "subject", "", "Email subject line (required)")
	send.Flags().StringVar(&bodyFlag, "body", "", "Path to body file, or - for stdin (required)")
	send.Flags().StringVar(&attachFlag, "attach", "", "Comma-separated list of attachment file paths")

	_ = send.MarkFlagRequired("from")
	_ = send.MarkFlagRequired("to")
	_ = send.MarkFlagRequired("subject")
	_ = send.MarkFlagRequired("body")

	return send
}

// runEmailSend executes the km email send logic.
func runEmailSend(ctx context.Context, cfg *config.Config, deps *EmailSendDeps, from, to, subject, bodyPath, attachCSV string, out io.Writer) error {
	// Validate sandbox ID format for sender and recipient.
	if !sandboxIDLike.MatchString(from) {
		return fmt.Errorf("invalid sender sandbox ID %q: must match {prefix}-{id} format", from)
	}
	if !sandboxIDLike.MatchString(to) {
		return fmt.Errorf("invalid recipient sandbox ID %q: must match {prefix}-{id} format", to)
	}

	// Read body.
	body, err := readBodyArg(bodyPath, os.Stdin)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Read attachments.
	var attachments []kmaws.Attachment
	if attachCSV != "" {
		for _, filePath := range strings.Split(attachCSV, ",") {
			filePath = strings.TrimSpace(filePath)
			if filePath == "" {
				continue
			}
			data, readErr := os.ReadFile(filePath)
			if readErr != nil {
				return fmt.Errorf("read attachment %q: %w", filePath, readErr)
			}
			attachments = append(attachments, kmaws.Attachment{
				Filename: filepath.Base(filePath),
				Data:     data,
			})
		}
	}

	// Resolve email addresses.
	domain := emailDomain(cfg)
	fromEmail := fmt.Sprintf("%s@%s", from, domain)
	toEmail := fmt.Sprintf("%s@%s", to, domain)

	// Resolve encryption policy: fetch sender identity to determine policy.
	tableName := cfg.IdentityTableName
	if tableName == "" {
		tableName = "km-identities"
	}

	// Build real clients if not injected.
	var sesClient kmaws.SESV2API
	var ssmClient EmailSSMAPI
	var identityClient kmaws.IdentityTableAPI

	if deps != nil {
		sesClient = deps.SES
		ssmClient = deps.SSMParam
		identityClient = deps.Identity
	} else {
		awsCfg, awsErr := kmaws.LoadAWSConfig(ctx, cfg.AWSProfile)
		if awsErr != nil {
			return fmt.Errorf("load AWS config: %w", awsErr)
		}
		sesClient = sesv2.NewFromConfig(awsCfg)
		ssmClient = ssm.NewFromConfig(awsCfg)
		identityClient = dynamodb.NewFromConfig(awsCfg)
	}

	// Fetch sender's identity to get encryption policy.
	encryptionPolicy := ""
	senderRecord, fetchErr := kmaws.FetchPublicKey(ctx, identityClient, tableName, from)
	if fetchErr == nil && senderRecord != nil {
		encryptionPolicy = senderRecord.Encryption
	}
	// If fetch fails or no record, proceed with empty policy (no encryption).

	// Send the email.
	if err := kmaws.SendSignedEmail(
		ctx,
		sesClient,
		ssmClient,
		identityClient,
		fromEmail, toEmail, subject, body,
		from, to, tableName, encryptionPolicy,
		attachments,
	); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	fmt.Fprintf(out, "Sent signed email from %s to %s (subject: %s, attachments: %d)\n",
		from, to, subject, len(attachments))
	return nil
}

// readBodyArg reads the email body from a file path, or from stdin if path is "-".
func readBodyArg(bodyPath string, stdin io.Reader) (string, error) {
	if bodyPath == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(bodyPath)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", bodyPath, err)
	}
	return string(data), nil
}

// ============================================================
// km email read
// ============================================================

// newEmailReadCmd creates the "km email read" subcommand.
func newEmailReadCmd(cfg *config.Config, deps *EmailReadDeps) *cobra.Command {
	var jsonFlag bool
	var rawFlag bool
	var messageIDFlag string

	read := &cobra.Command{
		Use:          "read <sandbox-id>",
		Short:        "Read messages from a sandbox mailbox",
		Long:         "List and display messages from a sandbox mailbox. Auto-decrypts encrypted messages when keys are available.",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runEmailRead(ctx, cfg, deps, args[0], jsonFlag, rawFlag, messageIDFlag, cmd.OutOrStdout())
		},
	}

	read.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON array")
	read.Flags().BoolVar(&rawFlag, "raw", false, "Dump raw MIME bytes to stdout")
	read.Flags().StringVar(&messageIDFlag, "message-id", "", "Specific message ID to read (used with --raw; defaults to latest)")

	return read
}

// parsedEntry holds a fully parsed mailbox message along with its ordinal index and S3 key.
type parsedEntry struct {
	idx int
	key string
	msg *kmaws.MailboxMessage
}

// mailboxMessageJSON is the JSON-serializable form of a mailbox message.
type mailboxMessageJSON struct {
	Index       int    `json:"index"`
	MessageID   string `json:"message_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	SenderID    string `json:"sender_id,omitempty"`
	SignatureOK bool   `json:"signature_ok"`
	Encrypted   bool   `json:"encrypted"`
	Plaintext   bool   `json:"plaintext"`
	Attachments int    `json:"attachments"`
}

// runEmailRead executes the km email read logic.
func runEmailRead(ctx context.Context, cfg *config.Config, deps *EmailReadDeps, sandboxID string, jsonOut, rawOut bool, messageIDFilter string, out io.Writer) error {
	// Validate sandbox ID format.
	if !sandboxIDLike.MatchString(sandboxID) {
		return fmt.Errorf("invalid sandbox ID %q: must match {prefix}-{id} format", sandboxID)
	}

	// Get artifacts bucket and email domain.
	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		return fmt.Errorf("artifacts bucket not configured: set ArtifactsBucket in config or KM_ARTIFACTS_BUCKET env var")
	}
	domain := emailDomain(cfg)
	tableName := cfg.IdentityTableName
	if tableName == "" {
		tableName = "km-identities"
	}

	// Build real clients if not injected.
	var s3Client EmailS3API
	var ssmClient EmailSSMAPI
	var identityClient kmaws.IdentityTableAPI

	if deps != nil {
		s3Client = deps.S3Client
		ssmClient = deps.SSMParam
		identityClient = deps.Identity
	} else {
		awsCfg, awsErr := kmaws.LoadAWSConfig(ctx, cfg.AWSProfile)
		if awsErr != nil {
			return fmt.Errorf("load AWS config: %w", awsErr)
		}
		s3Client = s3.NewFromConfig(awsCfg)
		ssmClient = ssm.NewFromConfig(awsCfg)
		identityClient = dynamodb.NewFromConfig(awsCfg)
	}

	// List messages.
	keys, err := kmaws.ListMailboxMessages(ctx, s3Client, bucket, sandboxID, domain)
	if err != nil {
		return fmt.Errorf("list mailbox messages: %w", err)
	}

	// --raw mode: output a single message's raw MIME bytes.
	if rawOut {
		key := selectMessageKey(keys, messageIDFilter)
		if key == "" {
			fmt.Fprintln(out, "No messages")
			return nil
		}
		raw, readErr := kmaws.ReadMessage(ctx, s3Client, bucket, key)
		if readErr != nil {
			return fmt.Errorf("read message: %w", readErr)
		}
		_, writeErr := out.Write(raw)
		return writeErr
	}

	if len(keys) == 0 {
		fmt.Fprintln(out, "No messages")
		return nil
	}

	// Parse all messages.
	var entries []parsedEntry

	for i, key := range keys {
		rawMIME, readErr := kmaws.ReadMessage(ctx, s3Client, bucket, key)
		if readErr != nil {
			continue // skip unreadable messages
		}

		// Fetch sender's public key for verification.
		var pubKeyB64 string
		// We need to parse headers first to get X-KM-Sender-ID.
		// A quick header-only parse to extract sender ID:
		senderID := extractHeader(rawMIME, "X-KM-Sender-ID")
		if senderID != "" && identityClient != nil {
			senderRecord, fetchErr := kmaws.FetchPublicKey(ctx, identityClient, tableName, senderID)
			if fetchErr == nil && senderRecord != nil {
				pubKeyB64 = senderRecord.PublicKeyB64
			}
		}

		// Fetch receiver's allow-list for ParseSignedMessage.
		var allowedSenders []string
		if identityClient != nil {
			receiverRecord, fetchErr := kmaws.FetchPublicKey(ctx, identityClient, tableName, sandboxID)
			if fetchErr == nil && receiverRecord != nil {
				allowedSenders = receiverRecord.AllowedSenders
			}
		}

		parsedMsg, parseErr := kmaws.ParseSignedMessage(rawMIME, sandboxID, pubKeyB64, allowedSenders, "")
		if parseErr != nil {
			continue // skip messages that fail parsing (e.g., ErrSenderNotAllowed)
		}

		// Set the S3 key and message ID on the parsed message.
		parsedMsg.S3Key = key
		parsedMsg.MessageID = messageIDFromKey(key)

		// Auto-decrypt if the body is encrypted (regardless of signing status).
		if parsedMsg.Encrypted && ssmClient != nil {
			decrypted, decErr := autoDecrypt(ctx, ssmClient, identityClient, tableName, sandboxID, parsedMsg.Body)
			if decErr == nil {
				parsedMsg.Body = decrypted
			}
			// Decryption failure is non-fatal: Body stays as ciphertext.
		}

		entries = append(entries, parsedEntry{idx: i + 1, key: key, msg: parsedMsg})
	}

	// Output.
	if jsonOut {
		return outputJSON(out, entries)
	}
	return outputTable(out, entries)
}

// autoDecrypt fetches the recipient's private key from SSM and decrypts the ciphertext.
func autoDecrypt(ctx context.Context, ssmClient EmailSSMAPI, identityClient kmaws.IdentityTableAPI, tableName, sandboxID, ciphertextB64 string) (string, error) {
	// Fetch private key from SSM.
	keyPath := fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID)
	ssmOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           strPtr(keyPath),
		WithDecryption: boolPtr(true),
	})
	if err != nil {
		return "", fmt.Errorf("fetch encryption private key from SSM: %w", err)
	}
	if ssmOut.Parameter == nil || ssmOut.Parameter.Value == nil {
		return "", fmt.Errorf("SSM parameter %s has no value", keyPath)
	}

	privBytes, err := base64.StdEncoding.DecodeString(*ssmOut.Parameter.Value)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("encryption private key has wrong length: %d (want 32)", len(privBytes))
	}
	var privKey [32]byte
	copy(privKey[:], privBytes)

	// Fetch public key from DynamoDB.
	record, fetchErr := kmaws.FetchPublicKey(ctx, identityClient, tableName, sandboxID)
	if fetchErr != nil {
		return "", fmt.Errorf("fetch encryption public key from DynamoDB: %w", fetchErr)
	}
	if record == nil || record.EncryptionPublicKeyB64 == "" {
		return "", fmt.Errorf("no encryption public key for sandbox %s", sandboxID)
	}

	pubBytes, err := base64.StdEncoding.DecodeString(record.EncryptionPublicKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode public key: %w", err)
	}
	if len(pubBytes) != 32 {
		return "", fmt.Errorf("encryption public key has wrong length: %d (want 32)", len(pubBytes))
	}
	var pubKey [32]byte
	copy(pubKey[:], pubBytes)

	// Decode ciphertext.
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}

	plaintext, decErr := kmaws.DecryptFromSender(&privKey, &pubKey, ciphertext)
	if decErr != nil {
		return "", fmt.Errorf("decrypt: %w", decErr)
	}
	return string(plaintext), nil
}

// outputJSON writes a JSON array of parsed messages to out.
func outputJSON(out io.Writer, entries []parsedEntry) error {
	var result []mailboxMessageJSON
	for _, e := range entries {
		result = append(result, mailboxMessageJSON{
			Index:       e.idx,
			MessageID:   e.msg.MessageID,
			From:        e.msg.From,
			To:          e.msg.To,
			Subject:     e.msg.Subject,
			Body:        e.msg.Body,
			SenderID:    e.msg.SenderID,
			SignatureOK: e.msg.SignatureOK,
			Encrypted:   e.msg.Encrypted,
			Plaintext:   e.msg.Plaintext,
			Attachments: len(e.msg.Attachments),
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// outputTable writes a human-readable table of messages to out.
func outputTable(out io.Writer, entries []parsedEntry) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tFROM\tSUBJECT\tSIG\tENC\tBODY PREVIEW")
	fmt.Fprintln(w, "─\t────\t───────\t───\t───\t────────────")
	for _, e := range entries {
		sigStatus := "?"
		if e.msg.Plaintext {
			sigStatus = "plain"
		} else if e.msg.SignatureOK {
			sigStatus = "OK"
		} else {
			sigStatus = "FAIL"
		}
		encStatus := "no"
		if e.msg.Encrypted {
			encStatus = "yes"
		}
		preview := e.msg.Body
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		preview = strings.ReplaceAll(preview, "\r", "")
		attachInfo := ""
		if len(e.msg.Attachments) > 0 {
			attachInfo = fmt.Sprintf(" [%d attach]", len(e.msg.Attachments))
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s%s\n",
			e.idx, e.msg.From, e.msg.Subject, sigStatus, encStatus, preview, attachInfo)
	}
	return w.Flush()
}

// selectMessageKey returns the S3 key for a message by ID filter or the latest (last) key.
func selectMessageKey(keys []string, messageIDFilter string) string {
	if len(keys) == 0 {
		return ""
	}
	if messageIDFilter == "" {
		return keys[len(keys)-1]
	}
	for _, k := range keys {
		if messageIDFromKey(k) == messageIDFilter {
			return k
		}
	}
	return ""
}

// messageIDFromKey extracts the last path segment of an S3 key as the message ID.
func messageIDFromKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) == 0 {
		return key
	}
	return parts[len(parts)-1]
}

// extractHeader does a lightweight parse of MIME headers from raw bytes
// to extract a single named header value without full parsing.
func extractHeader(rawMIME []byte, headerName string) string {
	// Only scan the header section (up to the first blank line).
	idx := bytes.Index(rawMIME, []byte("\r\n\r\n"))
	if idx == -1 {
		idx = bytes.Index(rawMIME, []byte("\n\n"))
	}
	headerSection := rawMIME
	if idx != -1 {
		headerSection = rawMIME[:idx]
	}
	prefix := []byte(strings.ToLower(headerName) + ":")
	for _, line := range bytes.Split(headerSection, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(bytes.ToLower(line), prefix) {
			return strings.TrimSpace(string(line[len(prefix):]))
		}
	}
	return ""
}

// strPtr returns a pointer to a string (helper for AWS SDK calls).
func strPtr(s string) *string { return &s }

// boolPtr returns a pointer to a bool (helper for AWS SDK calls).
func boolPtr(b bool) *bool { return &b }
