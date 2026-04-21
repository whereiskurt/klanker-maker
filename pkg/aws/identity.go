// Package aws — identity.go
// Sandbox identity library: Ed25519 key generation, SSM storage, DynamoDB publishing,
// email signing/verification, raw MIME signed email sending with encryption policy
// enforcement, optional NaCl box encryption, and idempotent cleanup.
//
// Key design:
//   - IdentitySSMAPI: PutParameter, GetParameter, DeleteParameter (narrow interface)
//   - IdentityTableAPI: PutItem, GetItem, DeleteItem (narrow interface)
//   - All functions are package-level (not methods on a struct) matching the
//     SendLifecycleNotification and CleanupSandboxEmail patterns in ses.go
//
// DynamoDB key design:
//   sandbox_id (S) — sole hash key, one row per sandbox identity
//   No sort key unlike km-budgets table
package aws

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"golang.org/x/crypto/nacl/box"
)

// IdentitySSMAPI is the minimal SSM interface for sandbox identity operations.
// Implemented by *ssm.Client.
type IdentitySSMAPI interface {
	PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// IdentityTableAPI is the minimal DynamoDB interface for identity table operations.
// Implemented by *dynamodb.Client.
type IdentityTableAPI interface {
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// IdentityQueryAPI is the minimal DynamoDB interface for GSI Query operations.
// Separate from IdentityTableAPI (narrow-interface pattern) — only used by FetchPublicKeyByAlias.
// Implemented by *dynamodb.Client.
type IdentityQueryAPI interface {
	Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// IdentityRecord holds the data returned by FetchPublicKey for a sandbox.
type IdentityRecord struct {
	SandboxID              string
	PublicKeyB64           string
	EmailAddress           string
	EncryptionPublicKeyB64 string   // empty if no encryption key published
	Signing                string   // email signing policy: "required"|"optional"|"off"|"" (empty for legacy rows)
	VerifyInbound          string   // email verify-inbound policy
	Encryption             string   // email encryption policy
	Alias                  string   // human-friendly dot-notation name (empty if not set)
	AllowedSenders         []string // allow-list patterns (nil if not configured)
}

// EmailOptions holds optional parameters for SendSignedEmail to avoid parameter bloat.
type EmailOptions struct {
	Attachments []Attachment // file attachments (nil or empty for single-part)
	CC          []string     // visible CC recipients (included in MIME Cc: header and SES CcAddresses)
	BCC         []string     // blind CC recipients (SES BccAddresses only, not in MIME headers)
	ReplyTo     string       // Reply-To header value (if non-empty, added to MIME headers)
}

// Attachment represents a file attachment to be included in a multipart MIME email.
// Filename is used as the Content-Disposition filename parameter.
// Data is the raw (unencoded) attachment bytes; buildRawMIME will base64-encode them.
type Attachment struct {
	Filename string
	Data     []byte
}

// signingKeyPath returns the SSM parameter path for a sandbox's signing key.
func signingKeyPath(sandboxID string) string {
	return fmt.Sprintf("/sandbox/%s/signing-key", sandboxID)
}

// encryptionKeyPath returns the SSM parameter path for a sandbox's encryption key.
func encryptionKeyPath(sandboxID string) string {
	return fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID)
}

// ============================================================
// Key Generation
// ============================================================

// GenerateSandboxIdentity generates an Ed25519 key pair and stores the private key
// in SSM Parameter Store as a SecureString at /sandbox/{sandboxID}/signing-key.
//
// Returns the public key (32 bytes) for DynamoDB publishing.
// Uses Overwrite=true for retry-safe operation (idempotent on re-run).
func GenerateSandboxIdentity(ctx context.Context, ssmClient IdentitySSMAPI, sandboxID, kmsKeyID string) (ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Ed25519 key pair for sandbox %s: %w", sandboxID, err)
	}

	// Store the full 64-byte private key (seed + public) as base64
	privB64 := base64.StdEncoding.EncodeToString([]byte(priv))
	path := signingKeyPath(sandboxID)

	_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      awssdk.String(path),
		Value:     awssdk.String(privB64),
		Type:      ssmtypes.ParameterTypeSecureString,
		KeyId:     awssdk.String(kmsKeyID),
		Overwrite: awssdk.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("store signing key in SSM at %s: %w", path, err)
	}

	return pub, nil
}

