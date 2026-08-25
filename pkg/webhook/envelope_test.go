package webhook

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const wizV1Body = `{
  "km_schema": "v1",
  "source": "wiz",
  "delivery_key": "iss-1:CREATED:2026-08-25T10:00:00Z",
  "type": "issue",
  "id": "iss-1",
  "severity": "CRITICAL",
  "status": "OPEN",
  "title": "Public S3 bucket",
  "entity": {"type":"BUCKET","name":"logs","cloud_platform":"AWS","cloud_id":"arn:aws:s3:::logs"},
  "url": "https://app.wiz.io/issues#~(issue~'iss-1)"
}`

func TestParseEnvelope_CanonicalV1(t *testing.T) {
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if env.Schema != "v1" {
		t.Errorf("Schema: got %q, want v1", env.Schema)
	}
	if env.DeliveryKey != "iss-1:CREATED:2026-08-25T10:00:00Z" {
		t.Errorf("DeliveryKey: got %q", env.DeliveryKey)
	}
	if env.Severity != "CRITICAL" || env.Type != "issue" || env.ID != "iss-1" {
		t.Errorf("typed fields: %+v", env)
	}
	if env.Entity["cloud_id"] != "arn:aws:s3:::logs" {
		t.Errorf("Entity[cloud_id]: got %q", env.Entity["cloud_id"])
	}
	if env.Raw == "" {
		t.Error("Raw must always be populated")
	}
}

// Field() is what match and group_by read. Dotted entity access must work.
func TestEnvelope_Field(t *testing.T) {
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"severity", "CRITICAL", true},
		{"type", "issue", true},
		{"id", "iss-1", true},
		{"entity.cloud_id", "arn:aws:s3:::logs", true},
		{"entity.name", "logs", true},
		{"nope", "", false},
		{"entity.nope", "", false},
	}
	for _, c := range cases {
		got, ok := env.Field(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("Field(%q): got (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// No km_schema => the declared field_paths drive routing, and field_paths.id
// doubles as the replay key.
func TestParseEnvelope_FieldPathsFallback(t *testing.T) {
	body := `{"objectType":"issue","id":"abc","severity":"HIGH","entity":{"cloud_id":"i-1"}}`
	src := config.WebhookSource{
		Name: "generic",
		FieldPaths: map[string]string{
			"id":       "$.id",
			"type":     "$.objectType",
			"severity": "$.severity",
			"group":    "$.entity.cloud_id",
		},
	}
	env, err := ParseEnvelope([]byte(body), src)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if env.Schema != "" {
		t.Errorf("Schema: got %q, want empty", env.Schema)
	}
	if env.ID != "abc" || env.Type != "issue" || env.Severity != "HIGH" {
		t.Errorf("path-extracted fields: %+v", env)
	}
	if env.DeliveryKey != "abc" {
		t.Errorf("DeliveryKey must fall back to field_paths.id: got %q", env.DeliveryKey)
	}
	if got, _ := env.Field("group"); got != "i-1" {
		t.Errorf("Field(group): got %q, want i-1", got)
	}
}

// No km_schema and no field_paths: still parses, carries Raw, but exposes no
// routing fields — only an empty-match rule can fire.
func TestParseEnvelope_NoSchemaNoPaths(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{"anything":1}`), config.WebhookSource{Name: "x"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if _, ok := env.Field("severity"); ok {
		t.Error("Field(severity) must not resolve without schema or field_paths")
	}
	if env.Raw == "" {
		t.Error("Raw must always be populated")
	}
	if env.DeliveryKey == "" {
		t.Error("DeliveryKey must fall back to a body hash")
	}
}

func TestParseEnvelope_Unparseable(t *testing.T) {
	_, err := ParseEnvelope([]byte(`{not json`), config.WebhookSource{Name: "x"})
	if !errors.Is(err, ErrUnparseable) {
		t.Fatalf("want ErrUnparseable, got %v", err)
	}
}

func TestDecompress_Gzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	got, err := Decompress(buf.Bytes(), "gzip")
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %q", got)
	}

	// Absent/!gzip encoding is a passthrough.
	plain := []byte(`{"b":2}`)
	got, err = Decompress(plain, "")
	if err != nil || string(got) != `{"b":2}` {
		t.Errorf("passthrough: got %q err %v", got, err)
	}
}
