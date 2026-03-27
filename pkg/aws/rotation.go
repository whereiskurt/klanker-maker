// Package aws — rotation.go
// Core credential rotation library for sandbox platform.
//
// Provides building-block functions for the `km roll creds` command:
//   - RotateSandboxIdentity: rotate Ed25519 signing key, update DynamoDB unconditionally
//   - RotateProxyCACert: generate new ECDSA P-256 CA cert+key, upload to S3
//   - ReEncryptSSMParameters: re-encrypt all SSM params under a sandbox path
//   - UpdateIdentityPublicKey: unconditional DynamoDB PutItem (NOT attribute_not_exists)
//   - WriteRotationAudit: write structured JSON audit event to CloudWatch
//
// All functions use narrow interfaces for mock-testable unit tests.
// Follows the doctor.go/identity.go narrow-interface pattern.
package aws

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ============================================================
// Narrow Interfaces
// ============================================================

// RotationSSMAPI is the minimal SSM interface for rotation operations.
// Embeds IdentitySSMAPI (PutParameter, GetParameter, DeleteParameter) and adds
// GetParametersByPath for bulk re-encryption.
// Implemented by *ssm.Client.
type RotationSSMAPI interface {
	IdentitySSMAPI
	GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// RotationS3API is the minimal S3 interface for CA cert upload and retrieval.
// Implemented by *s3.Client.
type RotationS3API interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// RotationCWAPI is the minimal CloudWatch Logs interface for audit logging.
// Subset of CWLogsAPI — only what WriteRotationAudit needs.
// Implemented by *cloudwatchlogs.Client.
type RotationCWAPI interface {
	CreateLogGroup(ctx context.Context, input *cloudwatchlogs.CreateLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error)
	CreateLogStream(ctx context.Context, input *cloudwatchlogs.CreateLogStreamInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error)
	PutLogEvents(ctx context.Context, input *cloudwatchlogs.PutLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error)
}

// ============================================================
// Types
// ============================================================

// RotationAuditEvent is the structured JSON audit record written to CloudWatch
// for every credential rotation action. Includes before/after fingerprints
// for compliance traceability.
type RotationAuditEvent struct {
	Event     string    `json:"event"`      // e.g. "rotate-identity", "rotate-proxy-ca", "re-encrypt-ssm"
	SandboxID string    `json:"sandbox_id"` // empty for platform-level events (proxy CA)
	KeyType   string    `json:"key_type"`   // "ed25519", "ecdsa-p256", "ssm-params"
	BeforeFP  string    `json:"before_fp"`  // fingerprint of old credential (empty if fresh)
	AfterFP   string    `json:"after_fp"`   // fingerprint of new credential
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"` // non-empty on failure
}

// ============================================================
// Fingerprint Helpers
// ============================================================

// ed25519Fingerprint produces a sha256:XXXXXXXXXXXXXXXX fingerprint from an Ed25519 public key.
// The fingerprint is the first 8 bytes of the SHA-256 hash of the raw key bytes,
// encoded as lowercase hex with a "sha256:" prefix.
func ed25519Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return fmt.Sprintf("sha256:%x", sum[:8])
}

// ecdsaFingerprint produces a sha256:XXXXXXXXXXXXXXXX fingerprint from DER-encoded cert bytes.
// Used to fingerprint the old and new proxy CA cert for audit logging.
func ecdsaFingerprint(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return fmt.Sprintf("sha256:%x", sum[:8])
}

// pemCertFingerprint extracts the DER cert from a PEM block and returns its fingerprint.
// Returns empty string if the PEM cannot be parsed (e.g. old cert missing or malformed).
func pemCertFingerprint(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	return ecdsaFingerprint(block.Bytes)
}

// ============================================================
// UpdateIdentityPublicKey
// ============================================================

