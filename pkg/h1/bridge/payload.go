// Package bridge implements the km-h1-bridge Lambda handler — the HackerOne-shaped
// twin of pkg/github/bridge. It ports the hard pipeline (HMAC-SHA256 verify,
// X-H1-Delivery GUID dedupe, deny-by-default allowlist, config-driven program→target
// resolve, warm/cold/resume dispatch) with HackerOne header/field renames.
//
// This file carries the genuinely-new HackerOne surface: the webhook payload shape
// (data.activity + data.report, struct tags pinned from 103-CAPTURE/field-paths.md),
// the H1Envelope carried to the poller, and the header-swapped HMAC verifier.
//
// Parse-tolerance (per the synthetic-fallback directive in field-paths.md): the
// report object is located tolerantly — both data.report.{...} and the JSON:API
// double-data data.report.data.{...} wrapper are accepted. A missing program handle
// is a hard resolve-miss (empty string), never a panic.
package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// H1WebhookPayload is the parsed HackerOne webhook body. The webhook nests an
// "activity" object (event metadata) and a "report" object (the full report)
// under a top-level "data" key.
//
// Struct tags are pinned from 103-CAPTURE/field-paths.md:
//   - program handle: data.report.relationships.program.data.attributes.handle  (LIVE-CONFIRMED)
//   - report id:      data.report.id                                            (LIVE-CONFIRMED)
//   - report title:   data.report.attributes.title                             (LIVE-CONFIRMED)
//   - report state:   data.report.attributes.state                             (LIVE-CONFIRMED)
//   - actor username: data.activity.relationships.actor.data.attributes.username (LIVE-CONFIRMED)
//   - internal flag:  data.activity.attributes.internal                        (LIVE-CONFIRMED)
//   - comment body:   data.activity.attributes.message                         (LIVE-CONFIRMED)
//
// Field accessors (ProgramHandle(), ReportID(), ...) are the handler/template-layer
// consumption surface — they hide the wrapper-tolerance fixup applied at parse time.
type H1WebhookPayload struct {
	Data H1Data `json:"data"`

	// report is the wrapper-resolved report object, memoized by ParsePayload so
	// accessors don't re-resolve the single-data vs double-data wrapper per call.
	report H1Report `json:"-"`
}

// H1Data holds the activity + report objects under the top-level data key.
type H1Data struct {
	Activity H1Activity `json:"activity"`
	// Report is captured as raw JSON so ParsePayload can resolve the
	// single-data vs JSON:API double-data wrapper tolerantly before unmarshalling
	// into the report struct.
	Report json.RawMessage `json:"report"`
}

// H1Activity is the subset of data.activity we need: the event actor, the
// internal-visibility flag (safety-critical), and the comment message body.
type H1Activity struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	Attributes    H1ActivityAttrs    `json:"attributes"`
	Relationships H1ActivityRelation `json:"relationships"`
}

// H1ActivityAttrs holds the comment message + internal flag.
type H1ActivityAttrs struct {
	// Message is the comment body — the @-handle scan + /command parse source on
	// report_comment_created. Empty on report_created.
	Message string `json:"message"`
	// Internal is the SAFETY-CRITICAL visibility flag. Internal comments carry true.
	Internal bool `json:"internal"`
}

// H1ActivityRelation carries the actor relationship.
type H1ActivityRelation struct {
	Actor H1RelationData `json:"actor"`
}

// H1Report is the subset of data.report we need.
type H1Report struct {
	ID            string           `json:"id"`
	Type          string           `json:"type"`
	Attributes    H1ReportAttrs    `json:"attributes"`
	Relationships H1ReportRelation `json:"relationships"`
}

// H1ReportAttrs holds the report title + state (used for {{title}}/{{state}} pre-expansion).
type H1ReportAttrs struct {
	Title string `json:"title"`
	State string `json:"state"`
}

// H1ReportRelation carries the program relationship (the routing key).
type H1ReportRelation struct {
	Program H1RelationData `json:"program"`
}

