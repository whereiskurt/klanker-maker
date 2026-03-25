# Phase 21 E2E Verification Checklist

This document is the operator procedure for verifying all Phase 21 features on live AWS infrastructure.
Work through each section in order. Record PASS or FAIL for each step.

---

## Prerequisites

- AWS SSO credentials active (`aws sso login --profile <your-profile>`)
- `km` binary built and on PATH (`go build -o km ./cmd/km && export PATH=$PWD:$PATH`)
- At least one sandbox profile ready (e.g. `profiles/sealed.yaml`)
- Domain configured and `km doctor` passes

---

## Section 1: Sidecar E2E Verification (Phase 21 Item 3)

### Prerequisites

- A running sandbox provisioned via `km create <profile.yaml>`
- Note the sandbox ID (e.g. `sb-abc123`) and its EC2 instance ID or ECS task ARN
- SSH or SSM session into the sandbox: `aws ssm start-session --target <instance-id>`

### 1.1 DNS Proxy

**Step 1:** Inside the sandbox, resolve a blocked domain:

```bash
nslookup blocked.example.com
```

**Expected:** NXDOMAIN response or connection timeout (query intercepted by DNS proxy)

**Failure looks like:** A valid IP address is returned — DNS proxy is not intercepting queries

---

**Step 2:** Inside the sandbox, resolve an allowed domain (from `allowedDomains` in profile):

```bash
nslookup <allowed-domain>
# e.g. nslookup github.com
```

**Expected:** A valid IP address is returned — resolution succeeds

**Failure looks like:** NXDOMAIN or timeout for an allowed domain

---

**Step 3:** Check CloudWatch logs for DNS proxy entries:

```bash
aws logs filter-log-events \
  --log-group-name "/km/sandboxes/<sandbox-id>/dns-proxy" \
  --start-time $(date -d '5 minutes ago' +%s000) \
  --query 'events[*].message'
```

**Expected:** JSON log entries with `query` or `domain` fields showing intercepted DNS lookups

**Failure looks like:** No log group or no events

**Result:** [ ] PASS  [ ] FAIL

---

### 1.2 HTTP Proxy

**Step 4:** Inside the sandbox, attempt to reach a blocked host:

```bash
curl -v https://blocked-host.com
# (use a domain not in your allowedDomains list)
```

**Expected:** Proxy returns HTTP 403, or connection is refused by the proxy

**Failure looks like:** 200 OK — proxy is not blocking the request

---

**Step 5:** Inside the sandbox, reach an allowed host:

```bash
curl -v https://<allowed-host>
# e.g. curl -v https://github.com
```

**Expected:** HTTP 200 (or appropriate redirect) — proxy allows the request

**Failure looks like:** 403 or connection refused for an allowed host

---

**Step 6:** Check CloudWatch logs for HTTP proxy CONNECT entries:

```bash
aws logs filter-log-events \
  --log-group-name "/km/sandboxes/<sandbox-id>/http-proxy" \
  --start-time $(date -d '5 minutes ago' +%s000) \
  --query 'events[*].message'
```

**Expected:** JSON log entries with CONNECT method and target host fields

**Failure looks like:** No log group or no events

**Result:** [ ] PASS  [ ] FAIL

---

### 1.3 Audit Log

**Step 7:** Inside the sandbox, run a few shell commands (e.g. `ls`, `whoami`, `date`)

**Step 8:** Check the audit log group for command capture:

```bash
aws logs filter-log-events \
  --log-group-name "/km/sandboxes/<sandbox-id>/" \
  --start-time $(date -d '5 minutes ago' +%s000) \
  --query 'events[*].message'
```

**Expected:** Log entries containing command text, timestamp, and sandbox ID

**Failure looks like:** No audit entries, or entries missing timestamps/command content

**Result:** [ ] PASS  [ ] FAIL

---

### 1.4 OTel Tracing

**Step 9:** Inside the sandbox, run a workload that makes HTTP requests (e.g. `curl https://<allowed-host>`)

**Step 10:** Check your configured OTel collector endpoint for traces tagged with the sandbox ID

**Expected:** Spans with `sandbox.id=<sandbox-id>` attribute appear at the collector

