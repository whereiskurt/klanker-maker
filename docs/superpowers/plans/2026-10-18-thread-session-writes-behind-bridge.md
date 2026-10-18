# Thread Session State Behind the Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every sandbox-role grant on the three threads tables by keeping box-private session state in local files and sending the two bridge-needed attributes (session id, `agent_type`) through one new signed bridge action.

**Architecture:** `pkg/slack` gains `ActionSession` and omitempty envelope fields; the Slack bridge (`pkg/slack/bridge`) gains `handleSession` (ownership-checked `UpdateItem` on the Slack, GitHub and H1 threads tables) and `post --claim-thread` (bridge-written row for a handoff's new top-level message); `cmd/km-slack` gains the `session` verb and the flag; the pollers in `pkg/compiler/userdata.go` keep `/var/lib/km/sessions/<source>/<key>.json` and call `km-slack session` instead of DynamoDB; `infra/modules/ec2spot/v1.9.0` drops `ec2spot_threads_rw`; `lambda-slack-bridge` gains the two extra tables.

**Tech Stack:** Go 1.25, aws-sdk-go-v2 DynamoDB, Ed25519 signed JSON envelopes, bash-in-Go-template userdata, Terraform HCL.

**Spec:** `docs/superpowers/specs/2026-10-18-thread-session-writes-behind-bridge-design.md` (read first; §3 decisions and §4 components are the authority).

## Global Constraints

- Every new `SlackEnvelope` field is `omitempty`; `TestCanonicalJSON_PreDMWireFormatPinned` (pkg/slack) must keep passing unchanged — an older `km-slack`'s signatures must still verify.
- `session` action codes: `400 missing_fields` (per-source key incomplete), `404 thread_unknown` (no row; never create), `403 thread_not_owned` (`row.sandbox_id != env.SenderID`), `503 source_unavailable` (bridge lacks that source's table name). The write SETs only the session attribute (`claude_session_id` for Slack; `agent_session_id` for GitHub/H1), `agent_type`, `last_turn_ts`, `ttl_expiry` (30 days) — or `REMOVE`s the session attribute when `session_id` is empty — and never names `sandbox_id`, `last_dispatch_ts`, `created_at`, `turn_count`.
- `--claim-thread` applies only to a successful TOP-LEVEL `post` (`ThreadTS == ""`); the bridge then calls `Threads.Upsert(channel, newTS, SenderID)` (the v1.8.0 `if_not_exists` form). Threaded posts ignore it; an Upsert failure is logged and does not fail the post.
- State files: `/var/lib/km/sessions/<source>/<key>.json` (dir `0700` root), fields `session_id`, `agent_type`, `last_assistant_msg`, `last_processed_event_ts`, `updated_at`; keys: Slack `<channel>_<thread_ts>`, GitHub `<owner>_<repo>_<number>` (`/`→`_`), H1 `<report_id>_<target>`; atomic write (`mktemp` + `mv`).
- After the pollers change, the template contains NO `aws dynamodb` invocation whose block names a `*THREADS_TABLE` variable, and no `THREADS_TABLE=` definitions remain.
- New module dir only: `infra/modules/ec2spot/v1.9.0` (never edit v1.8.0 or older); the three pin sites move together (`infra/templates/sandbox/terragrunt.hcl`, `pkg/compiler/ec2spot_timeout_test.go`, `pkg/terragrunt/substrate_version_pin_test.go`).
- Frozen golden `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` is hand-patched only (never `CAPTURE_PRE92_BASELINE=1`); `userdata_additional_volume_only.golden.sh` → `CAPTURE_ADDVOL_GOLDEN=1`; `h1_byte_identity_golden.txt` → `CAPTURE_PRE_H1_BASELINE=1`.
- Commit by pathspec; never stage `VERSION`; every commit message ends with exactly:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`.
- `go test <pkg> ; echo exit=$?` — never pipe to `tail`. No AWS calls and no deploy in Tasks 1–7; Task 8 is controller-run.

## File map

| File | Responsibility |
|---|---|
| `pkg/slack/payload.go` (+ `payload_session_test.go`) | `ActionSession`, envelope fields (T1) |
| `pkg/slack/bridge/events_interfaces.go`, `aws_adapters.go`, new `aws_adapters_sessions.go`, new `handler_session.go`, `handler.go` | `SetSession`, GitHub/H1 session stores, `handleSession`, action admission (T2); `claim_thread` on post (T3) |
| `cmd/km-slack-bridge/main.go` | wiring of the two extra stores from env (T2) |
| `cmd/km-slack/session.go` (+ test), `main.go` | `km-slack session` verb (T4); `post --claim-thread` flag (T3) |
| `pkg/compiler/userdata.go` + tests + goldens | state-file helpers, Slack poller (T5), GitHub/H1 pollers + guard (T6) |
| `infra/modules/ec2spot/v1.9.0`, three pins, `pkg/terragrunt/*guard*`, `internal/app/cmd/doctor_sandbox_role.go`, `infra/modules/lambda-slack-bridge/v1.0.0`, `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | role + bridge IAM (T7) |
| docs, CLAUDE.md, memory | T8 |
| deploy + canary | T9 (controller) |

---

### Task 1: Envelope — `ActionSession` and the new omitempty fields

**Files:**
- Modify: `pkg/slack/payload.go` (action constants ~line 36-43; `SlackEnvelope` struct ~line 71-101)
- Create: `pkg/slack/payload_session_test.go`

**Interfaces:**
- Produces: `slack.ActionSession = "session"`; `SlackEnvelope` fields `Source string json:"source,omitempty"`, `Repo string json:"repo,omitempty"`, `Number int json:"number,omitempty"`, `ReportID string json:"report_id,omitempty"`, `Target string json:"target,omitempty"`, `AgentType string json:"agent_type,omitempty"`, `ClaimThread bool json:"claim_thread,omitempty"`. Existing `SessionID`, `Channel`, `ThreadTS` are reused by the session action.

- [ ] **Step 1: Write the failing test**

```go
package slack_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// Every field the session action adds is omitempty: an envelope from an older
// km-slack must canonicalise to the same bytes it did before, or its
// signatures stop verifying at the bridge.
func TestCanonicalJSON_SessionFieldsOmittedWhenZero(t *testing.T) {
	env := &slack.SlackEnvelope{Action: slack.ActionPost, Body: "b", Channel: "C1", Nonce: "n", SenderID: "sb-1", Timestamp: 1, Version: 1}
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "repo", "number", "report_id", "target", "agent_type", "claim_thread"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Fatalf("zero %s serialised (breaks old signers): %s", k, b)
		}
	}
	env.Action = slack.ActionSession
	env.Source, env.Repo, env.Number, env.AgentType, env.SessionID = "github", "o/r", 7, "codex", "sess-1"
	b2, _ := slack.CanonicalJSON(env)
	for _, want := range []string{`"source":"github"`, `"repo":"o/r"`, `"number":7`, `"agent_type":"codex"`, `"session_id":"sess-1"`} {
		if !strings.Contains(string(b2), want) {
			t.Fatalf("missing %s in %s", want, b2)
		}
	}
	env2 := &slack.SlackEnvelope{Action: slack.ActionPost, ClaimThread: true}
	b3, _ := slack.CanonicalJSON(env2)
	if !strings.Contains(string(b3), `"claim_thread":true`) {
		t.Fatalf("claim_thread not serialised when set: %s", b3)
	}
}

func TestActionSessionConstant(t *testing.T) {
	if slack.ActionSession != "session" {
		t.Fatalf("ActionSession = %q", slack.ActionSession)
	}
}
```

- [ ] **Step 2: Run — expect compile failure (`ActionSession`, fields undefined)**

Run: `cd pkg/slack && go test . -run 'TestCanonicalJSON_SessionFields|TestActionSessionConstant' -v; echo exit=$?`

- [ ] **Step 3: Implement**

In the constants block add `ActionSession = "session" // 2026-10-18 — poller records its agent session id + agent_type on a thread row the bridge routed to it`.
In `SlackEnvelope`, after `TargetUser`, add (keep the struct alphabetical-ish as it is; JSON order is what matters for canonical bytes and `omitempty` keeps zero values out):

```go
	// Session action (2026-10-18). All omitempty — see the TargetUser note: a
	// field that serialises when zero changes every older km-slack's signed bytes.
	Source      string `json:"source,omitempty"`      // "slack" | "github" | "h1"
	Repo        string `json:"repo,omitempty"`        // github: owner/name
	Number      int    `json:"number,omitempty"`      // github: issue/PR number
	ReportID    string `json:"report_id,omitempty"`   // h1
	Target      string `json:"target,omitempty"`      // h1: alias (or sandbox id)
	AgentType   string `json:"agent_type,omitempty"`  // "claude" | "codex"
	// ClaimThread on a top-level post: the bridge writes the thread row for the
	// message it just posted, owned by the sender (the Phase 70 handoff thread).
	ClaimThread bool `json:"claim_thread,omitempty"`
```
Place them so the existing field order is otherwise untouched (CanonicalJSON sorts keys — check `CanonicalJSON` at ~line 174; if it marshals the struct directly, order follows the struct; either way the pinned test decides).

- [ ] **Step 4: Run the package**

Run: `cd pkg/slack && go test . ; echo exit=$?` — `TestCanonicalJSON_PreDMWireFormatPinned` must still pass unchanged.

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/payload.go pkg/slack/payload_session_test.go
git commit -m "feat(slack): ActionSession and omitempty envelope fields for session writes and claim_thread

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 2: Bridge — `SetSession`, the GitHub/H1 session stores, `handleSession`

**Files:**
- Modify: `pkg/slack/bridge/events_interfaces.go` (`SlackThreadStore`, ~line 76-108)
- Modify: `pkg/slack/bridge/aws_adapters.go` (`DDBThreadStore`, ~line 920)
- Create: `pkg/slack/bridge/aws_adapters_sessions.go`, `pkg/slack/bridge/handler_session.go`, `pkg/slack/bridge/handler_session_test.go`, `pkg/slack/bridge/aws_adapters_sessions_test.go`
- Modify: `pkg/slack/bridge/handler.go` (action allowlist ~line 142; `channelRequired` ~line 133; dispatch `switch` — add `case slack.ActionSession: return h.handleSession(ctx, env)` next to `ActionDM`)
- Modify: `cmd/km-slack-bridge/main.go` (store wiring ~line 48-60)

**Interfaces:**
- Produces on `SlackThreadStore`: `SetSession(ctx context.Context, channelID, threadTS, sessionID, agentType string) error`.
- Produces: `type ThreadSessionStore interface { LookupSandbox(ctx context.Context, k ThreadKey) (string, error); SetSession(ctx context.Context, k ThreadKey, sessionID, agentType string) error }` with `type ThreadKey struct { Repo string; Number int; ReportID, Target string }`; implementations `*GitHubThreadSessions{Client DDBUpdateItemAPI; TableName string}` (key `repo` S, `number` N) and `*H1ThreadSessions{Client DDBUpdateItemAPI; TableName string}` (key `report_id` S, `target` S). Both SET `agent_session_id`, `agent_type`, `last_turn_ts`, `ttl_expiry` or `REMOVE agent_session_id` (+ SET `agent_type`) when the session is empty.
- Produces on `Handler`: fields `GitHubSessions ThreadSessionStore` and `H1Sessions ThreadSessionStore` (nil ⇒ `503 source_unavailable`).
- Consumes: `slack.ActionSession` and the envelope fields from Task 1.

- [ ] **Step 1: Write the failing handler tests**

Mirror the fakes in `handler_dm_test.go` (a fake `SlackThreadStore` exists in the package tests — find it with `grep -n 'LookupSandbox(ctx' pkg/slack/bridge/*_test.go` and extend it with `SetSession` recording its arguments). Create `handler_session_test.go`:

```go
package bridge

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

type fakeSessionStore struct {
	owner   map[string]string // key -> sandbox_id
	calls   []string          // "repo/number|sess|agent" or "report/target|sess|agent"
	lookErr error
}

func (f *fakeSessionStore) LookupSandbox(_ context.Context, k ThreadKey) (string, error) {
	if f.lookErr != nil {
		return "", f.lookErr
	}
	return f.owner[sessionKeyString(k)], nil
}
func (f *fakeSessionStore) SetSession(_ context.Context, k ThreadKey, sid, agent string) error {
	f.calls = append(f.calls, sessionKeyString(k)+"|"+sid+"|"+agent)
	return nil
}

func sessionEnv(source, sender string) slack.SlackEnvelope {
	return slack.SlackEnvelope{Action: slack.ActionSession, SenderID: sender, Source: source, SessionID: "sess-1", AgentType: "claude", Nonce: "n", Timestamp: 1, Version: slack.EnvelopeVersion}
}

func TestHandleSession_Slack_OwnedRowIsUpdated(t *testing.T) {
	threads := newFakeThreadStore() // the existing package fake, extended with SetSession recording
	threads.owner["C1|1.1"] = "sb-1"
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "1.1"
	resp := h.handleSession(context.Background(), env)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, resp.Body)
	}
	if got := threads.setSessionCalls; len(got) != 1 || got[0] != "C1|1.1|sess-1|claude" {
		t.Fatalf("SetSession calls = %v", got)
	}
}

func TestHandleSession_Slack_MissingRowIs404_NeverCreates(t *testing.T) {
	threads := newFakeThreadStore()
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "9.9"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 404 || !contains(resp.Body, "thread_unknown") {
		t.Fatalf("want 404 thread_unknown, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(threads.setSessionCalls)+len(threads.upserts) != 0 {
		t.Fatal("a missing row must never be written or created")
	}
}

func TestHandleSession_Slack_ForeignRowIs403(t *testing.T) {
	threads := newFakeThreadStore()
	threads.owner["C1|1.1"] = "sb-OTHER"
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "1.1"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 403 || !contains(resp.Body, "thread_not_owned") {
		t.Fatalf("want 403 thread_not_owned, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_GitHub_UsesItsStore(t *testing.T) {
	gh := &fakeSessionStore{owner: map[string]string{"o/r#7": "sb-1"}}
	h := &Handler{Threads: newFakeThreadStore(), GitHubSessions: gh}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if len(gh.calls) != 1 || gh.calls[0] != "o/r#7|sess-1|claude" {
		t.Fatalf("calls = %v", gh.calls)
	}
}

func TestHandleSession_H1_UnsetStoreIs503(t *testing.T) {
	h := &Handler{Threads: newFakeThreadStore()}
	env := sessionEnv("h1", "sb-1")
	env.ReportID, env.Target = "r1", "ir-bot"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 503 || !contains(resp.Body, "source_unavailable") {
		t.Fatalf("want 503 source_unavailable, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_MissingKeyIs400(t *testing.T) {
	h := &Handler{Threads: newFakeThreadStore(), GitHubSessions: &fakeSessionStore{}}
	for _, env := range []slack.SlackEnvelope{
		func() slack.SlackEnvelope { e := sessionEnv("slack", "sb-1"); e.Channel = "C1"; return e }(), // no thread_ts
		func() slack.SlackEnvelope { e := sessionEnv("github", "sb-1"); e.Repo = "o/r"; return e }(), // no number
		func() slack.SlackEnvelope { e := sessionEnv("h1", "sb-1"); e.ReportID = "r1"; return e }(),  // no target
		func() slack.SlackEnvelope { e := sessionEnv("bogus", "sb-1"); return e }(),
		func() slack.SlackEnvelope { e := sessionEnv("slack", "sb-1"); e.Channel, e.ThreadTS, e.AgentType = "C1", "1.1", "gemini"; return e }(),
	} {
		if resp := h.handleSession(context.Background(), env); resp.StatusCode != 400 {
			t.Fatalf("source=%s: want 400, got %d %s", env.Source, resp.StatusCode, resp.Body)
		}
	}
}

func TestHandleSession_EmptySessionClears(t *testing.T) {
	threads := newFakeThreadStore()
	threads.owner["C1|1.1"] = "sb-1"
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS, env.SessionID = "C1", "1.1", ""
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if got := threads.setSessionCalls; len(got) != 1 || got[0] != "C1|1.1||claude" {
		t.Fatalf("SetSession calls = %v (empty session id must pass through as the clear signal)", got)
	}
}
```
`contains` is `strings.Contains`; `sessionKeyString(k ThreadKey)` is a test helper: `k.Repo+"#"+itoa(k.Number)` when `k.Repo != ""`, else `k.ReportID+"/"+k.Target`. Add `setSessionCalls []string`, `upserts []string`, `owner map[string]string` to the existing fake thread store if it lacks them (its `LookupSandbox` must read `owner[channel+"|"+thread]`).

Then the adapter tests in `aws_adapters_sessions_test.go`, using the package's existing fake DynamoDB client (`grep -n 'UpdateItemInput' pkg/slack/bridge/aws_adapters_test.go` for the recorder pattern):

```go
func TestDDBThreadStore_SetSession_SetsOnlySessionAttributes(t *testing.T) {
	fake := &recordingDDB{} // package fake capturing the last UpdateItemInput
	s := &DDBThreadStore{Client: fake, TableName: "km-slack-threads"}
	if err := s.SetSession(context.Background(), "C1", "1.1", "sess-1", "codex"); err != nil {
		t.Fatal(err)
	}
	in := fake.lastUpdate
	expr := *in.UpdateExpression
	for _, want := range []string{"claude_session_id = :sid", "agent_type = :at", "last_turn_ts = :lt", "ttl_expiry = :ttl"} {
		if !strings.Contains(expr, want) {
			t.Errorf("expression lacks %s: %s", want, expr)
		}
	}
	for _, never := range []string{"sandbox_id", "last_dispatch_ts", "created_at", "turn_count", "last_assistant_msg", "last_processed_event_ts"} {
		if strings.Contains(expr, never) {
			t.Errorf("expression names %s: %s", never, expr)
		}
	}
	if in.ConditionExpression != nil {
		t.Error("SetSession must not carry a condition (ownership is checked by the handler before the write)")
	}
}

func TestDDBThreadStore_SetSession_EmptyRemovesSession(t *testing.T) {
	fake := &recordingDDB{}
	s := &DDBThreadStore{Client: fake, TableName: "t"}
	_ = s.SetSession(context.Background(), "C1", "1.1", "", "claude")
	expr := *fake.lastUpdate.UpdateExpression
	if !strings.Contains(expr, "REMOVE claude_session_id") || !strings.Contains(expr, "agent_type = :at") {
		t.Fatalf("empty session must REMOVE the session attribute and still SET agent_type: %s", expr)
	}
}

func TestGitHubThreadSessions_KeyAndAttribute(t *testing.T) {
	fake := &recordingDDB{}
	s := &GitHubThreadSessions{Client: fake, TableName: "km-github-threads"}
	_ = s.SetSession(context.Background(), ThreadKey{Repo: "o/r", Number: 7}, "sess-1", "claude")
	in := fake.lastUpdate
	if in.Key["repo"].(*dynamodbtypes.AttributeValueMemberS).Value != "o/r" || in.Key["number"].(*dynamodbtypes.AttributeValueMemberN).Value != "7" {
		t.Fatalf("key = %v", in.Key)
	}
	if !strings.Contains(*in.UpdateExpression, "agent_session_id = :sid") {
		t.Fatalf("expr = %s", *in.UpdateExpression)
	}
}

func TestH1ThreadSessions_KeyAndAttribute(t *testing.T) {
	fake := &recordingDDB{}
	s := &H1ThreadSessions{Client: fake, TableName: "km-h1-threads"}
	_ = s.SetSession(context.Background(), ThreadKey{ReportID: "r1", Target: "ir-bot"}, "sess-1", "claude")
	in := fake.lastUpdate
	if in.Key["report_id"].(*dynamodbtypes.AttributeValueMemberS).Value != "r1" || in.Key["target"].(*dynamodbtypes.AttributeValueMemberS).Value != "ir-bot" {
		t.Fatalf("key = %v", in.Key)
	}
}
```
If the package has no recording fake with a `lastUpdate` field, add a minimal one in the new test file implementing `DDBUpdateItemAPI` (its methods: those of `DDBQueryGetPutAPI` + `UpdateItem`; `GetItem` returns `Item` from a `getItem map[string]types.AttributeValue` field so `LookupSandbox` can be tested too).

- [ ] **Step 2: Run — expect compile failures**

Run: `cd pkg/slack/bridge && go test . -run 'TestHandleSession|TestDDBThreadStore_SetSession|TestGitHubThreadSessions|TestH1ThreadSessions' -v; echo exit=$?`

- [ ] **Step 3: Implement**

`events_interfaces.go` — add to `SlackThreadStore`:
```go
	// SetSession records the poller's agent session on a row the bridge routed
	// (2026-10-18): SET claude_session_id (or REMOVE it when sessionID is ""),
	// agent_type, last_turn_ts, ttl_expiry. Never touches sandbox_id,
	// last_dispatch_ts, created_at or turn_count. Ownership is the handler's job.
	SetSession(ctx context.Context, channelID, threadTS, sessionID, agentType string) error
```
and the new interface + key type:
```go
// ThreadKey addresses a GitHub (Repo, Number) or H1 (ReportID, Target) thread row.
type ThreadKey struct {
	Repo     string
	Number   int
	ReportID string
	Target   string
}

// ThreadSessionStore is the session-write surface for the GitHub and H1 threads
// tables, served by the Slack bridge on the pollers' behalf (the only
// sandbox-facing bridge).
type ThreadSessionStore interface {
	LookupSandbox(ctx context.Context, k ThreadKey) (sandboxID string, err error)
	SetSession(ctx context.Context, k ThreadKey, sessionID, agentType string) error
}
```
`aws_adapters.go` — `DDBThreadStore.SetSession`:
```go
func (s *DDBThreadStore) SetSession(ctx context.Context, channelID, threadTS, sessionID, agentType string) error {
	expr, vals := sessionUpdate("claude_session_id", sessionID, agentType)
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
		},
		UpdateExpression:          awssdk.String(expr),
		ExpressionAttributeValues: vals,
	})
	return err
}
```
`aws_adapters_sessions.go`:
```go
package bridge

// sessionUpdate builds the SET/REMOVE expression shared by the three threads
// tables. attr is the session attribute name for the table.
func sessionUpdate(attr, sessionID, agentType string) (string, map[string]dynamodbtypes.AttributeValue) {
	now := time.Now()
	vals := map[string]dynamodbtypes.AttributeValue{
		":at":  &dynamodbtypes.AttributeValueMemberS{Value: agentType},
		":lt":  &dynamodbtypes.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)},
		":ttl": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(now.Add(30*24*time.Hour).Unix(), 10)},
	}
	if sessionID == "" {
		return "SET agent_type = :at, last_turn_ts = :lt, ttl_expiry = :ttl REMOVE " + attr, vals
	}
	vals[":sid"] = &dynamodbtypes.AttributeValueMemberS{Value: sessionID}
	return "SET " + attr + " = :sid, agent_type = :at, last_turn_ts = :lt, ttl_expiry = :ttl", vals
}

type GitHubThreadSessions struct {
	Client    DDBUpdateItemAPI
	TableName string
}

func (s *GitHubThreadSessions) key(k ThreadKey) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"repo":   &dynamodbtypes.AttributeValueMemberS{Value: k.Repo},
		"number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(k.Number)},
	}
}
func (s *GitHubThreadSessions) LookupSandbox(ctx context.Context, k ThreadKey) (string, error) {
	return lookupSandboxID(ctx, s.Client, s.TableName, s.key(k))
}
func (s *GitHubThreadSessions) SetSession(ctx context.Context, k ThreadKey, sessionID, agentType string) error {
	expr, vals := sessionUpdate("agent_session_id", sessionID, agentType)
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: awssdk.String(s.TableName), Key: s.key(k), UpdateExpression: awssdk.String(expr), ExpressionAttributeValues: vals})
	return err
}

type H1ThreadSessions struct {
	Client    DDBUpdateItemAPI
	TableName string
}

func (s *H1ThreadSessions) key(k ThreadKey) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"report_id": &dynamodbtypes.AttributeValueMemberS{Value: k.ReportID},
		"target":    &dynamodbtypes.AttributeValueMemberS{Value: k.Target},
	}
}
func (s *H1ThreadSessions) LookupSandbox(ctx context.Context, k ThreadKey) (string, error) {
	return lookupSandboxID(ctx, s.Client, s.TableName, s.key(k))
}
func (s *H1ThreadSessions) SetSession(ctx context.Context, k ThreadKey, sessionID, agentType string) error {
	expr, vals := sessionUpdate("agent_session_id", sessionID, agentType)
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: awssdk.String(s.TableName), Key: s.key(k), UpdateExpression: awssdk.String(expr), ExpressionAttributeValues: vals})
	return err
}

// lookupSandboxID projects sandbox_id from one row; "" when absent.
func lookupSandboxID(ctx context.Context, c DDBUpdateItemAPI, table string, key map[string]dynamodbtypes.AttributeValue) (string, error) {
	out, err := c.GetItem(ctx, &dynamodb.GetItemInput{TableName: awssdk.String(table), Key: key, ProjectionExpression: awssdk.String("sandbox_id")})
	if err != nil {
		return "", err
	}
	if v, ok := out.Item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}
```
`handler_session.go`:
```go
package bridge

import (
	"context"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// handleSession is ActionSession (2026-10-18): a poller records its agent
// session id + agent_type on a thread row. The row must exist (the bridge
// dispatched into it, or claim_thread created it) and name the caller; a box
// can never mint a row here. Empty session_id clears the attribute.
func (h *Handler) handleSession(ctx context.Context, env slack.SlackEnvelope) *Response {
	if env.AgentType != "claude" && env.AgentType != "codex" {
		return h.sessionReject(ctx, env, 400, "missing_fields", "agent_type must be claude or codex")
	}
	var owner string
	var err error
	write := func() error { return nil }
	switch env.Source {
	case "slack":
		if env.Channel == "" || env.ThreadTS == "" {
			return h.sessionReject(ctx, env, 400, "missing_fields", "channel and thread_ts required")
		}
		owner, err = h.Threads.LookupSandbox(ctx, env.Channel, env.ThreadTS)
		write = func() error { return h.Threads.SetSession(ctx, env.Channel, env.ThreadTS, env.SessionID, env.AgentType) }
	case "github":
		if env.Repo == "" || env.Number == 0 {
			return h.sessionReject(ctx, env, 400, "missing_fields", "repo and number required")
		}
		if h.GitHubSessions == nil {
			return h.sessionReject(ctx, env, 503, "source_unavailable", "bridge has no github threads table")
		}
		k := ThreadKey{Repo: env.Repo, Number: env.Number}
		owner, err = h.GitHubSessions.LookupSandbox(ctx, k)
		write = func() error { return h.GitHubSessions.SetSession(ctx, k, env.SessionID, env.AgentType) }
	case "h1":
		if env.ReportID == "" || env.Target == "" {
			return h.sessionReject(ctx, env, 400, "missing_fields", "report_id and target required")
		}
		if h.H1Sessions == nil {
			return h.sessionReject(ctx, env, 503, "source_unavailable", "bridge has no h1 threads table")
		}
		k := ThreadKey{ReportID: env.ReportID, Target: env.Target}
		owner, err = h.H1Sessions.LookupSandbox(ctx, k)
		write = func() error { return h.H1Sessions.SetSession(ctx, k, env.SessionID, env.AgentType) }
	default:
		return h.sessionReject(ctx, env, 400, "missing_fields", "source must be slack, github or h1")
	}
	if err != nil {
		logger.ErrorContext(ctx, "bridge: session_lookup_failed", "step", "session", "source", env.Source, "sender_id", env.SenderID, "error", err.Error(), "status", 500)
		return errResp(500, "thread_lookup_failed")
	}
	if owner == "" {
		return h.sessionReject(ctx, env, 404, "thread_unknown", "no row for this thread")
	}
	if owner != env.SenderID {
		return h.sessionReject(ctx, env, 403, "thread_not_owned", "row names another sandbox")
	}
	if werr := write(); werr != nil {
		logger.ErrorContext(ctx, "bridge: session_write_failed", "step", "session", "source", env.Source, "sender_id", env.SenderID, "error", werr.Error(), "status", 500)
		return errResp(500, "session_write_failed")
	}
	logger.InfoContext(ctx, "bridge: session", "source", env.Source, "sender_id", env.SenderID, "agent_type", env.AgentType, "cleared", env.SessionID == "", "status", 200)
	return jsonResp(200, map[string]any{"ok": true})
}

func (h *Handler) sessionReject(ctx context.Context, env slack.SlackEnvelope, status int, code, why string) *Response {
	logger.WarnContext(ctx, "bridge: session_"+code, "step", "session", "source", env.Source, "sender_id", env.SenderID, "reason", why, "status", status)
	return errResp(status, code)
}
```
`handler.go`: add `GitHubSessions ThreadSessionStore` and `H1Sessions ThreadSessionStore` to the `Handler` struct; extend the `unknown_action` allowlist with `&& env.Action != slack.ActionSession`; extend `channelRequired` with `&& env.Action != slack.ActionSession`; add `case slack.ActionSession: return h.handleSession(ctx, env)` beside the `ActionDM` case. Check the authz block before the switch (~line 280-320, owned-channel vs `threadRoutedToSender`) — it runs on `env.Channel`; a `session` envelope for GitHub/H1 has no channel and a Slack one names a channel the sender may not own (routed thread). Make the pre-switch channel authz skip `ActionSession` exactly as it skips `lookup-thread`/`dm` (read how those are exempted and mirror it); the handler's own ownership check is the authorization.

`cmd/km-slack-bridge/main.go`: after `handler.Threads = threadStore`, wire
```go
	if t := os.Getenv("KM_GITHUB_THREADS_TABLE"); t != "" {
		handler.GitHubSessions = &bridge.GitHubThreadSessions{Client: ddbClient, TableName: t}
	}
	if t := os.Getenv("KM_H1_THREADS_TABLE"); t != "" {
		handler.H1Sessions = &bridge.H1ThreadSessions{Client: ddbClient, TableName: t}
	}
```
using the same DynamoDB client variable the thread store uses (read the surrounding lines for its name). Log one info line naming which sources are available.

- [ ] **Step 4: Run the package**

Run: `cd pkg/slack/bridge && go test . ; echo exit=$?` and `cd ../../../cmd/km-slack-bridge && go build ./... && go test . ; echo exit=$?`. Every pre-existing fake implementing `SlackThreadStore` needs the new `SetSession` method (compile errors tell you which).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/events_interfaces.go pkg/slack/bridge/aws_adapters.go pkg/slack/bridge/aws_adapters_sessions.go pkg/slack/bridge/aws_adapters_sessions_test.go pkg/slack/bridge/handler_session.go pkg/slack/bridge/handler_session_test.go pkg/slack/bridge/handler.go cmd/km-slack-bridge/main.go
# plus any test file whose fake gained SetSession
git commit -m "feat(slack-bridge): session action — ownership-checked session writes for the Slack, GitHub and H1 threads tables

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 3: `post --claim-thread` — bridge writes the row for a top-level message it just posted

**Files:**
- Modify: `pkg/slack/bridge/handler.go` (`case slack.ActionPost, slack.ActionTest:` branch ~line 324-360)
- Create: `pkg/slack/bridge/handler_claim_thread_test.go`
- Modify: `cmd/km-slack/main.go` (`runPost` flag parsing ~line 128-140; envelope build where `slack.BuildEnvelope(slack.ActionPost, …)` is called)
- Modify: `cmd/km-slack/main_test.go` or a new `cmd/km-slack/post_claim_test.go`

**Interfaces:**
- Consumes: `SlackEnvelope.ClaimThread` (Task 1), `SlackThreadStore.Upsert` (exists).
- Produces: CLI flag `km-slack post --new-message --claim-thread`.

- [ ] **Step 1: Write the failing bridge test**

```go
package bridge

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// claim_thread: after a successful TOP-LEVEL post the bridge writes the thread
// row for the new message, owned by the sender. Threaded posts ignore the flag;
// a failed post writes nothing.
func TestPost_ClaimThread_UpsertsRowForTopLevelPost(t *testing.T) {
	threads := newFakeThreadStore()
	poster := &fakeSlackPoster{ts: "1700.1"} // package fake whose PostMessage returns ts
	h := newTestHandlerWith(threads, poster)   // the package's existing Handler constructor for tests; owned channel "C1" for "sb-1"
	env := slack.SlackEnvelope{Action: slack.ActionPost, SenderID: "sb-1", Channel: "C1", Body: "hello", ClaimThread: true, Nonce: "n", Timestamp: 1, Version: slack.EnvelopeVersion}
	resp := h.dispatch(context.Background(), env) // whatever the package test helper is that reaches the post branch past signature/nonce (see handler_dm_test.go)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if got := threads.upserts; len(got) != 1 || got[0] != "C1|1700.1|sb-1" {
		t.Fatalf("upserts = %v, want the new top-level ts owned by the sender", got)
	}
}

func TestPost_ClaimThread_IgnoredForThreadedPost(t *testing.T) {
	threads := newFakeThreadStore()
	h := newTestHandlerWith(threads, &fakeSlackPoster{ts: "1700.2"})
	env := slack.SlackEnvelope{Action: slack.ActionPost, SenderID: "sb-1", Channel: "C1", ThreadTS: "1600.0", Body: "hi", ClaimThread: true, Nonce: "n", Timestamp: 1, Version: slack.EnvelopeVersion}
	if resp := h.dispatch(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(threads.upserts) != 0 {
		t.Fatalf("threaded post must not claim: %v", threads.upserts)
	}
}

func TestPost_WithoutClaimThread_WritesNoRow(t *testing.T) {
	threads := newFakeThreadStore()
	h := newTestHandlerWith(threads, &fakeSlackPoster{ts: "1700.3"})
	env := slack.SlackEnvelope{Action: slack.ActionPost, SenderID: "sb-1", Channel: "C1", Body: "hi", Nonce: "n", Timestamp: 1, Version: slack.EnvelopeVersion}
	h.dispatch(context.Background(), env)
	if len(threads.upserts) != 0 {
		t.Fatalf("plain post must not claim: %v", threads.upserts)
	}
}
```
Replace `newTestHandlerWith`/`dispatch`/`fakeSlackPoster` with the package's real test helpers (read `handler_dm_test.go` and `handler_test.go` for how an envelope reaches the `ActionPost` branch in tests — most likely a `Handler` literal with `Channels`, `Slack`, `Nonces` fakes and a direct `h.Handle(ctx, &Request{...})` with a signed body; use that shape and say which in the report).

- [ ] **Step 2: Run — expect FAIL (no upsert recorded)**

Run: `cd pkg/slack/bridge && go test . -run 'TestPost_ClaimThread|TestPost_WithoutClaimThread' -v; echo exit=$?`

- [ ] **Step 3: Implement the bridge side**

In the `ActionPost` branch, right after the success log line (`logger.InfoContext(ctx, "bridge: ok", …)`):
```go
			// claim_thread (2026-10-18): the Phase 70 handoff posts a new top-level
			// message and needs a thread row owned by the sender before any reply
			// is dispatched into it. Only the bridge creates rows; here it knows
			// exactly which message it just created and who asked for it.
			if env.ClaimThread && env.ThreadTS == "" && env.Action == slack.ActionPost && h.Threads != nil {
				if uerr := h.Threads.Upsert(ctx, env.Channel, ts, env.SenderID); uerr != nil {
					logger.WarnContext(ctx, "bridge: claim_thread_failed", "step", "dispatch", "channel", env.Channel, "ts", ts, "sender_id", env.SenderID, "error", uerr.Error())
				} else {
					logger.InfoContext(ctx, "bridge: claim_thread", "channel", env.Channel, "ts", ts, "sender_id", env.SenderID)
				}
			}
```
(the `err == nil` else-branch is where `ts` is valid).

- [ ] **Step 4: km-slack flag**

In `runPost`: `fs.BoolVar(&claimThread, "claim-thread", false, "With --new-message: ask the bridge to record the new top-level message as a thread owned by this sandbox (Phase 70 handoff)")`; after parsing, `if claimThread && !newMessage { fmt.Fprintln(stderr, "km-slack post: --claim-thread requires --new-message"); return 2 }`; set `env.ClaimThread = claimThread` on the built envelope (and on the `invalid_blocks` fallback envelope, next to where `Blocks` is set). Test (`cmd/km-slack`): parsing rejects `--claim-thread` without `--new-message` (exit 2); with both, the envelope handed to the bridge-post seam carries `ClaimThread: true` — use the package's existing seam for capturing the posted envelope (`main_dispatch_test.go` shows how `runPost` is driven with a fake bridge).

- [ ] **Step 5: Run both packages**

Run: `cd pkg/slack/bridge && go test . ; echo exit=$?` and `cd ../../../cmd/km-slack && go test . ; echo exit=$?`.

- [ ] **Step 6: Commit**

```bash
git add pkg/slack/bridge/handler.go pkg/slack/bridge/handler_claim_thread_test.go cmd/km-slack/main.go cmd/km-slack/<test file>
git commit -m "feat(slack): post --claim-thread — the bridge records a handoff's new top-level message as a thread owned by the sender

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 4: `km-slack session` verb

**Files:**
- Create: `cmd/km-slack/session.go`, `cmd/km-slack/session_test.go`
- Modify: `cmd/km-slack/main.go` (verb dispatch `switch` ~line 63-77: add `case "session": return runSession(args[1:], stderr)`; help text ~line 90: add a `session` line)

**Interfaces:**
- Consumes: `slack.ActionSession`, envelope fields (Task 1); `loadPrivateKey`, `slack.BuildEnvelope`, `slack.SignEnvelope`, `slack.PostToBridge`, `defaultTimeout` (exist in the package, see `dm.go`).
- Produces: `func runSession(args []string, stderr io.Writer) int`; `func runSessionWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, o sessionOptions) error`; `type sessionOptions struct { source, channel, thread, repo string; number int; report, target, sessionID, agent string; clear bool }`.

CLI:
```
km-slack session --source slack  --channel C… --thread TS       --session-id ID --agent claude|codex
km-slack session --source github --repo owner/name --number N    --session-id ID --agent …
km-slack session --source h1     --report R --target T          --session-id ID --agent …
km-slack session … --clear                      # clears the stored session id (no --session-id)
```

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSession_FlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
		msg  string
	}{
		{"slack ok shape", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--agent", "claude"}, 1, "KM_SANDBOX_ID"}, // passes validation, fails on env (exit 1)
		{"slack missing thread", []string{"--source", "slack", "--channel", "C1", "--session-id", "s", "--agent", "claude"}, 2, "--thread"},
		{"github missing number", []string{"--source", "github", "--repo", "o/r", "--session-id", "s", "--agent", "claude"}, 2, "--number"},
		{"h1 missing target", []string{"--source", "h1", "--report", "r", "--session-id", "s", "--agent", "claude"}, 2, "--target"},
		{"bad source", []string{"--source", "jira", "--session-id", "s", "--agent", "claude"}, 2, "--source"},
		{"bad agent", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--agent", "gemini"}, 2, "--agent"},
		{"session-id and clear", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--clear", "--agent", "claude"}, 2, "--clear"},
		{"neither session-id nor clear", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--agent", "claude"}, 2, "--session-id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KM_SANDBOX_ID", "")
			var stderr bytes.Buffer
			if got := runSession(c.args, &stderr); got != c.want {
				t.Fatalf("exit %d, want %d (stderr: %s)", got, c.want, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.msg) {
				t.Fatalf("stderr %q should mention %s", stderr.String(), c.msg)
			}
		})
	}
}

func TestBuildSessionEnvelope_Shapes(t *testing.T) {
	env, err := buildSessionEnvelope("sb-1", sessionOptions{source: "github", repo: "o/r", number: 7, sessionID: "s1", agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if env.Action != "session" || env.Source != "github" || env.Repo != "o/r" || env.Number != 7 || env.SessionID != "s1" || env.AgentType != "codex" || env.SenderID != "sb-1" {
		t.Fatalf("envelope = %+v", env)
	}
	env2, _ := buildSessionEnvelope("sb-1", sessionOptions{source: "slack", channel: "C1", thread: "1.1", clear: true, agent: "claude"})
	if env2.SessionID != "" || env2.Channel != "C1" || env2.ThreadTS != "1.1" {
		t.Fatalf("clear envelope = %+v", env2)
	}
	env3, _ := buildSessionEnvelope("sb-1", sessionOptions{source: "h1", report: "r1", target: "ir-bot", sessionID: "s", agent: "claude"})
	if env3.ReportID != "r1" || env3.Target != "ir-bot" {
		t.Fatalf("h1 envelope = %+v", env3)
	}
}
```
Plus one bridge-response test using the package's existing fake-bridge seam (see `main_dispatch_test.go`): a 404 `thread_unknown` body makes `runSessionWith` return an error containing `thread_unknown`; a 200 `{"ok":true}` returns nil.

- [ ] **Step 2: Run — expect compile failure**

Run: `cd cmd/km-slack && go test . -run 'TestRunSession|TestBuildSessionEnvelope' -v; echo exit=$?`

- [ ] **Step 3: Implement `session.go`** (model on `dm.go`; same env requirements `KM_SANDBOX_ID`, `KM_SLACK_BRIDGE_URL`, `AWS_REGION`; same signing key load; `defaultTimeout`)

```go
type sessionOptions struct {
	source, channel, thread, repo string
	number                        int
	report, target                string
	sessionID, agent              string
	clear                         bool
}

func runSession(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o sessionOptions
	fs.StringVar(&o.source, "source", "", "slack | github | h1")
	fs.StringVar(&o.channel, "channel", "", "slack: channel id")
	fs.StringVar(&o.thread, "thread", "", "slack: thread ts")
	fs.StringVar(&o.repo, "repo", "", "github: owner/name")
	fs.IntVar(&o.number, "number", 0, "github: issue or PR number")
	fs.StringVar(&o.report, "report", "", "h1: report id")
	fs.StringVar(&o.target, "target", "", "h1: target alias")
	fs.StringVar(&o.sessionID, "session-id", "", "agent session id to record (or --clear)")
	fs.BoolVar(&o.clear, "clear", false, "clear the stored session id")
	fs.StringVar(&o.agent, "agent", "", "claude | codex")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if msg := validateSessionOptions(o); msg != "" {
		fmt.Fprintln(stderr, "km-slack session: "+msg)
		return 2
	}
	sandboxID, bridgeURL := os.Getenv("KM_SANDBOX_ID"), os.Getenv("KM_SLACK_BRIDGE_URL")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if sandboxID == "" || bridgeURL == "" || region == "" {
		fmt.Fprintln(stderr, "km-slack session: KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL and AWS_REGION must be set")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack session: load signing key: %v\n", err)
		return 1
	}
	if err := runSessionWith(ctx, priv, sandboxID, bridgeURL, o); err != nil {
		fmt.Fprintf(stderr, "km-slack session: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "km-slack: session recorded")
	return 0
}

func validateSessionOptions(o sessionOptions) string {
	switch o.source {
	case "slack":
		if o.channel == "" || o.thread == "" {
			return "--source slack needs --channel and --thread"
		}
	case "github":
		if o.repo == "" || o.number == 0 {
			return "--source github needs --repo and --number"
		}
	case "h1":
		if o.report == "" || o.target == "" {
			return "--source h1 needs --report and --target"
		}
	default:
		return "--source must be slack, github or h1"
	}
	if o.agent != "claude" && o.agent != "codex" {
		return "--agent must be claude or codex"
	}
	if o.clear && o.sessionID != "" {
		return "--clear and --session-id are mutually exclusive"
	}
	if !o.clear && o.sessionID == "" {
		return "one of --session-id or --clear is required"
	}
	return ""
}

func buildSessionEnvelope(sandboxID string, o sessionOptions) (*slack.SlackEnvelope, error) {
	env, err := slack.BuildEnvelope(slack.ActionSession, sandboxID, o.channel, "", "", o.thread)
	if err != nil {
		return nil, err
	}
	env.Source, env.Repo, env.Number, env.ReportID, env.Target, env.AgentType = o.source, o.repo, o.number, o.report, o.target, o.agent
	if !o.clear {
		env.SessionID = o.sessionID
	}
	return env, nil
}

func runSessionWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, o sessionOptions) error {
	env, err := buildSessionEnvelope(sandboxID, o)
	if err != nil {
		return err
	}
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return err
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New("bridge returned not-ok: " + resp.Error)
	}
	return nil
}
```
Check `slack.BuildEnvelope`'s validation: if it rejects an empty `channel` for non-dm actions, pass through whatever `dm` does for a top-level dm (it builds with `o.fromChannel` possibly empty — so empty is allowed). Add `session` to the help text and to the `switch`.

- [ ] **Step 4: Run the package**

Run: `cd cmd/km-slack && go test . ; echo exit=$?`

- [ ] **Step 5: Commit**

```bash
git add cmd/km-slack/session.go cmd/km-slack/session_test.go cmd/km-slack/main.go
git commit -m "feat(km-slack): session verb — record the agent session id and agent type on a thread the bridge routed

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 5: Pollers — state-file helpers and the Slack poller

**Files:**
- Modify: `pkg/compiler/userdata.go` — the Slack poller block (`# km-slack-inbound-poller` script; `THREADS_TABLE=` definition ~line 1979; the `DDB_ITEM=$(aws dynamodb get-item` read ~line 2227; the `LAST_EVENT` guard just below; the Phase 70 handoff Step 3 `km-slack post --new-message` and Step 8 state rewrite ~line 2470-2550; the end-of-turn `aws dynamodb update-item` ~line 2705)
- Modify: `pkg/compiler/userdata_slack_inbound_test.go` (`TestPoller_ThreadRowWriteIsUpdateNotPut` ~line 930 → replaced)
- Create: `pkg/compiler/userdata_session_state_test.go`
- Modify: `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` (hand-patch)

**Interfaces:**
- Produces (bash, rendered once per poller via a Go template fragment `{{ template "km-session-helpers" }}` or a plain Go const spliced into each poller — choose the const spliced with `{{ .SessionHelpers }}` only if the template already uses such a pattern; otherwise paste the block verbatim into each of the three pollers and pin identity with a test):
  - `km_session_file <source> <key>` → prints `/var/lib/km/sessions/<source>/<key>.json` and `mkdir -p`s the dir (`0700`).
  - `km_session_read <file>` → sets `S_SESSION_ID S_AGENT_TYPE S_LAST_MSG S_LAST_EVENT` (empty when the file is absent or unparsable).
  - `km_session_write <file> <session_id> <agent_type> <last_msg_file_or_empty> <last_event>` → atomic JSON write (`jq -n --arg … > tmp && mv tmp file`), `updated_at` ISO-8601.
- Consumes: `km-slack session` (Task 4), `km-slack post --claim-thread` (Task 3).

- [ ] **Step 1: Write the failing tests**

`userdata_session_state_test.go` (package `compiler`, executes the rendered bash):
```go
package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sessionHelpersBlock lifts the helper definitions out of the rendered Slack
// poller (between the markers the template carries).
func sessionHelpersBlock(t *testing.T) string {
	t.Helper()
	p := pollerWithAgentClaude(t)
	a, b := strings.Index(p, "# --- km-session-helpers ---"), strings.Index(p, "# --- end km-session-helpers ---")
	if a < 0 || b < a {
		t.Fatal("session helper markers missing from the Slack poller")
	}
	return p[a:b]
}

func TestSessionHelpers_WriteThenReadRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	block := strings.ReplaceAll(sessionHelpersBlock(t), "/var/lib/km/sessions", dir)
	msg := filepath.Join(dir, "msg.txt")
	os.WriteFile(msg, []byte("say \"hi\"\nback\\slash"), 0o644)
	script := "set -euo pipefail\n" + block + `
F=$(km_session_file slack "C1_1700.1")
km_session_write "$F" sess-1 codex "` + msg + `" 1700.2
km_session_read "$F"
printf '%s|%s|%s|%s\n' "$S_SESSION_ID" "$S_AGENT_TYPE" "$S_LAST_EVENT" "$S_LAST_MSG"
km_session_read "$(km_session_file slack nope)"; printf 'absent=[%s|%s]\n' "$S_SESSION_ID" "$S_AGENT_TYPE"
stat -f '%Lp' "$(dirname "$F")" 2>/dev/null || stat -c '%a' "$(dirname "$F")"`
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("helpers failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "sess-1|codex|1700.2|say \"hi\"\nback\\slash") {
		t.Fatalf("round trip lost data:\n%s", s)
	}
	if !strings.Contains(s, "absent=[|]") {
		t.Fatalf("absent file must read as empty:\n%s", s)
	}
	if !strings.Contains(s, "700") {
		t.Fatalf("sessions dir must be 0700:\n%s", s)
	}
}

func TestSessionHelpers_WriteIsAtomic(t *testing.T) {
	block := sessionHelpersBlock(t)
	if !strings.Contains(block, "mktemp") || !strings.Contains(block, "mv -f") {
		t.Fatalf("km_session_write must write to a temp file and mv it into place:\n%s", block)
	}
}

// The template must not reach any threads table from a box any more.
func TestUserdata_NoDynamoDBAgainstThreadsTables(t *testing.T) {
	for i, line := range strings.Split(userDataTemplate, "\n") {
		if strings.Contains(line, "THREADS_TABLE") {
			t.Errorf("line %d still names a threads table variable: %s", i+1, strings.TrimSpace(line))
		}
	}
	rest := userDataTemplate
	for {
		i := strings.Index(rest, "aws dynamodb ")
		if i < 0 {
			break
		}
		block := rest[i:]
		if end := strings.Index(block, "\n\n"); end > 0 {
			block = block[:end]
		}
		if strings.Contains(block, "threads") {
			t.Errorf("a dynamodb call still targets a threads table:\n%s", block)
		}
		rest = rest[i+len("aws dynamodb "):]
	}
}
```
In `userdata_slack_inbound_test.go` replace `TestPoller_ThreadRowWriteIsUpdateNotPut` with `TestPoller_EndOfTurnRecordsSessionLocallyAndViaBridge`: lift the end-of-turn block (from the comment `# Record the turn's session` you will add, to `2>/dev/null || true` after the `km-slack session` call), run it under bash with a fake `km-slack` recording argv and a temp sessions dir, with `THREAD_TS=1700.1 CHANNEL=C1 NEW_SESSION=sess-1 EFFECTIVE_AGENT=claude EVENT_TS=1700.2 LAST_MSG_JSON` set as the old test did; assert (a) the state file exists with `session_id=sess-1`, `agent_type=claude`, `last_processed_event_ts=1700.2`; (b) exactly one `km-slack` call: `session --source slack --channel C1 --thread 1700.1 --session-id sess-1 --agent claude`; (c) no `aws` invocation at all (put a fake `aws` on PATH that fails loudly). Keep the "no put-item anywhere" assertion.

Add `TestPoller_HandoffPostClaimsThread`: in the handoff block, the Step 3 `km-slack post` line carries `--new-message` AND `--claim-thread` (string assertion on the lifted block), and Step 8 writes the NEW thread's state file with an empty session and `agent_type=$NEW_AGENT` (assert the block contains `km_session_write "$(km_session_file slack "${REPLY_CHANNEL}_${NEW_TOP_TS}")" "" "$NEW_AGENT"`).

- [ ] **Step 2: Run — expect FAIL / missing markers**

Run: `cd pkg/compiler && go test . -run 'TestSessionHelpers|TestUserdata_NoDynamoDBAgainstThreads|TestPoller_EndOfTurn|TestPoller_Handoff' -v; echo exit=$?`

- [ ] **Step 3: Implement the helpers (paste into the Slack poller right after its `dispatch_as_sandbox()` definition; Task 6 pastes the identical block into the GitHub and H1 pollers)**

```bash
# --- km-session-helpers ---
# Per-thread agent-session state lives on the box (2026-10-18): the sandbox
# role has no access to the threads tables any more. Only the session id and
# agent type leave the box, through `km-slack session` (bridge-verified).
km_session_file() { # <source> <key> -> path; creates the dir
  local d="/var/lib/km/sessions/$1"
  mkdir -p "$d" && chmod 700 /var/lib/km/sessions "$d" 2>/dev/null || true
  printf '%s/%s.json' "$d" "$(printf '%s' "$2" | tr '/ ' '__')"
}
km_session_read() { # <file> -> S_SESSION_ID S_AGENT_TYPE S_LAST_MSG S_LAST_EVENT
  S_SESSION_ID=""; S_AGENT_TYPE=""; S_LAST_MSG=""; S_LAST_EVENT=""
  [ -s "$1" ] || return 0
  S_SESSION_ID=$(jq -r '.session_id // empty' "$1" 2>/dev/null || true)
  S_AGENT_TYPE=$(jq -r '.agent_type // empty' "$1" 2>/dev/null || true)
  S_LAST_MSG=$(jq -r '.last_assistant_msg // empty' "$1" 2>/dev/null || true)
  S_LAST_EVENT=$(jq -r '.last_processed_event_ts // empty' "$1" 2>/dev/null || true)
}
km_session_write() { # <file> <session_id> <agent_type> <last_msg_file|""> <last_event>
  local f="$1" tmp msg=""
  [ -n "$4" ] && [ -s "$4" ] && msg=$(head -c 4000 "$4")
  tmp=$(mktemp "$(dirname "$f")/.tmp.XXXXXX") || return 1
  jq -n --arg sid "$2" --arg at "$3" --arg msg "$msg" --arg ev "$5" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{session_id:$sid, agent_type:$at, last_assistant_msg:$msg, last_processed_event_ts:$ev, updated_at:$now}' > "$tmp" \
    && mv -f "$tmp" "$f"
}
# --- end km-session-helpers ---
```

- [ ] **Step 4: Rewire the Slack poller**

- Delete the `THREADS_TABLE=…` line and its comment.
- Replace the `DDB_ITEM=$(aws dynamodb get-item …)` read and the four `jq -r '.Item.…'` lines with:
  ```bash
      SESSION_FILE=$(km_session_file slack "${CHANNEL}_${THREAD_TS}")
      km_session_read "$SESSION_FILE"
      CLAUDE_SESSION="$S_SESSION_ID"; CURRENT_AGENT="$S_AGENT_TYPE"; LAST_ASSISTANT_MSG="$S_LAST_MSG"; LAST_EVENT="$S_LAST_EVENT"
  ```
  (keep the Phase 119 guard that compares `EVENT_TS` to `LAST_EVENT` unchanged).
- Handoff Step 3: add `--claim-thread` to the `km-slack post --channel "$REPLY_CHANNEL" --new-message …` call. Step 8: after `THREAD_TS="$NEW_TOP_TS"` add `SESSION_FILE=$(km_session_file slack "${REPLY_CHANNEL}_${NEW_TOP_TS}")` and `km_session_write "$SESSION_FILE" "" "$NEW_AGENT" "" ""` so the new thread starts with no session and the new agent pinned.
- End of turn: replace the `aws dynamodb update-item … 2>/dev/null || true` block with
  ```bash
        # Record the turn's session: locally (what this box resumes from and
        # dedupes on) and at the bridge (the session id and agent type other
        # components read). The bridge refuses a thread it did not route to us.
        LAST_MSG_FILE=$(mktemp /tmp/km-lastmsg.XXXXXX); printf '%s' "$LAST_MSG_JSON" | jq -r . > "$LAST_MSG_FILE" 2>/dev/null || : > "$LAST_MSG_FILE"
        km_session_write "$SESSION_FILE" "$NEW_SESSION" "$EFFECTIVE_AGENT" "$LAST_MSG_FILE" "$EVENT_TS" || echo "[km-slack-inbound-poller] WARN: session state write failed ($SESSION_FILE)" >&2
        rm -f "$LAST_MSG_FILE"
        /opt/km/bin/km-slack session --source slack --channel "$CHANNEL" --thread "$THREAD_TS" --session-id "$NEW_SESSION" --agent "$EFFECTIVE_AGENT" 2>>"$RUN_DIR/stderr.log" \
          || echo "[km-slack-inbound-poller] WARN: bridge session record failed for thread $THREAD_TS (see $RUN_DIR/stderr.log)" >&2
  ```
  `LAST_MSG_JSON` is the JSON-encoded string the old block passed as `:msg`; `jq -r .` decodes it back to text. Keep the `$NEW_SESSION`-gated placement.
- Any other `.Item.` reads in the Slack poller must be gone (grep).

- [ ] **Step 5: Run, then hand-patch the frozen golden**

Run: `cd pkg/compiler && go test . ; echo exit=$?`. The byte-identity test prints the diff; apply exactly those hunks to `testdata/userdata_learn_v2_pre92_baseline.golden.sh` (helpers block, the read, the handoff flags, the end-of-turn block, the removed `THREADS_TABLE=` line). Never run `CAPTURE_PRE92_BASELINE=1`. The GitHub/H1 pollers still fail `TestUserdata_NoDynamoDBAgainstThreadsTables` until Task 6 — report that as the expected remaining red and leave it (or mark that one test `t.Skip` with a `// Task 6 removes this skip` comment; prefer leaving it red and saying so).

- [ ] **Step 6: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_slack_inbound_test.go pkg/compiler/userdata_session_state_test.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
git commit -m "feat(slack-poller): thread session state in local files + km-slack session; handoff claims its new thread

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 6: GitHub and H1 pollers; retire the fit tests

**Files:**
- Modify: `pkg/compiler/userdata.go` — GitHub poller (`GITHUB_THREADS_TABLE=` ~line 2875; `DDB_THREAD=$(aws dynamodb get-item` ~line 3005; `REMOVE agent_session_id` update ~line 3183; `SET agent_session_id` update ~line 3225) and H1 poller (`H1_THREADS_TABLE=` ~line 3324; get-item ~line 3445; REMOVE ~line 3595; SET ~line 3633)
- Delete: `pkg/compiler/userdata_threads_iam_fit_test.go` (its invariant is now `TestUserdata_NoDynamoDBAgainstThreadsTables` from Task 5)
- Create: `pkg/compiler/userdata_github_h1_session_test.go`
- Regenerate/hand-patch goldens as in Global Constraints

**Interfaces:**
- Consumes: the `km-session-helpers` block (Task 5, pasted verbatim into both pollers), `km-slack session --source github|h1` (Task 4).

- [ ] **Step 1: Write the failing tests**

```go
package compiler

import (
	"strings"
	"testing"
)

func pollerBlock(t *testing.T, startMarker, endMarker string) string {
	t.Helper()
	a, b := strings.Index(userDataTemplate, startMarker), strings.Index(userDataTemplate, endMarker)
	if a < 0 || b < a {
		t.Fatalf("markers %q/%q not found", startMarker, endMarker)
	}
	return userDataTemplate[a:b]
}

// The three pollers carry one identical helper block.
func TestSessionHelpers_IdenticalInEveryPoller(t *testing.T) {
	blocks := map[string]string{}
	rest := userDataTemplate
	for n := 0; ; n++ {
		a := strings.Index(rest, "# --- km-session-helpers ---")
		if a < 0 {
			break
		}
		b := strings.Index(rest[a:], "# --- end km-session-helpers ---")
		blocks[strings.Repeat("x", n)] = rest[a : a+b]
		rest = rest[a+b:]
	}
	if len(blocks) != 3 {
		t.Fatalf("want the helper block in exactly 3 pollers, found %d", len(blocks))
	}
	var first string
	for _, b := range blocks {
		if first == "" {
			first = b
		} else if b != first {
			t.Fatal("helper blocks drifted between pollers")
		}
	}
}

func TestGitHubPoller_SessionViaLocalFileAndBridge(t *testing.T) {
	gh := pollerBlock(t, "# km-github-inbound-poller:", "cat > /etc/systemd/system/km-github-inbound-poller.service") // adjust the end marker to whatever follows the heredoc
	for _, want := range []string{
		`km_session_file github "${REPO}_${NUMBER}"`,
		`km-slack session --source github --repo "$REPO" --number "$NUMBER" --session-id "$NEW_GITHUB_SESSION" --agent "$EFFECTIVE_AGENT"`,
		`km-slack session --source github --repo "$REPO" --number "$NUMBER" --clear --agent`,
	} {
		if !strings.Contains(gh, want) {
			t.Errorf("github poller lacks %q", want)
		}
	}
	if strings.Contains(gh, "aws dynamodb") {
		t.Error("github poller still calls dynamodb")
	}
}

func TestH1Poller_SessionViaLocalFileAndBridge(t *testing.T) {
	h1 := pollerBlock(t, "# km-h1-inbound-poller:", "cat > /etc/systemd/system/km-h1-inbound-poller.service")
	for _, want := range []string{
		`km_session_file h1 "${REPORT_ID}_${TARGET}"`,
		`km-slack session --source h1 --report "$REPORT_ID" --target "$TARGET" --session-id "$NEW_H1_SESSION" --agent "$EFFECTIVE_AGENT"`,
		`km-slack session --source h1 --report "$REPORT_ID" --target "$TARGET" --clear --agent`,
	} {
		if !strings.Contains(h1, want) {
			t.Errorf("h1 poller lacks %q", want)
		}
	}
	if strings.Contains(h1, "aws dynamodb") {
		t.Error("h1 poller still calls dynamodb")
	}
}
```
Find the real start/end markers of each poller heredoc (`grep -n 'km-github-inbound-poller\|km-h1-inbound-poller' pkg/compiler/userdata.go | head`) and use them.

- [ ] **Step 2: Run — expect FAIL**

Run: `cd pkg/compiler && go test . -run 'TestSessionHelpers_Identical|TestGitHubPoller_Session|TestH1Poller_Session|TestUserdata_NoDynamoDBAgainstThreads' -v; echo exit=$?`

- [ ] **Step 3: Rewire both pollers**

GitHub: delete `GITHUB_THREADS_TABLE=…`; paste the helper block after `dispatch_as_sandbox()`; replace the `DDB_THREAD=$(aws dynamodb get-item …)` + two `jq` lines with
```bash
    SESSION_FILE=$(km_session_file github "${REPO}_${NUMBER}")
    km_session_read "$SESSION_FILE"
    GITHUB_SESSION="$S_SESSION_ID"; THREAD_AGENT_TYPE="$S_AGENT_TYPE"
```
(keep the `if [ -n "$REPO" ] && [ -n "$NUMBER" ] && [ "$NUMBER" != "0" ]` guard around it); replace the stale-session `REMOVE` update with
```bash
        km_session_write "$SESSION_FILE" "" "$EFFECTIVE_AGENT" "" "" || true
        /opt/km/bin/km-slack session --source github --repo "$REPO" --number "$NUMBER" --clear --agent "$EFFECTIVE_AGENT" 2>>"$RUN_DIR/stderr.log" || true
```
and the end-of-turn `SET` update with
```bash
      km_session_write "$SESSION_FILE" "$NEW_GITHUB_SESSION" "$EFFECTIVE_AGENT" "" "" || echo "[km-github-inbound-poller] WARN: session state write failed" >&2
      /opt/km/bin/km-slack session --source github --repo "$REPO" --number "$NUMBER" --session-id "$NEW_GITHUB_SESSION" --agent "$EFFECTIVE_AGENT" 2>>"$RUN_DIR/stderr.log" \
        || echo "[km-github-inbound-poller] WARN: bridge session record failed — repo=$REPO PR=#$NUMBER" >&2
```
H1: the same with `h1`, key `"${REPORT_ID}_${TARGET}"`, `--report "$REPORT_ID" --target "$TARGET"`, session var `NEW_H1_SESSION`/`H1_SESSION`. The GitHub/H1 pollers need `KM_SLACK_BRIDGE_URL` and `KM_SANDBOX_ID` for `km-slack session` — confirm their units source `/etc/km/notify.env` (they do, `EnvironmentFile=-/etc/km/notify.env`) and that `KM_SLACK_BRIDGE_URL` is in it on a box without Slack inbound: it is written at runtime by `km create` only when Slack is configured — if a GitHub-only profile has no bridge URL, `km-slack session` fails with the "must be set" error, which the `|| echo WARN` absorbs; note this in the docs task (the bridge-side `agent_type` then stays stale on such profiles; local state still works).

- [ ] **Step 4: Delete the fit test, run, goldens**

`git rm pkg/compiler/userdata_threads_iam_fit_test.go`. Run `cd pkg/compiler && go test . ; echo exit=$?`. Hand-patch the pre-92 golden (GitHub poller hunks; it has no H1 poller); regenerate the other two with their flags; `git diff --stat testdata/` must show only the goldens, each diff only these hunks.

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_github_h1_session_test.go pkg/compiler/testdata/
git rm -q pkg/compiler/userdata_threads_iam_fit_test.go
git commit -m "feat(pollers): GitHub and H1 pollers keep session state locally and record it via km-slack session; no poller touches a threads table

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 7: Role v1.9.0, pins, guards, doctor, bridge IAM

**Files:**
- Create: `infra/modules/ec2spot/v1.9.0/{main,variables,outputs}.tf` (copy of v1.8.0 minus `aws_iam_role_policy.ec2spot_threads_rw`)
- Modify: `infra/templates/sandbox/terragrunt.hcl` (`ec2spot = "v1.9.0"`), `pkg/compiler/ec2spot_timeout_test.go` (`ec2spotModuleDir`, + comment), `pkg/terragrunt/substrate_version_pin_test.go` (expectation)
- Modify: `pkg/terragrunt/slack_threads_sandbox_role_guard_test.go` (back to "no grant at all")
- Modify: `pkg/terragrunt/sandbox_role_call_coverage_test.go` only if it fails (it should not — fewer calls)
- Modify: `internal/app/cmd/doctor_sandbox_role.go` (+ its test)
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/{main,variables}.tf`, `infra/live/use1/lambda-slack-bridge/terragrunt.hcl`
- Create: `pkg/terragrunt/slack_bridge_session_tables_test.go`

**Interfaces:**
- Consumes: `pinnedEC2SpotModuleDir(t)` (exists in the guard file).
- Produces: bridge module variables `github_threads_table_name`, `h1_threads_table_name` (string, default `""`), env `KM_GITHUB_THREADS_TABLE`, `KM_H1_THREADS_TABLE`.

- [ ] **Step 1: Rewrite the threads guard test (RED against v1.8.0)**

Replace `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID` with:
```go
// TestSandboxRole_HasNoGrantOnThreadsTables — the original invariant, true
// again since the pollers moved their session state to local files and the
// km-slack session bridge action (2026-10-18): no Allow statement in the pinned
// ec2spot module may name a threads table, and none may grant DynamoDB writes
// on a wildcard resource.
func TestSandboxRole_HasNoGrantOnThreadsTables(t *testing.T) {
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	if len(files) == 0 {
		t.Fatalf("no .tf under %s", dir)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		rel, _ := filepath.Rel(findRepoRoot(t), f)
		seen := map[int]bool{}
		for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
			a, b := enclosingBlock(s, m[0])
			if a == -1 || seen[a] {
				continue
			}
			seen[a] = true
			stmt := s[a:b]
			if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) {
				continue
			}
			if threadsTableRe.MatchString(stmt) {
				t.Errorf("%s (policy %q): an Allow names a threads table; the sandbox role must have none (session state is local + km-slack session)", rel, owningPolicy(s, a))
			}
			actions := stmtField(stmt, "Action") + stmtField(stmt, "NotAction")
			resources := stmtField(stmt, "Resource") + stmtField(stmt, "NotResource")
			if ddbWriteRe.MatchString(actions) && wildcardResRe.MatchString(resources) {
				t.Errorf("%s (policy %q): DynamoDB write on a wildcard resource reaches the threads tables", rel, owningPolicy(s, a))
			}
		}
	}
}
```
Keep `pinnedEC2SpotModuleDir` and the regex vars (other tests use them); drop the now-unused `attrsRe`/`qualifierRe` if nothing else references them. Rewrite the file's top comment to the new model (local files + bridge action; rows born only in the bridge). Run: `cd pkg/terragrunt && go test . -run TestSandboxRole_HasNoGrant -v; echo exit=$?` → FAIL on v1.8.0's `ec2spot_threads_rw`.

- [ ] **Step 2: Module v1.9.0 + pins**

```bash
cp -R infra/modules/ec2spot/v1.8.0 infra/modules/ec2spot/v1.9.0
```
Delete the whole `resource "aws_iam_role_policy" "ec2spot_threads_rw"` block from `v1.9.0/main.tf` and update the header comment (`# v1.9.0: no threads-table grant — session state is local; the bridge writes the session attributes`). Bump the three pins (`sed -i '' 's/ec2spot = "v1.8.0"/ec2spot = "v1.9.0"/' infra/templates/sandbox/terragrunt.hcl`; the `ec2spotModuleDir` constant; the `v1.8.0` expectation and message in `substrate_version_pin_test.go`); extend the `ec2spotModuleDir` comment with `, and again v1.8.0 -> v1.9.0 (no threads-table grant)`. `terraform init -backend=false && terraform validate` in the new dir, then remove `.terraform*`.

- [ ] **Step 3: Doctor**

In `checkSandboxRoleShape`, add a third reason inside the inline loop: `if strings.HasSuffix(pn, "-threads-rw") { add("threads-table grant (pre-v1.9.0)") }` — refactor the `reason` accumulation into a tiny `add(string)` closure joining with ` + `. Test: a role with `km-sb-x-threads-rw` inline is named with that reason; one without is OK. Update the message text to "pre-v1.9.0 role".

- [ ] **Step 4: Bridge module + live unit + guard**

`lambda-slack-bridge/v1.0.0/variables.tf`: add
```hcl
variable "github_threads_table_name" {
  description = "km-github-threads table; when set, the bridge's `session` action may record agent sessions there on the pollers' behalf (2026-10-18). Empty = source unavailable (503)."
  type        = string
  default     = ""
}
variable "h1_threads_table_name" {
  description = "km-h1-threads table; see github_threads_table_name."
  type        = string
  default     = ""
}
```
`main.tf`: in the Lambda environment block add `KM_GITHUB_THREADS_TABLE = var.github_threads_table_name` and `KM_H1_THREADS_TABLE = var.h1_threads_table_name`; add a statement (in the same policy document as the Slack threads grant) with `Sid = "DDBOtherThreadsSession"`, `Action = ["dynamodb:GetItem", "dynamodb:UpdateItem"]`, `Resource = compact([ var.github_threads_table_name != "" ? "arn:aws:dynamodb:*:${acct}:table/${var.github_threads_table_name}" : "", var.h1_threads_table_name != "" ? "…/${var.h1_threads_table_name}" : "" ])` — because an empty `Resource` list is invalid, wrap the statement in `concat(…, length(compact([...])) > 0 ? [stmt] : [])` as the ec2spot module does for OTP delete.
`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`: add `dependency "github_threads" { config_path = "../dynamodb-github-threads" … mock_outputs table_name = "km-github-threads" }` and `dependency "h1_threads"` (copy the `slack_threads` dependency block's shape incl. `mock_outputs_allowed_terraform_commands`), and inputs `github_threads_table_name = dependency.github_threads.outputs.table_name`, `h1_threads_table_name = dependency.h1_threads.outputs.table_name`. Check `internal/app/cmd/init.go`'s module ORDER list: `lambda-slack-bridge` must come after both `dynamodb-github-threads` and `dynamodb-h1-threads` (today h1-threads is applied after the slack bridge — move the bridge later or confirm terragrunt's dependency ordering is honoured by `km init`'s sequential list; read `regionalModules()` and adjust the order, with the comment explaining why).
New `pkg/terragrunt/slack_bridge_session_tables_test.go`: parse `lambda-slack-bridge/v1.0.0/main.tf` text; assert both env keys reference their vars and both vars appear in a DynamoDB IAM resource (the `TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant` regex shape); assert the live unit declares both dependencies.

- [ ] **Step 5: Run**

`cd pkg/terragrunt && go test . ; echo exit=$?`; `cd ../compiler && go test . -run 'TestEC2S' ; echo exit=$?`; `cd ../../internal/app/cmd && go test . -run 'TestCheckSandboxRoleShape' ; echo exit=$?`; `go vet ./internal/app/cmd/`.

- [ ] **Step 6: Commit**

```bash
git add infra/modules/ec2spot/v1.9.0 infra/templates/sandbox/terragrunt.hcl pkg/compiler/ec2spot_timeout_test.go pkg/terragrunt/substrate_version_pin_test.go pkg/terragrunt/slack_threads_sandbox_role_guard_test.go pkg/terragrunt/slack_bridge_session_tables_test.go internal/app/cmd/doctor_sandbox_role.go internal/app/cmd/doctor_sandbox_role_test.go infra/modules/lambda-slack-bridge/v1.0.0 infra/live/use1/lambda-slack-bridge/terragrunt.hcl internal/app/cmd/init.go
git commit -m "feat(ec2spot): v1.9.0 — no threads-table grant; slack bridge may write github/h1 session attributes; doctor names pre-v1.9.0 roles

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 8: Docs, CLAUDE.md, memory

**Files:** `docs/slack-notifications.md` (§ km-slack verb reference: `session`, `post --claim-thread`; § Bot/default-route text where the threads row is described), `docs/slack-default-route.md` (§6.4: residual closed; §11 follow-ups), `docs/operational-gotchas.md` (least-privilege section: v1.9.0, local state files, the GitHub-only-profile caveat about `KM_SLACK_BRIDGE_URL`), `docs/github-bridge.md` and `docs/h1-bridge.md` (one paragraph each: session continuity now local + `km-slack session`), `CLAUDE.md` (new dated top bullet; helper table `km-slack` row gains `session`; the least-privilege bullet's "threads tables are writable only for a named attribute set" → "the sandbox role has no threads-table grant"), memory `project_slack_thread_row_shared_by_bridge_and_poller.md` (poller no longer writes the row; bridge is the single writer; `MEMORY.md` line).

- [ ] **Step 1:** Make the edits (read each passage first; keep each doc's voice; generic placeholders). The CLAUDE.md bullet:
```
**Thread session state leaves the sandbox role — ec2spot v1.9.0 (2026-10-18):**
- **Pollers keep their per-thread agent session in `/var/lib/km/sessions/<source>/<key>.json`** (session id, agent type, last assistant message, last processed event) and tell the bridge only the session id + agent type through the new signed **`km-slack session`** action; the bridge writes the row only if it names the caller (`404 thread_unknown` never creates; `403 thread_not_owned`). **The sandbox role has no grant on any threads table** (`TestSandboxRole_HasNoGrantOnThreadsTables`), which removes the cross-thread prompt-injection/skip residual of v1.8.0. Rows are born only in the bridge: dispatch, `dm`, and **`km-slack post --new-message --claim-thread`** (the Phase 70 handoff's new thread).
- **Deploy = `make build` → `make build-lambdas` → `km init --dry-run=false`** (bridge code + its two new table grants + the v1.9.0 pin) → recreate; existing boxes keep v1.8.0 until recreated (`km doctor` "Sandbox role shape" names a `-threads-rw` role). Half-deploy: new poller + old bridge ⇒ `400 unknown_action` logged, turn completes, `km-slack reply` session lookup stale until the bridge deploys. A GitHub/H1-only profile with no `KM_SLACK_BRIDGE_URL` keeps local state but cannot record `agent_type` at the bridge (WARN in the poller log).
```
- [ ] **Step 2:** `go test ./pkg/hygiene/ ; echo exit=$?`; commit by pathspec with the trailers:
```bash
git commit -m "docs: thread session state behind the bridge (v1.9.0) — verbs, residual closed, deploy

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 9: Deploy and canary (controller-run)

- [ ] `make build && make build-lambdas && go test ./pkg/slack/... ./pkg/compiler/ ./pkg/terragrunt/ ./cmd/km-slack/ ./cmd/km-slack-bridge/ ; echo exit=$?`; `./km --version` = HEAD; `km init --plan` (expect the slack bridge + nothing destroyed); `km init --dry-run=false` logged to a file (not `| tail`; re-login if it stalls).
- [ ] `km destroy <ir-bot id> --remote --yes && km create profiles/ir-bot.yaml ir-bot`; wait for `running`; role has 16 inline policies (no `-threads-rw`); aggregate size recorded.
- [ ] Negative (uid `sandbox`): `aws dynamodb update-item --table-name km-slack-threads … SET claude_session_id = :s` ⇒ AccessDenied; `get-item` ⇒ AccessDenied; `km-slack session --source slack --channel <foreign> --thread <foreign ts> --session-id x --agent claude` ⇒ `thread_not_owned` or `thread_unknown`.
- [ ] Positive: a Slack turn (owned) and one routed turn ⇒ replies; `/var/lib/km/sessions/slack/<channel>_<ts>.json` present with the session id; the row's `claude_session_id` set (by the bridge) and `sandbox_id` intact; a second turn in the same thread resumes (poller log shows `--resume`); a `codex:` handoff ⇒ new top-level, the new thread's row has `sandbox_id` (claim) and the reply lands in the new thread; `km-slack reply` from the box using the session id finds the thread; stop/resume keeps the state files; `km doctor` clean.
- [ ] Go ⇒ write §8 UAT results into the spec, commit, push, open the PR. No-go ⇒ repin v1.8.0, redeploy the bridge from `main`, recreate the canary.

---

## Self-review (plan time)

- **Spec coverage.** §3 local files → T5/T6; `session` action + codes → T2; ownership check → T2; `claim_thread` → T3; role v1.9.0 → T7; bridge IAM/env → T7; GitHub/H1 bridges unchanged → (nothing); half-deploys → T8 docs + behaviour falls out of T2/T4 (`unknown_action` from an old bridge is already the bridge's response); §4.1 fields → T1; §4.2 verb → T4; §4.3 handler + stores → T2; §4.4 claim → T3; §4.5 pollers → T5/T6; §4.6 module/pins/bridge → T7; §5 tests → each task + T9 live; §6 rollout incl. doctor → T7/T9; §7 out of scope untouched.
- **Decided here, not in the spec:** the helper block is pasted verbatim into the three pollers and pinned identical by test (no Go template refactor); `km-slack session` is called from the GitHub/H1 pollers even on profiles without a Slack bridge URL, absorbing the failure with a WARN (documented in T8).
- **Type consistency.** `SetSession(ctx, channelID, threadTS, sessionID, agentType string) error` (T2) is what T2's handler calls; `ThreadKey`/`ThreadSessionStore` names match between the interface, stores, handler and tests; `runSession`/`runSessionWith`/`buildSessionEnvelope`/`sessionOptions` (T4) match their tests; the CLI flag spellings in T4 (`--source --channel --thread --repo --number --report --target --session-id --clear --agent`) are the ones T5/T6's bash and tests use; the helper names `km_session_file/read/write` and the `S_*` variables are identical across T5/T6.
- **Known risk.** `BuildEnvelope` may validate fields for non-dm actions (T4 says to check); the pre-switch channel authz in `Handle` must exempt `ActionSession` (T2 says to mirror `lookup-thread`/`dm`); `km init`'s module order vs the new bridge dependencies (T7 step 4 says to check and move).