// H1RelationData is the JSON:API relationship wrapper: a "data" object carrying
// nested attributes (e.g. relationships.program.data.attributes.handle and
// relationships.actor.data.attributes.username).
type H1RelationData struct {
	Data H1RelationInner `json:"data"`
}

// H1RelationInner holds the inner attributes object of a relationship.
type H1RelationInner struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes H1RelationInnerAttr `json:"attributes"`
}

// H1RelationInnerAttr carries the handle/username leaf fields used by both the
// program relationship (handle) and the actor relationship (username).
type H1RelationInnerAttr struct {
	Handle   string `json:"handle"`
	Username string `json:"username"`
}

// reportEnvelope is the intermediate used to resolve the JSON:API double-data
// wrapper: data.report may be the report object directly, OR may wrap it under
// an extra "data" key (data.report.data.{...}). We unmarshal into this shape and
// prefer the inner object when it carries the program relationship.
type reportEnvelope struct {
	// Data is the optional inner double-data wrapper.
	Data *json.RawMessage `json:"data"`
}

// ParsePayload unmarshals a raw HackerOne webhook body into H1WebhookPayload,
// resolving the report-wrapper variance tolerantly (single-data vs JSON:API
// double-data). It never panics on a missing field — a missing program handle
// yields an empty ProgramHandle() (hard resolve-miss for the caller to drop).
func ParsePayload(body []byte) (*H1WebhookPayload, error) {
	var p H1WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("h1-bridge: parse webhook body: %w", err)
	}

	// Resolve the report wrapper tolerantly. Prefer the deepest object that
	// carries relationships.program (per field-paths.md directive).
	rep, err := resolveReport(p.Data.Report)
	if err != nil {
		return nil, fmt.Errorf("h1-bridge: resolve report object: %w", err)
	}
	p.report = rep
	return &p, nil
}

// resolveReport locates the report object inside the (possibly double-nested)
// data.report raw message. It tries the direct shape first; if that yields no
// program handle but a double-data inner object is present that DOES carry a
// program handle, it prefers the inner object.
func resolveReport(raw json.RawMessage) (H1Report, error) {
	if len(raw) == 0 {
		return H1Report{}, nil
	}

	// Direct shape: data.report = {id, attributes, relationships}.
	var direct H1Report
	_ = json.Unmarshal(raw, &direct) // tolerant: ignore type errors, fall through to inner

	// Double-data shape: data.report.data = {id, attributes, relationships}.
	var env reportEnvelope
	_ = json.Unmarshal(raw, &env)
	if env.Data != nil {
		var inner H1Report
		if err := json.Unmarshal(*env.Data, &inner); err == nil {
			// Prefer the inner object when the direct one lacks a program handle
			// but the inner one provides it (the JSON:API double-data wrapper).
			if direct.Relationships.Program.Data.Attributes.Handle == "" &&
				inner.Relationships.Program.Data.Attributes.Handle != "" {
				return inner, nil
			}
			// Also prefer inner when the direct shape carried no id at all.
			if direct.ID == "" && inner.ID != "" {
				return inner, nil
			}
		}
	}

	return direct, nil
}

// ---- Field accessors (handler/template consumption surface) ----

// ProgramHandle returns the report's program handle — the resolve/routing key.
// Empty string ⇒ hard resolve-miss (caller drops with a log line).
func (p *H1WebhookPayload) ProgramHandle() string {
	return p.report.Relationships.Program.Data.Attributes.Handle
}

// ReportID returns the report id (thread-continuity key + comment target).
func (p *H1WebhookPayload) ReportID() string { return p.report.ID }

// Title returns the report title (for {{title}} pre-expansion).
func (p *H1WebhookPayload) Title() string { return p.report.Attributes.Title }

// State returns the report state, e.g. "new"/"triaged" (for {{state}} pre-expansion).
func (p *H1WebhookPayload) State() string { return p.report.Attributes.State }

