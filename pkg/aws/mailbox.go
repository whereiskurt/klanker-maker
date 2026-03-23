// Package aws — mailbox.go
// Sandbox mailbox reader library: listing, reading, and parsing signed MIME emails from S3.
//
// Key design:
//   - MailboxS3API: narrow interface (ListObjectsV2 + GetObject)
//   - ListMailboxMessages: returns all S3 keys under mail/ prefix without filtering
//   - ReadMessage: fetches raw MIME bytes from S3
//   - ParseSignedMessage: parses MIME headers, verifies Ed25519 signature, enforces allow-list
//   - Self-mail (senderID == receiverSandboxID) always permitted regardless of allowedSenders
//   - ErrSenderNotAllowed: typed sentinel for caller discrimination
package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ErrSenderNotAllowed is returned by ParseSignedMessage when the message sender
// is not on the receiver's allow-list and is not self-mail.
var ErrSenderNotAllowed = errors.New("sender not on allow-list")

// MailboxS3API is the narrow S3 interface for mailbox read operations.
// Implemented by *s3.Client.
type MailboxS3API interface {
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// MailboxMessage holds the parsed content of a sandbox email message.
type MailboxMessage struct {
	MessageID   string // extracted from S3 key (last path segment)
	S3Key       string // full S3 object key
	From        string
	To          string
	Subject     string
	Body        string // body string (ciphertext if Encrypted=true; caller must call DecryptFromSender separately)
	SenderID    string // X-KM-Sender-ID header value
	SignatureOK bool   // true if Ed25519 signature verified successfully
	Encrypted   bool   // true if X-KM-Encrypted: true was present
	Plaintext   bool   // true if no X-KM-Signature header (unsigned message)
}

// ListMailboxMessages lists all S3 object keys under the mail/ prefix for a sandbox.
//
// Returns all keys without filtering by recipient. Caller is responsible for
// per-message allow-list filtering via ParseSignedMessage.
// Handles pagination for buckets with more than 1000 objects.
func ListMailboxMessages(ctx context.Context, client MailboxS3API, bucket, sandboxID, emailDomain string) ([]string, error) {
	prefix := "mail/"
	var keys []string
	var continuationToken *string

	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list mailbox messages (bucket=%s, prefix=%s): %w", bucket, prefix, err)
		}

		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	return keys, nil
}

// ReadMessage fetches the raw MIME bytes for a single message from S3.
//
// Returns an error (including NoSuchKey) if the object does not exist.
func ReadMessage(ctx context.Context, client MailboxS3API, bucket, s3Key string) ([]byte, error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return nil, fmt.Errorf("read message (bucket=%s, key=%s): %w", bucket, s3Key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read message body (key=%s): %w", s3Key, err)
	}
	return data, nil
}

// ParseSignedMessage parses a raw MIME email, verifies the Ed25519 signature,
// and enforces the receiver's allow-list.
//
// Signature handling:
//   - If X-KM-Signature is present: call VerifyEmailSignature; set SignatureOK accordingly.
//     Signature failure does NOT return an error — set SignatureOK=false, let caller decide.
//   - If X-KM-Signature is absent: set Plaintext=true.
//
// Allow-list enforcement:
//   - Self-mail (X-KM-Sender-ID == receiverSandboxID): always permitted.
//   - Otherwise: MatchesAllowList(allowedSenders, senderID, "", receiverSandboxID).
//     Returns ErrSenderNotAllowed if not matched.
//
// Note: Actual NaCl decryption is not performed here. When Encrypted=true, Body contains
// the raw ciphertext and the caller must call DecryptFromSender separately.
func ParseSignedMessage(rawMIME []byte, receiverSandboxID, pubKeyB64 string, allowedSenders []string) (*MailboxMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
	if err != nil {
		return nil, fmt.Errorf("parse MIME message: %w", err)
	}

	// Standard headers
	from := msg.Header.Get("From")
	to := msg.Header.Get("To")
	subject := msg.Header.Get("Subject")

	// Custom KM headers
	senderID := msg.Header.Get("X-KM-Sender-ID")
	sigB64 := msg.Header.Get("X-KM-Signature")
	encryptedHeader := msg.Header.Get("X-KM-Encrypted")

	// Read body
	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}
	body := string(bodyBytes)

	// Allow-list enforcement (before signature verification)
	isSelfMail := senderID != "" && senderID == receiverSandboxID
	if !isSelfMail {
		if !MatchesAllowList(allowedSenders, senderID, "", receiverSandboxID) {
			return nil, ErrSenderNotAllowed
		}
	}

	// Signature verification
	signatureOK := false
	plaintext := false

	if sigB64 != "" {
		// X-KM-Signature present — attempt to verify
		if verifyErr := VerifyEmailSignature(pubKeyB64, body, sigB64); verifyErr == nil {
			signatureOK = true
		}
		// verifyErr != nil: signatureOK stays false, no error returned to caller
	} else {
		// No signature header — plaintext message
		plaintext = true
	}

	// Encrypted flag
	encrypted := encryptedHeader == "true"

	result := &MailboxMessage{
		S3Key:       "",  // not set here — caller can set from ListMailboxMessages result
		From:        from,
		To:          to,
		Subject:     subject,
		Body:        body,
		SenderID:    senderID,
		SignatureOK: signatureOK,
		Encrypted:   encrypted,
		Plaintext:   plaintext,
	}

	return result, nil
}