**Step 11:** Verify trace context propagation — check that `X-Trace-Id` (or `traceparent`) headers are preserved through the proxy in collector span data

**Expected:** Parent trace IDs match between outbound request and proxy-logged spans

**Failure looks like:** No traces appear, or spans lack sandbox ID tag, or trace context is broken (parent IDs mismatch)

**Result:** [ ] PASS  [ ] FAIL

---

## Section 2: GitHub Repo Cloning (Phase 21 Item 4)

### Prerequisites

- GitHub App configured: `km configure github` completed
- `km doctor` GitHub check passes
- Sandbox provisioned with `sourceAccess.github` configured for a specific repo (e.g. `{org}/{allowed-repo}`)

### 2.1 Clone an Allowed Repo

**Step 1:** Inside the sandbox:

```bash
git clone https://github.com/<org>/<allowed-repo>
```

**Expected:** Clone succeeds — GitHub App token is injected and the repo is allowed

**Failure looks like:** Authentication error (401/403) or "repository not found"

**Result:** [ ] PASS  [ ] FAIL

---

### 2.2 Clone a Non-Allowed Repo

**Step 2:** Inside the sandbox, attempt to clone a repo not in `sourceAccess.github.repos`:

```bash
git clone https://github.com/<org>/<non-allowed-repo>
```

**Expected:** Authentication failure (401) — GitHub App token does not grant access to this repo

**Failure looks like:** Clone succeeds — access control is not enforced

**Result:** [ ] PASS  [ ] FAIL

---

### 2.3 Push Restriction (if push restrictions configured)

**Step 3:** Inside the sandbox, attempt to push to a non-allowed ref:

```bash
cd <allowed-repo>
git checkout -b test-branch
echo "test" >> README.md
git commit -am "test push"
git push origin test-branch
```

**Expected:** Push is rejected — only allowed refs can be pushed to

**Failure looks like:** Push succeeds to a non-allowed ref

**Result:** [ ] PASS  [ ] FAIL  [ ] N/A (push restrictions not configured)

---

## Section 3: Inter-Sandbox Email (Phase 21 Item 5)

### Prerequisites

- Two running sandboxes (A and B) with email configured in their profiles
- Sandbox A ID: _______________
- Sandbox B ID: _______________
- Email domain (from platform config): _______________
- Sandbox A address: `<A-id>@<domain>`
- Sandbox B address: `<B-id>@<domain>`

### 3.1 Send Email from Sandbox A to Sandbox B

**Step 1:** From sandbox A's context (or via the km API), call `SendSignedEmail` targeting sandbox B's address:

```
To: <B-id>@<domain>
From: <A-id>@<domain>
Subject: inter-sandbox test
Body: Hello from A. KM-AUTH: testphrase123
```

**Expected:** No error returned from `SendSignedEmail`

**Result:** [ ] PASS  [ ] FAIL

---

### 3.2 Receive in Sandbox B's Mailbox

**Step 2:** From sandbox B's context, call `ListMailboxMessages`:

```bash
# via km API or direct S3 list
aws s3 ls s3://<artifact-bucket>/mailboxes/<B-id>/
```

**Expected:** At least one new object appears (the message from A)

**Failure looks like:** No new objects — SES receipt rule or routing is broken

**Result:** [ ] PASS  [ ] FAIL

---

### 3.3 Read and Verify Signature

**Step 3:** Call `ReadMessage` to retrieve the raw message, then `ParseSignedMessage`:

**Expected:** `SignatureOK=true` — the message was signed by A's sandbox key and verification passes

**Failure looks like:** `SignatureOK=false` — signature verification fails (signing or verification bug)

**Step 4:** Verify message content matches what sandbox A sent (body text, subject, From address)

**Result:** [ ] PASS  [ ] FAIL

---

## Section 4: Email Allow-List Enforcement (Phase 21 Item 6)

### Prerequisites

- Sandbox C configured with `allowedSenders: ["self"]` (restrictive — only accepts mail from itself)
- Sandbox D (a third party sandbox, not C) also running
- Sandbox C ID: _______________
- Sandbox D ID: _______________

### 4.1 Blocked Sender Rejected

