package bridge_test

// payload_test.go — tests for the HackerOne webhook payload parse layer and the
// HMAC-SHA256 signature verifier. Mirrors pkg/github/bridge payload coverage,
// adapted to the HackerOne data.activity + data.report envelope (struct tags
// pinned from 103-CAPTURE/field-paths.md, parse-tolerant per the synthetic-fallback
// directive).
//
// Coverage:
//   TestParsePayload          — report_created + report_comment_created fixtures parse
//                               into H1WebhookPayload (program handle, report id/title/state,
//                               actor username, internal flag, comment body) via pinned paths.
//   TestParseTolerant         — accepts BOTH data.report.{...} and the JSON:API
//                               double-data data.report.data.{...} wrapper.
//   TestVerifyH1Signature     — correct sha256=<hex> over raw bytes → nil; tampered → error;
//                               wrong-format header → error; constant-time compare.
//   TestVerifyH1Signature_Base64 — HMAC is computed over the DECODED body, not the
//                               base64-encoded string (Pitfall 1).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// ============================================================
// TestParsePayload — captured bodies parse via pinned tags
// ============================================================

func TestParsePayload(t *testing.T) {
	t.Run("report_comment_created extracts program/report/actor/comment", func(t *testing.T) {
		body := readFixture(t, "report_comment_created.json")

		p, err := bridge.ParsePayload(body)
		if err != nil {
			t.Fatalf("ParsePayload: %v", err)
		}

		if got := p.ProgramHandle(); got != "km-sandbox" {
			t.Errorf("ProgramHandle()=%q want %q", got, "km-sandbox")
		}
		if got := p.ReportID(); got != "7000001" {
			t.Errorf("ReportID()=%q want %q", got, "7000001")
		}
		if got := p.ActorUsername(); got != "synthetic-operator" {
			t.Errorf("ActorUsername()=%q want %q", got, "synthetic-operator")
		}
		if got := p.CommentBody(); got != "@km please triage this. /reply_to_researcher" {
			t.Errorf("CommentBody()=%q want %q", got, "@km please triage this. /reply_to_researcher")
		}
		if !p.Internal() {
			t.Errorf("Internal()=false want true (activity.attributes.internal:true)")
		}
		if got := p.ActivityID(); got != "9000002" {
			t.Errorf("ActivityID()=%q want %q", got, "9000002")
		}
	})

	t.Run("report_created extracts program/report/title/state, empty comment", func(t *testing.T) {
		body := readFixture(t, "report_created.json")

		p, err := bridge.ParsePayload(body)
		if err != nil {
			t.Fatalf("ParsePayload: %v", err)
		}

		if got := p.ProgramHandle(); got != "km-sandbox" {
			t.Errorf("ProgramHandle()=%q want %q", got, "km-sandbox")
		}
		if got := p.ReportID(); got != "7000001" {
			t.Errorf("ReportID()=%q want %q", got, "7000001")
		}
		if got := p.Title(); got != "Synthetic test report: reflected XSS on /search" {
			t.Errorf("Title()=%q want title", got)
		}
		if got := p.State(); got != "new" {
			t.Errorf("State()=%q want %q", got, "new")
		}
		if got := p.CommentBody(); got != "" {
			t.Errorf("CommentBody()=%q want empty on report_created", got)
		}
		// internal:false on report_created
		if p.Internal() {
			t.Errorf("Internal()=true want false (report_created carries internal:false)")
		}
	})
}

// ============================================================
// TestParseTolerant — wrapper variance (data.report vs data.report.data)
// ============================================================

func TestParseTolerant(t *testing.T) {
	// JSON:API double-data nesting: the report object is wrapped under an extra
	// "data" key (data.report.data.{...}). The parser must locate the report
	// tolerantly per the field-paths.md synthetic-fallback directive.
	doubleData := []byte(`{
	  "data": {
	    "activity": {
	      "id": "9000003",
	      "type": "activity-comment",
	      "attributes": {"message": "@km hi", "internal": true},
	      "relationships": {
	        "actor": {"data": {"attributes": {"username": "wrapper-actor"}}}
	      }
	    },
	    "report": {
	      "data": {
	        "id": "7000099",
	        "attributes": {"title": "Double-nested", "state": "triaged"},
	        "relationships": {
	          "program": {"data": {"attributes": {"handle": "km-sandbox"}}}
	        }
	      }
	    }
	  }
	}`)

	p, err := bridge.ParsePayload(doubleData)
	if err != nil {
		t.Fatalf("ParsePayload (double-data wrapper): %v", err)
	}
	if got := p.ProgramHandle(); got != "km-sandbox" {
		t.Errorf("double-data ProgramHandle()=%q want %q", got, "km-sandbox")
	}
	if got := p.ReportID(); got != "7000099" {
		t.Errorf("double-data ReportID()=%q want %q", got, "7000099")
	}
	if got := p.Title(); got != "Double-nested" {
		t.Errorf("double-data Title()=%q want %q", got, "Double-nested")
	}
	if got := p.ActorUsername(); got != "wrapper-actor" {
		t.Errorf("double-data ActorUsername()=%q want %q", got, "wrapper-actor")
	}

	// Single-data shape (the captured fixtures) must still parse.
	single := readFixture(t, "report_created.json")
	ps, err := bridge.ParsePayload(single)
	if err != nil {
		t.Fatalf("ParsePayload (single-data wrapper): %v", err)
	}
	if got := ps.ProgramHandle(); got != "km-sandbox" {
		t.Errorf("single-data ProgramHandle()=%q want %q", got, "km-sandbox")
	}
}