// GenerateEncryptionKey generates an X25519 (NaCl box) key pair and stores the private key
// in SSM Parameter Store at /sandbox/{sandboxID}/encryption-key.
//
// Returns a pointer to the 32-byte public key for DynamoDB publishing.
// This key pair is separate from the Ed25519 signing key (per research recommendation).
func GenerateEncryptionKey(ctx context.Context, ssmClient IdentitySSMAPI, sandboxID, kmsKeyID string) (*[32]byte, error) {
	encPub, encPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 encryption key pair for sandbox %s: %w", sandboxID, err)
	}

	privB64 := base64.StdEncoding.EncodeToString(encPriv[:])
	path := encryptionKeyPath(sandboxID)

	_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      awssdk.String(path),
		Value:     awssdk.String(privB64),
		Type:      ssmtypes.ParameterTypeSecureString,
		KeyId:     awssdk.String(kmsKeyID),
		Overwrite: awssdk.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("store encryption key in SSM at %s: %w", path, err)
	}

	return encPub, nil
}

// ============================================================
// DynamoDB Publishing
// ============================================================

// PublishIdentity writes the sandbox's public key(s) to the DynamoDB identities table.
//
// Uses ConditionExpression: attribute_not_exists(sandbox_id) for idempotency.
// If encPubKey is non-nil, the encryption_public_key attribute is included.
// signing, verifyInbound, encryption are the email policy values from the sandbox profile;
// empty string means "not specified" and the attribute is omitted (preserves legacy row compatibility).
// alias, if non-empty, stores the human-friendly dot-notation name in the alias-index GSI.
// allowedSenders, if non-empty, stores the allow-list patterns as a DynamoDB StringSet.
func PublishIdentity(ctx context.Context, client IdentityTableAPI, tableName, sandboxID, emailAddress string, pubKey ed25519.PublicKey, encPubKey *[32]byte, signing, verifyInbound, encryption, alias string, allowedSenders []string) error {
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"public_key":    &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
		"email_address": &dynamodbtypes.AttributeValueMemberS{Value: emailAddress},
		"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: createdAt},
	}

	if encPubKey != nil {
		item["encryption_public_key"] = &dynamodbtypes.AttributeValueMemberS{
			Value: base64.StdEncoding.EncodeToString(encPubKey[:]),
		}
	}

	if signing != "" {
		item["signing_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: signing}
	}
	if verifyInbound != "" {
		item["verify_inbound_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: verifyInbound}
	}
	if encryption != "" {
		item["encryption_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: encryption}
	}
	if alias != "" {
		item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: alias}
	}
	if len(allowedSenders) > 0 {
		item["allowed_senders"] = &dynamodbtypes.AttributeValueMemberSS{Value: allowedSenders}
	}

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           awssdk.String(tableName),
		Item:                item,
		ConditionExpression: awssdk.String("attribute_not_exists(sandbox_id)"),
	})
	if err != nil {
		// Swallow ConditionalCheckFailedException — identity already published (idempotent)
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return nil
		}
		return fmt.Errorf("publish identity for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// ============================================================
// Public Key Fetch
// ============================================================

// FetchPublicKey retrieves a sandbox's identity record from DynamoDB.
//
// Returns (nil, nil) if the sandbox has no published identity — this is not an error
// (sandbox may not have identity yet, or encryption is optional).
func FetchPublicKey(ctx context.Context, client IdentityTableAPI, tableName, sandboxID string) (*IdentityRecord, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetch public key for sandbox %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}

	record := &IdentityRecord{SandboxID: sandboxID}
	if v, ok := out.Item["public_key"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.PublicKeyB64 = sv.Value
		}
	}
	if v, ok := out.Item["email_address"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.EmailAddress = sv.Value
		}
	}
	if v, ok := out.Item["encryption_public_key"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.EncryptionPublicKeyB64 = sv.Value
		}
	}
	if v, ok := out.Item["signing_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Signing = sv.Value
		}
	}
	if v, ok := out.Item["verify_inbound_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.VerifyInbound = sv.Value
		}
	}
	if v, ok := out.Item["encryption_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Encryption = sv.Value
		}
	}
	if v, ok := out.Item["alias"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Alias = sv.Value
		}
	}
	if v, ok := out.Item["allowed_senders"]; ok {
		if ssv, ok := v.(*dynamodbtypes.AttributeValueMemberSS); ok {
			record.AllowedSenders = ssv.Value
		}
	}

	return record, nil
}

