# Deferred Items — Phase 62

Out-of-scope discoveries from Phase 62 UAT. Do NOT fix in Phase 62.

---

## 1. `km-session-entry: No such file or directory` on `km shell`

**Discovered during:** 62-05 T2 (UAT — sandbox `nt-5cd75540`, 2026-04-26)
**Symptom:** `./km shell <id>` lands operator in a bare `sh-5.2$` shell at `/usr/bin` instead of running `/usr/local/bin/km-session-entry`. `exit` from the broken shell does not return cleanly. Verification proceeded with absolute paths and manual env sourcing.
**Why deferred:** Pre-existing issue, scheduled for Phase 61 (parameterized SSM-document fix). Plans 61-01..61-03 written but not yet executed.
**Owner:** Phase 61.
**Workaround:** Source `/etc/profile.d/*.sh` manually inside the shell.
**Action:** None for Phase 62. Tracked here for traceability.

---

## 2. `./km email read` shows `SIG: FAIL` for sandbox-self-mail

**Discovered during:** 62-05 T3 (UAT — sandbox `nt-5cd75540`, 2026-04-26)
**Symptom:** `./km email read <sandbox-id>` reports `SIG: FAIL` despite the sending side (`km-send`) producing valid signatures (output shows `KM-AUTH phrase appended` and a base64 signature). Email body, headers, and SES MessageId are all valid; only the receive-side verification fails.
**Why deferred:** Not a Phase 62 regression — the hook delivered the email correctly with a valid sandbox-side signature. Verification path is downstream of `km-send` (Phase 14/45/57 territory).
**Likely candidates:**
- Public-key registration timing in DynamoDB at sandbox boot
- MIME body normalization in SES (line endings, charset, multipart boundaries)
- Signature/canonicalization mismatch between `km-send` and `km-recv`
**Owner:** Phase 14, 45, or 57 (email signing/verification stack).
**Action:** None for Phase 62. Tracked here for follow-up investigation.

---

## 3. `TestUnlockCmd_RequiresStateBucket` failing in `internal/app/cmd`

**Discovered during:** 62-05 T8 (full test suite run after T4 inline fix, 2026-04-26)
**Symptom:** `go test ./internal/app/cmd/...` reports:
```
--- FAIL: TestUnlockCmd_RequiresStateBucket (1.56s)
    unlock_test.go:73: error should mention 'state bucket', got: sandbox sb-aabbccdd is not locked
```
**Why deferred:** Pre-existing, unrelated to Phase 62. Test files last touched in Phase 30-02 (`22366b1 feat(30-02): add km lock and km unlock commands with tests`) and Phase 39-03 (`90efc1a feat(39-03): switch all metadata call sites from S3 to DynamoDB`). The DynamoDB migration appears to have changed the error path so the lock check now runs before the state-bucket check.
**Owner:** Phase 39 or whichever phase owns post-migration validation cleanup.
**Action:** None for Phase 62. Phase 62-specific tests (`TestNotify*`, `TestUserDataNotify*`, `TestBuildAgentShellCommands_Notify*`, `TestBuildNotifySendCommands*`, `TestResolveNotifyFlags*`, `TestParse_CLISpec_Notify*`, `TestValidate_NotifyFields*`) all green.