func TestParseMissingHandle(t *testing.T) {
	// A missing program handle is a hard resolve-miss (empty string), never a panic.
	noHandle := []byte(`{"data":{"activity":{"id":"1","attributes":{"message":"hi"}},"report":{"id":"2","attributes":{"title":"t","state":"new"}}}}`)
	p, err := bridge.ParsePayload(noHandle)
	if err != nil {
		t.Fatalf("ParsePayload (no handle): %v", err)
	}
	if got := p.ProgramHandle(); got != "" {
		t.Errorf("ProgramHandle()=%q want empty (resolve-miss, no panic)", got)
	}
	if got := p.ReportID(); got != "2" {
		t.Errorf("ReportID()=%q want %q", got, "2")
	}
}

// ============================================================
// TestVerifyH1Signature
// ============================================================

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyH1Signature(t *testing.T) {
	secret := "supersecret-webhook-key"
	body := []byte(`{"data":{"report":{"id":"7000001"}}}`)
	good := sign(secret, body)

	t.Run("correct signature → nil", func(t *testing.T) {
		if err := bridge.VerifyH1Signature(secret, good, body); err != nil {
			t.Errorf("VerifyH1Signature(correct) = %v want nil", err)
		}
	})

	t.Run("tampered body → error", func(t *testing.T) {
		tampered := append([]byte{}, body...)
		tampered[len(tampered)-2] ^= 0x20 // flip a byte
		if err := bridge.VerifyH1Signature(secret, good, tampered); err == nil {
			t.Errorf("VerifyH1Signature(tampered body) = nil want error")
		}
	})

	t.Run("wrong-format header (no sha256= prefix) → error", func(t *testing.T) {
		raw := hex.EncodeToString([]byte("whatever"))
		if err := bridge.VerifyH1Signature(secret, raw, body); err == nil {
			t.Errorf("VerifyH1Signature(no sha256= prefix) = nil want error")
		}
	})

	t.Run("wrong secret → error", func(t *testing.T) {
		if err := bridge.VerifyH1Signature("other-secret", good, body); err == nil {
			t.Errorf("VerifyH1Signature(wrong secret) = nil want error")
		}
	})

	t.Run("empty header → error", func(t *testing.T) {
		if err := bridge.VerifyH1Signature(secret, "", body); err == nil {
			t.Errorf("VerifyH1Signature(empty header) = nil want error")
		}
	})
}

// TestVerifyH1Signature_Base64 documents the decode contract: the HMAC is over
// the DECODED body bytes, not the base64-encoded string (Pitfall 1 — the silent-fail
// trap where a Lambda forwards still-encoded bytes). VerifyH1Signature takes
// already-decoded bytes; this test proves verification succeeds only after decode.
func TestVerifyH1Signature_Base64(t *testing.T) {
	secret := "supersecret-webhook-key"
	rawBody := []byte(`{"data":{"report":{"id":"7000001"}}}`)
	encoded := base64.StdEncoding.EncodeToString(rawBody)

	// HackerOne signs the DECODED body.
	sig := sign(secret, rawBody)

	// Feeding the encoded string's bytes (the bug) must FAIL.
	if err := bridge.VerifyH1Signature(secret, sig, []byte(encoded)); err == nil {
		t.Errorf("VerifyH1Signature over base64-ENCODED bytes = nil; want error (must decode first)")
	}

	// Decoding first, then verifying, must SUCCEED.
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := bridge.VerifyH1Signature(secret, sig, decoded); err != nil {
		t.Errorf("VerifyH1Signature over DECODED bytes = %v want nil", err)
	}
}