// UpdateIdentityPublicKey writes a new Ed25519 public key to the DynamoDB identities table
// using an unconditional PutItem. This OVERWRITES any existing record.
//
// CRITICAL: Unlike PublishIdentity, this function does NOT set ConditionExpression
// (no attribute_not_exists). It is safe to call on existing sandboxes during rotation.
//
// The function reads the current record first via FetchPublicKey to preserve existing
// fields (alias, allowedSenders, email_address, policy fields). Only public_key and
// optionally encryption_public_key are updated.
//
// Parameters:
//   - client: IdentityTableAPI (PutItem + GetItem)
//   - tableName: DynamoDB km-identities table
//   - sandboxID: sandbox identifier (hash key)
//   - pubKey: new Ed25519 public key to publish
//   - encPubKey: optional new NaCl box encryption public key (nil to preserve existing)
func UpdateIdentityPublicKey(ctx context.Context, client IdentityTableAPI, tableName, sandboxID string, pubKey ed25519.PublicKey, encPubKey *[32]byte) error {
	// Read current record to preserve policy fields, alias, email, allowedSenders.
	existing, err := FetchPublicKey(ctx, client, tableName, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch existing identity for sandbox %s: %w", sandboxID, err)
	}

	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	updatedAt := time.Now().UTC().Format(time.RFC3339)

	// Build item — start with the new key and merge preserved fields.
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"public_key": &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
		"updated_at": &dynamodbtypes.AttributeValueMemberS{Value: updatedAt},
	}

	// Preserve fields from existing record if present.
	if existing != nil {
		if existing.EmailAddress != "" {
			item["email_address"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.EmailAddress}
		}
		if existing.Signing != "" {
			item["signing_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.Signing}
		}
		if existing.VerifyInbound != "" {
			item["verify_inbound_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.VerifyInbound}
		}
		if existing.Encryption != "" {
			item["encryption_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.Encryption}
		}
		if existing.Alias != "" {
			item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.Alias}
		}
		if len(existing.AllowedSenders) > 0 {
			item["allowed_senders"] = &dynamodbtypes.AttributeValueMemberSS{Value: existing.AllowedSenders}
		}
		// Preserve existing encryption key if caller did not supply a new one.
		if encPubKey == nil && existing.EncryptionPublicKeyB64 != "" {
			item["encryption_public_key"] = &dynamodbtypes.AttributeValueMemberS{Value: existing.EncryptionPublicKeyB64}
		}
	}

	// Apply new encryption public key if provided.
	if encPubKey != nil {
		item["encryption_public_key"] = &dynamodbtypes.AttributeValueMemberS{
			Value: base64.StdEncoding.EncodeToString(encPubKey[:]),
		}
	}

	// Unconditional PutItem — NO ConditionExpression (unlike PublishIdentity).
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(tableName),
		Item:      item,
		// ConditionExpression intentionally omitted — this is a rotation (overwrite), not a creation.
	})
	if err != nil {
		return fmt.Errorf("update identity public key for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// ============================================================
// RotateSandboxIdentity
// ============================================================

// RotateSandboxIdentity rotates the Ed25519 signing key for a sandbox.
//
// Steps:
//  1. Fetch old public key from DynamoDB via FetchPublicKey (nil = fresh sandbox)
//  2. Generate new Ed25519 key pair via GenerateSandboxIdentity (stores private key in SSM)
//  3. Update DynamoDB via UpdateIdentityPublicKey (unconditional PutItem)
//  4. Return old and new fingerprints
//
// Returns:
//   - oldFP: fingerprint of old key (empty string for fresh sandboxes with no existing key)
//   - newFP: fingerprint of new key
//   - err: non-nil on failure
func RotateSandboxIdentity(ctx context.Context, ssmClient RotationSSMAPI, dynamoClient IdentityTableAPI, sandboxID, kmsKeyID, tableName string) (oldFP, newFP string, err error) {
	// Step 1: Fetch old public key (nil is valid — fresh sandbox).
	existing, err := FetchPublicKey(ctx, dynamoClient, tableName, sandboxID)
	if err != nil {
		return "", "", fmt.Errorf("fetch old public key for sandbox %s: %w", sandboxID, err)
	}

	if existing != nil && existing.PublicKeyB64 != "" {
		oldPubBytes, decErr := base64.StdEncoding.DecodeString(existing.PublicKeyB64)
		if decErr != nil {
			return "", "", fmt.Errorf("decode old public key for sandbox %s: %w", sandboxID, decErr)
		}
		oldFP = ed25519Fingerprint(ed25519.PublicKey(oldPubBytes))
	}
	// If existing is nil or PublicKeyB64 is empty, oldFP stays as "" (fresh sandbox).

	// Step 2: Generate new key pair and store private key in SSM.
	// GenerateSandboxIdentity returns the new public key.
	newPub, err := GenerateSandboxIdentity(ctx, ssmClient, sandboxID, kmsKeyID)
	if err != nil {
		return "", "", fmt.Errorf("generate new signing key for sandbox %s: %w", sandboxID, err)
	}

	newFP = ed25519Fingerprint(newPub)

	// Step 3: Update DynamoDB with the new public key (unconditional overwrite).
	if err := UpdateIdentityPublicKey(ctx, dynamoClient, tableName, sandboxID, newPub, nil); err != nil {
		return "", "", fmt.Errorf("update DynamoDB identity for sandbox %s: %w", sandboxID, err)
	}

	return oldFP, newFP, nil
}

// ============================================================
// RotateProxyCACert
// ============================================================

// RotateProxyCACert generates a new ECDSA P-256 CA certificate and private key,
// uploads both to S3, and returns the old and new cert fingerprints.
//
// S3 upload paths:
//   - sidecars/km-proxy-ca.crt (PEM-encoded certificate)
//   - sidecars/km-proxy-ca.key (PEM-encoded EC private key)
//
// The old cert is fetched from S3 before rotation for fingerprint comparison.
// If the old cert does not exist (fresh setup), oldFP is returned as empty string.
//
// Returns:
//   - oldFP: fingerprint of old cert (empty if no old cert found)
//   - newFP: fingerprint of new cert
//   - err: non-nil on failure
func RotateProxyCACert(ctx context.Context, s3Client RotationS3API, bucket string) (oldFP, newFP string, err error) {
	const (
		certKey = "sidecars/km-proxy-ca.crt"
		privKey = "sidecars/km-proxy-ca.key"
	)

	// Step 1: Fetch old cert for fingerprint (ignore error if not found).
	oldOut, getErr := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(certKey),
	})
	if getErr == nil && oldOut.Body != nil {
		oldPEM, readErr := io.ReadAll(oldOut.Body)
		oldOut.Body.Close()
		if readErr == nil {
			oldFP = pemCertFingerprint(oldPEM)
		}
	}
	// getErr or readErr → oldFP stays "" (acceptable, old cert may not exist)

	// Step 2: Generate ECDSA P-256 private key.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ECDSA P-256 CA key: %w", err)
	}

	// Step 3: Create self-signed CA certificate (5-year validity).
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "km-platform-ca"},
		NotBefore:    time.Now().UTC(),
		NotAfter:     time.Now().UTC().Add(5 * 365 * 24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create CA certificate: %w", err)
	}

	// Step 4: Marshal private key to DER.
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal CA private key: %w", err)
	}

	// Step 5: PEM-encode cert and key.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Step 6: Upload cert to S3.
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(certKey),
		Body:        bytes.NewReader(certPEM),
		ContentType: awssdk.String("application/x-pem-file"),
	})
	if err != nil {
		return "", "", fmt.Errorf("upload CA cert to s3://%s/%s: %w", bucket, certKey, err)
	}

	// Step 7: Upload private key to S3.
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(privKey),
		Body:        bytes.NewReader(keyPEM),
		ContentType: awssdk.String("application/x-pem-file"),
	})
	if err != nil {
		return "", "", fmt.Errorf("upload CA key to s3://%s/%s: %w", bucket, privKey, err)
	}

	newFP = ecdsaFingerprint(certDER)
	return oldFP, newFP, nil
}