// ActorUsername returns the activity actor username — the allowlist key AND the
// self-loop guard identity.
func (p *H1WebhookPayload) ActorUsername() string {
	return p.Data.Activity.Relationships.Actor.Data.Attributes.Username
}

// CommentBody returns the activity comment message — the @-handle scan + /command
// parse source. Empty on report_created.
func (p *H1WebhookPayload) CommentBody() string {
	return p.Data.Activity.Attributes.Message
}

// Internal returns the activity internal-visibility flag. Internal comments → true.
func (p *H1WebhookPayload) Internal() bool {
	return p.Data.Activity.Attributes.Internal
}

// ActivityID returns the activity id (carried into the envelope for logging/dedup).
func (p *H1WebhookPayload) ActivityID() string { return p.Data.Activity.ID }

// ActivityType returns data.activity.type — a secondary event discriminator used
// only as a fallback when the X-H1-Event header is absent (per field-paths.md).
func (p *H1WebhookPayload) ActivityType() string { return p.Data.Activity.Type }

// H1Envelope is the message enqueued to the per-sandbox h1-inbound FIFO queue
// (warm path) or carried in the EventBridge SandboxCreate detail (cold path).
// It mirrors GitHubEnvelope but with HackerOne-specific fields.
//
// SAFETY INVARIANT: ReplyToResearcher defaults to false (zero value = internal).
// The actual reply-visibility gate lives in Plan 04; here the field is parse-only
// — the envelope merely carries the parsed intent. An external researcher-visible
// reply must NEVER be the default.
type H1Envelope struct {
	// Source is always "hackerone" (source-aware poller discriminator).
	Source string `json:"source"`
	// Program is the resolved program handle (routing key).
	Program string `json:"program"`
	// ReportID is the HackerOne report id (thread-continuity key + comment target).
	ReportID string `json:"report_id"`
	// Kind is the webhook event type: report_created | report_comment_created | ...
	Kind string `json:"kind"`
	// ActivityID is the triggering activity id (for logging/dedup).
	ActivityID string `json:"activity_id,omitempty"`
	// ReportURL is the permalink to the report (for logging/debug).
	ReportURL string `json:"report_url,omitempty"`
	// Actor is the activity actor username (the allowlist key / self-loop identity).
	Actor string `json:"actor"`
	// Body is the extracted agent prompt (the comment body after handle/verb strip,
	// or the expanded event→prompt template).
	Body string `json:"body"`
	// Agent is the parsed agent override: "claude", "codex", or "" (profile default).
	// omitempty so old envelope shapes decode cleanly as "" (profile default).
	Agent string `json:"agent,omitempty"`
	// ReplyToResearcher carries the parsed /reply_to_researcher intent. ZERO VALUE
	// (false) = internal — the safety default. The gate that honors this is Plan 04.
	ReplyToResearcher bool `json:"reply_to_researcher,omitempty"`
}

// VerifyH1Signature verifies the HMAC-SHA256 signature of a HackerOne webhook.
// Ported verbatim from VerifyGitHubSignature (header-name swap only): HackerOne
// sends X-H1-Signature: sha256=<hex(HMAC-SHA256(rawBody, secret))>, the same scheme
// as GitHub's X-Hub-Signature-256.
//
// rawBody MUST be the already-base64-DECODED request body bytes (Pitfall 1 — the
// HMAC is over the decoded body, NEVER the base64 string). The Lambda main.go
// (Plan 08) performs the decode before calling this. No timestamp header ⇒ no skew
// check; replay protection is via X-H1-Delivery dedup.
func VerifyH1Signature(secret, sigHeader string, rawBody []byte) error {
	if !strings.HasPrefix(sigHeader, "sha256=") {
		return fmt.Errorf("h1-bridge: missing or wrong-format signature header (got %q)", sigHeader)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	// Constant-time compare prevents timing attacks.
	if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
		return fmt.Errorf("h1-bridge: signature mismatch")
	}
	return nil
}
