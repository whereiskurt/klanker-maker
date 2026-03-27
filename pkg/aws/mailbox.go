// Package aws — mailbox.go
// Sandbox mailbox reader library: listing, reading, and parsing signed MIME emails from S3.
//
// Key design:
//   - MailboxS3API: narrow interface (ListObjectsV2 + GetObject)
//   - ListMailboxMessages: returns S3 keys under mail/ filtered to this sandbox's address
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
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// KMAuthPattern matches "KM-AUTH: <phrase>" anywhere in an email body.
// The (?m) flag makes ^ match at line boundaries.
var KMAuthPattern = regexp.MustCompile(`(?m)KM-AUTH:\s*(\S+)`)

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
	MessageID    string // extracted from S3 key (last path segment)
	S3Key        string // full S3 object key
	From         string
	To           string
	Subject      string
	Body         string // body string (ciphertext if Encrypted=true; caller must call DecryptFromSender separately)
	SenderID     string // X-KM-Sender-ID header value
	SignatureOK  bool   // true if Ed25519 signature verified successfully
	Encrypted    bool   // true if X-KM-Encrypted: true was present
	Plaintext    bool   // true if no X-KM-Signature header (unsigned message)
	SafePhrase   string // extracted from body if "KM-AUTH: <phrase>" pattern found
	SafePhraseOK bool   // true if SafePhrase matches expected value passed to ParseSignedMessage
}

// ListMailboxMessages lists S3 object keys under the mail/ prefix and filters
// to only return messages addressed to this sandbox.
//
// SES stores all inbound email under mail/ with opaque keys. This function
// reads each object's MIME headers to check if the To address matches
// {sandboxID}@{emailDomain}. Only matching keys are returned.
// Handles pagination for buckets with more than 1000 objects.
func ListMailboxMessages(ctx context.Context, client MailboxS3API, bucket, sandboxID, emailDomain string) ([]string, error) {
	prefix := "mail/"
	myAddr := strings.ToLower(sandboxID + "@" + emailDomain)
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
			if obj.Key == nil {
				continue
			}
			// Read the message and check the To header
			getOut, getErr := client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    obj.Key,
			})
			if getErr != nil {
				continue // skip unreadable messages
			}
			// Read just enough for headers (first 8KB should cover MIME headers)
			headerBuf := make([]byte, 8192)
			n, _ := getOut.Body.Read(headerBuf)
			getOut.Body.Close()
			headerStr := strings.ToLower(string(headerBuf[:n]))

			if strings.Contains(headerStr, myAddr) {
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
// Safe phrase:
//   - If body contains "KM-AUTH: <phrase>", SafePhrase is set to the extracted value.
//   - If expectedSafePhrase is non-empty and matches SafePhrase, SafePhraseOK is set to true.
//   - If expectedSafePhrase is empty, SafePhraseOK is always false (skip safe phrase checking).
//
// Note: Actual NaCl decryption is not performed here. When Encrypted=true, Body contains
// the raw ciphertext and the caller must call DecryptFromSender separately.
func ParseSignedMessage(rawMIME []byte, receiverSandboxID, pubKeyB64 string, allowedSenders []string, expectedSafePhrase string) (*MailboxMessage, error) {
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

	// Safe phrase extraction: look for "KM-AUTH: <phrase>" in body
	safePhrase := ""
	safePhraseOK := false
	if matches := KMAuthPattern.FindStringSubmatch(body); len(matches) == 2 {
		safePhrase = matches[1]
	}
	if expectedSafePhrase != "" && safePhrase == expectedSafePhrase {
		safePhraseOK = true
	}

	result := &MailboxMessage{
		S3Key:        "",  // not set here — caller can set from ListMailboxMessages result
		From:         from,
		To:           to,
		Subject:      subject,
		Body:         body,
		SenderID:     senderID,
		SignatureOK:  signatureOK,
		Encrypted:    encrypted,
		Plaintext:    plaintext,
		SafePhrase:   safePhrase,
		SafePhraseOK: safePhraseOK,
	}

	return result, nil
}

// ApprovalResult holds the outcome of polling the sandbox mailbox for an operator approval reply.
type ApprovalResult struct {
	Found    bool   // true if a reply matching the action was found in the mailbox
	Approved bool   // true if the reply body contains APPROVED (case-insensitive)
	Denied   bool   // true if the reply body contains DENIED (case-insensitive)
	Reply    string // the reply body text
}

// PollForApproval scans the sandbox mailbox for an operator reply to an approval request.
//
// It calls ListMailboxMessages to enumerate all messages, then reads each one and
// looks for a reply whose subject contains both "Re:" and the action string
// (case-insensitive). When a matching reply is found, the body is scanned for
// "APPROVED" or "DENIED" (case-insensitive).
//
// Returns &ApprovalResult{Found: false} if no matching reply is in the mailbox.
// This function does not perform signature verification — operator replies are
// external plaintext emails, not signed sandbox messages.
func PollForApproval(ctx context.Context, client MailboxS3API, bucket, sandboxID, emailDomain, action string) (*ApprovalResult, error) {
	keys, err := ListMailboxMessages(ctx, client, bucket, sandboxID, emailDomain)
	if err != nil {
		return nil, fmt.Errorf("poll for approval (sandbox=%s, action=%s): %w", sandboxID, action, err)
	}

	actionUpper := strings.ToUpper(action)

	for _, key := range keys {
		rawMIME, err := ReadMessage(ctx, client, bucket, key)
		if err != nil {
			// Skip unreadable messages rather than aborting the poll
			continue
		}

		msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
		if err != nil {
			continue
		}

		subject := msg.Header.Get("Subject")
		subjectUpper := strings.ToUpper(subject)

		// Match reply: subject must contain "RE:" and the action string
		if !strings.Contains(subjectUpper, "RE:") || !strings.Contains(subjectUpper, actionUpper) {
			continue
		}

		// Found a matching reply — parse body for APPROVED/DENIED
		bodyBytes, err := io.ReadAll(msg.Body)
		if err != nil {
			continue
		}
		bodyUpper := strings.ToUpper(string(bodyBytes))

		approved := strings.Contains(bodyUpper, "APPROVED")
		denied := strings.Contains(bodyUpper, "DENIED")

		return &ApprovalResult{
			Found:    true,
			Approved: approved,
			Denied:   denied,
			Reply:    string(bodyBytes),
		}, nil
	}

	return &ApprovalResult{Found: false}, nil
}