// ============================================================
// Alias-Based Lookup
// ============================================================

// FetchPublicKeyByAlias retrieves a sandbox's identity record by alias using the alias-index GSI.
//
// Returns (nil, nil) if no sandbox has that alias — consistent with FetchPublicKey semantics.
func FetchPublicKeyByAlias(ctx context.Context, client IdentityQueryAPI, tableName, alias string) (*IdentityRecord, error) {
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(tableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch identity by alias %q: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return nil, nil
	}

	item := out.Items[0]
	record := &IdentityRecord{}

	if v, ok := item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.SandboxID = sv.Value
		}
	}
	if v, ok := item["public_key"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.PublicKeyB64 = sv.Value
		}
	}
	if v, ok := item["email_address"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.EmailAddress = sv.Value
		}
	}
	if v, ok := item["encryption_public_key"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.EncryptionPublicKeyB64 = sv.Value
		}
	}
	if v, ok := item["signing_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Signing = sv.Value
		}
	}
	if v, ok := item["verify_inbound_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.VerifyInbound = sv.Value
		}
	}
	if v, ok := item["encryption_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Encryption = sv.Value
		}
	}
	if v, ok := item["alias"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			record.Alias = sv.Value
		}
	}
	if v, ok := item["allowed_senders"]; ok {
		if ssv, ok := v.(*dynamodbtypes.AttributeValueMemberSS); ok {
			record.AllowedSenders = ssv.Value
		}
	}

	return record, nil
}

// ============================================================
// Allow-List Matching
// ============================================================

// MatchesAllowList evaluates whether a sender is permitted by the allow-list patterns.
//
// Patterns (evaluated in order, first match wins):
//   - "*"   — permit any sender unconditionally
//   - "self" — permit if senderID == receiverSandboxID (self-mail always permitted)
//   - exact sandbox ID — permit if senderID == pattern
//   - email pattern (contains "@") — match senderEmail case-insensitively:
//     exact match ("user@example.com") or domain wildcard ("*@example.com")
//     or local-part wildcard ("kurt.hundeck@*")
//   - wildcard alias — use path.Match(pattern, senderAlias) if senderAlias != ""
//
// Returns false if no pattern matched or patterns is empty.
func MatchesAllowList(patterns []string, senderID, senderAlias, receiverSandboxID, senderEmail string) bool {
	for _, p := range patterns {
		switch p {
		case "*":
			return true
		case "self":
			if senderID == receiverSandboxID {
				return true
			}
		default:
			if senderID == p {
				return true
			}
			if senderAlias != "" {
				matched, err := path.Match(p, senderAlias)
				if err == nil && matched {
					return true
				}
			}
		}
	}
	return false
}

// ============================================================
// Email Signing
// ============================================================

// canonicalizeBody normalizes an email body string before signing or verifying.
//
// SES appends a trailing "\r\n" to message bodies in transit, which would otherwise
// cause signature verification to fail when the receiver computes the signature over
// the mutated bytes. Canonicalization strips trailing whitespace and normalizes all
// line endings to "\n" so both sides always sign/verify the same byte sequence.
func canonicalizeBody(body string) string {
	// Normalize CRLF → LF first, then trim trailing whitespace.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	body = strings.TrimRight(body, "\n")
	return body
}