// ============================================================
// ReEncryptSSMParameters
// ============================================================

// ReEncryptSSMParameters re-encrypts all SSM SecureString parameters under
// a sandbox's path prefix (/sandbox/{sandboxID}/) with the given KMS key.
//
// This is used after a KMS key rotation to re-wrap all ciphertext.
// Uses GetParametersByPath with Recursive=true and WithDecryption=true,
// then re-writes each parameter with PutParameter(Overwrite=true).
//
// Returns the count of parameters re-encrypted, or an error.
func ReEncryptSSMParameters(ctx context.Context, ssmClient RotationSSMAPI, sandboxID, kmsKeyID string) (int, error) {
	path := fmt.Sprintf("/sandbox/%s/", sandboxID)

	out, err := ssmClient.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:           awssdk.String(path),
		Recursive:      awssdk.Bool(true),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return 0, fmt.Errorf("get parameters by path %s for sandbox %s: %w", path, sandboxID, err)
	}

	count := 0
	for _, param := range out.Parameters {
		if param.Name == nil || param.Value == nil {
			continue
		}

		paramType := param.Type
		if paramType == "" {
			paramType = ssmtypes.ParameterTypeSecureString
		}

		_, putErr := ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      param.Name,
			Value:     param.Value,
			Type:      paramType,
			KeyId:     awssdk.String(kmsKeyID),
			Overwrite: awssdk.Bool(true),
		})
		if putErr != nil {
			return count, fmt.Errorf("re-encrypt SSM parameter %s for sandbox %s: %w", *param.Name, sandboxID, putErr)
		}
		count++
	}

	return count, nil
}

