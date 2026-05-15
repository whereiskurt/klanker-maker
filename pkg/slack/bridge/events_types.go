package bridge

import "encoding/json"

// slackEnvelope is the outermost payload from Slack Events API.
type slackEnvelope struct {
	Type      string          `json:"type"`
	Challenge string          `json:"challenge,omitempty"` // url_verification
	EventID   string          `json:"event_id,omitempty"`  // event_callback
	TeamID    string          `json:"team_id,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
}

// slackMessageEvent is the inner event for type=message.
type slackMessageEvent struct {
	Type     string      `json:"type"`
	Channel  string      `json:"channel"`
	User     string      `json:"user,omitempty"`
	BotID    string      `json:"bot_id,omitempty"`
	Subtype  string      `json:"subtype,omitempty"`
	Text     string      `json:"text,omitempty"`
	TS       string      `json:"ts"`
	ThreadTS string      `json:"thread_ts,omitempty"`
	Files    []SlackFile `json:"files,omitempty"` // Phase 75: populated on file_share events
}

// SlackFile is a Slack file object as delivered in a file_share message event's files[].
// Phase 75: only the fields the bridge consumes are modeled — Slack delivers many more
// (timestamp, user, permalink, etc.) but Go's json package silently ignores unknown keys.
type SlackFile struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Mimetype           string `json:"mimetype"`
	URLPrivateDownload string `json:"url_private_download"`
	Size               int64  `json:"size"`
}

// InboundQueueBody is what the bridge writes to SQS — the poller parses this.
type InboundQueueBody struct {
	Channel     string       `json:"channel"`
	ThreadTS    string       `json:"thread_ts"`
	Text        string       `json:"text"`
	User        string       `json:"user"`
	EventTS     string       `json:"event_ts"`
	Attachments []Attachment `json:"attachments,omitempty"` // Phase 75: per-file records; absent (not null) when empty for back-compat
}

// Attachment is the per-file record written to the SQS body for the sandbox poller.
// S3Key is "slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>".
// OriginalName is the raw Slack-supplied filename (preserved for the master-prompt wrapper).
// Mimetype is the Slack-supplied mimetype (used in the wrapper as "(<mimetype>)").
type Attachment struct {
	S3Key        string `json:"s3_key"`
	OriginalName string `json:"original_name"`
	Mimetype     string `json:"mimetype"`
}

// EventsRequest is the framework-agnostic request the handler accepts.
// The adapter (main.go) translates the Lambda Function URL request to this.
// Header keys MUST be lowercased by the adapter.
type EventsRequest struct {
	Headers map[string]string // adapter MUST lowercase keys
	Body    string
}

// EventsResponse is what the handler returns.
type EventsResponse struct {
	StatusCode int
	Body       string            // JSON-encoded body or plain text
	Headers    map[string]string
}