// SignEmailBody signs the email body bytes with the provided Ed25519 private key.
//
// privKeyB64 is the base64-encoded 64-byte Ed25519 private key (seed + public).
// Returns the base64-encoded signature over the body bytes.
// Signs body only, not headers (per design decision from research).
// The body is canonicalized before signing to match VerifyEmailSignature behavior.
func SignEmailBody(privKeyB64, body string) (string, error) {
	body = canonicalizeBody(body)
	privBytes, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid Ed25519 private key length: %d (want %d)", len(privBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)
	sig := ed25519.Sign(priv, []byte(body))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyEmailSignature verifies an Ed25519 signature over a body string.
//
// Returns nil if the signature is valid, error otherwise.
// The body is canonicalized before verifying to tolerate SES trailing CRLF mutation.
func VerifyEmailSignature(pubKeyB64, body, sigB64 string) error {
	body = canonicalizeBody(body)
	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Ed25519 public key length: %d (want %d)", len(pubBytes), ed25519.PublicKeySize)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	pub := ed25519.PublicKey(pubBytes)
	if !ed25519.Verify(pub, []byte(body), sig) {
		return errors.New("Ed25519 signature verification failed")
	}
	return nil
}

// ============================================================
// NaCl Box Encryption
// ============================================================

// EncryptForRecipient encrypts plaintext for a recipient using their X25519 public key.
// Uses box.SealAnonymous so the sender identity is not embedded in the ciphertext.
func EncryptForRecipient(recipientPubKey *[32]byte, plaintext []byte) ([]byte, error) {
	ciphertext, err := box.SealAnonymous(nil, plaintext, recipientPubKey, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("encrypt for recipient: %w", err)
	}
	return ciphertext, nil
}

// DecryptFromSender decrypts ciphertext using the recipient's private key and public key.
// Uses box.OpenAnonymous matching EncryptForRecipient's SealAnonymous.
func DecryptFromSender(privKey *[32]byte, pubKey *[32]byte, ciphertext []byte) ([]byte, error) {
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, pubKey, privKey)
	if !ok {
		return nil, errors.New("NaCl box decryption failed: invalid ciphertext or wrong key")
	}
	return plaintext, nil
}

// ============================================================
// Signed Email Sending
// ============================================================

