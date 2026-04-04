package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ConversationState tracks the multi-turn email conversation state for the AI interpretation flow.
// Persisted to S3 under mail/conversations/{thread-id}.json.
type ConversationState struct {
	ThreadID     string            `json:"thread_id"`
	Sender       string            `json:"sender"`
	Started      time.Time         `json:"started"`
	Updated      time.Time         `json:"updated"`
	State        string            `json:"state"` // new|interpreted|awaiting_confirmation|confirmed|revised|cancelled
	ResolvedCmd  *InterpretedCommand `json:"resolved_command,omitempty"`
	ConfirmMsgID string            `json:"confirm_message_id,omitempty"`
	Messages     []ConversationMsg `json:"messages"`
}

// ConversationMsg is a single message in the conversation history.
type ConversationMsg struct {
	Role    string    `json:"role"` // "operator" or "system"
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// conversationKey returns the S3 key for a conversation state object.
func conversationKey(threadID string) string {
	return fmt.Sprintf("mail/conversations/%s.json", threadID)
}

// extractThreadID extracts a canonical thread identifier from email MIME headers.
// Priority: In-Reply-To > References (first entry) > Message-ID.
// Angle brackets and surrounding whitespace are stripped.
func extractThreadID(msg *mail.Message) string {
	clean := func(s string) string {
		return strings.Trim(s, "<> \t\r\n")
	}

	// Prefer In-Reply-To: points to the immediate parent, consistent thread root.
	if v := msg.Header.Get("In-Reply-To"); v != "" {
		if id := clean(v); id != "" {
			return id
		}
	}

	// Fall back to References: first entry is the thread root.
	if v := msg.Header.Get("References"); v != "" {
		parts := strings.Fields(v)
		if len(parts) > 0 {
			if id := clean(parts[0]); id != "" {
				return id
			}
		}
	}

	// New thread: use own Message-ID as the thread root.
	return clean(msg.Header.Get("Message-ID"))
}

// extractAllThreadIDs returns all candidate thread IDs from MIME headers.
// Used to find a conversation that may be keyed on any of these IDs.
// Returns: [In-Reply-To, ...References entries, Message-ID] (deduplicated, non-empty).
func extractAllThreadIDs(msg *mail.Message) []string {
	clean := func(s string) string {
		return strings.Trim(s, "<> \t\r\n")
	}
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		id = clean(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	add(msg.Header.Get("In-Reply-To"))
	for _, ref := range strings.Fields(msg.Header.Get("References")) {
		add(ref)
	}
	add(msg.Header.Get("Message-ID"))
	return ids
}

// loadConversation fetches and deserializes a ConversationState from S3.
// Returns the state if found, or an error (including NoSuchKey) if not found.
// Callers should check for "NoSuchKey" in the error message to distinguish
// "new conversation" from "actual error".
func loadConversation(ctx context.Context, s3Client OperatorS3API, bucket, threadID string) (*ConversationState, error) {
	out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(conversationKey(threadID)),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	var state ConversationState
	if err := json.NewDecoder(out.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("decode conversation state for thread %q: %w", threadID, err)
	}
	return &state, nil
}

// saveConversation serializes a ConversationState and writes it to S3.
// It updates state.Updated to time.Now() before writing.
func saveConversation(ctx context.Context, s3Client OperatorS3API, bucket string, state *ConversationState) error {
	state.Updated = time.Now().UTC()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal conversation state: %w", err)
	}

	if _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(conversationKey(state.ThreadID)),
		Body:        bytes.NewReader(data),
		ContentType: awssdk.String("application/json"),
	}); err != nil {
		return fmt.Errorf("save conversation state for thread %q: %w", state.ThreadID, err)
	}
	return nil
}