// ============================================================
// WriteRotationAudit
// ============================================================

// WriteRotationAudit writes a structured JSON RotationAuditEvent to the
// /km/credential-rotation CloudWatch log group. Creates the log group and
// stream idempotently.
//
// Log stream name format: {YYYY-MM-DD}/{event.Event}
// This allows grouping rotation events by date and type.
//
// The RotationAuditEvent is JSON-marshaled and written as a single log event.
func WriteRotationAudit(ctx context.Context, cwClient RotationCWAPI, event RotationAuditEvent) error {
	const logGroup = "/km/credential-rotation"

	// Stream name includes date + event type for easy filtering.
	date := event.Timestamp.UTC().Format("2006-01-02")
	if date == "0001-01-01" {
		// Fallback for zero-value timestamp.
		date = time.Now().UTC().Format("2006-01-02")
	}
	logStream := fmt.Sprintf("%s/%s", date, event.Event)

	// Create log group (idempotent — swallow ResourceAlreadyExistsException).
	_, err := cwClient.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: awssdk.String(logGroup),
	})
	if err != nil && !isCWAlreadyExists(err) {
		return fmt.Errorf("create log group %q for rotation audit: %w", logGroup, err)
	}

	// Create log stream (idempotent).
	_, err = cwClient.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  awssdk.String(logGroup),
		LogStreamName: awssdk.String(logStream),
	})
	if err != nil && !isCWAlreadyExists(err) {
		return fmt.Errorf("create log stream %q/%q for rotation audit: %w", logGroup, logStream, err)
	}

	// Marshal the audit event to JSON.
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal rotation audit event: %w", err)
	}

	// Write the audit event.
	_, err = cwClient.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  awssdk.String(logGroup),
		LogStreamName: awssdk.String(logStream),
		LogEvents: []cwltypes.InputLogEvent{
			{
				Timestamp: awssdk.Int64(event.Timestamp.UnixMilli()),
				Message:   awssdk.String(string(eventJSON)),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("put rotation audit log event to %q/%q: %w", logGroup, logStream, err)
	}

	return nil
}

// isCWAlreadyExists reports whether err is a CloudWatch ResourceAlreadyExistsException.
// Local version to avoid tight coupling with cloudwatch.go's isAlreadyExists.
func isCWAlreadyExists(err error) bool {
	return isAlreadyExists(err)
}