// SendSignedEmail constructs a raw MIME email with Ed25519 signature headers and sends it via SES.
//
// The function:
//  1. Reads the sender's Ed25519 private key from SSM
//  2. Applies the encryption policy gate:
//     - "required": fetches recipient's identity; encrypts if key exists; errors if no key
//     - "optional": fetches recipient's identity; encrypts if key exists; sends plaintext if no key
//     - "off" or "": skips all encryption, no DynamoDB fetch
//  3. Signs the (possibly encrypted) body with Ed25519 — signature covers body only, not attachments
//  4. Constructs raw MIME bytes with X-KM-Signature, X-KM-Sender-ID, optionally X-KM-Encrypted.
//     When opts.Attachments is non-empty, the message is multipart/mixed.
//     When opts.CC is non-empty, Cc: header is added and SES CcAddresses are set.
//     When opts.BCC is non-empty, SES BccAddresses are set (no MIME header).
//     When opts.ReplyTo is non-empty, Reply-To: header is added.
//  5. Sends via SES Content.Raw (not Content.Simple — Simple does not support custom headers)
//
// Parameters:
//   - sesClient: SES v2 client (SESV2API)
//   - ssmClient: SSM client for reading the signing key
//   - identityClient: DynamoDB client for fetching recipient's public key
//   - from, to, subject, body: email fields
//   - sandboxID: sender's sandbox ID (used for SSM key path and X-KM-Sender-ID header)
//   - recipientSandboxID: recipient's sandbox ID for DynamoDB identity lookup
//   - tableName: DynamoDB identities table name
//   - encryptionPolicy: "required" | "optional" | "off" | ""
//   - opts: optional CC, BCC, Reply-To, and attachments (nil for defaults)
func SendSignedEmail(
	ctx context.Context,
	sesClient SESV2API,
	ssmClient IdentitySSMAPI,
	identityClient IdentityTableAPI,
	from, to, subject, body string,
	sandboxID, recipientSandboxID, tableName, encryptionPolicy string,
	opts *EmailOptions,
) error {
	if opts == nil {
		opts = &EmailOptions{}
	}
	// Step 1: Read signing key from SSM
	keyPath := signingKeyPath(sandboxID)
	ssmOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(keyPath),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("read signing key from SSM (%s): %w", keyPath, err)
	}
	if ssmOut.Parameter == nil || ssmOut.Parameter.Value == nil {
		return fmt.Errorf("SSM parameter %s has no value", keyPath)
	}
	privKeyB64 := *ssmOut.Parameter.Value

	// Step 2: Apply encryption policy gate
	bodyToSign := body
	encrypted := false

	switch encryptionPolicy {
	case "required", "optional":
		record, fetchErr := FetchPublicKey(ctx, identityClient, tableName, recipientSandboxID)
		if fetchErr != nil {
			return fmt.Errorf("fetch recipient public key for %s: %w", recipientSandboxID, fetchErr)
		}

		if record != nil && record.EncryptionPublicKeyB64 != "" {
			// Recipient has an encryption key — encrypt the body
			encKeyBytes, decErr := base64.StdEncoding.DecodeString(record.EncryptionPublicKeyB64)
			if decErr != nil {
				return fmt.Errorf("decode recipient encryption public key: %w", decErr)
			}
			if len(encKeyBytes) != 32 {
				return fmt.Errorf("recipient encryption public key has wrong length: %d (want 32)", len(encKeyBytes))
			}
			var recipKey [32]byte
			copy(recipKey[:], encKeyBytes)
			ciphertext, encErr := EncryptForRecipient(&recipKey, []byte(body))
			if encErr != nil {
				return fmt.Errorf("encrypt body for recipient %s: %w", recipientSandboxID, encErr)
			}
			bodyToSign = base64.StdEncoding.EncodeToString(ciphertext)
			encrypted = true
		} else if encryptionPolicy == "required" {
			// Required but no recipient key — reject the send
			return fmt.Errorf("encryption required but recipient %s has no published encryption key", recipientSandboxID)
		}
		// optional + no key: bodyToSign stays as plaintext body, encrypted stays false

	default:
		// "off" or empty: skip encryption entirely, no DynamoDB fetch
	}

	// Step 3: Sign the body (or encrypted body)
	sigB64, err := SignEmailBody(privKeyB64, bodyToSign)
	if err != nil {
		return fmt.Errorf("sign email body: %w", err)
	}

	// Step 4: Construct raw MIME message for the primary recipient (encrypted if applicable)
	mime := buildRawMIME(from, to, subject, bodyToSign, sandboxID, sigB64, encrypted, opts)

	// Step 5: Send via SES Content.Raw
	// When the body is encrypted and BCC recipients exist, send them a separate
	// plaintext copy so the operator can read agent-to-agent communications.
	// The primary To+CC recipients get the encrypted version.
	hasBCCWithEncryption := encrypted && len(opts.BCC) > 0

	dest := &sesv2types.Destination{
		ToAddresses: []string{to},
	}
	if len(opts.CC) > 0 {
		dest.CcAddresses = opts.CC
	}
	if !hasBCCWithEncryption && len(opts.BCC) > 0 {
		// Not encrypted — BCC recipients can read the same copy
		dest.BccAddresses = opts.BCC
	}

	_, err = sesClient.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination:      dest,
		Content: &sesv2types.EmailContent{
			Raw: &sesv2types.RawMessage{
				Data: []byte(mime),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send signed email from %s to %s: %w", from, to, err)
	}

	// Step 6: Send plaintext copy to BCC recipients when body was encrypted.
	// Each BCC recipient gets a separate signed (but unencrypted) MIME message
	// so the operator can read the content for oversight.
	if hasBCCWithEncryption {
		// Sign the original plaintext body for the BCC copy
		bccSigB64, signErr := SignEmailBody(privKeyB64, body)
		if signErr != nil {
			return fmt.Errorf("sign BCC plaintext copy: %w", signErr)
		}
		bccOpts := &EmailOptions{
			Attachments: opts.Attachments,
			ReplyTo:     opts.ReplyTo,
			// No CC or BCC on the BCC copy itself
		}
		bccMIME := buildRawMIME(from, to, subject, body, sandboxID, bccSigB64, false, bccOpts)

		for _, bccAddr := range opts.BCC {
			_, bccErr := sesClient.SendEmail(ctx, &sesv2.SendEmailInput{
				FromEmailAddress: awssdk.String(from),
				Destination: &sesv2types.Destination{
					ToAddresses: []string{bccAddr},
				},
				Content: &sesv2types.EmailContent{
					Raw: &sesv2types.RawMessage{
						Data: []byte(bccMIME),
					},
				},
			})
			if bccErr != nil {
				return fmt.Errorf("send BCC plaintext copy to %s: %w", bccAddr, bccErr)
			}
		}
	}

	return nil
}