**Step 1:** From sandbox D, send email to sandbox C:

```
To: <C-id>@<domain>
From: <D-id>@<domain>
Subject: blocked sender test
Body: This should be rejected
```

**Step 2:** Call `ParseSignedMessage` with sandbox C's allow list (`allowedSenders: ["self"]`):

**Expected:** Returns `ErrSenderNotAllowed` or `SignatureOK=false` — D is not in C's allow list

**Failure looks like:** Message is accepted (allow-list enforcement is broken)

**Result:** [ ] PASS  [ ] FAIL

---

### 4.2 Self-Mail Succeeds

**Step 3:** From sandbox C, send email to itself:

```
To: <C-id>@<domain>
From: <C-id>@<domain>
Subject: self-mail test
Body: Self message. KM-AUTH: selfphrase456
```

**Step 4:** Call `ParseSignedMessage` with `allowedSenders: ["self"]` and `expectedSafePhrase: "selfphrase456"`:

**Expected:** `SignatureOK=true` and `SafePhraseOK=true` — self is always on the allow list, safe phrase matches

**Failure looks like:** Self-mail rejected, or safe phrase not extracted

**Result:** [ ] PASS  [ ] FAIL

---

## Section 5: Budget Precision (Phase 21 Plan 01)

### 5.1 km status shows 4-decimal budget amounts

**Step 1:**

```bash
km status <sandbox-id>
```

**Expected:** Budget amounts shown with 4 decimal places (e.g. `$0.0012`, `$1.2345`)

**Failure looks like:** Amounts rounded to 2 decimals (`$0.00` for sub-penny amounts)

**Result:** [ ] PASS  [ ] FAIL

---

### 5.2 km budget add shows 4-decimal confirmation

**Step 2:**

```bash
km budget add <sandbox-id> 0.0050
```

**Expected:** Confirmation message shows `$0.0050` (not `$0.01`)

**Result:** [ ] PASS  [ ] FAIL

---

## Section 6: CloudWatch Log Export Before Destroy (Phase 21 Plan 01)

### 6.1 Destroy a test sandbox and verify S3 log export

**Step 1:** Provision a throwaway sandbox and run a few commands inside to generate logs

**Step 2:** Destroy it:

```bash
km destroy <test-sandbox-id>
```

**Expected in CLI output:** A log line like `exporting sandbox logs to S3` (non-fatal warning if export fails is acceptable)

**Step 3:** Verify logs were exported to S3:

```bash
aws s3 ls s3://<artifact-bucket>/logs/<test-sandbox-id>/
```

**Expected:** One or more `.gz` export objects appear under the `logs/<sandbox-id>` prefix

**Failure looks like:** No objects under the `logs/` prefix after destroy completes

**Result:** [ ] PASS  [ ] FAIL

---

## Section 7: Full Test Suite

**Step 1:** Run the full Go test suite:

```bash
cd <repo-root>
go test ./... -count=1 2>&1 | tail -20
```

**Expected:** All packages PASS. Known pre-existing failures are acceptable:
- `TestBootstrapSCPApplyPath` — requires live AWS KMS/SSO credentials (pre-existing, out of scope)
- `TestDestroyCmd_InvalidSandboxID` — intermittent binary caching flakiness (passes in isolation)

**Result:** [ ] PASS  [ ] PASS with known pre-existing failures  [ ] FAIL (new failure)

If new failures found, document below:

```
Package:
Test:
Error:
```

---

## Sign-Off

| Section | Feature | Result |
|---------|---------|--------|
| 1 | Sidecar E2E (DNS proxy, HTTP proxy, audit log, OTel tracing) | |
| 2 | GitHub repo cloning (allowed succeeds, non-allowed fails) | |
| 3 | Inter-sandbox email (send, receive, verify signature) | |
| 4 | Email allow-list enforcement (blocked rejected, self-mail succeeds) | |
| 5 | Budget precision (4-decimal display in km status and km budget add) | |
| 6 | CloudWatch log export to S3 on destroy | |
| 7 | Full test suite green | |

**Operator:** _______________

**Date:** _______________

**Phase 21 approved:** [ ] YES  [ ] NO

**Notes / known failures:**

```

```
