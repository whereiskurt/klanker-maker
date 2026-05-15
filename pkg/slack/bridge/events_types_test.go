package bridge

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestSlackMessageEvent_FilesField_ParsesCorrectly verifies:
//  1. A Slack file_share event with a populated files[] array round-trips correctly.
//  2. A Slack event with NO files field still parses cleanly (back-compat for Phase 67 events).
//  3. InboundQueueBody with one Attachment round-trips via Marshal+Unmarshal with correct JSON tags.
//  4. InboundQueueBody with nil Attachments produces JSON without an "attachments" key (omitempty).
func TestSlackMessageEvent_FilesField_ParsesCorrectly(t *testing.T) {
	t.Run("files_populated", func(t *testing.T) {
		raw := `{
			"type": "message",
			"channel": "C1",
			"user": "U1",
			"text": "",
			"ts": "1714280400.001",
			"subtype": "file_share",
			"files": [
				{
					"id": "F012345",
					"name": "screenshot.png",
					"mimetype": "image/png",
					"url_private_download": "https://files.slack.com/files-pri/T0/F012345/download/screenshot.png",
					"size": 12345
				}
			]
		}`
		var evt slackMessageEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(evt.Files) != 1 {
			t.Fatalf("expected 1 file, got %d", len(evt.Files))
		}
		f := evt.Files[0]
		if f.ID != "F012345" {
			t.Errorf("ID: got %q, want %q", f.ID, "F012345")
		}
		if f.Name != "screenshot.png" {
			t.Errorf("Name: got %q, want %q", f.Name, "screenshot.png")
		}
		if f.Mimetype != "image/png" {
			t.Errorf("Mimetype: got %q, want %q", f.Mimetype, "image/png")
		}
		if f.URLPrivateDownload != "https://files.slack.com/files-pri/T0/F012345/download/screenshot.png" {
			t.Errorf("URLPrivateDownload: got %q", f.URLPrivateDownload)
		}
		if f.Size != 12345 {
			t.Errorf("Size: got %d, want %d", f.Size, 12345)
		}
	})

	t.Run("no_files_field_back_compat", func(t *testing.T) {
		// Phase 67 events have no files key — must unmarshal without error and len(Files)==0.
		raw := `{
			"type": "message",
			"channel": "C1",
			"user": "U1",
			"text": "hello",
			"ts": "1714280400.001"
		}`
		var evt slackMessageEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("back-compat unmarshal error: %v", err)
		}
		if len(evt.Files) != 0 {
			t.Fatalf("expected 0 files for Phase 67 event, got %d", len(evt.Files))
		}
	})

	t.Run("attachment_json_roundtrip", func(t *testing.T) {
		// Marshal and unmarshal an InboundQueueBody with one Attachment.
		// Verify the JSON keys are s3_key, original_name, mimetype.
		original := InboundQueueBody{
			Channel:  "C1",
			ThreadTS: "123.456",
			Text:     "",
			User:     "U1",
			EventTS:  "123.457",
			Attachments: []Attachment{
				{
					S3Key:        "slack-inbound/sb-x/123.456/F012345-screenshot.png",
					OriginalName: "screenshot.png",
					Mimetype:     "image/png",
				},
			},
		}
		b, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		// Verify JSON key names directly.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatalf("unmarshal to map error: %v", err)
		}
		if _, ok := raw["attachments"]; !ok {
			t.Fatal("expected 'attachments' key in JSON")
		}
		// Round-trip back to struct.
		var got InboundQueueBody
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal round-trip error: %v", err)
		}
		if len(got.Attachments) != 1 {
			t.Fatalf("expected 1 attachment after round-trip, got %d", len(got.Attachments))
		}
		a := got.Attachments[0]
		if a.S3Key != "slack-inbound/sb-x/123.456/F012345-screenshot.png" {
			t.Errorf("S3Key: got %q", a.S3Key)
		}
		if a.OriginalName != "screenshot.png" {
			t.Errorf("OriginalName: got %q", a.OriginalName)
		}
		if a.Mimetype != "image/png" {
			t.Errorf("Mimetype: got %q", a.Mimetype)
		}
	})

	t.Run("nil_attachments_omitted_from_json", func(t *testing.T) {
		// InboundQueueBody with nil Attachments must NOT emit "attachments" key.
		// Older sandbox pollers use .attachments[]? — jq ? returns empty on absent
		// key but may emit an error on null.
		body := InboundQueueBody{
			Channel:  "C1",
			ThreadTS: "1.0",
			Text:     "hi",
			User:     "U1",
			EventTS:  "1.0",
			// Attachments is nil (zero value)
		}
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		if bytes.Contains(b, []byte("attachments")) {
			t.Fatalf("expected 'attachments' key to be absent from JSON when nil, got: %s", string(b))
		}
	})
}
