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
	Type     string `json:"type"`
	Channel  string `json:"channel"`
	User     string `json:"user,omitempty"`
	BotID    string `json:"bot_id,omitempty"`
	Subtype  string `json:"subtype,omitempty"`
	Text     string `json:"text,omitempty"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

// InboundQueueBody is what the bridge writes to SQS — the poller parses this.
type InboundQueueBody struct {
	Channel  string `json:"channel"`
	ThreadTS string `json:"thread_ts"`
	Text     string `json:"text"`
	User     string `json:"user"`
	EventTS  string `json:"event_ts"`
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