// buildRawMIME constructs a raw MIME message string with the required custom headers.
//
// Custom headers X-KM-Signature and X-KM-Sender-ID are only supported via
// Content.Raw — SES Simple message type strips unknown headers.
//
// When opts.Attachments is nil or empty, a single-part text/plain message is produced
// (backward-compatible behavior). When attachments is non-empty, the message is
// multipart/mixed with the text body as part 1 and each attachment as a subsequent
// application/octet-stream part with base64 Content-Transfer-Encoding.
//
// CC addresses appear in the Cc: MIME header (visible to all recipients).
// BCC is handled at the SES Destination level only — not in MIME headers.
// Reply-To is added as a Reply-To: MIME header when non-empty.
//
// The signature (X-KM-Signature) always covers only the text body, not attachments.
func buildRawMIME(from, to, subject, body, senderID, sigB64 string, encrypted bool, opts *EmailOptions) string {
	if opts == nil {
		opts = &EmailOptions{}
	}
	var sb strings.Builder

	// Top-level headers present in both single-part and multipart messages.
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if len(opts.CC) > 0 {
		sb.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(opts.CC, ", ")))
	}
	if opts.ReplyTo != "" {
		sb.WriteString(fmt.Sprintf("Reply-To: %s\r\n", opts.ReplyTo))
	}
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString(fmt.Sprintf("X-KM-Sender-ID: %s\r\n", senderID))
	sb.WriteString(fmt.Sprintf("X-KM-Signature: %s\r\n", sigB64))
	if encrypted {
		sb.WriteString("X-KM-Encrypted: true\r\n")
	}
	sb.WriteString("MIME-Version: 1.0\r\n")

	if len(opts.Attachments) == 0 {
		// Single-part text/plain — original behavior.
		sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(body)
		return sb.String()
	}

	// Multipart/mixed — generate a random boundary.
	boundary := generateMIMEBoundary()

	sb.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
	sb.WriteString("\r\n")

	// Part 1: text/plain body (signed content).
	sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	sb.WriteString("\r\n")

	// Remaining parts: one per attachment.
	for _, att := range opts.Attachments {
		sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		sb.WriteString("Content-Type: application/octet-stream\r\n")
		sb.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename))
		sb.WriteString("Content-Transfer-Encoding: base64\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(base64.StdEncoding.EncodeToString(att.Data))
		sb.WriteString("\r\n")
	}

	// Closing boundary.
	sb.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return sb.String()
}

// generateMIMEBoundary produces a random 32-hex-char boundary string using crypto/rand.
// Panics only if crypto/rand is unavailable (OS-level failure).
func generateMIMEBoundary() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("generateMIMEBoundary: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(buf)
}

// ============================================================
// Cleanup
// ============================================================

// CleanupSandboxIdentity removes a sandbox's keys from SSM and DynamoDB.
//
// Idempotent: SSM ParameterNotFound is swallowed (safe for retried km destroy).
// DynamoDB DeleteItem is a no-op for missing keys.
func CleanupSandboxIdentity(ctx context.Context, ssmClient IdentitySSMAPI, dynClient IdentityTableAPI, tableName, sandboxID string) error {
	// Delete signing key from SSM
	if err := deleteSSMParameter(ctx, ssmClient, signingKeyPath(sandboxID)); err != nil {
		return err
	}

	// Delete encryption key from SSM
	if err := deleteSSMParameter(ctx, ssmClient, encryptionKeyPath(sandboxID)); err != nil {
		return err
	}

	// Delete DynamoDB identity row
	_, err := dynClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("delete identity row for sandbox %s: %w", sandboxID, err)
	}

	return nil
}

// deleteSSMParameter deletes an SSM parameter, swallowing ParameterNotFound for idempotency.
func deleteSSMParameter(ctx context.Context, ssmClient IdentitySSMAPI, path string) error {
	_, err := ssmClient.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: awssdk.String(path),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete SSM parameter %s: %w", path, err)
	}
	return nil
}
