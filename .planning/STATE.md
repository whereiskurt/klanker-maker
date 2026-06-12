---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_plan: 103-01 complete; next 103-02
status: in-progress
stopped_at: Completed 107-07-PLAN.md — shell pre-flight error fix; TestShellCmd_Stopped/Unknown/MissingInstance all PASS
last_updated: "2026-06-12T02:40:51.256Z"
last_activity: 2026-06-12
progress:
  total_phases: 123
  completed_phases: 109
  total_plans: 553
  completed_plans: 511
  percent: 91
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-03-21)

**Core value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment
**Current focus:** Phase 103 — HackerOne comment-trigger bridge (km-h1-bridge) — Wave 0 (103-01) complete

## Current Position

Phase: 103 (hackerone-comment-trigger-bridge) — 10 plans planned (103-01 → 103-10); Wave 0 (103-01) executed
Plan: 103-01 — all 3 tasks done; synthetic HackerOne webhook bodies + pinned field-paths (OQ1 resolved, OQ2 deferred) + pre-H1 userdata dormancy golden captured
Total Plans in Phase: 10 (103-01 → 103-10)
Current Plan: 103-01 complete; next 103-02
Status: in-progress
Last activity: 2026-06-12

Plan 03 (payload parse) + Plan 08 (byte-identity guard) UNBLOCKED: field-paths.md pins the resolve key + safety-critical internal flag (wrapper-tolerant parse directive); TestUserdataH1ByteIdentity green against the pre-H1 dormancy golden.

Carried to Plan 10 (Wave 6 E2E): re-pin every DOCS-SHAPED field-path row + the OQ2 state endpoint against a real HackerOne Sandbox webhook delivery (synthetic-fallback used in Wave 0 per operator pre-authorization). No production program is a target — only the operator's HackerOne Sandbox account.

Plan 103-05 complete (cmd/km-h1 sandbox helper): comment/read/state over HTTP Basic Auth; internal-by-default reply guard enforced at the flag AND JSON-body level (safety layer 4), --reply-to-researcher explicit for external; 429/5xx backoff; state is OQ2 best-effort (POST /reports/{id}/state_changes + KM_H1_STATE_ENDPOINT override) pending the Plan 10 live pinning. 10/10 tests green. Commits f78707d3, 27d3efb4.

Progress: [█████████░] 91%

## Performance Metrics

**Velocity:**
- Total plans completed: 0
- Average duration: —
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**
- Last 5 plans: —
- Trend: —

*Updated after each plan completion*
| Phase 01-schema-compiler-aws-foundation P01 | 5 | 2 tasks | 14 files |
| Phase 01-schema-compiler-aws-foundation P02 | 25 | 2 tasks | 22 files |
| Phase 01-schema-compiler-aws-foundation P04 | 45 | 1 tasks | 21 files |
| Phase 02-core-provisioning-security-baseline P02 | 4 | 2 tasks | 9 files |
| Phase 02-core-provisioning-security-baseline P01 | 353s | 2 tasks | 12 files |
| Phase 02-core-provisioning-security-baseline P03 | 8 | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P03 | 7min | 2 tasks | 5 files |
| Phase 03-sidecar-enforcement-lifecycle-management P02 | 12min | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P01 | 8min | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P00 | 15 | 2 tasks | 14 files |
| Phase 03-sidecar-enforcement-lifecycle-management P04 | 568s | 3 tasks | 14 files |
| Phase 03-sidecar-enforcement-lifecycle-management P05 | 10min | 2 tasks | 9 files |
| Phase 04-lifecycle-hardening-artifacts-email P01 | 237s | 2 tasks | 8 files |
| Phase 04-lifecycle-hardening-artifacts-email P02 | 3min | 2 tasks | 5 files |
| Phase 04-lifecycle-hardening-artifacts-email P03 | 5min | 3 tasks | 10 files |
| Phase 04-lifecycle-hardening-artifacts-email P04 | 371s | 2 tasks | 8 files |
| Phase 04-lifecycle-hardening-artifacts-email P05 | 282s | 2 tasks | 11 files |
| Phase 02-core-provisioning-security-baseline P04 | 1 | 2 tasks | 0 files |
| Phase 05-configui P01 | 426s | 2 tasks | 9 files |
| Phase 05-configui P02 | 4min | 2 tasks | 6 files |
| Phase 05-configui P03 | 5min | 2 tasks | 6 files |
| Phase 05-configui P04 | 60min | 2 tasks | 9 files |
| Phase 06-budget-enforcement-platform-configuration P02 | 15min | 2 tasks | 11 files |
| Phase 06-budget-enforcement-platform-configuration P01 | 8min | 2 tasks | 7 files |
| Phase 06-budget-enforcement-platform-configuration P03 | 955 | 2 tasks | 14 files |
| Phase 06-budget-enforcement-platform-configuration P04 | 218s | 2 tasks | 5 files |
| Phase 06-budget-enforcement-platform-configuration P05 | 399s | 2 tasks | 11 files |
| Phase 06-budget-enforcement-platform-configuration P06 | 374s | 2 tasks | 6 files |
| Phase 06-budget-enforcement-platform-configuration P07 | 4min | 1 tasks | 6 files |
| Phase 06-budget-enforcement-platform-configuration P08 | 222s | 2 tasks | 5 files |
| Phase 06-budget-enforcement-platform-configuration P09 | 286s | 2 tasks | 5 files |
| Phase 07-unwired-code-paths P01 | 92s | 1 tasks | 2 files |
| Phase 07-unwired-code-paths P02 | 197s | 2 tasks | 6 files |
| Phase 08-sidecar-build-deployment-pipeline P01 | 127s | 2 tasks | 6 files |
| Phase 08-sidecar-build-deployment-pipeline P02 | 2min | 1 tasks | 2 files |
| Phase 09-live-infrastructure-operator-docs P01 | 20min | 2 tasks | 4 files |
| Phase 09-live-infrastructure-operator-docs P02 | 128s | 2 tasks | 5 files |
| Phase 09 P03 | 94s | 1 tasks | 1 files |
| Phase 09-live-infrastructure-operator-docs P04 | 3min | 1 tasks | 2 files |
| Phase 10-scp-sandbox-containment-org-level-ec2-breakout-prevention P01 | 139s | 2 tasks | 4 files |
| Phase 10 P02 | 143s | 1 tasks | 2 files |
| Phase 11-sandbox-auto-destroy-metadata-wiring P01 | 185s | 1 tasks | 6 files |
| Phase 11-sandbox-auto-destroy-metadata-wiring P02 | 406s | 2 tasks | 12 files |
| Phase 12-ecs-budget-topup-s3-replication P02 | 61s | 1 tasks | 1 files |
| Phase 12-ecs-budget-topup-s3-replication P01 | 216s | 1 tasks | 3 files |
| Phase 13-github-app-token-integration-scoped-repo-access-for-sandboxes P02 | 6min | 2 tasks | 5 files |
| Phase 13 P01 | 499s | 2 tasks | 5 files |
| Phase 13-github-app-token-integration-scoped-repo-access-for-sandboxes P03 | 18min | 2 tasks | 7 files |
| Phase 13-github-app-token-integration-scoped-repo-access-for-sandboxes P04 | 396s | 2 tasks | 7 files |
| Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust P01 | 31540187 | 2 tasks | 14 files |
| Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust P02 | 5min | 2 tasks | 4 files |
| Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust P03 | 365 | 2 tasks | 5 files |
| Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust P04 | 4min | 2 tasks | 5 files |
| Phase 15 P02 | 506s | 2 tasks | 2 files |
| Phase 15-km-doctor-platform-health-check-and-bootstrap-verification P01 | 9min | 2 tasks | 4 files |
| Phase 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader P01 | 220s | 2 tasks | 12 files |
| Phase 17 P02 | 352s | 2 tasks | 5 files |
| Phase 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader P03 | 184s | 2 tasks | 3 files |
| Phase 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features P03 | 133s | 2 tasks | 2 files |
| Phase 16 P02 | 3min | 2 tasks | 2 files |
| Phase 16-documentation-refresh P01 | 12min | 2 tasks | 3 files |
| Phase 18-loose-ends P01 | 396s | 2 tasks | 5 files |
| Phase 18-loose-ends P03 | 15min | 1 tasks | 4 files |
| Phase 18-loose-ends P02 | 700s | 2 tasks | 5 files |
| Phase 18-loose-ends P04 | 8min | 2 tasks | 6 files |
| Phase 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix P02 | 54 | 1 tasks | 2 files |
| Phase 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix P01 | 71 | 2 tasks | 3 files |
| Phase 20-anthropic-api-metering-claude-code-ai-spend-tracking P01 | 4 | 2 tasks | 3 files |
| Phase 20-anthropic-api-metering-claude-code-ai-spend-tracking P02 | 302 | 2 tasks | 7 files |
| Phase 21 P02 | 452s | 2 tasks | 7 files |
| Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements P01 | 706s | 2 tasks | 10 files |
| Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements P03 | 249s | 2 tasks | 4 files |
| Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements P04 | 10min | 2 tasks | 1 files |
| Phase 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos P01 | 4min | 2 tasks | 8 files |
| Phase 25-github-source-access-restrictions P02 | 238s | 2 tasks | 4 files |
| Phase 22-remote-sandbox-creation P02 | 232s | 2 tasks | 3 files |
| Phase 22-remote-sandbox-creation P01 | 10min | 2 tasks | 7 files |
| Phase 22-remote-sandbox-creation P03 | 208s | 2 tasks | 8 files |
| Phase 23-credential-rotation P03 | 8min | 1 tasks | 2 files |
| Phase 23-credential-rotation P01 | 266s | 1 tasks | 2 files |
| Phase 23-credential-rotation P02 | 412s | 2 tasks | 3 files |
| Phase 26 P03 | 4 | 2 tasks | 8 files |
| Phase 26-live-operations-hardening P02 | 18min | 2 tasks | 7 files |
| Phase 26 P04 | 10 | 2 tasks | 9 files |
| Phase 26 P05 | 3min | 1 tasks | 2 files |
| Phase 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry P01 | 130s | 2 tasks | 12 files |
| Phase 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry P02 | 202s | 2 tasks | 4 files |
| Phase 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry P03 | 148s | 2 tasks | 2 files |
| Phase 28-github-repo-level-mitm-filtering-in-http-proxy P01 | 524s | 1 tasks | 4 files |
| Phase 28-github-repo-level-mitm-filtering-in-http-proxy P02 | 420s | 1 tasks | 5 files |
| Phase 29-configurable-sandbox-id-prefix P01 | 5min | 2 tasks | 6 files |
| Phase 29-configurable-sandbox-id-prefix P02 | 325s | 2 tasks | 6 files |
| Phase 29-configurable-sandbox-id-prefix P03 | 25min | 2 tasks | 10 files |
| Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock P01 | 8min | 1 tasks | 5 files |
| Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock P02 | 372s | 3 tasks | 13 files |
| Phase 32-profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards P01 | 87s | 1 tasks | 4 files |
| Phase 32 P02 | 289s | 2 tasks | 2 files |
| Phase 32 P03 | 618s | 2 tasks | 2 files |
| Phase 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles P01 | 14min | 2 tasks | 3 files |
| Phase 36-km-sandbox-base-container-image P02 | 136s | 2 tasks | 2 files |
| Phase 36-km-sandbox-base-container-image P01 | 296 | 2 tasks | 2 files |
| Phase 36-km-sandbox-base-container-image P03 | 15 | 2 tasks | 2 files |
| Phase 36-km-sandbox-base-container-image P04 | 3min | 2 tasks | 2 files |
| Phase 37-docker-compose-local-substrate P01 | 6min | 2 tasks | 11 files |
| Phase 37-docker-compose-local-substrate P02 | 7min | 3 tasks | 5 files |
| Phase 37-docker-compose-local-substrate P03 | 8min | 3 tasks | 6 files |
| Phase 39-migrate-sandbox-metadata-s3-to-dynamodb P02 | 131s | 2 tasks | 9 files |
| Phase 39-migrate-sandbox-metadata-s3-to-dynamodb P01 | 184 | 1 tasks | 2 files |
| Phase 40-ebpf-network-enforcement P01 | 4min | 2 tasks | 8 files |
| Phase 40-ebpf-network-enforcement P03 | 221s | 2 tasks | 3 files |
| Phase 40-ebpf-network-enforcement P02 | 224s | 2 tasks | 4 files |
| Phase 40-ebpf-network-enforcement P04 | 3min | 1 tasks | 6 files |
| Phase 40-ebpf-network-enforcement P05 | 282s | 2 tasks | 6 files |
| Phase 40-ebpf-network-enforcement P06 | 16min | 3 tasks | 11 files |
| Phase 40-ebpf-network-enforcement P07 | 585s | 2 tasks | 16 files |
| Phase 41 P01 | 5min | 2 tasks | 12 files |
| Phase 41 P03 | 2min | 1 tasks | 4 files |
| Phase 41-04 P04 | 2min | 1 tasks | 6 files |
| Phase 41 P02 | 4min | 2 tasks | 6 files |
| Phase 41 P05 | 3min | 2 tasks | 3 files |
| Phase 33-ec2-storage-and-ami P01 | 3 | 2 tasks | 5 files |
| Phase 33-ec2-storage-and-ami P02 | 3min | 1 tasks | 4 files |
| Phase 33-ec2-storage-and-ami P03 | 4min | 2 tasks | 6 files |
| Phase 43 P01 | 160s | 2 tasks | 8 files |
| Phase 43 P02 | 7min | 2 tasks | 7 files |
| Phase 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations P01 | 139s | 1 tasks | 4 files |
| Phase 44 P02 | 3min | 2 tasks | 5 files |
| Phase 44 P03 | 318s | 2 tasks | 3 files |
| Phase 44 P04 | 5min | 1 tasks | 3 files |
| Phase 45-km-email-send-recv-scripts-and-cli P1 | 5 | 6 tasks | 4 files |
| Phase 45-km-email-send-recv-scripts-and-cli P02 | 8min | 5 tasks | 2 files |
| Phase 45-km-email-send-recv-scripts-and-cli P03 | 5min | 5 tasks | 2 files |
| Phase 45 P4 | 2380 | 7 tasks | 3 files |
| Phase 31-allowlist-profile-generator P01 | 611s | 2 tasks | 9 files |
| Phase 31-allowlist-profile-generator P02 | 436s | 2 tasks | 6 files |
| Phase 46-ai-email-to-command P01 | 3min | 2 tasks | 6 files |
| Phase 46-ai-email-to-command P02 | 9min | 2 tasks | 4 files |
| Phase 47-privileged-execution-and-learn-profile P01 | 3min | 6 tasks | 5 files |
| Phase 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set P01 | 454 | 1 tasks | 3 files |
| Phase 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set P02 | 10min | 2 tasks | 4 files |
| Phase 50 P01 | 5min | 1 tasks | 3 files |
| Phase 50 P02 | 3min | 1 tasks | 2 files |
| Phase 51 P01 | 2min | 1 tasks | 2 files |
| Phase 51 P02 | 6min | 2 tasks | 2 files |
| Phase 52-clone-sandbox P01 | 4min | 2 tasks | 5 files |
| Phase 52-clone-sandbox P02 | 174 | 2 tasks | 3 files |
| Phase 53-persistent-local-sandbox-numbering P01 | 84 | 1 tasks | 2 files |
| Phase 53 P02 | 515s | 2 tasks | 4 files |
| Phase 54 P01 | 289 | 2 tasks | 2 files |
| Phase 54 P03 | 672s | 2 tasks | 4 files |
| Phase 54 P02 | 27min | 2 tasks | 2 files |
| Phase 55 P01 | 8min | 2 tasks | 4 files |
| Phase 55-learn-mode-command-capture P02 | 515643min | 3 tasks | 6 files |
| Phase 58 P01 | 2min | 2 tasks | 3 files |
| Phase 58 P02 | 687 | 2 tasks | 2 files |
| Phase 58 P03 | 360 | 2 tasks | 2 files |
| Phase 59 P02 | 8min | 2 tasks | 3 files |
| Phase 60 P01 | 10min | 2 tasks | 2 files |
| Phase 60 P02 | 743 | 2 tasks | 5 files |
| Phase 60 P03 | 20min | 2 tasks | 2 files |
| Phase 61-km-shell-ctrl-c-fix P01 | 156 | 2 tasks | 6 files |
| Phase 61-km-shell-ctrl-c-fix P02 | 1210 | 2 tasks | 4 files |
| Phase 33.1-raw-ami-id-support P01 | 163s | 2 tasks | 4 files |
| Phase 33.1 P02 | 420s | 2 tasks | 4 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P02 | 92s | 2 tasks | 3 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P01 | 2min | 2 tasks | 2 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P03 | 12min | 1 tasks | 2 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P04 | 188min | 3 tasks | 3 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P06 | 477 | 3 tasks | 3 files |
| Phase 56-learn-mode-ami-snapshot-and-lifecycle-management P05 | 45min | 2 tasks | 6 files |
| Phase 56.1 P01 | 841s | 3 tasks | 9 files |
| Phase 56.1 P02 | 936 | 3 tasks | 9 files |
| Phase 56.2 P01 | 10min | 3 tasks | 2 files |
| Phase 57-email-enhancement P00 | 3min | 2 tasks | 5 files |
| Phase 57 P01 | 6min | 2 tasks | 1 files |
| Phase 57 P02 | 235 | 3 tasks | 1 files |
| Phase 57 P03 | 157s | 2 tasks | 1 files |
| Phase 57 P04 | 85s | 2 tasks | 2 files |
| Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events P01 | 182s | 2 tasks | 5 files |
| Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events P02 | 18min | 3 tasks | 4 files |
| Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events P04 | 12min | 3 tasks | 5 files |
| Phase 62 P03 | 3 | 2 tasks | 5 files |
| Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events P05 | 90min | 8 tasks | 5 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P02 | 12min | 2 tasks | 4 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P03 | 177 | 1 tasks | 3 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P01 | 5min | 2 tasks | 7 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P04 | 2min | 1 tasks | 2 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P05 | 162 | 2 tasks | 5 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P06 | 7min | 2 tasks | 15 files |
| Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events P08 | 989 | 2 tasks | 12 files |
| Phase 63 P07 | 1135s | 1 tasks | 2 files |
| Phase 63.1 P01 | 993s | 3 tasks | 4 files |
| Phase 63.1 P02 | 11 | 5 tasks | 4 files |
| Phase 63.1 P03 | 45min | 5 tasks | 9 files |
| Phase 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename P01 | 373 | 3 tasks | 7 files |
| Phase 65 P02 | 26min | 3 tasks | 12 files |
| Phase 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename P03 | 553s | 2 tasks | 2 files |
| Phase 65 P04 | 504s | 3 tasks | 5 files |
| Phase 67 P00 | 366 | 2 tasks | 9 files |
| Phase 67-slack-inbound P01 | 102s | 2 tasks | 4 files |
| Phase 67-slack-inbound P03 | 231s | 2 tasks | 4 files |
| Phase 67-slack-inbound P02 | 4min | 2 tasks | 9 files |
| Phase 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch P04 | 240s | 2 tasks | 2 files |
| Phase 67 P05 | 467 | 2 tasks | 5 files |
| Phase 67-slack-inbound P06 | 699s | 2 tasks | 8 files |
| Phase 67-slack-inbound P09 | 18 | 1 tasks | 2 files |
| Phase 67 P07 | 783 | 2 tasks | 8 files |
| Phase 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch P08 | 48min | 2 tasks | 7 files |
| Phase 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch P11 | 6min | 2 tasks | 5 files |
| Phase 67 P12 | 2min | 2 tasks | 3 files |
| Phase 67-slack-inbound P10 | 2 days | 2 tasks | 5 files |
| Phase 67.1-slack-inbound-ack-reaction P01 | 447 | 3 tasks | 6 files |
| Phase 67.1 P02 | 634 | 2 tasks | 4 files |
| Phase 67.1 P03 | ~10min (+ UAT) | 3 tasks | 4 files |
| Phase 68 P00 | 7min | 2 tasks | 17 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P03 | 3min | 2 tasks | 5 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P02 | 4min | 2 tasks | 3 files |
| Phase 68 P01 | 230s | 2 tasks | 4 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P04 | 3min | 2 tasks | 2 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P05 | 30min | 4 tasks | 2 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P06 | 8min | 4 tasks | 6 files |
| Phase 68 P07 | 22min | 3 tasks | 5 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P08 | 8 min | 5 tasks | 6 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P09 | 30min | 5 tasks | 7 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P11 | 7min | 3 tasks | 4 files |
| Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload P10 | 13min | 2 tasks | 5 files |
| Phase 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain P01 | 179 | 2 tasks | 4 files |
| Phase 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain P02 | 1023 | 3 tasks | 14 files |
| Phase 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain P04 | 31539929 | 4 tasks | 22 files |
| Phase 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain P05 | 25min | 3 tasks | 8 files |
| Phase 73-km-vscode-remote-session-via-ssm P00 | 855 | 3 tasks | 8 files |
| Phase 73-km-vscode-remote-session-via-ssm P02 | 323 | 2 tasks | 3 files |
| Phase 73 P01 | 352 | 2 tasks | 2 files |
| Phase 73-km-vscode-remote-session-via-ssm P03 | 9min | 3 tasks | 2 files |
| Phase 73-km-vscode-remote-session-via-ssm P07 | 6min | 1 tasks | 2 files |
| Phase 73-km-vscode-remote-session-via-ssm P04 | 6min | 4 tasks | 4 files |
| Phase 73 P05 | 352 | 1 tasks | 1 files |
| Phase 73-km-vscode-remote-session-via-ssm P06 | 900 | 3 tasks | 3 files |
| Phase 73-km-vscode-remote-session-via-ssm P08 | 158 | 2 tasks | 2 files |
| Phase 73-km-vscode-remote-session-via-ssm P09 | 4min | 2 tasks | 2 files |
| Phase 73-km-vscode-remote-session-via-ssm P09 | 5min | 3 tasks | 2 files |
| Phase 74-slack-mrkdwn-rendering P01 | 1703 | 3 tasks | 27 files |
| Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox P00 | 268s | 1 tasks | 1 files |
| Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox P01 | 4min | 2 tasks | 3 files |
| Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox P03 | 2min | 2 tasks | 2 files |
| Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox P02 | 854s | 2 tasks | 2 files |
| Phase 78 P01 | 32 | 2 tasks | 4 files |
| Phase 78 P02 | 8 | 2 tasks | 2 files |
| Phase 79-km-presence-daemon P00 | 181 | 3 tasks | 6 files |
| Phase 79-km-presence-daemon P03 | 112s | 1 tasks | 1 files |
| Phase 79-km-presence-daemon P01 | 720 | 2 tasks | 3 files |
| Phase 79-km-presence-daemon P02 | 174s | 1 tasks | 2 files |
| Phase 79 P04 | 8min | 2 tasks | 2 files |
| Phase 80-km-cluster-cross-account-irsa-for-k8s-integrations P01 | 4min | 2 tasks | 3 files |
| Phase 80-km-cluster-cross-account-irsa-for-k8s-integrations P02 | 20min | 3 tasks | 5 files |
| Phase 80 P04 | 142s | 2 tasks | 2 files |
| Phase 80 P03 | 198 | 2 tasks | 6 files |
| Phase 80 P05 | 630 | 3 tasks | 4 files |
| Phase 80-km-cluster-cross-account-irsa-for-k8s-integrations P06 | 420 | 3 tasks | 3 files |
| Phase 80.1-auto-detect-existing-oidc-provider-in-cluster-irsa-module-supporting-same-account-irsa-without-manual-flags P02 | 1min | 1 tasks | 1 files |
| Phase 80.1-auto-detect-existing-oidc-provider-in-cluster-irsa-module-supporting-same-account-irsa-without-manual-flags P01 | 3min | 1 tasks | 1 files |
| Phase 80.1-auto-detect-existing-oidc-provider-in-cluster-irsa-module-supporting-same-account-irsa-without-manual-flags P03 | 2 | 3 tasks | 3 files |
| Phase 80.1-auto-detect-existing-oidc-provider-in-cluster-irsa-module-supporting-same-account-irsa-without-manual-flags P04 | 18 | 2 tasks | 2 files |
| Phase 67.2 P01 | 4 min | 3 tasks | 3 files |
| Phase 67.2 P02 | 11min | 3 tasks | 3 files |
| Phase 67.2 P03 | 2 min (Tasks 1-2; Task 3 awaiting operator UAT) | 2 tasks | 2 files |
| Phase 67.2 P03 | 50min | 3 tasks | 2 files |
| Phase 77 P00 | 4min | 3 tasks | 5 files |
| Phase 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback P1 | 3min | 2 tasks | 4 files |
| Phase 77 P02 | 4min | 2 tasks | 2 files |
| Phase 77-failed-sandbox-discoverability P03 | 630 | 1 tasks | 2 files |
| Phase 77-failed-sandbox-discoverability P04 | 631 | 2 tasks | 4 files |
| Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels P01 | 175 | 3 tasks | 4 files |
| Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels P03 | 137s | 2 tasks | 2 files |
| Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels P02 | 438 | 2 tasks | 4 files |
| Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels P04 | 12min | 3 tasks | 7 files |
| Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels P05 | 31541344 | 1 tasks | 1 files |
| Phase 74-slack-mrkdwn-rendering P02 | 6 days | 6 tasks | 27 files |
| Phase 79.1 P01 | 97 | 2 tasks | 2 files |
| Phase 79.1 P02 | 4min | 1 tasks | 1 files |
| Phase 79.1 P03 | 3min | 1 tasks | 1 files |
| Phase 79.1 P04 | 600s | 2 tasks | 2 files |
| Phase 82-multi-instance-resource-prefix-isolation P03 | 8min | 1 tasks | 3 files |
| Phase 82-multi-instance-resource-prefix-isolation P02 | 30min | 2 tasks | 7 files |
| Phase 82-multi-instance-resource-prefix-isolation P01 | 11min | 2 tasks | 2 files |
| Phase 82-multi-instance-resource-prefix-isolation P05 | 617 | 2 tasks | 4 files |
| Phase 82-multi-instance-resource-prefix-isolation P04 | 13min | 2 tasks | 5 files |
| Phase 82 P07 | 30s | 1 tasks | 3 files |
| Phase 82-multi-instance-resource-prefix-isolation P06 | 39s | 1 tasks | 3 files |
| Phase 82-multi-instance-resource-prefix-isolation P08 | 3min | 1 tasks | 3 files |
| Phase 82-multi-instance-resource-prefix-isolation P09 | 220s | 2 tasks | 9 files |
| Phase 82-multi-instance-resource-prefix-isolation P10 | 45min | 5 tasks | 3 files |
| Phase 82.1 P02 | 96 | 2 tasks | 2 files |
| Phase 82.1-01 P01 | 8min | 2 tasks | 2 files |
| Phase 82.1-multi-instance-polish P03 | 25 | 4 tasks | 5 files |
| Phase 84 P02 | 227 | 3 tasks | 4 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P06 | 182 | 1 tasks | 2 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P03 | 6 | 3 tasks | 4 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P05 | 5 | 2 tasks | 5 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P01 | 622 | 3 tasks | 5 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P04 | 770 | 1 tasks | 3 files |
| Phase 84 P08 | 81 | 3 tasks | 2 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P07 | 45 | 3 tasks | 9 files |
| Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix P09 | 2min | 3 tasks | 4 files |
| Phase 84.1 P01 | 16min | 2 tasks | 10 files |
| Phase 84.1 P03 | 25min | 2 tasks | 4 files |
| Phase 84.1 P02 | 18 min | 3 tasks | 6 files |
| Phase 84.1 P04 | 12min | 2 tasks | 9 files |
| Phase 84.1 P05 | 35min | 3 tasks | 9 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P03 | 177 | 1 tasks | 2 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P02 | 20min | 2 tasks | 11 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P01 | 7min | 3 tasks | 11 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P05 | 22min | 1 tasks | 3 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P04 | 933s | 2 tasks | 3 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P06 | 12min | 2 tasks | 5 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P07 | 45min | 2 tasks | 1 files |
| Phase 84.2-km-init-plan-flag-and-destroy-class-gate P08 | 1219 | 3 tasks | 6 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P01 | 524 | 3 tasks | 6 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P02 | 9min | 2 tasks | 8 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P03 | 4min | 2 tasks | 2 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P04 | 8min | 3 tasks | 5 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P06 | 720 | 1 tasks | 4 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P08 | 5 | 1 tasks | 1 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P07 | 347 | 2 tasks | 3 files |
| Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted P09 | 534 | 2 tasks | 3 files |
| Phase 84.3 P10 | 12 | 2 tasks | 3 files |
| Phase 84.4-multi-install-module-hardening P00 | 1134 | 2 tasks | 11 files |
| Phase 84.4-multi-install-module-hardening P01 | 153s | 1 tasks | 2 files |
| Phase 84.4-03 P03 | 2min | 1 tasks | 4 files |
| Phase 84.4-multi-install-module-hardening P04 | 8 | 1 tasks | 5 files |
| Phase 84.4-multi-install-module-hardening P02 | 891 | 2 tasks | 5 files |
| Phase 84.4-multi-install-module-hardening P06 | 883 | 1 tasks | 2 files |
| Phase 84.4-multi-install-module-hardening P05 | 3 | 1 tasks | 3 files |
| Phase 84.4-multi-install-module-hardening P07 | 525755min | 3 tasks | 4 files |
| Phase 84.4.1-multi-install-identity-permission-gap-closure P03 | 171s | 1 tasks | 2 files |
| Phase 84.4.1-multi-install-identity-permission-gap-closure P01 | 232 | 2 tasks | 4 files |
| Phase 84.4.1-multi-install-identity-permission-gap-closure P00 | -215 | 3 tasks | 7 files |
| Phase 84.4.1-multi-install-identity-permission-gap-closure P02 | 840 | 2 tasks | 11 files |
| Phase 84.4.1-multi-install-identity-permission-gap-closure P04 | 851s | 3 tasks | 12 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P00 | 10 | 3 tasks | 7 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P01 | 8 | 1 tasks | 2 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P02 | 344 | 2 tasks | 4 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P03 | 5 | 1 tasks | 2 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P05 | 3 | 1 tasks | 2 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P04 | 4min | 1 tasks | 2 files |
| Phase 84.4.1.1-multi-install-follow-on-gaps P06 | 8min | 1 tasks | 2 files |
| Phase 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup P01 | 3 min | 3 tasks | 3 files |
| Phase 85 P02 | 13min | 3 tasks | 4 files |
| Phase 85 P03 | 13 min | 3 tasks | 3 files |
| Phase 85 P04 | UAT | 3 tasks | 2 files |
| Phase 86-km-create-prompt-queue P01 | 633s | 3 tasks | 4 files |
| Phase 86-km-create-prompt-queue P03 | 724 | 2 tasks | 4 files |
| Phase 86-km-create-prompt-queue P02 | 815 | 2 tasks | 3 files |
| Phase 86-km-create-prompt-queue P05 | 495 | 2 tasks | 4 files |
| Phase 86-km-create-prompt-queue P04 | 900 | 1 tasks | 4 files |
| Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile P01 | 307 | 2 tasks | 7 files |
| Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile P02 | 165s | 2 tasks | 3 files |
| Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile P03 | 342s | 2 tasks | 4 files |
| Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile P04 | 242s | 2 tasks | 3 files |
| Phase 87-additionalsnapshots P06 | 121 | 2 tasks | 4 files |
| Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile P05 | 281s | 1 tasks | 3 files |
| Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher P01 | 2min | 2 tasks | 3 files |
| Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher P04 | 591s | 3 tasks | 10 files |
| Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher P03 | 269s | 2 tasks | 2 files |
| Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher P02 | 345 | 2 tasks | 3 files |
| Phase 70 P05 | 10 | 2 tasks | 3 files |
| Phase 70 P06 | 327s | 3 tasks | 2 files |
| Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher P07 | 22 | 2 tasks | 3 files |
| Phase 70 P08 | 191s | 2 tasks | 3 files |
| Phase 88 P03 | 5min | 1 tasks | 1 files |
| Phase 88 P01 | 3 | 3 tasks | 1 files |
| Phase 88 P06 | 81 | 1 tasks | 1 files |
| Phase 88 P02 | 222s | 3 tasks | 1 files |
| Phase 88 P04 | 7min | 2 tasks | 3 files |
| Phase 88 P05 | 639 | 2 tasks | 3 files |
| Phase 88 P07 | operator-led | 3 tasks | 2 files |
| Phase 89 P01 | 197s | 2 tasks | 6 files |
| Phase 89 P02 | 217 | 3 tasks | 9 files |
| Phase 89-sops-secret-injection-for-sandboxes P04 | 735s | 3 tasks | 4 files |
| Phase 89 P03 | 833s | 2 tasks | 4 files |
| Phase 89 P05 | 750s | 4 tasks | 9 files |
| Phase 89 P06 | 5min | 2 tasks | 5 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P00 | 652 | 3 tasks | 10 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P01 | 2 | 2 tasks | 3 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P02 | 3 | 2 tasks | 4 files |
| Phase 72 P03 | 526s | 2 tasks | 4 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P04 | 5min | 2 tasks | 4 files |
| Phase 72 P07 | 31533176s | 1 tasks | 4 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P05 | 18min | 1 tasks | 4 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P08 | 12 | 1 tasks | 3 files |
| Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator P06 | 16min | 1 tasks | 3 files |
| Phase 72 P09 | 8min | 2 tasks | 4 files |
| Phase 91 P00 | 204s | 3 tasks | 6 files |
| Phase 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot P01 | 140s | 3 tasks | 3 files |
| Phase 91 P02 | 155s | 2 tasks | 2 files |
| Phase 91 P04 | 726 | 2 tasks | 5 files |
| Phase 91 P03 | 20min | 3 tasks | 9 files |
| Phase 91 P05 | 12min | 2 tasks | 3 files |
| Phase 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot P06 | 3min | 3 tasks | 3 files |
| Phase 92 P00 | 4min | 3 tasks | 8 files |
| Phase 92 P01 | 30min | 3 tasks | 78 files |
| Phase 92 P02 | 12min | 3 tasks | 11 files |
| Phase 92 P03 | 55min | 4 tasks | 36 files |
| Phase 92 P04 | 20min | 4 tasks | 24 files |
| Phase 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating P05 | 24min | 4 tasks | 24 files |
| Phase 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward P00 | 8min | 2 tasks | 4 files |
| Phase 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward P01 | 119s | 2 tasks | 3 files |
| Phase 93 P02 | 171s | 2 tasks | 2 files |
| Phase 93 P03 | 297s | 2 tasks | 3 files |
| Phase 93 P05 | 336 | 2 tasks | 4 files |
| Phase 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward P04 | 10min | 1 tasks | 2 files |
| Phase 93-km-desktop P06 | 363 | 3 tasks | 8 files |
| Phase 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle P01 | 13min | 3 tasks | 4 files |
| Phase 94 P02 | 10min | 2 tasks | 3 files |
| Phase 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle P03 | 7min | 2 tasks | 3 files |
| Phase 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle P04 | 612 | 2 tasks | 3 files |
| Phase 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle P05 | 6min | 3 tasks | 11 files |
| Phase 95-slack-federated-bridge-relay-one-app-many-prefixes P01 | 259s | 3 tasks | 7 files |
| Phase 95-slack-federated-bridge-relay-one-app-many-prefixes P02 | 316 | 3 tasks | 6 files |
| Phase 95-slack-federated-bridge-relay-one-app-many-prefixes P03 | 7min | 2 tasks | 5 files |
| Phase 96-slack-default-router-orphan-channel-mention-reply P01 | 274s | 3 tasks | 7 files |
| Phase 96-slack-default-router-orphan-channel-mention-reply P02 | 8min | 3 tasks | 7 files |
| Phase 96-slack-default-router-orphan-channel-mention-reply P03 | 314 | 3 tasks | 6 files |
| Phase 97-github-comment-trigger-mvp P02 | 527 | 3 tasks | 11 files |
| Phase 97 P03 | 13m44s | 3 tasks | 16 files |
| Phase 97-github-comment-trigger-mvp P01 | 801 | 3 tasks | 9 files |
| Phase 97 P04 | 605s | 3 tasks | 13 files |
| Phase 97-github-comment-trigger-mvp P05 | 868 | 2 tasks | 4 files |
| Phase 97 P06 | 1042s | 2 tasks | 7 files |
| Phase 97-github-comment-trigger-mvp P07 | 15 | 3 tasks | 5 files |
| Phase 98-github-bridge-expansion P00 | 590 | 3 tasks | 12 files |
| Phase 98-github-bridge-expansion P03 | 8min | 2 tasks | 3 files |
| Phase 98-github-bridge-expansion P01 | 238s | 2 tasks | 5 files |
| Phase 98-github-bridge-expansion P02 | 540s | 3 tasks | 13 files |
| Phase 98-github-bridge-expansion P04 | 532s | 4 tasks | 10 files |
| Phase 98-github-bridge-expansion P05 | 26m | 2 tasks | 5 files |
| Phase 98-github-bridge-expansion P06 | 8 | 2 tasks | 6 files |
| Phase 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing P01 | 113 | 2 tasks | 2 files |
| Phase 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing P02 | 4 | 2 tasks | 3 files |
| Phase 99 P04 | 323 | 3 tasks | 5 files |
| Phase 99-github-bridge-commands P03 | 11min | 2 tasks | 3 files |
| Phase 99 P05 | 556 | 3 tasks | 5 files |
| Phase 99.1 P01 | 3min | 2 tasks | 3 files |
| Phase 99.1 P02 | 3min | 3 tasks | 7 files |
| Phase 99.1 P03 | 2min | 3 tasks | 6 files |
| Phase 99.1-harden-github-slack-inbound-pollers-against-fifo-poison-message-wedge-via-shared-per-install-dlq-redrivepolicy P04 | 13min | 3 tasks | 7 files |
| Phase 100 P01 | 3m13s | 4 tasks | 7 files |
| Phase 100 P02 | 4min | 2 tasks | 3 files |
| Phase 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges P03 | 188 | 3 tasks | 4 files |
| Phase 100 P04 | 11 | 3 tasks | 8 files |
| Phase 101 P01 | 213s | 3 tasks | 7 files |
| Phase 101 P02 | 286 | 2 tasks | 5 files |
| Phase 101 P03 | 239 | 4 tasks | 4 files |
| Phase 101 P04 | 200s | 2 tasks | 4 files |
| Phase 102 P02 | 324s | 3 tasks | 5 files |
| Phase 102 P01 | 470s | 3 tasks | 5 files |
| Phase 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog P03 | 348 | 3 tasks | 3 files |
| Phase 102 P04 | 429s | 3 tasks | 7 files |
| Phase 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog P05 | 30min | 2 tasks | 1 files |
| Phase 103 P01 | 2min | 3 tasks | 5 files |
| Phase 103 P02 | 8min | 2 tasks | 5 files |
| Phase 103-hackerone-comment-trigger-bridge P03 | 9min | 2 tasks | 6 files |
| Phase 103 P06 | 3m | 2 tasks | 3 files |
| Phase 103 P04 | 17min | 3 tasks | 5 files |
| Phase 103 P07 | 6min | 2 tasks | 6 files |
| Phase 103 P08 | 19min | 2 tasks | 9 files |
| Phase 103 P09 | 18 | 2 tasks | 7 files |
| Phase 103 P10 | 5 min | 2 tasks | 4 files |
| Phase 104 P01 | 1080 | 2 tasks | 11 files |
| Phase 104 P02 | 137 | 2 tasks | 5 files |
| Phase 104 P03 | 775 | 3 tasks | 9 files |
| Phase 104 P04 | 370s | 2 tasks | 5 files |
| Phase 104-slack-channel-o-1-resolution-on-alias-reuse P05 | 240min | 3 tasks | 2 files |
| Phase 105 P01 | 62s | 1 tasks | 1 files |
| Phase 105 P02 | 191 | 1 tasks | 2 files |
| Phase 105 P03 | 380s | 2 tasks | 2 files |
| Phase 105 P04 | 480 | 1 tasks | 2 files |
| Phase 105-scoped-km-init-for-bridge-config P05 | 25min | 2 tasks | 7 files |
| Phase 107 P01 | 2min | 1 tasks | 1 files |
| Phase 107 P03 | 51s | 1 tasks | 1 files |
| Phase 107 P02 | 3min | 1 tasks | 1 files |
| Phase 107-reconcile-22-stale-internal-app-cmd-unit-tests P06 | 225 | 2 tasks | 4 files |
| Phase 107-reconcile-22-stale-internal-app-cmd-unit-tests P04 | 4min | 2 tasks | 4 files |
| Phase 107-07 P07 | 5 | 1 tasks | 1 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Roadmap]: SG-first security model must be established in Phase 2 — VPC Security Groups are the real enforcement boundary; proxy sidecars are a policy layer on top
- [Roadmap]: Profile inheritance semantics (child overrides parent, no additive merge on allowlists) must be defined and tested in Phase 1 before any IAM compilation code is written
- [Roadmap]: INFR (AWS account setup) is assigned to Phase 1 because provisioning in Phase 2 depends on the account structure, Route53, KMS, and S3 being present
- [Roadmap]: MAIL (email/SES) is assigned to Phase 4 alongside artifact hardening — it depends on a working sandbox runtime but is independent of the sidecar enforcement layer
- [Roadmap revision 2026-03-21]: ECS/Fargate is a v1 substrate alongside EC2 — `runtime.substrate: ec2 | ecs` is the selection mechanism; the compiler must produce different Terragrunt artifacts per substrate; Phase 2 includes both ec2-instance and ecs-cluster/ecs-task/ecs-service modules from defcon.run.34
- [Roadmap revision 2026-03-21]: Sidecar model differs by substrate — EC2 sidecars are OS-level processes injected into the instance; ECS sidecars are additional containers in the Fargate task definition; NETW-02, NETW-03, OBSV-01, OBSV-02 must work on both
- [Roadmap revision 2026-03-21]: Kubernetes (k8s/EKS) positioned as v2 PLAT-01; Docker/local substrate remains out of scope for v1
- [Phase 01-schema-compiler-aws-foundation]: go:embed requires schema inside package directory tree — schema lives at schemas/ root for tooling and pkg/profile/schemas/ for embedding
- [Phase 01-schema-compiler-aws-foundation]: ValidateSchema uses YAML->JSON->jsonschema pipeline; jsonschema/v6 AddResource requires parsed JSON value not raw bytes
- [Phase 01-02]: Network module security groups have no egress — Phase 2 profile compiler adds per-profile egress rules based on allowlists
- [Phase 01-02]: ECS service module has no load balancer — sandboxes use service discovery; FARGATE_SPOT preferred capacity strategy
- [Phase 01-02]: ec2spot IMDSv2 enforced (http_tokens=required); SSH removed; SSM-only access
- [Phase 01-04]: CLI architecture: cmd/ entry point -> internal/app/cmd/ Cobra commands -> pkg/ libraries (tiogo pattern)
- [Phase 01-04]: km validate adds file's directory to search paths for extends resolution; schema validation on child bytes, semantic on merged struct
- [Phase 01-04]: Plan 03 artifacts (inherit.go, builtins.go) implemented as Rule 3 auto-fix — blocking dependency for Plan 04
- [Phase 02-core-provisioning-security-baseline]: BuildXxxCommand methods expose exec.Cmd for test inspection without executing terragrunt — preserves testability while keeping Apply/Destroy simple
- [Phase 02-core-provisioning-security-baseline]: ErrSandboxNotFound defined as package-level sentinel — callers use errors.Is() for typed handling in destroy path
- [Phase 02-01]: Baseline SG egress: TCP 443 + UDP 53 to 0.0.0.0/0 in Phase 2; Phase 3 tightens when proxy sidecars enforce per-host filtering
- [Phase 02-01]: sg_egress_rules and iam_session_policy serialized into service.hcl module_inputs — Terragrunt passes them as Terraform variables automatically (NETW-01/NETW-04 reach AWS)
- [Phase 02-01]: Compiler pattern: pure function Compile(profile, sandboxID, onDemand) -> CompiledArtifacts; text/template for HCL generation, never fmt.Sprintf
- [Phase 02-core-provisioning-security-baseline]: findRepoRoot() walks up from source path anchor then falls back to cwd — works in both tests and production without environment variables
- [Phase 02-core-provisioning-security-baseline]: AWS credential validation is the gate between profile parsing and compilation — STS GetCallerIdentity called before any compile or filesystem work
- [Phase 02-core-provisioning-security-baseline]: destroy reconstructs minimal sandbox dir from template when missing locally — only sandbox_id in service.hcl for Terragrunt state key resolution
- [Phase 03-03]: ExitStatus stored as *int in MLflowRun so exit_status=0 (success) is preserved through JSON omitempty serialization
- [Phase 03-03]: S3RunAPI narrow interface (PutObject + GetObject) for MLflow run logging — real *s3.Client satisfies it directly
- [Phase 03-03]: OTel sidecar config uses env-var substitution for AWS_REGION/OTEL_S3_BUCKET/SANDBOX_ID — zero Go config parsing needed
- [Phase 03-02]: Package layout: auditlog.go (package auditlog) + cmd/main.go (package main) in subdirectory — Go disallows two packages in one directory; cmd/ pattern separates library from binary
- [Phase 03-02]: CloudWatchBackend interface in auditlog package decouples sidecar from AWS SDK — tests run without credentials; CWLogsAPI interface in pkg/aws follows same narrow-interface pattern as TagAPI
- [Phase 03-01]: DNS/HTTP proxy library code in dnsproxy/httpproxy subdirs to resolve Go package conflict between library and main
- [Phase 03-01]: InjectTraceContext() exported as testable function since goproxy CONNECT handler chain breaks on first non-nil result
- [Phase 03]: Sidecar library packages use subdirectories (dnsproxy/, httpproxy/, auditlog/) with package main at parent for binary entry points
- [Phase 03]: Wave-0 stubs: dns-proxy, audit-log, http-proxy packages pre-built by linter; scheduler, lifecycle, list-cmd, status-cmd remain as failing stubs for Plans 03-04/03-05
- [Phase Phase 03-04]: SandboxMetadata defined in pkg/aws/metadata.go — sandbox.go stub expected Plan 03-04 to create it
- [Phase Phase 03-04]: DeleteTTLSchedule called BEFORE terragrunt destroy — schedule cancelled even if destroy partially fails
- [Phase Phase 03-04]: TTL schedule creation is non-fatal in km create — sandbox provisioned even if EventBridge call fails
- [Phase 03-05]: SandboxLister/SandboxFetcher DI interfaces exported (uppercase) so cmd_test (external package) can inject fakes without AWS credentials
- [Phase 03-05]: SandboxRecord placed in pkg/aws (not cmd) so it can be shared with future plans that read sandbox state
- [Phase 04-01]: Regex patterns compiled once at NewRedactingDestination construction — safe for concurrent use, zero allocation per Write call
- [Phase 04-01]: UploadArtifacts returns ArtifactSkippedEvent slice for size-limit violations; PutObject failures are logged but not returned
- [Phase 04-01]: S3PutAPI narrow interface (PutObject only) for artifact uploads — mirrors S3RunAPI pattern from mlflow.go
- [Phase 04-lifecycle-hardening-artifacts-email]: SES receipt rule 'position' attribute removed — not supported in provider v6 at rule level (only on actions)
- [Phase 04-lifecycle-hardening-artifacts-email]: CleanupSandboxEmail swallows sesv2types.NotFoundException for idempotent km destroy retries
- [Phase 04-03]: IMDS token TTL changed from 60s to 21600s — spot poll loop runs for hours, 60s token would expire
- [Phase 04-03]: Two-step bind mount required for EC2 read-only enforcement (mount --bind then remount,bind,ro)
- [Phase 04-03]: ECS Fargate writable volumes use scope=task named volumes, not linuxParameters.tmpfs (Fargate does not support tmpfs)
- [Phase 04-03]: UploadArtifacts called for ALL teardown policies including retain — data preservation always desired
- [Phase 04-lifecycle-hardening-artifacts-email]: Profile YAML stored in S3 at artifacts/{sandbox-id}/.km-profile.yaml to enable destroy-path artifact upload without passing profile through command args
- [Phase 04-lifecycle-hardening-artifacts-email]: SES IAM uses ses:FromAddress StringEquals condition — each sandbox can only send from its own address, preventing cross-sandbox email abuse
- [Phase 04-lifecycle-hardening-artifacts-email]: S3 replication excludes mail/ prefix — inbox objects are ephemeral; only artifacts/ is replicated for durability
- [Phase 04-lifecycle-hardening-artifacts-email]: TTL Lambda scope: artifact upload + notification + schedule cleanup only; actual terragrunt destroy delegated (Lambda has no km binary)
- [Phase 04-lifecycle-hardening-artifacts-email]: OnNotify/OnIdleNotify: optional callbacks (nil-safe, best-effort) — backward compatible; past-tense event names match ses.go convention
- [Phase 02-core-provisioning-security-baseline]: All 6 E2E tests passed on real AWS — EC2 spot, EC2 on-demand, ECS Fargate Spot, EC2 destroy, ECS destroy, secrets injection verified with zero orphaned resources
- [Phase 02-core-provisioning-security-baseline]: IMDSv2 enforced on EC2 (http_tokens=required) and SSM-only access confirmed on real AWS infrastructure
- [Phase 05-configui]: package main for all cmd/configui files — Go prohibits two packages per directory; handler logic co-located with main since no separate library consumer
- [Phase 05-configui]: buildTestTemplates() in handlers.go with inline template strings — test isolation without filesystem; truncateID no-op registered for test compatibility
- [Phase 05-configui]: handleSandboxLogs graceful degradation when cwClient nil or call fails — logs are informational, not critical path
- [Phase Phase 05-configui]: handleProfileList dual-mode: HTMX returns profile_list HTML partial; plain API returns JSON array — single handler, no route duplication
- [Phase Phase 05-configui]: Hidden textarea for Monaco initial content — avoids html/template HTML-escaping of YAML special characters
- [Phase Phase 05-configui]: handleProfileSave: validation errors are warnings (save proceeds) — operators may save work-in-progress profiles
- [Phase 05-configui]: [Phase 05-configui P03]: SSMAPI narrow interface in handlers_secrets.go; stub file from Plan 02 deleted to resolve redeclaration conflict
- [Phase 05-configui]: [Phase 05-configui P03]: handleSecretDecrypt returns pii-blur HTML for HTMX requests, JSON for plain API — enables both UI reveal and CLI/API access
- [Phase 05-configui]: [Phase 05-configui P03]: secrets.html is self-contained (not using block override) — avoids multi-page Go template content block conflict in shared parse set
- [Phase 05-configui]: Per-page template cloning in Go html/template prevents content block collision — clone base template per render, parse page template into clone
- [Phase 05-configui]: Dashboard graceful degradation: ListSandboxes AWS failure renders warning banner instead of HTTP 500 — operators can access editor/secrets without AWS credentials
- [Phase 06-02]: BudgetAPI uses DynamoDB ADD expression for atomic spend increment — eliminates read-modify-write races under concurrent sandbox workloads
- [Phase 06-02]: GetBedrockModelRates returns static fallback when client=nil or API unreachable — budget calculations work without Pricing API access
- [Phase 06-02]: DynamoDB Streams enabled with NEW_AND_OLD_IMAGES — enables Lambda budget enforcement triggers to read before/after spend values
- [Phase 06-budget-enforcement-platform-configuration]: Two-viper merge: v1 loads ~/.km/config.yaml, v2 loads ./km-config.yaml; isSetByEnv() guard ensures KM_* env vars always win over km-config.yaml
- [Phase 06-budget-enforcement-platform-configuration]: BudgetTableName defaults to 'km-budgets' in config.go SetDefault — budget plans have usable default without mandatory configuration
- [Phase 06-budget-enforcement-platform-configuration]: km configure io.Reader/io.Writer injection for testability; --non-interactive flag for scripted/CI usage; findKMConfigPath() cwd-first for test binary portability
- [Phase 06-budget-enforcement-platform-configuration]: Config.Domain is empty by default — callers use empty check with klankermaker.ai fallback to preserve TestLoadBackwardCompat compatibility
- [Phase 06-budget-enforcement-platform-configuration]: JSON schema  uses __SCHEMA_DOMAIN__ placeholder replaced at compileSchemaForDomain() call time; apiVersion changed from const to pattern ^.+/v1alpha1$
- [Phase 06-budget-enforcement-platform-configuration]: NetworkConfig.EmailDomain threads domain through compiler pipeline without changing Compile() signature
- [Phase 06-04]: AlwaysMitm registered before OkConnect for Bedrock MITM (goproxy first-match ordering)
- [Phase 06-04]: IncrementAISpend in fire-and-forget goroutine — response never held pending DynamoDB
- [Phase 06-04]: budgetCache.UpdateLocalSpend called synchronously before goroutine so follow-on requests see optimistic increment
- [Phase 06-05]: Budget enforcer uses DynamoDB SET (not ADD) for compute spend — Lambda recalculates absolute cost from CreatedAt each minute, so SET is idempotent
- [Phase 06-05]: Spot rate embedded in EventBridge payload at sandbox creation time — pricing API resolution deferred as TODO in budget_enforcer_inputs compiler output
- [Phase 06-05]: Per-sandbox Lambda naming (km-budget-enforcer-{sandbox-id}) — one Lambda per sandbox for resource isolation and sandbox-scoped IAM conditions
- [Phase 06-06]: EC2 auto-resume uses DescribeInstances tag filter not stored instance ID — avoids metadata schema changes and handles multi-instance sandboxes
- [Phase 06-06]: BudgetFetcher is parallel DI interface to SandboxFetcher — allows independent testing and graceful degradation of budget fetch errors in km status
- [Phase 06-06]: km create budget init is non-fatal Step 12b — sandbox provisioned even if DynamoDB SetBudgetLimits write fails
- [Phase 06-07]: DashboardSandbox wrapper embeds SandboxRecord+Budget pointer — avoids modifying pkg/aws.SandboxRecord (shared type) while giving templates budget data
- [Phase 06-07]: dynoBudgetFetcher treats DynamoDB errors as HasBudget=false — budget display is informational, not critical path; no HTTP 500 from budget failures
- [Phase 06-budget-enforcement-platform-configuration]: NetworkConfig.SpotRateUSD threads spot rate through compiler pipeline without changing Compile() signature — consistent with EmailDomain pattern
- [Phase 06-budget-enforcement-platform-configuration]: staticSpotRate() in separate spot_rate.go isolates lookup table from command logic; unknown instance families get 0.10/hr conservative fallback
- [Phase 06-09]: ShellExecFunc package-level type for DI — tests capture exec.Cmd.Args without executing AWS CLI
- [Phase 06-09]: ECS dispatch passes full cluster/task ARNs (not just IDs) — aws ecs execute-command accepts ARNs directly
- [Phase 06-09]: Rule 3 auto-fix: spot_rate.go created to provide staticSpotRate() referenced in create.go and spot_rate_test.go but missing from package
- [Phase 07-01]: buildDest() takes cwClient param — single CW session shared between destination and idle detector
- [Phase 07-01]: newIdleDetector() helper extracted for testability; OnIdle calls cancel() only, TTL Lambda handles actual destroy
- [Phase 07-02]: MLflow writes in create/destroy are non-fatal (log.Warn + continue) — sandbox lifecycle must not be blocked by observability failures
- [Phase 07-02]: site.hcl accounts block defaults to empty string for KM_ACCOUNTS_* — consuming modules not yet deployed in live (Phase 9 concern)
- [Phase 07-02]: Source-level verification test pattern for MLflow wiring: os.ReadFile(source_file) + strings.Contains checks for call site presence
- [Phase 08-01]: tracing ecr-push uses sidecars/tracing/ as Docker build context (not repo root) — tracing is not a Go binary, no shared pkg/ imports needed
- [Phase 08-01]: audit-log Dockerfile builds from ./sidecars/audit-log/cmd/ not ./sidecars/audit-log/ — cmd/ holds package main; root is package auditlog library
- [Phase 08-02]: ECR URI computation reads KM_ACCOUNTS_APPLICATION at generateECSServiceHCL call time — consistent with KM_ARTIFACTS_BUCKET pattern already in the function
- [Phase 08-02]: PLACEHOLDER_ECR/ prefix used when KM_ACCOUNTS_APPLICATION is unset — parseable HCL, distinguishable from real URIs, no special-casing needed
- [Phase 08-02]: KM_SIDECAR_VERSION defaults to 'latest' when unset — deploy pipeline sets explicit tag; local dev gets a usable default
- [Phase 09-01]: Lambda binaries use GOARCH=arm64 (not amd64) matching architectures=[arm64] in Terraform modules — mismatch causes exec format error
- [Phase 09-01]: KM_ROUTE53_ZONE_ID referenced inline in ses/terragrunt.hcl only — not added to site.hcl per plan spec
- [Phase 09-02]: Budget enforcer terragrunt.hcl reads budget_enforcer_inputs from sibling service.hcl via read_terragrunt_config — template is substrate-agnostic
- [Phase 09-02]: budget-enforcer destroy fires before main sandbox destroy — Lambda depends on sandbox IAM role and instance ID which main module manages
- [Phase 09]: OPERATOR-GUIDE.md documents km configure as the SSO/config entry point (INFR-02) — satisfies requirement through documentation, not code
- [Phase 09]: Deployment ordering in operator guide: network first, then dynamodb-budget/ses/ttl-handler in any order, all before km create
- [Phase 09-04]: lambda_zip_path uses build/budget-enforcer.zip matching Makefile build-lambdas output — dist/ path never existed and caused terragrunt apply to fail at Terraform validation
- [Phase 10-01]: ArnNotLike on aws:PrincipalARN used in SCP statements — NotPrincipal is not supported in SCPs
- [Phase 10-01]: km-ecs-task-* intentionally NOT carved out from any SCP deny — it IS the sandbox workload and must be fully contained
- [Phase 10-01]: Statement-specific carve-out locals (trusted_arns_instance, trusted_arns_iam, trusted_arns_ssm) for per-statement precision — budget-enforcer only bypasses IAM escalation, spot-handler only bypasses instance mutation
- [Phase 10-01]: DenyOrganizationsDiscovery has no condition — applies to ALL roles; management account exempt by AWS design
- [Phase 10-01]: Region lock uses not_actions (NotAction) with global service exemptions; no trusted role carve-out — applies to operators too
- [Phase 10]: Exported TerragruntApplyFunc type and ApplyTerragruntFunc var — external test package cmd_test requires exported symbols for DI; mirrors ShellExecFunc pattern from Phase 06-09
- [Phase 10]: runBootstrap accepts cfg directly when fields are populated — avoids requiring km-config.yaml on disk during unit tests while keeping production path unchanged
- [Phase 11-sandbox-auto-destroy-metadata-wiring]: Deleted defaultStateBucket constant; cfg.StateBucket is sole source of truth for state bucket in all command paths
- [Phase 11-sandbox-auto-destroy-metadata-wiring]: Empty-bucket guard before AWS config load — fast cheap check returning actionable error pointing to KM_STATE_BUCKET env var
- [Phase 11-02]: TeardownFunc uses func(ctx, sandboxID) error — two params only; closure captures AWS clients from main() keeping DI interface simple
- [Phase 11-02]: DestroySandboxResources uses AWS SDK not terragrunt subprocess — Lambda runtime has no km binary
- [Phase 11-02]: EventBridge publish in idle sidecar is best-effort — failure logged but sidecar still exits via cancel()
- [Phase 12-02]: generate 'provider' block must use same name as root to overwrite root provider.tf via overwrite_terragrunt — misname causes duplicate provider blocks
- [Phase 12-02]: Replica region read from KM_REPLICA_REGION env var (default us-west-2); no dependency block since source bucket is pre-existing
- [Phase 12-01]: reprovisionECSSandbox uses existing sandboxID — never generates new; source-level verification pattern for non-DI-injectable functions
- [Phase 12-01]: ArtifactsBucket and AWSProfile added to Config struct (KM_ARTIFACTS_BUCKET, KM_AWS_PROFILE env vars) for ECS budget top-up path
- [Phase 13-02]: github-token-refresher added to trusted_arns_ssm only (not base/instance/iam) — it only needs SSM GetParameter/PutParameter, not EC2/IAM/instance mutation
- [Phase 13-02]: KMS key policy: three-principal model (root admin + Lambda encrypt/decrypt + sandbox role decrypt only)
- [Phase 13-02]: EventBridge Scheduler payload carries kms_key_arn, allowed_repos, permissions — Lambda has all data per invocation without extra SSM reads
- [Phase 13-01]: GitHubAPIBaseURL package-level var enables httptest injection without function signature changes
- [Phase 13-01]: PKCS#1 tried first in key parsing, PKCS#8 as fallback — matches GitHub App key export behavior
- [Phase 13-01]: MockSSMClient exported from pkg/github so external test packages can reuse without duplication
- [Phase 13-03]: GIT_ASKPASS reads /sandbox/${SANDBOX_ID}/github-token at git time — token never in environment variables
- [Phase 13-03]: github_token_inputs emitted for both EC2 and ECS substrates so Lambda/EventBridge infra deploys for ECS sandboxes even though in-sandbox GIT_ASKPASS is deferred
- [Phase 13-03]: permissionsToHCL placed in service_hcl.go — both EC2 and ECS generators use it alongside existing template functions
- [Phase 13-github-app-token-integration-scoped-repo-access-for-sandboxes]: km configure github registers as subcommand of km configure (not root level); SSMWriteAPI narrow interface for PutParameter DI; goto replaced with helper function to avoid Go variable-jump restriction; PEM validation is decode-only in CLI layer
- [Phase 14]: EmailSpec is a pointer on Spec (same pattern as Budget/Artifacts) — nil means email policy not specified
- [Phase 14]: dynamodb-identities uses sandbox_id (S) as sole hash key — one identity row per sandbox, no sort key unlike budget table
- [Phase 14]: No DynamoDB Streams on identities table — identity reads are on-demand lookups, no Lambda trigger needed
- [Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust]: SignEmailBody signs body only (not headers) — simpler to verify, headers can change in transit through SES
- [Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust]: SendSignedEmail uses Content.Raw (not Content.Simple) — SES Simple strips custom X-KM-* headers; Raw MIME preserves them
- [Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust]: box.SealAnonymous / box.OpenAnonymous for NaCl encryption — sender identity in X-KM-Sender-ID header, not ciphertext
- [Phase 14]: KMS alias for identity keys uses KM_PLATFORM_KMS_KEY_ARN env var with alias/km-platform fallback — same as GitHub token Step 13a; cfg.Label/cfg.Region do not exist on Config
- [Phase 14]: NewStatusCmdWithFetchers delegates to NewStatusCmdWithAllFetchers(nil) — backward-compatible extension for identity DI; IdentityFetcher is 4th parameter
- [Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust]: Conditionally add policy DynamoDB attributes only when non-empty — empty string means 'not specified'; omitted attrs preserve legacy row compatibility without schema migration
- [Phase 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust]: Display 'unknown' when IdentityRecord policy field is empty string in km status — signals field exists but was not set at provisioning time (legacy sandbox)
- [Phase 15]: githubManifestBaseURL package-level var for test injection — same pattern as GitHubAPIBaseURL in pkg/github/token.go
- [Phase 15]: ReceiveManifestCodeWithPortCb port callback — allows tests to send HTTP before timeout without polling; base ReceiveManifestCode delegates to no-op callback
- [Phase 15]: redirect_url in manifest JSON body (not query param) — GitHub manifest flow requires it in body for callback redirect
- [Phase 15-01]: DoctorConfigProvider interface abstracts *config.Config so tests use testDoctorConfig without requiring real AWS or yaml files
- [Phase 15-01]: Nil AWS client in any DoctorDeps field produces CheckSkipped — non-fatal, never panics
- [Phase 15-01]: checkGitHubConfig returns CheckWarn (not ERROR) on ParameterNotFound — GitHub integration is optional
- [Phase 15-01]: runChecks sorts results by Name for stable output regardless of goroutine completion order
- [Phase 17-01]: [Phase 17-01]: EmailSpec.Alias is omitempty string; AllowedSenders is omitempty []string; alias JSON schema pattern enforces lowercase dot-notation; alias not added to built-in profiles; v1.0.0 DynamoDB module left unchanged
- [Phase 17-02]: IdentityQueryAPI is a separate narrow interface from IdentityTableAPI; FetchPublicKeyByAlias queries alias-index GSI
- [Phase 17-02]: MatchesAllowList exported for cross-package use by mailbox.go ParseSignedMessage
- [Phase 17-02]: ParseSignedMessage enforces allow-list before signature verification; signature failure sets SignatureOK=false without error
- [Phase 17-02]: ListMailboxMessages uses mail/ prefix flat (Option A per research); no per-recipient subdirectory filtering
- [Phase 17-03]: Alias line conditionally omitted (identity.Alias == '') — consistent with TTL Expiry conditional display pattern already established in printSandboxStatus
- [Phase 17-03]: Allowed Senders always shown in Identity section (joined list or 'not configured') — more useful than omitting; operator needs to know if allow-list is active
- [Phase 16-03]: spec.email field documented as *EmailSpec pointer (nil = disabled) matching actual type; three Phase 14 sections in email guide match source code layering in identity.go
- [Phase 16]: Budget enforcer Lambda uses SET (not ADD) for compute spend — idempotent absolute calculation from created_at
- [Phase 16]: SCP DenyOrganizationsDiscovery has no carve-out condition — applies to all roles in Application account
- [Phase 16]: km-ecs-task-* not carved out from DenyInstanceMutation SCP — task role is the sandbox workload, must stay contained
- [Phase 16]: GitHub App tokens stored at /sandbox/{sandbox-id}/github-token, read by GIT_ASKPASS at git time not at boot
- [Phase 16]: Ed25519 signs body only (not headers) — simpler verification, headers may change in SES transit
- [Phase 16-01]: Operator guide sections 11-17 added sequentially after existing section 10 for Phase 6-15 features
- [Phase 16-01]: User manual new command sections placed before walkthroughs; profile sections as subsections of Profile Authoring Guide
- [Phase 18-loose-ends]: Export RunInitWithRunner for testability so cmd_test package can call the testable core without export_test.go
- [Phase 18-loose-ends]: km init skip-with-warning for missing dirs and unset env vars — idempotency over strictness
- [Phase 18-loose-ends]: state_bucket uses omitempty in km-config.yaml so operators who skip it get clean YAML
- [Phase 18-loose-ends]: ErrGitHubNotConfigured sentinel: ParameterNotFound from SSM maps to clean skip message, not stack trace
- [Phase 18-loose-ends]: uninit.go active-sandbox guard: refuses teardown when running sandboxes exist unless --force
- [Phase 18-loose-ends]: km uninit: non-fatal destroy errors warn-and-continue (partial teardown better than stopping)
- [Phase 18-loose-ends]: SSMGetPutAPI interface extracted in create.go to enable unit testing of generateAndStoreGitHubToken
- [Phase 18-loose-ends]: Lambda/SES doctor checks use CheckWarn (not CheckError) for missing regional infra — consistent with optional components
- [Phase 18-loose-ends]: ensureKMSPlatformKey uses variadic KMSEnsureAPI for DI without breaking existing callers
- [Phase 18-loose-ends]: site.hcl is canonical locals file (not stale); root.hcl reads it — both coexist by design
- [Phase 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix]: Used source-level tag key test (os.ReadFile + strings.Contains) instead of mock extension because fakeEC2StartAPI ignores filter args; negative check targets exact broken Go string literal to avoid false negatives from substring match
- [Phase 19-01]: Use try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, '') for EC2 instance_id to handle ECS sandboxes and mock_outputs
- [Phase 19-01]: Use mock_outputs_allowed_on_destroy = true in dependency block to prevent destroy failures when sandbox module is already gone
- [Phase 20-anthropic-api-metering-claude-code-ai-spend-tracking]: Extract Anthropic model ID from response body (not URL): SSE message_start.message.model or non-streaming top-level model field
- [Phase 20-anthropic-api-metering-claude-code-ai-spend-tracking]: Use staticAnthropicRates in handler closure directly, not via WithBudgetEnforcement, to avoid changing the Bedrock API
- [Phase 20-02]: quiet mode (Verbose=false) is default — operators see step summaries not raw HCL plan output
- [Phase 20-02]: errors always printed in quiet mode — captured stderr shown on non-zero exit
- [Phase 20-02]: runner.Verbose = verbose pattern established for all commands that call terragrunt
- [Phase 21]: SafePhraseOK=false when expectedSafePhrase='' (skip check) even when KM-AUTH pattern is present
- [Phase 21]: Safe phrase generated at create time, shown once to stdout, stored in SSM only - never in profile YAML
- [Phase 21]: OTP env var name derived from last SSM path segment with KM_OTP_ prefix and uppercase (e.g. github-token -> KM_OTP_GITHUB_TOKEN)
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: Budget display uses 4 decimal places (%.4f) to show sub-penny AI charges correctly
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: ExportSandboxLogs is fire-and-forget (non-fatal): deletion proceeds immediately after async CreateExportTask
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: S3 bucket policy for logs.amazonaws.com restricts by aws:SourceAccount for security
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: SendApprovalRequest uses sandboxEmailAddress helper (domain already contains subdomain)
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: PollForApproval skips unreadable messages; no signature verification for external operator replies
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: TestBootstrapSCPApplyPath is a pre-existing TDD RED test from phase 10-02 — not a Phase 21 regression; deferred
- [Phase 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements]: Operator approved Phase 21 by reviewing checklist content rather than executing against live AWS
- [Phase 25-01]: Empty allowedRepos is denied by default: guard both != nil AND len(AllowedRepos) > 0 across compiler.go, service_hcl.go, userdata.go, create.go
- [Phase 25-01]: service_hcl.go HasGitHub gate controls github_token_inputs in service.hcl independently from compiler.go
- [Phase 25-02]: AllowedRefs enforcement is EC2-only via pre-push hook; ECS gap is documented as v1 limitation
- [Phase 25-02]: KM_ALLOWED_REFS uses colon separator; git config --system core.hooksPath applies system-wide
- [Phase 22-remote-sandbox-creation]: Local SESEmailAPI interface (send-only) preferred over pkg/aws.SESV2API for email-create-handler to avoid mock complexity and follow narrow interface principle
- [Phase 22-remote-sandbox-creation]: putSandboxCreateEvent defined locally in handler; will consolidate with pkg/aws/eventbridge.go when Plan 01 merges
- [Phase 22-remote-sandbox-creation]: EventBridgeAPI already defined in idle_event.go — reused shared interface rather than redefining
- [Phase 22-remote-sandbox-creation]: create-handler RunCommandFunc injection: subprocess testing without os/exec in test binary
- [Phase 22-remote-sandbox-creation]: create-handler does NOT send 'created' notification — km create subprocess handles it at Step 14
- [Phase 22-remote-sandbox-creation]: EventBridge 0-retry for SandboxCreate: km create is not idempotent, retries after partial provisioning would corrupt sandbox state
- [Phase 22-remote-sandbox-creation]: Container image Lambda for create-handler: terraform+terragrunt binaries exceed zip limits (~500MB), container packaging is the only viable approach
- [Phase 22-remote-sandbox-creation]: Conditional SES create-inbound rule: email_create_handler_arn defaults empty so SES module deploys safely without email-create-handler
- [Phase 23-credential-rotation]: Skip missing SSM params gracefully (CheckOK) — existence validated by checkGitHubConfig
- [Phase 23-credential-rotation]: GetParameter without WithDecryption — only LastModifiedDate metadata needed, not secret value
- [Phase 23-credential-rotation]: RotationSSMAPI embeds IdentitySSMAPI to allow direct GenerateSandboxIdentity call in RotateSandboxIdentity
- [Phase 23-credential-rotation]: UpdateIdentityPublicKey reads existing record before PutItem to preserve alias, allowedSenders, email_address, policy fields
- [Phase 23-credential-rotation]: RollSSMAPI embeds RotationSSMAPI and adds SendCommand so rotation library functions work directly
- [Phase 23-credential-rotation]: Per-sandbox failures non-fatal collected in failures slice; platform failures abort
- [Phase 23-credential-rotation]: rollSSMAdapter wraps *ssm.Client as compile-time RollSSMAPI satisfier
- [Phase 26]: Used helpText() for extend/stop Long fields rather than hardcoded strings for consistency
- [Phase 26]: Did not add km ext/km log aliases — they don't save significant typing
- [Phase 26-live-operations-hardening]: Store MaxLifetime in SandboxMetadata (not reload profile) to keep extend path simple
- [Phase 26-live-operations-hardening]: Export CheckMaxLifetime() for unit testing without AWS mock infrastructure
- [Phase 26]: RemoteCommandPublisher interface extracted from publishRemoteCommand; WithPublisher constructors follow SandboxFetcher pattern
- [Phase 26]: colorizeListStatus applied at print time to keep SandboxRecord data pure
- [Phase 26]: Source-level test pattern used for MaxLifetime verification in create_test.go (consistent with existing pattern, avoids heavy create workflow mocking)
- [Phase 27-claude-code-otel-integration]: *bool for ClaudeTelemetrySpec.Enabled: nil=default true, explicit false to disable; named OTel exporters (awss3/traces, awss3/logs, awss3/metrics) for separate S3 prefixes per signal type
- [Phase 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry]: EC2 OTEL section uses >> append to km-profile-env.sh so it works regardless of whether ProfileEnv section ran
- [Phase 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry]: OTEL-07 EC2 confirmed by test asserting no DNAT REDIRECT rules target ports 4317/4318; ECS confirmed via NO_PROXY includes localhost
- [Phase 27-03]: otelcol-contrib needs explicit chmod +x (km-* glob does not match it); OTEL_S3_BUCKET maps to KMArtifactsBucket (telemetry lives alongside other sandbox artifacts)
- [Phase 28-github-repo-level-mitm-filtering-in-http-proxy]: GitHub repo MITM: implicit allow via githubHostsRegex guard in plain-HTTP handler; custom test dialer redirects github.com to local test server
- [Phase 28-02]: Single CSV helper per compiler file: joinGitHubAllowedRepos in userdata.go and joinGitHubAllowedReposCSV in service_hcl.go — nil-safe, returns empty string when GitHub config absent
- [Phase 28-02]: Two distinct fields in ecsHCLParams: GitHubAllowedRepos []string for Lambda HCL block vs GitHubAllowedReposCSV string for proxy container env var
- [Phase 29-configurable-sandbox-id-prefix]: GenerateSandboxID signature changed from () to (prefix string) — empty defaults to 'sb' for backwards compatibility
- [Phase 29-configurable-sandbox-id-prefix]: IsValidSandboxID validates ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$ — generalized sandbox ID validation helper
- [Phase 29-configurable-sandbox-id-prefix]: Inline regex in cmd package avoids compiler import coupling; sandboxIDPattern and sandboxIDLike both use ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$
- [Phase 29-configurable-sandbox-id-prefix]: Email handler captures full prefix-hex ID in group 1 (no post-extraction sb- repair) — simpler and correct for custom prefixes
- [Phase 29-configurable-sandbox-id-prefix]: Alias resolution uses S3 scan O(n); TODO DynamoDB GSI on km-identities for O(1)
- [Phase 29-configurable-sandbox-id-prefix]: NextAliasFromTemplate uses max+1 (not gap-filling) to prevent alias reuse of destroyed sandboxes
- [Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock]: Hibernate=true passed to StopInstances; EC2 falls back to normal stop if not configured for hibernation
- [Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock]: SandboxMetadata gains Locked/LockedAt as omitempty fields for backward JSON compat
- [Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock]: CheckSandboxLock fail-open: returns nil if StateBucket empty, AWS config fails, or metadata missing
- [Phase 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock]: runStop signature changed to accept cfg for lock guard consistency with pause/extend
- [Phase 32]: RsyncPaths and RsyncFileList added as omitempty fields to ExecutionSpec; JSON schema uses items:string constraint for type safety
- [Phase 32]: Unquoted paths in tar command for bash wildcard expansion after regex validation
- [Phase 32]: Best-effort S3 profile fetch in rsync save gracefully degrades to global fallback
- [Phase 32]: Extracted buildTarShellCmd as package-level helper for testability; paths space-joined for unquoted wildcard expansion
- [Phase 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles]: Rust agent binaries need explicit CA cert env var (SSL_CERT_FILE/CODEX_CA_CERTIFICATE) pointing to /usr/local/share/ca-certificates/km-proxy-ca.crt for MITM proxy
- [Phase 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles]: Lambda toolchain km binary must be updated alongside schema changes — cross-compile linux/arm64 and upload to s3://km-artifacts-12345/toolchain/km
- [Phase 36-km-sandbox-base-container-image]: mainImage uses sidecarImage('sandbox') closure — same ECR pattern as dns-proxy/http-proxy/audit-log/tracing sidecars
- [Phase 36-km-sandbox-base-container-image]: KM_INIT_COMMANDS and KM_PROFILE_ENV are base64-encoded JSON to safely embed multi-value config in ECS env vars
- [Phase 36-km-sandbox-base-container-image]: Used amazonlinux:2023 (not 2023-minimal): 2023-minimal tag not available on Docker Hub
- [Phase 36-km-sandbox-base-container-image]: km-sandbox entrypoint: critical steps abort on failure (log_fail), optional steps warn and continue (log_warn)
- [Phase 36-km-sandbox-base-container-image]: containers/sandbox/ used as docker build context (not repo root); buildAndPushSandboxImage uses *config.Config with correct field names ApplicationAccountID/PrimaryRegion
- [Phase 36-km-sandbox-base-container-image]: Used docker stop --timeout instead of docker kill --signal SIGTERM for SIGTERM test — SIGTERM via kill does not propagate reliably through QEMU (amd64 on arm64 host)
- [Phase 36-km-sandbox-base-container-image]: Smoke test asserts 'skipping' not 'WARNING' — entrypoint uses log() not log_warn() for graceful env-var-not-set skips
- [Phase 37-01]: Named volumes (not tmpfs) for cred-vol — macOS Docker Desktop does not support tmpfs named volumes
- [Phase 37-01]: Placeholder credentials in compose template; real AWS credentials injected by plan 02 create command
- [Phase 37-01]: Both schema files (root + pkg/profile/schemas/ embedded) must be kept in sync
- [Phase 37-02]: DockerComposeExecFunc package-level var for test injection — follows RemoteCommandPublisher injection pattern from destroy.go
- [Phase 37-02]: Destroy substrate detection via S3 metadata read before tag-based lookup — docker sandboxes have no AWS-tagged resources
- [Phase 37-02]: IAM role propagation: poll GetRole + 5s sleep before AssumeRole (Pitfall 4 from research)
- [Phase 37-docker-compose-local-substrate]: execDockerShell uses ShellExecFunc injection — consistent with EC2/ECS exec patterns, testable without running docker
- [Phase 37-docker-compose-local-substrate]: stop/pause detect docker substrate via S3 metadata read before EC2 API calls (same pattern as destroy.go)
- [Phase 39-02]: No replica_regions in dynamodb-sandboxes v1.0.0 (single-region); can add in v1.1.0
- [Phase 39-02]: dynamodb-sandboxes placed after dynamodb-identities, before s3-replication in km init ordering
- [Phase 39-migrate-sandbox-metadata-s3-to-dynamodb]: Manual DynamoDB item marshalling gives deterministic attribute types (ttl_expiry as N, alias omitted when empty)
- [Phase 39-migrate-sandbox-metadata-s3-to-dynamodb]: ConditionExpression-based lock uses attribute_not_exists(locked) OR locked = :f for atomic first-lock and re-lock prevention
- [Phase 40-ebpf-network-enforcement]: Single bpf.c file pattern for bpf2go compilation; volatile const for runtime config injection
- [Phase 40-ebpf-network-enforcement]: BPF_F_NO_PREALLOC mandatory on LPM_TRIE; socket cookie as cross-layer correlation key for transparent proxy
- [Phase 40-ebpf-network-enforcement]: MapUpdater interface in resolver decouples BPF map access for unit testing without kernel
- [Phase 40-ebpf-network-enforcement]: AAAA queries refused with NOERROR+empty answer (not NXDOMAIN) — IPv4-only BPF enforcement
- [Phase 40-ebpf-network-enforcement]: Link fields stored in Enforcer struct to prevent GC-triggered cgroup program detachment
- [Phase 40-ebpf-network-enforcement]: CollectionSpec.Variables injection before LoadAndAssign — volatile consts baked at kernel load time
- [Phase 40-ebpf-network-enforcement]: RecoverPinned reconstructs Enforcer from bpffs handles only — Config not stored in bpffs
- [Phase 40-ebpf-network-enforcement]: SEC(classifier/sni_filter) cls_bpf not TCX: AL2023 kernel 6.1 does not support TCX; best-effort pass-through on all parse failures prevents blocking fragmented ClientHellos
- [Phase 40-ebpf-network-enforcement]: Default enforcement is 'proxy' (omitted field) for backwards compatibility — no existing profiles need updating
- [Phase 40-ebpf-network-enforcement]: eBPF enforcement scoped to EC2 only in Phase 40 — Docker always gets proxy enforcement with explicit zerolog warning
- [Phase 40-ebpf-network-enforcement]: Semantic validation produces errors for eBPF on non-EC2 substrates to prevent silent misconfiguration
- [Phase 40-ebpf-network-enforcement]: Extracted audit helpers to helpers.go (no build tag) so audit tests run on macOS CI without Linux kernel
- [Phase 40-ebpf-network-enforcement]: cleanupEBPF in destroy is no-op on macOS; primary BPF cleanup for remote destroy is EC2 instance termination (bpffs is in-memory)
- [Phase 40-ebpf-network-enforcement]: registerEBPFCmds indirection pattern for Linux-only cobra commands without build-tagging root.go
- [Phase 40-ebpf-network-enforcement]: Renamed BPF sockops() to bpf_sockops() to resolve clang-14 symbol/section name collision on ARM64 Docker
- [Phase 40-ebpf-network-enforcement]: Removed -type event from bpf2go directive: BpfEvent type unused, clang-14 does not emit event struct in BTF
- [Phase 41]: Defined x86_64 pt_regs struct inline in ssl_common.h for uprobe PT_REGS macro compatibility
- [Phase 41]: Used stdlib http.ReadRequest for HTTP/1.1 parsing instead of manual line parsing
- [Phase 41]: EBPF-TLS-10 budget metering scoped as audit-only URL logging -- HTTP/2 DATA frames inaccessible via uprobes
- [Phase 41-04]: TlsCaptureSpec follows optional pointer pattern with IsEnabled(); only openssl implemented, others schema-forward-compatible
- [Phase 41]: Shared BPF maps between openssl and connect objects via MapReplacements
- [Phase 41]: Optional uprobe attach for SSL_write_ex/SSL_read_ex — gracefully skip on OpenSSL 1.1.x
- [Phase 41]: TLS probe failures are non-fatal -- warns and continues network enforcement
- [Phase 33-ec2-storage-and-ami]: rootVolumeSize minimum=0 (zero means use AMI default, not rejected by schema)
- [Phase 33-ec2-storage-and-ami]: validateEC2StorageFields() called before template execution to surface clear domain errors
- [Phase 33-ec2-storage-and-ami]: AMI defaults to amazon-linux-2023 in compiler when profile ami field empty
- [Phase 33-ec2-storage-and-ami]: AMI resolution via locals ami_filters map (not multiple data blocks) to avoid Terraform plan-time errors
- [Phase 33-ec2-storage-and-ami]: Spot instances get root_block_device sizing only, no encryption/hibernation (km pause rejects spot instances)
- [Phase 33-ec2-storage-and-ami]: Used aws_volume_attachment (not ebs_block_device) for additional EBS volume to decouple from instance lifecycle
- [Phase 33-ec2-storage-and-ami]: User-data device probe order: /dev/xvdf (AL2023 udev), /dev/sdf, /dev/nvme1n1, /dev/nvme2n1 with root-device guard
- [Phase 43]: NFS ingress restricted to sandbox_sg_id SG reference (not CIDR) for EFS security group
- [Phase 43]: efs module placed immediately after network in regionalModules() since its terragrunt.hcl reads network/outputs.json at parse time
- [Phase 43]: EFSMountPoint defaults to empty string; compiler/userdata (Plan 02) applies /shared when omitted
- [Phase 43]: generateUserData signature: network *NetworkConfig parameter added before variadic emailDomainOverride, nil-safe for all existing callers
- [Phase 43]: LoadEFSOutputs returns empty string (not error) when efs/outputs.json missing — EFS is optional infra unlike required network
- [Phase 44-km-at-schedule]: Use olebedev/when for one-time NL parsing; custom regex for recurring to avoid misclassification
- [Phase 44-km-at-schedule]: EventBridge cron DOW: 1=SUN through 7=SAT (not unix 0-based); enforced in ebDOW map
- [Phase 44]: SchedulerAPI extended with ListSchedules/GetSchedule; CreateAtSchedule/DeleteAtSchedule helpers added with idempotent delete
- [Phase 44]: ScheduleRecord DynamoDB CRUD uses manual attribute marshalling; sandbox_id omitted when empty for create commands; SchedulesTableName defaults to km-schedules
- [Phase 44]: Use Cobra Aliases field for km schedule alias — inherits all flags and subcommands automatically without duplication
- [Phase 44]: cmd.ErrOrStderr() for SCHED-GUARDRAIL warning — os.Stderr bypasses Cobra capture in tests
- [Phase 44]: E2E test uses //go:build e2e + KM_E2E=1 double-gate; e2eState struct pointer shares sandboxID across sequential subtests
- [Phase 45-km-email-send-recv-scripts-and-cli]: Ed25519 signature covers text body only, not attachments — separates auth from file payload
- [Phase 45-km-email-send-recv-scripts-and-cli]: ParseSignedMessage decodes base64 CTE attachment parts — callers receive raw bytes
- [Phase 45-km-email-send-recv-scripts-and-cli]: PKCS8 DER prefix hard-coded as hex constant — Ed25519 OID fixed, avoids asn1 tooling at runtime
- [Phase 45-km-email-send-recv-scripts-and-cli]: parseUserDataTemplate() added to userdata.go to enable direct template tests for empty-SandboxEmail case
- [Phase 45-km-email-send-recv-scripts-and-cli]: Ed25519 SubjectPublicKeyInfo DER prefix 302a300506032b6570032100 + 32-byte raw key for km-recv verification
- [Phase 45-km-email-send-recv-scripts-and-cli]: km-recv signature verification is best-effort: sets SIG_STATUS but never blocks message display
- [Phase 45]: Exported EmailSendDeps/EmailReadDeps structs for testability from cmd_test package
- [Phase 45]: emailSSMAPI embeds full kmaws.IdentitySSMAPI to satisfy SendSignedEmail signature
- [Phase 45]: Auto-decrypt condition: Encrypted only (not Encrypted && !Plaintext) to handle unsigned encrypted messages
- [Phase 31-allowlist-profile-generator]: HandleTLSEvent in linux-only file to isolate tls package build constraint
- [Phase 31-allowlist-profile-generator]: HTTPS-implies-DNS: union DNS+hosts before suffix collapse for self-consistent generated profiles
- [Phase 31-allowlist-profile-generator]: Generator emits full schema-valid scaffold (not just egress fragment) so output passes profile.Validate()
- [Phase 31-02]: DomainObserver added to ResolverConfig struct (nil-safe callback); exported GenerateProfileFromJSON/CollectDockerObservations for test DI without AWS/Docker
- [Phase 46-ai-email-to-command]: BedrockRuntimeAPI interface kept handler-local in cmd/email-create-handler (not pkg/aws) — narrow Lambda scope
- [Phase 46-ai-email-to-command]: InterpretedCommand.Type defaults to 'action' if absent from Haiku JSON for backward compatibility
- [Phase 46-ai-email-to-command]: Lenient confidence parsing: json.Number first, then string fallback to handle LLM type coercion
- [Phase 46-02]: replyIntent() line scanner: skips KM-AUTH prefix lines to find yes/cancel/revision intent in conversation replies
- [Phase 46-02]: mockDynamo in test file: newTestHandlerWithAI sets DynamoClient to avoid nil panic in status and list paths
- [Phase 46-02]: Non-create commands use awspkg.PublishSandboxCommand(sandboxID, eventType) matching existing idle_event.go signature
- [Phase 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set]: TTL=0 uses empty string sentinel to disable EventBridge schedule, aligning with existing TTL != '' guard in runCreate
- [Phase 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set]: applyLifecycleOverrides extracted as helper shared between runCreate and runCreateRemote
- [Phase 48]: IdleAction derived in compiler via idleActionFromProfile() — not in create.go — since compiler already receives TTL='' sentinel from Plan 01 overrides
- [Phase 48]: buildIdleCallback extracted as package-level func for unit testability; AfterFunc(2min) delay for hibernate/resume cycle before re-arm
- [Phase 50]: Moved NewAgentCmd from shell.go to agent.go for module separation; base64 prompt encoding for injection prevention
- [Phase 50]: Accepted SSM 24KB truncation with warning; sendSSMAndWait helper for DRY SSM command+poll
- [Phase 51]: RUN_ID generated deterministically in Go instead of shell for tmux session naming
- [Phase 51]: attach targets latest km-agent-* session via tmux list-sessions grep/tail
- [Phase 51]: --interactive sends script via SendCommand then uses SSM start-session for attached tmux new-session
- [Phase 52-clone-sandbox]: Follow alias omit-when-empty pattern for cloned_from in marshalSandboxItem to prevent DynamoDB item pollution
- [Phase 52-clone-sandbox]: ClonedFrom propagated to SandboxRecord via metadataToRecord so km list --wide displays lineage without extra reads
- [Phase 52-02]: Use SSMSendAPI/SandboxFetcher WithDeps DI pattern for test isolation; workspace staged once to source-scoped S3 key then downloaded post-provision via SSM to each clone
- [Phase 53]: Exported LoadFrom/SaveTo as public helpers so tests use t.TempDir() without touching real config dir
- [Phase 53]: Reconcile resets Next to 1 only when map is empty after pruning
- [Phase 53]: Assign local number once at sandboxID generation (covers both local EC2 and docker paths)
- [Phase 53]: Remote create path excluded from local number assignment — remote sandboxes assigned on next km list via Reconcile
- [Phase 54]: Per-account SSM keys use account login string as path suffix for readability
- [Phase 54]: Legacy installation-id key always written with first installation for backward compat
- [Phase 54]: Extended SSMReadAPI with GetParametersByPath for multi-installation discovery in doctor check
- [Phase 54]: Used string replacement to inject installation ID into compiled HCL rather than threading through compiler
- [Phase 54]: generateAndStoreGitHubToken returns (string, error) to expose resolved installation ID
- [Phase 55]: Commands() preserves first-seen order (map+slice dual structure) because command sequence is semantically meaningful for initCommands
- [Phase 55-02]: readLearnCommands feeds log commands into Recorder for dedup rather than appending directly
- [Phase 55-02]: CollectDockerObservations nil-safe auditLogs parameter preserves backward compatibility
- [Phase 55-02]: Container name km-{sandboxID}-audit-log confirmed against compose.go template before implementation
- [Phase 58]: CodexArgs kept parallel to ClaudeArgs (separate []string fields) not unified agentArgs map per CONTEXT.md
- [Phase 58]: agentType defaults to claude for empty string — all existing callers safe until Plan 03 wires the flag
- [Phase 58]: noBedrockLines stays empty for codex regardless of NoBedrock field — belt-and-braces guard even with Plan 03 RunE error path
- [Phase 58]: Default agent type is claude when neither --claude nor --codex is set — backward compat preserved
- [Phase 58]: Mutex and no-bedrock checks fire before ResolveSandboxID — zero AWS calls on validation failure
- [Phase 58]: km init --sidecars NOT required after phase 58 — codexArgs is client-side CLI field only
- [Phase 58]: Default agent type for km agent run is claude when neither --claude nor --codex is set (backward compat preserved)
- [Phase 58]: km init --sidecars NOT required after phase 58 — codexArgs is a client-side CLI field only
- [Phase 59]: Inline pattern matching in Lambda instead of importing pkg/aws MatchesAllowList
- [Phase 59]: Fail-open when km-config.yaml missing for backward compatibility
- [Phase 59]: Belt-and-suspenders filtering: km-mail-poller at download + km-recv at display
- [Phase 60]: RecordResumeClose swallows GetItem errors (non-fatal warn-and-continue); interval clamped to 0 for clock skew; single atomic ADD+REMOVE UpdateItem for resume close
- [Phase 60]: Exported RecordPauseForEC2 helper in pause.go so pause hook is unit-testable without DI surgery on runPause
- [Phase 60]: Extended resumeEC2Sandbox signature with budgetClient+budgetTable (single caller pattern)
- [Phase 60]: BudgetClient in TTLHandler reuses existing dynamoClient — no second DynamoDB client construction
- [Phase 60]: calculateComputeCost subtracts pausedSeconds from elapsed with zero clamp; HandleBudgetCheck reads budget first and threads closed+open pause intervals; enforceBudgetCompute records pausedAt after EC2 StopInstances so billing clock stops
- [Phase 61-km-shell-ctrl-c-fix]: CONTEXT.md IAM locked decision overridden: no per-resource ssm:StartSession ALLOW policy exists in infra/modules/ (only SCP deny); operator SSO uses AdministratorAccess from IAM Identity Center outside this repo
- [Phase 61-km-shell-ctrl-c-fix]: KM-Sandbox-Session uses Standard_Stream sessionType + conditional shellProfile.linux (exec bash -l for empty command, bash -lc for non-empty) to fix Ctrl+C teardown bug in km shell/agent
- [Phase 61-km-shell-ctrl-c-fix]: IsSSMDocumentMissingErr exported as thin wrapper over private form to allow external test package (cmd_test) access without changing internal visibility pattern
- [Phase 61-km-shell-ctrl-c-fix]: km shell RunE deliberately discards runShell error (by design); TestShellCmd_MissingSSMDoc validates helper + error format directly rather than through cobra Execute path
- [Phase 61-km-shell-ctrl-c-fix]: encoding/json.Marshal replaces fmt.Sprintf+strings.ReplaceAll for all SSM start-session --parameters JSON construction in agent.go
- [Phase 33.1-raw-ami-id-support]: Use profile.ValidateSchema (not profile.Parse) in schema rejection tests — Parse only does YAML unmarshal, ValidateSchema runs JSON schema validation
- [Phase 33.1-raw-ami-id-support]: ami schema: oneOf with inner enum (4 slugs) + pattern (^ami-[0-9a-f]{8,17}$) subschemas; isRawAMIID() is single detection point in compiler
- [Phase 33.1]: AMISlug and AMIID are discriminated union in ec2HCLParams — exactly one non-empty at render time
- [Phase 33.1]: use_slug_lookup local gates data.aws_ami count on ami_slug presence; effective_ami_id uses length() guard not try()
- [Phase 33.1]: Hibernation + raw AMI emits log.Printf warning not hard error — Phase 56 may use pre-encrypted private AMIs
- [Phase 56-02]: Use flat snake_case key doctor_stale_ami_days (not nested doctor.staleAMIDays) to match existing km-config.yaml conventions
- [Phase 56-02]: Clamp DoctorStaleAMIDays <= 0 to 30 silently to prevent operator misconfiguration disabling the doctor stale-AMI check
- [Phase 56-01]: EC2AMIAPI includes CreateTags (5 methods) so CopyAMI can re-tag without a separate interface
- [Phase 56-01]: describeImagesClient adapter struct bridges EC2AMIAPI to ec2.DescribeImagesAPIClient for NewImageAvailableWaiter — no runtime type assertion needed
- [Phase 56-01]: KMBakeTags omits km:alias tag entirely when alias is empty
- [Phase 56-03]: Added ec2:DeregisterImage/DeleteSnapshot/CreateTags to SCP DenyInfraAndStorage; read-only Describe* ops excluded from SCP (documented in IAM guidance instead); WriteOperatorIAMGuidance emits text block (not programmatic IAM) since SSO permission sets are out-of-scope for bootstrap.go
- [Phase 56]: BakeFromSandbox and FindProfilesReferencingAMI exported from internal/app/cmd/ami.go as Plan 05 and Plan 06 integration points
- [Phase 56-learn-mode-ami-snapshot-and-lifecycle-management]: checkStaleAMIs is flag-only (no deletion) per Phase 56 locked decision; profile-file-based sandboxUsesAMIInDoctor limitation documented
- [Phase 56]: bakeFromSandboxFn/flushEC2ObservationsFn/fetchEC2ObservedJSONFn as package-level vars for DI without interface changes
- [Phase 56]: GenerateProfileFromJSON third param amiID string — additive, all 5 existing callers updated to pass empty string
- [Phase 56]: shell_ami_test.go in package cmd (not cmd_test) — required to access unexported runLearnPostExit and package-level vars
- [Phase 56.1]: BDM lookup at compile time (not Terraform data source): compile-time inspection keeps logic in testable Go and avoids Terraform complexity
- [Phase 56.1]: amiBDMDeviceNames passed as pre-computed slice to Compile: avoids leaking AWS deps into pure HCL-generation layer (generateEC2ServiceHCL stays testable)
- [Phase 56.1]: Use timeout-tee pattern for FIFO writes in bash (not O_NONBLOCK or background fork)
- [Phase 56.1]: Place systemctl restart as last cloud-init step to guarantee runs after sidecar binaries downloaded
- [Phase 56.1]: Add FIFO retry in main.go (not ExecStartPre= systemd unit) to keep logic testable
- [Phase 56.2]: No Wants=km.slice in km-bootstrap.service: km.slice is a cgroup directory not a systemd unit; script uses mkdir -p to create the parent
- [Phase 56.2]: km-bootstrap.service always emitted (not gated on enforcement mode): /run/km parent dir needed for km-audit-log FIFO on all enforcement modes
- [Phase 56.2]: No km init --sidecars needed: template-only changes compiled into km binary; operator runs make build only
- [Phase 57-email-enhancement]: TestUserData_MailPoller_SkipsCheckForSandbox tightened to combined guard to avoid false PASS from pre-existing bare assertion in km-recv
- [Phase 57]: km-send --no-sign skips Ed25519 SSM key fetch and X-KM-* headers via guarded signing block; KM-AUTH safe-phrase append stays unconditional
- [Phase 57]: unfold_headers applied ONLY to header section: avoids base64 attachment line corruption (Pitfall 3)
- [Phase 57]: alt_boundary separate from outer boundary: avoids variable collision in second-level multipart scan (Pitfall 6)
- [Phase 57]: from-external: automatic detection via absent X-KM-Sender-ID + SIG_STATUS=unsigned, no CLI flag needed
- [Phase 57]: Enforcement layer is km-mail-poller (bash systemd service), not a SES receipt rule Lambda — sandbox_inbound SES rule is pure S3 action with no Lambda hook (infra/modules/ses/v1.0.0/main.tf line 126)
- [Phase 57]: grep -qF fixed-string match used for safe phrase (not grep -qP/-qE) to prevent regex injection from SSM-stored phrase values
- [Phase 57]: sender_id/sender_email hoisted unconditionally out of KM_ALLOWED_SENDERS block so safe-phrase gate can distinguish sandbox vs external senders when no allowlist is configured
- [Phase 57]: skills/operator/SKILL.md and skills/user/SKILL.md not modified — operator skill is operator-inbox-bound (always signed); user skill covers operator CLI only; neither involves external email (per RESEARCH.md scope decision)
- [Phase 57]: Tooling location note added with /opt/km/bin/ absolute paths for scripts/cron/systemd where PATH may be minimal
- [Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events]: Dropped format: email from JSON schema for notificationEmailAddress — santhosh-tekuri/jsonschema/v6 treats format as annotation-only; actual validation at SES send time
- [Phase 62]: Emit KM_NOTIFY_ON_PERMISSION/ON_IDLE whenever Spec.CLI != nil (v1 pragmatic); Go bool zero value + omitempty cannot distinguish explicit-false from unset
- [Phase 62]: Write notify env vars to /etc/profile.d/km-notify-env.sh (NOT /etc/environment); profile.d is guaranteed sourced in SSM sessions on Amazon Linux 2
- [Phase 62]: mergeNotifyHookIntoSettings() runs at compile time in Go (not shell jq); KM_NOTIFY_LAST_FILE override in hook script enables test isolation in Plan 03
- [Phase 62]: Use *bool for AgentRunOptions.NotifyOnPermission/Idle: nil=unset (no export emitted), non-nil=explicit override. Avoids explicit-false ambiguity without a companion Explicit field.
- [Phase 62]: km shell resolveNotifyFlags returns nil when no CLI flag changed — avoids SSM SendCommand roundtrip; profile.d km-notify-env.sh from Plan 02 supplies defaults.
- [Phase 62]: bash -u nounset empty-array fix: ${to_args[@]+"${to_args[@]}"} pattern required in hook heredoc for macOS bash compatibility
- [Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events]: T4 inline Rule-1 fix: jq exit-5 propagation in Stop-path transcript extraction — added '|| echo ""' fallback at userdata.go:399-401 + regression test (commits 095a51e + 9c0690c). Required km init --sidecars redeploy. HOOK-05 'never blocks Claude' invariant restored.
- [Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events]: T3 methodology: manual hook fire is the legitimate Notification test path — km agent run's implicit --dangerously-skip-permissions makes the path untriggerable via real Claude flow. Plan 03 unit tests cover firing semantics; live test confirms SES routing + Ed25519 signing.
- [Phase 62-claude-code-operator-notify-hook-for-permission-and-idle-events]: T5 methodology: env-var direct test (KM_NOTIFY_EMAIL) exercises the same runtime routing as the profile field. Compile-time path that writes /etc/profile.d/km-notify-env.sh already verified in T2. Avoids redundant sandbox provisioning.
- [Phase 63-slack-notify-hook]: pkg/slack: Alphabetical struct tag order + encoding/json gives deterministic canonical JSON; no custom serializer; BridgeBackoff exported var for test shim; PostToBridge uses http.DefaultClient (sandbox-side); 4 total attempts (1+3 retries) matching SLCK-03
- [Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events]: PublicKeyFetcher interface enforces DynamoDB as backend (NOT SSM) at the type contract level — RESEARCH.md correction #1 baked in
- [Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events]: Bridge handler: no AWS SDK imports in pkg/slack/bridge — pure library; Plan 06 owns production wiring via injectable interfaces
- [Phase 63-01]: *bool for notifyEmailEnabled/notifySlackEnabled/slackArchiveOnDestroy: nil=unset (Phase 62 backward compat default), explicit-false=disabled, explicit-true=forced on
- [Phase 63-01]: IsWarning bool on ValidationError (not separate type): minimal change, km validate is sole caller that needs separation; five Slack rules in ValidateSemantic (not JSON schema) because cross-field constraints require semantic layer
- [Phase 63-04]: KM_SLACK_BRIDGE_URL not emitted at compile time — requires runtime SSM lookup of /km/slack/bridge-url; Plan 08 injects it post-launch into /etc/profile.d/km-notify-env.sh
- [Phase 63-04]: sent_any *bool pointer semantics: nil NotifyEmailEnabled/NotifySlackEnabled = no env var emitted (hook :-default takes effect for Phase 62 compat); non-nil = emit KEY=0|1 via boolToZeroOne
- [Phase 63-slack-notify-hook-for-claude-code-permission-and-idle-events]: runWith() inner function accepts (ctx, priv, sandboxID, bridgeURL) for testability — SSM bypass in unit tests; GOARCH=amd64 for km-slack matching existing EC2 sidecars (RESEARCH.md Pitfall 7); three-site pipeline sync required (Makefile + init.go + userdata.go)
- [Phase 63-slack-notify-hook]: SlackPosterAdapter uses direct HTTP (Option B) not pkg/slack.Client to expose Retry-After headers for 429→ErrSlackRateLimited
- [Phase 63-slack-notify-hook]: Lambda Function URL: authorization_type=NONE; Ed25519 signature + nonce table provide application-layer auth (first Function URL in codebase)
- [Phase 63-slack-notify-hook]: replace_triggered_by = [aws_iam_role.slack_bridge] on Lambda function prevents stale KMS grants when IAM role recreated (CLAUDE.md memory)
- [Phase 63]: Per-sandbox name collision aborts km create with actionable error; no suffix fallback
- [Phase 63]: Per-sandbox channel NOT rolled back on create failure; operator does manual cleanup
- [Phase 63]: SlackArchiveOnDestroy field pulled forward from Plan 63-09 into Plan 63-08 to unblock build
- [Phase 63]: km slack init uses SlackInitAPI (adds AuthTest) separate from SlackAPI in create_slack.go; both satisfied by *slack.Client
- [Phase 63]: --force is the only way to recreate populated SSM state; default is idempotent skip
- [Phase 63-10]: EnsureSandboxIdentity called at both km init (operator) and km create (sandbox) boundaries to prevent DynamoDB/SSM key drift causing bad_signature on bridge
- [Phase 63-10]: km slack init --force made idempotent on name_taken — reuses existing channel ID; full revoke+cache TTL wait deferred as uncommon path
- [Phase 63-10]: Phase 63.1 gap list scoped to 2 items: Step 11d runtime injection (KM_SLACK_CHANNEL_ID/KM_SLACK_BRIDGE_URL not injected into /etc/profile.d) and km destroy Slack archive auto-trigger; both have operator workarounds and do not compromise security model
- [Phase 63.1]: Extract runStep11dInject to create_slack.go with retryMax/retryDelay injection for testability (Plan 63.1-01)
- [Phase 63.1]: Two-commit sequencing: visibility (single attempt) first, retry loop second — allows diagnosing which branch fires before adding retry noise (Plan 63.1-01)
- [Phase 63.1]: captureStderr lives exclusively in testhelpers_test.go — prevents duplicate-symbol compile errors when Plan 02 tests compile in Wave 1 (Plan 63.1-01)
- [Phase 63.1]: SLCK-12 root cause: km destroy --remote early-returns before destroySlackChannel; Lambda has no Slack code. Option A chosen: run Slack teardown locally before EventBridge dispatch.
- [Phase 63.1]: runSlackTeardown() shared helper extracted to destroy_slack.go — both remote and local paths use it to prevent drift.
- [Phase 63.1-03]: B1 (fail-fast on 5xx) chosen for PostToBridge: nonce-replay protection makes same-envelope 5xx retry harmful (turns Slack error into replayed_nonce); no caller needs retry-on-5xx; network errors pre-nonce-reserve still retried.
- [Phase 63.1-03]: Task 5 added during UAT (not in PLAN.md): bridge had zero logging; 5xx errors masked as replayed_nonce; both root causes fixed with slog + fail-fast policy.
- [Phase 63.1-03]: SetLogger() exported from bridge package for test log capture via bytes.Buffer; avoids test/production divergence.
- [Phase 65]: Hard rename of ManagementAccountID into OrganizationAccountID (SCP, optional) + DNSParentAccountID (DNS parent zone); no back-compat alias; Wave 1 cmd package intentionally broken until plan 02
- [Phase 65]: runShowPrereqs returns nil + message when OrganizationAccountID blank (not error)
- [Phase 65]: --dns-parent-account and --organization-account both optional in --non-interactive mode
- [Phase 65]: doctor.go checkConfig: management_account_id removed from required list (both new fields optional)
- [Phase 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename]: checkLegacyManagementField reads raw YAML (not Viper-loaded config) because Viper silently drops unknown keys — plan 02 shims fully removed from DoctorConfigProvider interface in plan 03
- [Phase 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename]: km doctor check count increased from 18 to 20 (two new checks: org-blank WARN, legacy-management FAIL) — CLAUDE.md update deferred to plan 04
- [Phase 65]: Phase 65 plan 04: HCL + docs fully migrated — accounts.management split to organization (SCP) + dns_parent (Route53) across all surfaces; km-config.yaml was pre-migrated
- [Phase 67]: SQS dep kept as indirect in go.mod; anchored via go get without tidy so Wave 1+ plans compile without re-fetching
- [Phase 67]: Wave 0 stub test files use internal package names (bridge/profile/compiler/cmd) consistent with existing non-_test source files in each directory
- [Phase 67-slack-inbound]: Use plain bool (not *bool) for NotifySlackInboundEnabled — false is the correct default, matches NotifySlackPerSandbox pattern
- [Phase 67-slack-inbound]: Three inbound validation rules (SI1/SI2/SI3) are hard errors, not warnings — misconfiguration must be caught at km validate time
- [Phase 67-03]: EventNonceStore interface separate from NonceStore: CheckAndStore returns (bool,error) vs Reserve returns error with sentinel — cleaner for events dedup branch
- [Phase 67-03]: 200-on-all-internal-errors invariant: SQS/DDB/sandbox-lookup/signing-secret failures return 200 not 5xx — Slack retries 5xx with new event_id bypassing dedup
- [Phase 67-slack-inbound]: Live Terragrunt configs at infra/live/use1/dynamodb-* not management/dynamodb/
- [Phase 67-slack-inbound]: GetResourcePrefix shim added in 67-02; Phase 66 can migrate later without breaking callers
- [Phase 67-slack-inbound]: dynamodb-sandboxes GSI bump is v1.1.0 copy, v1.0.0 left unchanged (stateful table safety)
- [Phase 67-slack-inbound]: Compile-time KM_SLACK_INBOUND_QUEUE_URL slot with empty value in env file; km create fills at runtime (Plan 67-06)
- [Phase 67-slack-inbound]: EnvironmentFile=/etc/profile.d/km-notify-env.sh in km-slack-inbound-poller.service for runtime-injected env vars
- [Phase 67-slack-inbound]: SlackThreadsTableName from KM_SLACK_THREADS_TABLE env var (mirrors budgetTable pattern); no Config threading needed
- [Phase 67-05]: slackAuthTestAdapter in main.go: pkg/slack.Client.AuthTest does not return user_id, so implemented thin HTTP adapter rather than breaking public API
- [Phase 67-05]: nonceStoreAdapter bridges DynamoNonceStore.Reserve to EventNonceStore.CheckAndStore — avoids duplicating nonce table logic
- [Phase 67-05]: DDBUpdateItemAPI extends DDBQueryGetPutAPI so single *dynamodb.Client satisfies both; threads adapter never needs UpdateItem
- [Phase 67-slack-inbound]: SQS queue provisioning is FATAL (not non-fatal): failure archives the per-sandbox Slack channel and aborts km create; without the queue the inbound path is permanently broken
- [Phase 67-slack-inbound]: last_pause_hint_ts must NOT be pre-populated at km create: DDBPauseHinter (67-05) treats absent as cooldown-expired, enabling first hint to fire immediately
- [Phase 67-slack-inbound]: Interactive mode gated on opts.BotToken=='' to avoid prompting for signing secret in CI mode
- [Phase 67-slack-inbound]: km slack rotate-signing-secret mirrors rotate-token but omits smoke test (no inbound smoke path)
- [Phase 67]: Used DDBThreadStore.Upsert from pkg/slack/bridge (not reimplementing) for consistent km-slack-threads schema
- [Phase 67]: Drain placed in Step 12 (after Terraform destroy) — instance may be gone but StopPoller/WaitForAgentRunIdle fail gracefully (best-effort)
- [Phase 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch]: Plan 67-08: SlackChannelID/SlackInboundQueueURL/ActiveThreads added to SandboxRecord (instead of separate metadata fetch in status/list); X-OAuth-Scopes response header parsing for auth.test (not body); type-assert *appConfigAdapter to derive ResourcePrefix
- [Phase 67]: Slack inbound Gap A closure: poller posts .result from output.json to Slack via km-slack post --thread AFTER aws sqs delete-message (ack-first); KM_SLACK_INBOUND_REPLY_HANDLED sentinel gates the Stop hook Slack branch (# 6b.) to prevent double-post; SSM resolves channel/bridge URL at poller startup (mirrors existing queue-url fallback) since systemd EnvironmentFile only loads km-notify-env.sh.
- [Phase 67]: isBotLoop uses allow-list (empty + thread_broadcast) instead of deny-list — closes UAT Gap B (channel_join slip-through) AND prevents every future Slack subtype regression by default
- [Phase 67-slack-inbound]: Phase 67 GREEN ship verdict: 11/13 actively-exercised UAT steps PASS, 1 partial, 2 NOT-EXERCISED with compensating coverage
- [Phase 67-slack-inbound]: RUN_SLACK_E2E=1 env-var gate (no -tags=e2e build tag) for opt-in live-workspace E2E tests; default go test stays green
- [Phase 67-slack-inbound]: UAT.md uses 4 verdict states (PASS/FAIL/PARTIAL/NOT-EXERCISED) where NOT-EXERCISED must cite compensating coverage (unit tests, AWS service guarantee, or alternative defence)
- [Phase 67.1-01]: Duplicate HTTP call body in SlackReactorAdapter rather than extract shared helper (one method; factor if third adapter appears)
- [Phase 67.1-01]: SlackReactorAdapter shares tokenFetcher instance with SlackPosterAdapter to preserve 15-min SSM token cache
- [Phase 67.1-01]: KM_SLACK_ACK_EMOJI env var (default eyes) controls ACK emoji — bridge-global, no profile field for v1
- [Phase 67.1]: reactions:write added as third required scope in both VerifyEventsAPIScopes and checkSlackAppEventsScopes; Remediation text softening deferred to Plan 03 (token rotation not needed for scope-add)
- [Phase 67.1-03]: lambda-slack-bridge v1.0.0 slack_ack_emoji variable added in-place (not version bump) — consistent with Phase 67-05 precedent of additive in-place env var additions with safe defaults
- [Phase 67.1-03]: Live terragrunt config relies on slack_ack_emoji default "eyes" — no live-config edit needed for v1 deployment
- [Phase 67.1-03]: Phase 67.1 COMPLETE GREEN — all 5 UAT requirements satisfied; operator confirmed 👀 on correct msg.TS, bot-loop filter holds, in-thread correctness validated
- [Phase 68]: Phase 68 Wave-0 stub-seeding mirrors Phase 67-00 — t.Skip stubs in package-aligned _test.go files, separate km-slack stub helper from km-send to keep post/upload/record-mapping subcommands explicit; out-of-scope baseline failures logged to deferred-items.md rather than auto-fixed
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-03: table name resolved to {prefix}-slack-stream-messages (NOT {prefix}-km-slack-stream-messages); resolves CONTEXT.md Open Question 1 — 68-CONTEXT.md should be amended on next pass
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-03: dynamodb-slack-stream-messages module mirrors Phase 67 dynamodb-slack-threads layout 1:1 (PAY_PER_REQUEST, native TTL on Number ttl_expiry, SSE on, PITR off); only resource name + key schema + Component=km-slack-transcript tag differ
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-02: Reused EnvelopeVersion=1 (no bump) for the four additive upload-envelope fields — zero values on legacy actions guarantee byte-identical canonical signing, so Phase 63 verifiers and Phase 68 senders interoperate after the struct refresh
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-02: Validation split between client (BuildEnvelopeUpload: filename ≤255B, no slash/NUL, sizeBytes>0, channel/s3Key non-empty) and bridge (content-type allow-list, deferred to Plan 08) — keeps the trust boundary at the network edge while giving callers fast-fail on shape errors
- [Phase 68]: Plan 68-01: notifySlackTranscriptEnabled field type bool (not *bool) — default false is no-opt-in semantic, mirrors Phase 67 NotifySlackInboundEnabled
- [Phase 68]: Plan 68-01: three transcript validation rules (ST1/ST2/ST3) emit hard errors mirroring Phase 67 inbound — same audience-containment prerequisites
- [Phase 68]: Plan 68-01: schema entry placed in pkg/profile/schemas/sandbox_profile.schema.json (real path), not the path stated in plan
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: UploadFile does NOT retry internally; retry stays at BridgeBackoff envelope layer to avoid replayed_nonce masking
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Streaming proven by 1 MiB SHA-256 round-trip + explicit Content-Length header assertion (Slack rejects chunked encoding on signed upload URLs)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: thread_ts key omitted from completeUploadExternal JSON when empty (Slack rejects empty-string thread_ts)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-05: Extracted dispatch(args, stderr) helper from main() so dispatch tests inject args/stderr directly — cleaner than os.Args mutation suggested in plan
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-05: km-slack subcommands all use flag.ContinueOnError + fs.SetOutput(stderr) so flag-error paths are deterministic and unit-testable (Pattern B for future cmd/* binaries)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-05: km-slack record-mapping uses LoadDefaultConfig (region from IMDS) instead of explicit AWS_REGION; runUpload retains explicit region requirement to mirror runPost
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: ec2spot transcript IAM policies gated on var.artifacts_bucket and var.slack_stream_messages_table_name (count = ... > 0 ? 1 : 0); empty defaults preserve back-compat for callers that have not yet wired the inputs
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Bridge transcript S3 read policy is broad (transcripts/*); per-sandbox prefix enforcement happens in handler.go envelope validation (application-layer security boundary), per RESEARCH Pitfall 4
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: SlackStreamMessagesTableName follows Phase 67 SlackThreadsTableName precedent: env var KM_SLACK_STREAM_TABLE with default km-slack-stream-messages; Plan 10 propagates Config.GetSlackStreamMessagesTableName() into the env var via km create
- [Phase 68]: Plan 68-07: Mirrored Phase 62 (HOOK-04) tri-state flag pattern for --transcript-stream / --no-transcript-stream on km agent run + km shell
- [Phase 68]: Plan 68-07: Extended buildNotifySendCommands to 3-arg (perm, idle, transcript) rather than introducing a parallel helper — keeps SSM SendCommand single-shot
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Bridge ActionUpload uses raw HTTP X-OAuth-Scopes probe at cold start (RESEARCH OQ 2) — Avoids extending Phase 63 SlackPosterAdapter.call() to surface response headers; one-shot probe at init() runs <100ms with 5s timeout cap; result cached for Lambda warm lifetime; fail-open on probe failure (empty header → MissingFilesWrite=false) so transient infra issues do not block all uploads.
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Streaming S3 → Slack via io.ReadCloser → io.Reader (zero buffering) — S3GetterAdapter returns out.Body directly; bridge defers body.Close() and passes the reader to Plan 04 pkg/slack.Client.UploadFile which streams to Slack with explicit Content-Length. Sustains 100MB cap on 256MB Lambda; peak memory stays at Go HTTP client baseline regardless of upload size.
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: 68-09: Extracted streaming logic into single _km_stream_drain() shell function (avoid 100-line duplication across PostToolUse + Stop branches)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: 68-09: Cooldown becomes a soft block flag — Notification + plain Stop preserve hard-exit semantics; PostToolUse + Stop+transcript bypass it (transcript completeness non-negotiable)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: 68-09: Preserved # 6a./# 6b. markers inside email-branch wrapper to keep Phase 67 slack-inbound structural tests passing without modification
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-11: pivoted from plan's Doctor-struct pseudo-code to closure-based dep injection (Phase 67 doctor_slack.go pattern); checkSlackFilesWriteScope reuses the existing Phase 67 SlackAuthTestScopes closure rather than duplicating fetchSlackBotScopes wiring
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-11: all three transcript-streaming doctor checks demote CheckError to CheckWarn at registration — Phase 68 is opt-in, so missing stream-messages table or absent files:write scope must not turn km doctor red for non-opted-in deployments (mirrors Phase 63/67 Slack-check policy)
- [Phase 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload]: Plan 68-10: NotifyEnv-block placement after SlackStreamMessagesTableName mirrors the Phase 62/63 'append additive Slack fields after existing assignments' pattern; printTranscriptWarning placed inside the existing Slack-resolution if-block to reuse slackClient
- [Phase 66-01]: EmailSubdomain field uses nil-safe GetEmailDomain() helper matching Phase 67 GetResourcePrefix() nil-safety pattern
- [Phase 66-01]: DoctorConfigProvider interface extended with 4 methods; type-assert hack at doctor.go:2344 removed (Pitfall 12 resolved)
- [Phase 66-02]: AMIName() uses variadic prefix string to preserve backward compat; caller passes cfg.GetResourcePrefix()+"-"
- [Phase 66-02]: Lambda getEmailDomain() uses lazy env read (not init-time var) so test tools can override at test time
- [Phase 66-02]: HCL template deferrals to plan 04 use inline TODO on same line as literal to enable grep audit
- [Phase 66]: mock_outputs in dependency blocks retain literal km- defaults (plan-time stubs, not deployed values)
- [Phase 66]: ecs-spot-handler has no live config under infra/live/use1/ — management SCP only; module parameterized but no live wire needed
- [Phase 66]: Lambda function_name rename caveat: default-prefix installs unaffected; custom-prefix migrations from default need terraform state mv for lambda-slack-bridge
- [Phase 66]: ExportConfigEnvVars now exports KM_RESOURCE_PREFIX + KM_EMAIL_SUBDOMAIN; fetchAndCacheOutputs uses env var for prefix-aware bucket naming
- [Phase 66]: km-config.yaml is gitignored by design; email_subdomain added on disk; grep audit residuals are pre-existing from phases 02-67 and documented
- [Phase 73-km-vscode-remote-session-via-ssm]: Test files in cmd use package cmd (not cmd_test) for internal-symbol access; boolPtr reused from validate_test.go to avoid redeclaration
- [Phase 73-km-vscode-remote-session-via-ssm]: VSCodeEnabled uses *bool pointer for omit-means-true semantics, matching NotifyEmailEnabled precedent
- [Phase 73-km-vscode-remote-session-via-ssm]: IsVSCodeEnabled exported as package-level helper (not inline nil-check) because 3 callers need it (compiler, create.go, doctor)
- [Phase 73]: Manual pubkey line construction (fmt.Sprintf) instead of gossh.MarshalAuthorizedKey to preserve comment field in authorized_keys output
- [Phase 73]: Returned pubContent has no trailing newline — safe for direct heredoc embedding in Wave 2 userdata templates
- [Phase 73-km-vscode-remote-session-via-ssm]: atomicWrite uses CreateTemp+Rename for existing-file modification; new-file creation uses WriteFile directly
- [Phase 73-km-vscode-remote-session-via-ssm]: Fixed SSH defaults (IdentitiesOnly, StrictHostKeyChecking, UserKnownHostsFile, ServerAliveInterval) locked into renderHostBlock - not exposed as HostOptions fields
- [Phase 73-km-vscode-remote-session-via-ssm]: 73-07: Factored Phase 73 destroy cleanup into cleanupVSCodeState helper called from both runDestroy and runDestroyDocker; unconditional + idempotent post-AWS-teardown
- [Phase 73-km-vscode-remote-session-via-ssm]: 73-04: VSCodeSSHPubKey validation scoped to non-nil network path to avoid breaking 66+ existing tests that use nil network
- [Phase 73-km-vscode-remote-session-via-ssm]: 73-04: template {{ .VSCodeSSHPubKey }} at column 0 in heredoc to prevent sshd silent key rejection (Pitfall 3)
- [Phase 73-km-vscode-remote-session-via-ssm]: 73-04: restorecon wrapped with command -v guard for cross-distro compatibility (Pitfall 5)
- [Phase 73]: Keypair generation inserted as Step 6d in runCreate between Slack resolution and compiler.Compile for fail-fast before AWS provisioning
- [Phase 73-km-vscode-remote-session-via-ssm]: parseVSCodeStatus extracted as shared helper so both runVSCodeStart pre-flight and runVSCodeStatus share identical 4-case sshd/authkeys discrimination
- [Phase 73-km-vscode-remote-session-via-ssm]: vscode_test.go kept in package cmd (white-box) to allow direct calls to runVSCodeStart/runVSCodeStatus; vsCodeSSMMock defined locally since agent_test.go mocks are in cmd_test package
- [Phase 73-km-vscode-remote-session-via-ssm]: docs/vscode.md follows slack-notifications.md structural pattern with ToC, section tables, and code-block lifecycle
- [Phase 73-km-vscode-remote-session-via-ssm]: CLAUDE.md vscode bullets placed after km slack rotate-token; VS Code section placed after Phase 68 block before Architecture
- [Phase 73]: Pre-existing pkg/compiler test failures (Slack-related) documented as out-of-scope for Phase 73 closeout
- [Phase 73]: Mid-phase operator fixes (6fd2fde, 3e4a69a, 9fe2f16) captured in 73-09 SUMMARY — keypair in remote path, pubkey env var propagation, ResolveSandboxID wiring
- [Phase 73-km-vscode-remote-session-via-ssm]: Scenarios 4+6 accepted as unit-test-covered for UAT sign-off; pre-bind port probe excludes Chrome DevTools port 9222
- [Phase 73-km-vscode-remote-session-via-ssm]: Four mid-phase fixes (6fd2fde, 3e4a69a, 9fe2f16, 2501bc9) documented in 73-09-SUMMARY.md; all validated live against sandbox lrn2-ee9499b5
- [Phase 74-01]: Transform order: mapHeadings before collapseBold for idempotence with heading content containing asterisks
- [Phase 74-01]: Slack link extraction at applyText level prevents convertLinks from re-matching on 2nd pass
- [Phase 74-01]: Long placeholder tokens (KMHTML_, KMBOLD_, KMLINK_) prevent collision from adjacent NUL boundaries
- [Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox]: Wave 0 stub pattern: all 16 TestVSCodeRekey_* tests use t.Skip + fully commented assertion bodies (no _ = var blanks), keeping go vet clean without any new production symbols
- [Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox]: Sequenced SSM mock documented inline per test body (commented) rather than shared helper — per 76-RESEARCH.md two-mock-instances recommendation
- [Phase 76]: ec2DescribeAPI interface in vscode.go alongside SSMSendAPI; checkSandboxLock var in lock.go for test injection without real DDB; runVSCodeRekey returns nil after pre-flight with TODO marker Plan 76-02 will delete
- [Phase 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox]: Documentation written against locked CLI surface (Wave 2 pattern: docs in parallel with impl); CONTEXT.md samples mirrored verbatim as canonical source of truth
- [Phase 76]: rekeyInstallSpyMock pattern: spy on install script SendCommand, extract embedded pubkey, return as readback — handles non-deterministic ed25519 keygen in tests
- [Phase 76]: Atomic rename .pub-first: os.Rename(.pub.new → .pub) before os.Rename(.new → priv) — if second rename fails, ssh keeps using old private key, access preserved
- [Phase 78]: Auth pre-check in runShellWithSSM (Option 2): smaller diff, ssmClient stays in scope via injection; production path uses nil ssmClient and silently skips check
- [Phase 78]: agent_auth_test.go uses package cmd (internal) to access unexported dispatch vars, matching vscode_test.go convention
- [Phase 78]: 78-02: localPort==remotePort for codex SSM port-forward (1455:1455 or 1457:1457); no mismatch needed because codex binds same port on both ends
- [Phase 78]: 78-02: deferred pfCmd.Process.Kill() placed immediately after Start() to cover all exit paths; runSSMInteractiveSubprocess masks SIGINT so explicit Kill is the only reliable cleanup
- [Phase 78]: 78-02: codex URL relay not auto-opened (v1 decision) — codex prints OAuth URL to SSM stdout where operator can click; no parallel poller goroutine added
- [Phase 79-km-presence-daemon]: Renamed fakeRunningSandboxLister to avoid conflict with existing fakeSandboxLister in doctor_ebs_test.go
- [Phase 79-km-presence-daemon]: Use -trimpath -ldflags '-s -w' for km-presence (stripped binary, no version embed, matches km-slack pattern)
- [Phase 79-km-presence-daemon]: pgrep -afE: AL2023 pgrep defaults to BRE; -E flag required for | alternation in agent-process regex
- [Phase 79-km-presence-daemon]: tmux list-clients without -t flag lists all sessions on default socket; matches agent.go:423 convention
- [Phase 79-km-presence-daemon]: emitFn var seam allows tick() tests to intercept emit without /run/km/audit-pipe subprocess
- [Phase 79-km-presence-daemon]: km-presence systemd unit placed unconditionally (outside SlackInboundEnabled gate), joining core sidecars in both eBPF and proxy enforcement branches
- [Phase 79]: Used runningSandboxListerFunc closure wrapping existing SandboxLister.ListSandboxes filtered to status=running for presence check lister
- [Phase 79]: Confirmed log group prefix as /{resource_prefix}/sandboxes/ from audit-log sidecar source
- [Phase 79-05]: doctor_presence.go CloudWatch log group must include trailing slash (/km/sandboxes/X/ not /km/sandboxes/X); filter pattern must use JSON metric filter syntax { $.source = "presence" } not bare string
- [Phase 79-05]: km init --sidecars Go-path gap deferred: buildAndUploadSidecars in init.go missing km-presence; workaround is make sidecars; flagged in deferred-items.md
- [Phase 79-05]: Phase 79 COMPLETE — orphaned-heartbeat bug provably fixed on sandbox learn-78ac4247 (8/8 must_haves PASS, UAT 2026-05-10)
- [Phase 80-km-cluster-cross-account-irsa-for-k8s-integrations]: mockClusterRunner is local to cluster_test.go (not extending init_test.go's mockRunner) to avoid breaking unrelated init tests
- [Phase 80-km-cluster-cross-account-irsa-for-k8s-integrations]: config_clusters_test.go uses package config_test (external) to exercise config.Load() through its public surface
- [Phase 80]: Terragrunt // double-slash source path required for cross-module local references: source = infra/modules//child-module/v1.0.0 copies infra/modules/ into cache, making sibling modules resolvable
- [Phase 80]: km-operator-policy/v1.0.0 exposes 8 variables (role_id, resource_prefix, artifact_bucket_arn, state_bucket, dynamodb_table_name, dynamodb_budget_table_arn, sandbox_table_name, identities_table_name); cloudwatch_logs intentionally not extracted
- [Phase 80]: ClusterConfig struct added above Config with dual mapstructure+yaml tags; Clusters field appended at end of Config struct; viper UnmarshalKey handles slice-of-structs decode
- [Phase 80]: cluster-irsa trust policy uses dynamic sub_condition (StringLike for wildcards, StringEquals for literals) derived from oidc_provider_arn via regex replace
- [Phase 80]: cluster-irsa/v1.0.0 module source path ../../km-operator-policy/v1.0.0 is relative; Plan 80-05 terragrunt.hcl must use // double-slash notation
- [Phase 80]: Exported RunClusterAdd/RunClusterRm/GenerateClusterHCL + seam vars so cmd_test (external package) can inject mocks; PersistClustersConfig(configPath, clusters) takes explicit path for testability
- [Phase 80]: runner.Plan(ctx, dir) reuses buildCommand+runCommand factory — no new fields on Runner struct; dry-run path calls Plan, apply path calls Apply; rollback contract: no auto-destroy on persist failure
- [Phase 80]: km-config.yaml must be chmod 644 before km cluster add --dry-run=false; ships read-only (chmod 400) by design
- [Phase 80]: CLAUDE.md Phase 80 section uses Phase 73/79 format: heading depth, flag table, one-time setup, important notes blocks
- [Phase 80.1]: OIDC provider IAM policy uses single-statement Resource '*' (consistent with kms/ses_send; List action cannot be resource-scoped per IAM docs)
- [Phase 80.1]: Added iam:UntagOpenIDConnectProvider for completeness (Terraform may call on destroy of tagged provider)
- [Phase 80.1]: Use aws.String() in t.Run bodies to keep aws import used while outer test is skipped; add _ = targetURL to prevent unused-const compile error
- [Phase 80.1-03]: register_oidc_provider defaults to true — preserves Phase 80 behavior for all existing stacks
- [Phase 80.1-03]: moved {} block before locals {} ensures Terraform state migration resolves before resource evaluation
- [Phase 80.1-03]: oidc_provider_arn_local local unifies both OIDC branches; trust policy and outputs reference the local
- [Phase 80.1]: Export GenerateClusterHCLWithOIDC so cmd_test package can test the false branch; RunClusterAdd takes registerOIDCProviderFlag string as 11th param; unit tests pass 'false' to skip IAM auto-detect
- [Phase 67.2]: 67.2-01: Used new internal test file aws_adapters_internal_test.go (package bridge) rather than test-only exporter shim — keeps production aws_adapters.go free of *ForTest symbols, matches existing convention (events_handler_test.go is also package bridge)
- [Phase 67.2]: 67.2-01: Enumerated all 11 extra Slack codes from RESEARCH.md in classifyReactionError switch (token_expired, ekm_access_denied, accesslimited, external_channel_migrating, etc.) instead of relying solely on default-unknown→transient — gives operators correct log levels (Error vs Warn) when these specific codes fire
- [Phase 67.2]: Used math/rand v1 (not crypto/rand or v2) for jitter — locked CONTEXT.md decision; v1's *rand.Rand fits test injection cleanly
- [Phase 67.2]: Removed inline already_reacted shortcut — classifier returns classSuccess for that case (single source of truth)
- [Phase 67.2]: Per-iteration doOneAttempt helper instead of inline body — addresses RESEARCH.md Pitfall 4 (defer resp.Body.Close stacking on retry exhaustion)
- [Phase 67.2]: 67.2-03: Operator UAT APPROVED 2026-05-15 — bridge Lambda redeployed (full `km init` required, not `km init --lambdas` partial path), live 👀 reaction confirmed on #sb-{id} smoke-test. Documentation accuracy issue noted: plan/spec/CLAUDE.md/slack-notifications.md all say `km init --lambdas` deploys the bridge zip — actually requires full `km init` since the bridge zip uploads via terragrunt-applied lambda-slack-bridge module. Phase 80 follow-up logged: `km init` should auto-remediate Terraform lock-file drift introduced by Phase 80 hashicorp/tls provider addition (operator hit this and resolved on a fresh workstation via `terragrunt run --all init -- -upgrade`).
- [Phase 77]: FilterLogEvents added to CWLogsAPI interface end — real *cloudwatchlogs.Client already satisfies it natively
- [Phase 77]: NewLogsCmdWithClient DI seam follows NewStatusCmdWithFetcher pattern: nil client builds real cloudwatchlogs.Client at runtime
- [Phase 77]: mockSandboxMetadataAPI is a private copy in create-handler package (pkg/aws/sandbox_dynamo_test.go is aws_test and cannot be imported cross-package)
- [Phase 77]: failure_reason and failed_at stored as RFC3339 String attributes — consistent with locked_at/expires_at convention
- [Phase 77]: UpdateSandboxStatusAndReasonDynamo coexists with UpdateSandboxStatusDynamo; Wave 2 plans wire new helper into create-handler failure branch
- [Phase 77]: marshalSandboxItem includes failure fields to prevent silent drop on read-modify-write paths (same rationale as SlackInboundQueueURL fix)
- [Phase 77]: Bottom-up scan chosen for extractFailureReason over regex — returns last Error: line (most actionable root cause in km error format)
- [Phase 77]: UpdateSandboxStatusDynamo removed from failure branch entirely; UpdateSandboxStatusAndReasonDynamo subsumes it for both failed+nocap status paths
- [Phase 77]: Failure:/Failed At: lines placed AFTER Status: and BEFORE Created At: — logically paired with failure status; gate uses rec.Status field not field presence for defensive correctness
- [Phase 77]: errors.As used (not errors.Is) for ResourceNotFoundException through %w wrapping in km logs fallback
- [Phase 77]: --follow short-circuits before FilterLogEvents call so filter is never invoked in terminal-failure fallback mode
- [Phase 75]: omitempty on InboundQueueBody.Attachments is load-bearing: absent key (not null) for back-compat with older SQS consumers using jq .attachments[]?
- [Phase 75]: SlackFile mirrors Slack Events API shape; Attachment is the SQS payload schema — two separate types to prevent Slack API changes leaking into SQS contract
- [Phase 75]: isBotLoop uses allow-list: file_share admitted in Phase 75 so user file uploads flow to SQS
- [Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels]: Mirror block uses basename of s3_key; poller trusts bridge wrote the safe name
- [Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels]: ATTACH_COUNT gating: text-only Phase 67 path is bit-for-bit unchanged when ATTACH_COUNT=0
- [Phase 75-02]: Pitfall 1: HTTPClient.CheckRedirect=ErrUseLastResponse + manual redirect re-issue with Bearer token preserved
- [Phase 75-02]: Pitfall 2: io.ReadAll into []byte buffer then bytes.NewReader for S3 PutObject.Body (re-readable for SDK retries)
- [Phase 75-02]: FileDownloader field is nullable on EventsHandler for back-compat with pre-Phase-75 Lambda images
- [Phase 75]: Bridge IAM scoped to slack-inbound/* prefix only (not bucket-wide); memory_size 256→1024 for Pitfall 2 PutObject retry-rewindability
- [Phase 75]: New standalone s3-artifacts-lifecycle module (no required_providers per project convention); sourced via KM_ARTIFACTS_BUCKET env var matching lambda-slack-bridge pattern
- [Phase 75]: files:read added to VerifyEventsAPIScopes + checkSlackAppEventsScopes required slices; success message updated to enumerate all four required scopes
- [Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels]: Shared httpClient with CheckRedirect=ErrUseLastResponse: safe for Slack API methods (no redirects); avoids two http.Client instances
- [Phase 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels]: S3FileDownloader wired conditionally on KM_ARTIFACTS_BUCKET — nil guard matches Phase 68 pattern for graceful degradation
- [Phase 79.1]: Wave-0 RED-first: four test stubs written before implementations in Plans 02+03; no skip gates
- [Phase 79.1]: Regex-tolerant whitespace for tmpfiles.d column assertions; exact mode+owner strings remain strict
- [Phase 79.1]: Use p+ (not p) for tmpfiles.d audit-pipe entry: p alone is a no-op when a regular file exists at the path
- [Phase 79.1]: Strip backticks from Go raw string template comments: backtick inside const backtick-string closes the literal
- [Phase 79.1]: Self-heal on wrong-type path uses os.Remove + syscall.Mkfifo; on Remove failure record lastErr and let OpenFile fail naturally (consistent with existing retry discipline)
- [Phase 79.1]: Insert tmpfiles.d p+ drop-in in EC2 userdata: systemd-tmpfiles-setup.service runs before sysinit.target on every boot, recreating the audit-pipe FIFO before km-presence or km-audit-log can race
- [Phase 79.1]: Use p+ (not p) in tmpfiles.d: p is a silent no-op when a regular file exists at the path; p+ atomically replaces wrong-type entries — required for FIFO entries in tmpfs directories subject to write races
- [Phase 82-03]: KMBakeTags: resourcePrefix added as trailing positional parameter; km:resource-prefix tag alphabetized in returned slice; all baked AMIs now carry install-discriminator tag for Plan 04 filter
- [Phase 82]: Move EventsHandler wiring from init() to wireEventsHandler() called from main() so test builds do not trigger os.Exit during package init
- [Phase 82]: Use resourcePrefix+'-slack-threads' env+prefix-compute in userdata.go rather than threading cfg *config.Config into generateUserData (too invasive)
- [Phase 82-01]: Changed --resource-prefix flag default from 'km' to '' so preserve-on-re-run logic can detect 'nothing passed' vs 'user explicitly passed km'; defaulting now lives in runConfigure after checking disk
- [Phase 82-01]: Added --reset-prefix bool flag (default false) as explicit opt-in for re-defaulting resource_prefix back to km; guards against accidental silent reset on re-run
- [Phase 82-05]: BackfillTaggingAPI uses manual PaginationToken loop (not SDK paginator) to keep interface minimal and mocks simple
- [Phase 82-05]: DDB GetItem cross-install guard: skip any resource whose sandbox_id is absent from this install's DDB (Pitfall 4 mitigation)
- [Phase 82-05]: Default --dry-run=true for --backfill-tags matches km init UX pattern
- [Phase 82-multi-instance-resource-prefix-isolation]: 82-04: ListBakedAMIs takes explicit string param (not functional option) — empty string means no prefix filter for all-installs diagnostics
- [Phase 82-multi-instance-resource-prefix-isolation]: 82-04: checkOrphanedEC2 post-fetch tag discrimination (not API-level filter) so pre-Phase-82 untagged instances surface as WARN with --backfill-tags pointer
- [Phase 82]: email-handler: add standalone state_prefix variable (default tf-km) rather than overloading resource_prefix — keeps IAM policy scope separate from resource naming
- [Phase 82-06]: No moved{} block for SES rule-set: existing km install evaluates to identical name — zero Terraform diff
- [Phase 82-multi-instance-resource-prefix-isolation]: 82-08: No new km_label variable needed in ECS modules — all three already declared it (confirmed pre-flight); one-line substitution per module preserves backward compatibility
- [Phase 82-multi-instance-resource-prefix-isolation]: 82-09: Added resource_prefix variable to ECS modules (ecs-task, ecs, ecs-cluster); all six Terraform modules now emit km:resource-prefix tag alongside km:sandbox-id
- [Phase 82-multi-instance-resource-prefix-isolation]: Wave 3 apply: tag-only additions produce zero must-be-replaced lines; existing km prefix evaluates to km-sandbox-email unchanged
- [Phase 82-multi-instance-resource-prefix-isolation]: km doctor --backfill-tags requires explicit AWS_DEFAULT_REGION env var; cross-install safety guard correctly skipped 30 foreign/orphaned resources
- [Phase 82.1]: service_hcl.go: used variable name ec2StreamPrefix (not ec2ResourcePrefix) to avoid shadowing the existing ec2ResourcePrefix block at line 792
- [Phase 82.1]: KM_RESOURCE_PREFIX fallback pattern (default 'km') now consistent across userdata.go (Phase 82), ec2ResourcePrefix block, and stream-table derivation in same function
- [Phase 82.1-01]: configure.go preserve guard extended to bare-path invocations via effectiveDir = outputDir || findRepoRoot(), mirroring write-path logic
- [Phase 82.1-03]: SES activate_rule_set opt-in (default true): count-gate aws_ses_active_receipt_rule_set so second installs set KM_SES_ACTIVATE_RULESET=false to avoid stealing primary install's inbound email activation; Terraform 1.x auto-migrates count-index address change (no moved{} block needed, confirmed by operator terragrunt plan)
- [Phase 84]: ses-shared-rule-set/v1.0.0 foundation module: register_X flags for idempotency; no data-source fallback (AWS SES provider gap); KM_ROUTE53_ZONE_ID matches existing ses/ convention
- [Phase 84-06]: Recipient verification gate inserted BEFORE allowlist/safe-phrase for cheap dispatch of foreign-prefix emails
- [Phase 84-06]: Test handler Domain updated to sandboxes.example.com to match KM_EMAIL_DOMAIN production value; test email To: headers updated to operator-km@sandboxes.example.com
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: rule_set_name is string constant 'sandbox-email-shared' — no aws_ses_receipt_rule_set data source exists in AWS Terraform provider
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: v2.0.0 S3 bucket policy preserves CloudWatch Logs export grants from v1.0.0 since only one aws_s3_bucket_policy can exist per bucket
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: v1.0.0 stays in tree untouched as historical reference per CONTEXT.md — not deleted, not modified
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: km-send heredoc uses backward-compat fallback: ${KM_OPERATOR_EMAIL:-operator@${KM_SANDBOX_DOMAIN:-...}} preserves old sandboxes
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: SendCreateNotification signature extended with resourcePrefix at position 6; call site passes cfg.GetResourcePrefix()
- [Phase 84-01]: W0-06/07/08 stubs RED via checkSESRules returning CheckSkipped; SESReceiptRuleAPI kept empty until Plan 84-07 adds classic SES SDK
- [Phase 84-01]: W0-11 test-no-82.1-leftovers Makefile target NOT wired into test umbrella; Plan 84-08 adds the dep once OPERATOR-GUIDE.md deletions land
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: --reset-prefix clears operator_email to empty string; same run does NOT re-derive; next km configure re-derives from new default prefix
- [Phase 84-ses-per-install-rule-namespacing-via-operator-address-prefix]: deriveOperatorEmail returns empty string on any blank input; callers handle fallback
- [Phase 84]: Grep gate excludes v1.0.0 dir via --exclude-dir='v1.0.0' (catches both canonical infra/modules/ses/v1.0.0/ and cached copies in .terragrunt-cache/)
- [Phase 84]: No umbrella test target exists in Makefile; test-no-82.1-leftovers CI integration is operator-driven
- [Phase 84-07]: SESIdentityLister combines ListReceiptRuleSets + ListEmailIdentities; InitSESPreflight is package-level func var for testable init gate; W0-06/W0-07 updated to use real mocks
- [Phase 84-09]: CLAUDE.md Phase 84 section inserted between Architecture and Network Enforcement (Alternative anchor — no Phase History parent section exists)
- [Phase 84-09]: KM_OPERATOR_EMAIL table row updated in OPERATOR-GUIDE.md not CLAUDE.md (variable description lives there; no duplicate created)
- [Phase 84.1]: Plan 84.1-01: no deprecation shim for ExportConfigEnvVars→ExportTerragruntEnvVars rename (H5)
- [Phase 84.1]: Plan 84.1-01: KM_REGION_LABEL derived via compiler.RegionLabel(cfg.PrimaryRegion), not added as a config field — single-sourced in pkg/compiler
- [Phase 84.1]: Plan 84.1-01: exported RunBootstrapSharedSES as one-line test seam forwarder; cobra command path unchanged
- [Phase 84.1]: Plan 84.1-03: Detection-only state-digest check (no auto-repair) — WARN level with copy-paste aws dynamodb update-item remediation; uses dynamodb.NewScanPaginator to handle >1MB lock-table scans
- [Phase 84.1]: Heartbeat lives at the runner layer, not the call-site layer — Putting the heartbeat goroutine inside runBounded means every km command that wraps terragrunt (km init, km bootstrap, km destroy, km cluster add, km slack init) inherits the heartbeat automatically without per-caller wiring
- [Phase 84.1]: ModuleTimeoutFunc is a package-level var, not a const — Enables sub-second tests that exercise the timeout path; production behaviour unaffected. Same pattern as ApplyTerragruntFunc and InitSESPreflight test seams.
- [Phase 84.1]: Single BootstrapApplyTimeout bounds Reconfigure+Apply together in defaultApplyTerragrunt — One budget for one module-apply, mirroring init.go per-module-Apply treatment. Separate bounds would have allowed 20 minutes wall-clock for a 10-minute-bootstrap-apply.
- [Phase 84.1]: cmd.Cancel uses os.Interrupt not syscall.SIGTERM (plan-checker rev 1 L15) — syscall.SIGTERM is undefined on Windows builds. os.Interrupt maps to SIGINT on Unix and CTRL_BREAK_EVENT on Windows, achieving the give-the-child-a-chance-to-clean-up-before-SIGKILL semantics cross-platform.
- [Phase 84.1]: Plan 84.1-04: foundation auto-detect prefers tfstate ownership over AWS reality (FoundationStateReader interface + s3FoundationStateReader impl); register_* flag semantics shifted from 'create only on first apply' to 'manage this resource' — closes GAP-2 (idempotency) + GAP-3 (state precedence)
- [Phase 84.1]: Plan 84.1-04: 6 import {} blocks in foundation main.tf + 7 removed {} blocks (lifecycle.destroy=false) in regional v2.0.0 main.tf make in-place v1.0.0→v2.0.0 cutover destroy zero shared AWS resources — closes GAP-6 (highest-impact gap); H9 keeps DKIM CNAMEs OUT of import blocks (operator-run per OPERATOR-GUIDE.md from Plan 84.1-05)
- [Phase 84.1]: Plan 84.1-04: Terraform required_version bumped 1.6.0 → 1.7.0 in infra/live/root.hcl (C3) for removed{} block support; bumped in separate chore commit BEFORE consuming syntax so operators see clean version-mismatch errors from terraform instead of parse errors
- [Phase 84.1]: Plan 84.1-05: closed `passed-with-caveats` 2026-05-17 — minimal UAT scope (Steps 1+2 only; GAP-6 structurally verified via uat-gap6-check.sh helper script); Steps 3-9 + DRIFT-B/C empirical re-runs deferred to Phase 84.2. Introduces `passed-with-caveats` UAT status (between `passed` and `diagnosed`) for the case where work is complete and no regressions found, but the most operationally-heavy verifications were intentionally deferred rather than skipped-without-acknowledgement.
- [Phase 84.1]: Plan 84.1-05: GAP-6 verification is structural (terraform validate + zero-destroy assertion against post-recovery regional state with no v1.0.0 resources to suppress) but NOT empirical (no Phase 82.x snapshot was constructed). Code is correct; the in-anger upgrade scenario was not re-exercised. Phase 84.2 will close this with the `km init --plan` flag against a fresh Phase 82.x install.
- [Phase 84.2-03]: PlanWithOutput uses runBounded (Phase 84.1-02 ctx bounding + heartbeat) while ShowPlanJSON uses cmd.Output() for clean stdout bytes — separation of methods lets Plan 04 error-differentiate plan failure from show/parse failure
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: Hand-rolled JSON struct over encoding/json instead of terraform-json library (forward-compat, library's own README warns against it)
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: planreport package is pure stdlib (encoding/json, slices, fmt, strings) — zero terragrunt/AWS/cmd dependencies for trivial unit testability
- [Phase 84.2]: Wave 0 RED-scaffolding: blank-identifier forward references produce verifiable undefined-symbol vet errors; mockPlanRunner embedded type avoids cross-file field access; writer-injected test seams match NewBootstrapCmdWithWriter pattern
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: Plan 04 auto-implemented as Rule 3 blocking prerequisite for Plan 05; runBootstrapSharedSESPlan reuses init.go helpers (same package)
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: --plan wins over --dry-run: RunE checks plan branch BEFORE sidecars/lambdas/dryRun (CONTEXT.md Decision 1)
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: runInitPlanWithWriter shim in cmd_test package (init_test.go) satisfies Wave 0 compile contract; InitRunner compile-time assertion catches runner drift
- [Phase 84.2-06]: appendKmInitPlanTip extracted as io.Writer-injected helper for unit testability via bytes.Buffer without full doctor mocking harness
- [Phase 84.2-06]: Doctor tip is unconditional (not gated on warnCount) — discovery channel must fire on every clean doctor run
- [Phase 84.2-06]: Multi-instance SKILL.md pre-apply verification updated to km init --plan (--dry-run=true comment implied plan semantics)
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: Phase 84.2 closes with status=diagnosed; GAP-1 (aws_ses_receipt_rule missing from ProtectedTypes) is regression-of-purpose requiring Phase 84.3 closure
- [Phase 84.2-km-init-plan-flag-and-destroy-class-gate]: Scenarios 3, 4b, 4c, 6 deferred to Phase 84.3 UAT; Scenario 7 accepted as PASS via incidental capture during Scenario 2 first attempt
- [Phase 84.2-08]: aws_ses_receipt_rule added as 10th ProtectedTypes entry with Phase 82->84 incident comment (CONTEXT.md Decision 6 requirement)
- [Phase 84.2-08]: Bootstrap behavioral tests use t.Skip+TODO — no RunBootstrapSharedSESPlanWithRunner seam exists (covered by UAT Scenario 4b/4c)
- [Phase 84.2-09]: GAP-1 closure verified via UAT — gate catches aws_ses_receipt_rule (the Phase 82 incident type) at exit 1 for both km init --plan (Scenario 2) and km bootstrap --shared-ses --plan (Scenario 4b); override flag exits 0 with full trip block printed (Scenarios 3, 4c)
- [Phase 84.2-09]: Scenario 4b retargeted to aws_ses_active_receipt_rule_set (lifecycle.prevent_destroy=true on aws_ses_receipt_rule_set blocks gate evaluation — prevent_destroy is first-line defense; gate is second-tier net)
- [Phase 84.2-09]: COSMETIC-1 deferred — printTripBlock headline hard-coded to km init --plan even when invoked via km bootstrap --shared-ses --plan; non-blocking, one-line fix queued for follow-up
- [Phase 84.2-09]: Phase 84.2 is complete — no blocking gaps remaining; all 4 deferred UAT scenarios PASS; destroy-class gate ready for production use
- [Phase 84.3]: mockS3HeadBucketConfigure renamed from plan's mockS3HeadBucketClient to avoid collision with doctor_test.go declaration
- [Phase 84.3]: init_plan_84_3_test.go created as new file (package cmd_test) per >600-line threshold rule; both files use existing mockPlanRunner
- [Phase 84.3]: TestConfig_AccountsTerraformEnvWins is also RED (viper AutomaticEnv dot-notation limitation) — Plan 02 must add SetEnvKeyReplacer to fix both yaml-authority tests
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: validateArtifactsBucket uses strings.Index for angle-bracket detection to catch embedded tokens like <prefix>-artifacts-12345678
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: accountsYamlAuthoritativeKeys: yaml wins for organization/dns_parent/application; accounts.terraform env-precedence preserved intentionally
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: warnShellEnvConflict called before validation so drift WARNs emit even when required flags are missing
- [Phase 84.3-03]: --all routing inserted before sharedSES+plan check; mutex error returned before any AWS call; test seam vars RunBootstrapFunc/RunBootstrapSharedSESFunc/RunBootstrapAllFunc added to bootstrap.go
- [Phase 84.3]: smithy.APIError used for 404 detection in ensureArtifactsBucketExists (matches configure.go precedent, test mock uses smithy errors)
- [Phase 84.3]: ensureArtifactsBucketExists uses 4-arg signature (ctx, cfg, io.Writer, S3HeadBucketAPI) matching stub and test call site
- [Phase 84.3-06]: makeEmptyRepoRoot + KM_REPO_ROOT override: fake repo root with no modules causes RunInitPlanWithRunner to skip all modules and return nil fast — enables fast RED assertion tests without blocking on terragrunt
- [Phase 84.3-06]: isolateExportEnvVars helper: pre-register all 12 ExportTerragruntEnvVars output env vars via t.Setenv to prevent os.Setenv calls leaking between tests
- [Phase 84.3-06]: TestRunBootstrap_DriftWarn_KM_REGION SKIP-guarded for Plan 07: YAMLDefaults field does not exist yet; test compiles clean and does not pollute CI FAIL count before Plan 07
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: Gap 2 closed: ExportTerragruntEnvVars(loadedCfg) inserted in runBootstrap at line 1317, matching existing pattern in runBootstrapSharedSES and runBootstrapSharedSESPlanWithWriter
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: Snapshot v2 yaml values into Config.YAMLDefaults before env baking so ExportTerragruntEnvVars can compare envVal against yaml value, not the already-baked cfg field
- [Phase 84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted]: warnAndSetEnv fallback to cfgVal when YAMLDefaults nil preserves backward compat for Plan 01 unit tests that build cfg directly without config.Load()
- [Phase 84.3]: isPlaceholderBucket inlined in config.go (not imported from cmd) to avoid circular dependency
- [Phase 84.3]: validateArtifactsBucket added to both runInitPlan and runInitPlanWithWriter (production + test seam)
- [Phase 84.3]: UAT status set to closed_with_deferred: all 4 automated gaps closed (84.3-07/08/09); 6 operator-follow-up items remain DEFERRED
- [Phase 84.3]: Phase 84.3 requirements tracked via REQUIREMENTS.md synthetic IDs (ENV-CONFIG-DRIFT-WARN, ARTIFACTS-BUCKET-DERIVATION, BOOTSTRAP-WORKFLOW-DISCOVERABILITY, CONFIG-DISPLAY-VS-YAML-AUTHORITY) following Phase 84.2 pattern
- [Phase 84.4-multi-install-module-hardening]: hcl/v2 anchored via modulehygiene_stub_test.go import to survive go mod tidy until Plan 01
- [Phase 84.4-multi-install-module-hardening]: make test: excludes 5 packages with pre-existing failures — documented in deferred-items.md
- [Phase 84.4-multi-install-module-hardening]: BuildImportCommand exposed as public method mirroring existing Build*Command pattern for testability
- [Phase 84.4-multi-install-module-hardening]: tLogger interface + mockT enables negative tests in checkTFFile without outer test failure — avoids expected-failure semantics that testing.T doesn't natively support
- [Phase 84.4-multi-install-module-hardening]: walkModulesV2Plus skips v1.0.0 explicitly but audits v1.1.0+ — any post-historical release is subject to prefix-clean enforcement
- [Phase 84.4-03]: resource_prefix default=km renders creation_token byte-identical to v1.0.0 — no moved{} blocks needed for default installs
- [Phase 84.4-03]: test_harness.tf.skip uses kph (3-char) and whereiskurt (11-char) synthetic prefixes to bracket the validation regex range
- [Phase 84.4-04]: s3-replication/v2.0.0: inline provider alias block preserved verbatim from v1.0.0; no required_providers added (root.hcl manages providers)
- [Phase 84.4-04]: doctor.go km-s3-replication- hardcoded literal fixed to rolePrefix+s3-replication- immediately on module parameterization (existing comment explicitly named this fix as pending)
- [Phase 84.4-02]: BuildSCPPolicyFromPrefix uses minimal trustedBase (SSO only) matching HCL module defaults — keeps JSON under 4KB even for 12-char prefix
- [Phase 84.4-02]: scp/v2.0.0 variable resource_prefix with default='km' renders byte-identical names to v1.0.0 — no Terraform state migration required for default km install
- [Phase 84.4-02]: 5000-byte safety threshold in terraform_data precondition (120-byte buffer below AWS 5120-byte hard limit)
- [Phase 84.4]: Plan 06: Gate DKIM auto-import on sesv1Client/r53ImportClient/stateReader non-nil — listerOverride (test) path unchanged
- [Phase 84.4]: Plan 06: Route53 import IDs use double underscore for TXT (<zone>__amazonses.<domain>_TXT)
- [Phase 84.4-05]: resource_prefix in all three live wiring files uses get_env with km default for exact backward compat
- [Phase 84.4-05]: No additional region variants (use2/usw2) found — only use1 and management/scp needed flipping
- [Phase 84.4-multi-install-module-hardening]: SCP dual-tracking: when second install imports canonical SCP, use terragrunt state rm before km uninit — never terraform destroy the shared policy
- [Phase 84.4-multi-install-module-hardening]: km uninit --force bypasses lister nil-pointer bug; canonical fix in commit 2861dbb (use newRealLister constructor)
- [Phase 84.4-multi-install-module-hardening]: Plan 08 PARTIAL PASS: resource-naming layer validated (v2.0.0 modules, 113 adds/0 destroys, EFS isolation OK); cross-install SCP AND-composition + ssm-session-doc shared-resource teardown + Plan 06 auto-import failure require 84.4.1 before multi-install is production-safe
- [Phase 84.4-multi-install-module-hardening]: ssm-session-doc elevated to TIER-1: terragrunt import workaround for hardcoded-name modules is unsafe at teardown — destroys shared AWS resource; must not be used as substitute for v2.0.0 migration
- [Phase 84.4.1-03]: Gate condition changed from !registerID to client-pair availability; registerID flag reflects state ownership not AWS existence
- [Phase 84.4.1-03]: Extract wrapAutoImportError helper so recovery-message format string is unit-tested
- [Phase 84.4.1-01]: SCP v2.0.0 updated in-place (no version bump): *-* wildcard patterns replace prefix-bound ARNs in trusted_arns slots; account wildcarded too. Security: operator-only IAM:CreateRole guards against misuse.
- [Phase 84.4.1-01]: BuildSCPPolicyFromPrefix signature unchanged (3 params): resourcePrefix+applicationAccountID blanked with _ = for compat; callers require zero edits. Live apply deferred to Wave 3 plan 84.4.1-05.
- [Phase 84.4.1-multi-install-identity-permission-gap-closure]: GetSandboxSessionDocumentName implemented fully in Wave 0 (delegates to nil-safe GetResourcePrefix) — 3 tests pass immediately without deferring to Wave 1
- [Phase 84.4.1-multi-install-identity-permission-gap-closure]: Interface-first contract: UnbootstrapDynamoDBAPI declared in unbootstrap.go with DynamoDB field in UnbootstrapDeps before any implementation (Wave 2 wires callsites)
- [Phase 84.4.1-02]: AWS SSM does not support document rename; moved {} block is documentation only; apply is destroy+create with 1-2s window
- [Phase 84.4.1-02]: execSSMSession receives cfg *config.Config rather than pre-computed docName, keeping function self-contained
- [Phase 84.4.1-04]: terraform.version sidecar chosen over exec of cross-arch arm64 binary for version-aware cache invalidation
- [Phase 84.4.1-04]: Internal package cmd test files created for unexported helper unit tests alongside external cmd_test scaffold stubs
- [Phase 84.4.1-04]: configure HeadBucket probe gated on in!=nil rather than nonInteractive flag for defensive stdin handling
- [Phase 84.4.1-06]: OPERATOR-GUIDE.md Phase 84.4 gaps+workarounds prose REPLACED with clean Phase 84.4.1 runbook (closure criterion j). Pattern-based SCP trust design, DKIM auto-import, and per-install SSM doc isolation documented as production-ready.
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: ValidateArtifactsBucket placed in config package to avoid import cycles from config.Load()
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: UninitOpts struct replaces positional force bool in RunUninitWithDeps, mirroring UnbootstrapOpts pattern
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: Export buildLambdaZipsFunc as BuildLambdaZipsFunc so cmd_test (external package) can override it; warn-and-continue semantics mirror runInit at line 491-496
- [Phase 84.4.1.1-02]: Validate yaml-authoritative bucket value in config.Load() (not viper env-merged) to preserve KM_ARTIFACTS_BUCKET env override semantics
- [Phase 84.4.1.1-02]: TestConfigLoad_AcceptsLegacyBucketLiteral updated to expect error: km-artifacts-12345 now fails canonical regex (5-digit suffix)
- [Phase 84.4.1.1-03]: Keep sentinel fast-path (km-artifacts-12345) even though regex catches it — better error message mentioning km-config.example.yaml placeholder
- [Phase 84.4.1.1-03]: cmdCanonicalBucketRE named with cmd- prefix to distinguish from canonicalBucketRE in config package
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: SCP cleanup gated on --include-scp (default false) to avoid accidentally removing SCP during partial teardowns; warn-and-continue on failure so uninit is never blocked by Organizations API issues
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: Detach-then-delete ordering enforced for SCP cleanup: DeletePolicy only called after DetachPolicy succeeds, mirroring Organizations API requirements
- [Phase 84.4.1.1-04]: OrgsListAllPoliciesAPI interface kept separate from OrgsListPoliciesAPI to preserve narrow interface for checkSCP
- [Phase 84.4.1.1-04]: checkOrphanSCPs filters by -sandbox-containment suffix (not allow-list), WARN level to avoid blocking operator workflow
- [Phase 84.4.1.1-multi-install-follow-on-gaps]: uninit active-sandbox filter status==running is correct; TTL-expired sandboxes block until ttl-handler Lambda runs; --force is the escape hatch
- [Phase 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup]: Locked checkStateLockDigestSweeper signature with separate S3StateHeadAPI / LockDigestDeleterAPI seams (Phase 84.1 S3StateReader stays narrow)
- [Phase 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup]: Wave 0 stub returns CheckSkipped so callers compile cleanly while the 8 new TDD tests stay RED until Plan 02 implementation
- [Phase 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup]: doctor.go remains byte-identical in Plan 01; Plan 02 has a documented exception to add CheckResult.Details for --json full-list plumbing; Plan 03 swaps buildChecks
- [Phase 85]: Phase 85 Plan 02: implemented checkStateLockDigestSweeper with parallel HEAD scan + ctx-safe semaphore + SandboxLister cross-reference age guard + 25-item BatchWriteItem batches + UnprocessedItems failure surfacing; added CheckResult.Details []string for --json ACCEPT-READ full-list output (documented file-ownership exception)
- [Phase 85]: Hoisted DoctorDeps.DeleteStateDigests bool field from plan Task 2 into Task 1 commit (compile dependency for deps.DeleteStateDigests = deleteStateDigests inside runDoctor); Task 2 still owns the two interface fields + wiring as planned
- [Phase 85]: buildChecks REPLACES (not duplicates) the Phase 84.1 checkStateLockDigest registration with checkStateLockDigestSweeper; the old function body stays at doctor.go:3486 as a regression baseline so the 7 TestCheckStateLockDigest_* tests still pass
- [Phase 85 P04]: Live UAT PASS on klankermaker.ai — 278 orphan rows cleaned in 10.232s, read-only run 9.496s (~10.6× speedup vs ~1:41 baseline), post-cleanup ✓ state digest consistent (913 items checked); live state-locks + running sandbox lock preserved
- [Phase 86-km-create-prompt-queue]: Used package cmd_test (not cmd) for Phase 86 stubs to match all other test files in the directory
- [Phase 86-km-create-prompt-queue]: Used t.Skip with Wave-N markers over compile-guard stubs — cleaner and matches go test SKIP semantics
- [Phase 86-km-create-prompt-queue]: Restart=on-failure for km-queue.service: runner exits 0 on empty queue; Restart=always would busy-loop
- [Phase 86-km-create-prompt-queue]: Unconditional seeding: every EC2 sandbox gets runner+unit via userdata.go (no profile flag); R1 regression is unit-installed-but-idle
- [Phase 86-km-create-prompt-queue]: Step 16 runs operator-side post runCreateRemote; Lambda untouched (RESEARCH.md Pitfall #1)
- [Phase 86-km-create-prompt-queue]: ReconcileMetaStatus exported; PQ-08 TestQueueRunnerStateMachine GREEN ahead of Wave 2 schedule
- [Phase 86-km-create-prompt-queue]: --queue is a BoolVar on existing agent list Cobra command (not new subcommand); runAgentListQueue uses nil-guard pattern identical to runAgentList
- [Phase 86-km-create-prompt-queue]: ExitCodeError typed error (not inline os.Exit): preserves RunE/Cobra deferred cleanup; single os.Exit at outermost Execute() boundary in root.go
- [Phase 86-km-create-prompt-queue]: QueuePollInterval exported var (not const) for cmd_test override; WaitForQueueDrain exported wrapper for external test package access
- [Phase 86-km-create-prompt-queue]: SilenceErrors NOT set on create cmd: Cobra prints typed error once; boundary does not double-print before os.Exit
- [Phase 87-additionalsnapshots]: Encrypted *bool (not plain bool) in AdditionalSnapshotSpec — nil maps to terraform null, inheriting snapshot encryption; distinguishes omitted from explicit false
- [Phase 87-additionalsnapshots]: snapshotId regex ^snap-[0-9a-f]{8,17}$ enforced at JSON schema (Layer 0); device constrained to ^/dev/sd[f-p]$ pool
- [Phase 87]: validateAdditionalSnapshots regexp at function scope (not package-level) — acceptable for non-hot-path validation
- [Phase 87]: EC2-only gate in both validate.go (offline) and service_hcl.go (compile-time) for defense-in-depth parity with additionalVolume
- [Phase 87]: Reserved mountpoint list uses exact-match (size==0 valid=inherit; size<0 rejected at Layer 1)
- [Phase 87-03]: UnauthorizedOperation (EC2-specific) → graceful WARN+nil; AccessDenied surfaces as error (different code path) per SNAP-03 aliasing risk
- [Phase 87-03]: BDM gate broadened: triggers when AdditionalVolume != nil OR len(AdditionalSnapshots) > 0 (Risk #4 fix for UAT-4 snapshots-only profiles)
- [Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile]: Pool exhaustion: string-return + caller-checks (minimal-diff vs returning (string, error) from pickAdditionalVolumeDevice)
- [Phase 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile]: additional_snapshots empty list renders as compact form [] (diff-stable; Wave 4 module default = [] accepts both)
- [Phase 87-06]: ec2spot v1.1.0 additive copy: for_each key=tostring(i), size=null inherits snapshot, encrypted=null inherits snapshot, Name tag uses var.resource_prefix, no required_providers block
- [Phase 87-05]: Legacy additionalVolume always uses device letter 'f' (no Device field in AdditionalVolumeSpec — historical /dev/sdf); additionalSnapshots use explicit Device field or sequential auto-assign in generateUserData
- [Phase 87-05]: No nvmeAlias Go template func added — bash-side device probe loop handles aliasing per critical_research_corrections #2
- [Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher]: Agent string yaml:agent,omitempty on CLISpec — absence parses as empty string; downstream treats empty as claude
- [Phase 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher]: JSON Schema enum [claude,codex] rejects any other value including wrong case; no semantic validator needed
- [Phase 70]: ActionPermalink + ActionUpdate added to SlackPoster interface; PostResponse.Permalink field added; runWith returns (string, error) to expose message ts to callers
- [Phase 70]: runPermalinkWith + runUpdateWith testable inner functions mirror run/runWith pattern for unit testing new sidecar subcommands without SSM dependency
- [Phase 70]: PermissionRequest and Notification share KM_NOTIFY_ON_PERMISSION gate via OR-pattern in case statement; tool_name field assumed per spike (UAT Plan 70-09 verifies)
- [Phase 70]: last_assistant_message field name assumed per SPEC.md (MEDIUM confidence); Stop fast-path is 1-line fix if UAT finds different name
- [Phase 70-codex-parity]: ~/.codex/config.toml written unconditionally in userdata (not gated on spec.cli.agent); KM_AGENT dual-file env emission via existing notifyEnv map; no PostToolUse hook (Tier 2 deferral)
- [Phase 70]: KM_CODEX_RUN_ID exported inline in sudo command string (Pitfall 3 mitigation)
- [Phase 70]: jq -Rs . for DDB last_assistant_msg encoding — no extra quotes around LAST_MSG_JSON
- [Phase 70]: Codex resume uses subcommand form 'codex exec resume' NOT --resume flag
- [Phase 70]: Cross-agent switch fetches OLD permalink FIRST so new top-level body embeds it at post-time — no placeholder ever posted to Slack, no chat.update in critical path
- [Phase 70]: Test bash scripts use tr for case-folding (macOS bash 3.2 portability); production poller uses bash 4+ dollar-comma expansion on EC2 Linux
- [Phase 70-07]: checkCodexVersionSupportsJSONL (Path B) replaces codex_hook_config_present; probes codex binary/version/--json flag via SSM rather than ~/.codex/config.toml hook entries
- [Phase 70-07]: CodexSSMRunner set nil in production; org-level SCP blocks ssm:SendCommand on application account; check returns CheckSkipped on standard installs
- [Phase 70]: docs/codex-parity.md documents Path B (JSONL stream) as actual mechanism; config.toml noted as forward-compat artifact with no runtime effect under Codex 0.121-0.133
- [Phase 70]: SC-3 drop documented: hooks don't fire under --dangerously-bypass-approvals-and-sandbox in shipping Codex; expected behavior not a bug
- [Phase 88]: Gate Codex L7 proxy on p.Spec.CLI.Agent=="codex" exact match per RESEARCH.md; plan 88-06 must honor this gate
- [Phase 88]: Gate Codex L7 proxy host on nil-safe p.Spec.CLI != nil && p.Spec.CLI.Agent == codex to avoid runtime panic on profiles without CLI block
- [Phase 88]: OpenAI cache-token subtraction (uncached = input - cached) belongs in CalculateOpenAICost, NOT ExtractOpenAITokens — extractor returns inclusive input_tokens
- [Phase 88]: All 11 RED tests in one file (openai_test.go), written atomically — mirrors single-file anthropic_test.go pattern
- [Phase 88]: All 3 RED tests placed in http_proxy_test.go (not a new file); captureModelIDStub reused from anthropic_test.go; TestTransparent_OpenAI uses black-box external package pattern
- [Phase 88]: Extend aws.BedrockModelRate with CachedInputPricePer1KTokens instead of parallel struct — uniform type across providers, Anthropic uses 0.1x multiplier, OpenAI uses explicit field
- [Phase 88]: openaiHostRegex placed in openai.go for grep-ability; scanner buffer bumped to 10MB for large Responses API events
- [Phase 88]: Added plain-HTTP bypass for OpenAI in proxy.go general handler (mirrors GitHub filter pattern) so budget-metered api.openai.com requests bypass allowlist check
- [Phase 88]: TestTransparent_OpenAI redesigned to use innerProxy WITH WithBudgetEnforcement — goproxy plain-HTTP path exercises the same OpenAI OnResponse metering handler
- [Phase 88]: 88-07: UAT via direct curl (not km agent run) — Codex CLI 0.133.0 WebSocket-first behavior blocks MITM TLS on wss:// upgrades; curl proves intercept+DDB pipeline end-to-end
- [Phase 88]: 88-07: gpt-4o-mini-2024-07-18 rate table confirmed live: spentUSD=0.0000033 matches 14×$0.00015/1k+2×$0.0006/1k; OAI-BUDGET-09 satisfied
- [Phase 89]: SecretsSpec as pointer on Spec (nil=absent) for backwards compat with pre-89 profiles
- [Phase 89]: ValidateSemantic checks .enc.yaml suffix only; ValidateSopsBundleFile layered by callers for file existence + sops: block
- [Phase 89]: Fixture generated via real sops v3.11.0 + age v1.3.1 (not synthetic fallback); downstream 89-05 can use age key for offline decrypt round-trip
- [Phase 89]: No required_providers blocks in any module HCL — root.hcl is the single provider source (project_terragrunt_providers_in_root)
- [Phase 89]: ec2spot IAM policies use kms:ResourceAliases condition instead of key ARN — sandbox compile does not need key ARN at template time
- [Phase 89-sops-secret-injection-for-sandboxes]: KMSAliasDeleter defined in uninit.go (not bootstrap.go); Tasks 2+3 committed together due to test-package compile dependency; 7-day pending window for ScheduleKeyDeletion in uninit (vs 30-day module default); km:resource_prefix tag added to sandbox-secrets-key/terragrunt.hcl for orphan-recovery predicate
- [Phase 89]: fetchAndUploadSops uses exec.Command(curl,...) directly (not bash -c) so PATH shims work in tests
- [Phase 89]: ensureSecretsGitignore uses line-anchored matching (TrimSpace+map) not strings.Contains to prevent false hits on partial-match gitignore lines
- [Phase 89]: klanker-terraform literal used directly in fetchAndUploadSops per project-wide convention (19 occurrences in init.go); no cfg.GetAWSProfile() method exists
- [Phase 89]: Template version bump (ec2spot v1.1.0 → v1.2.0) lives in infra/templates/sandbox/terragrunt.hcl (copied at CreateSandboxDir), not Go source; compiler_secrets_test.go tests file directly
- [Phase 89]: deleteSopsBundleNonFatal is intentionally void (no return value) — SOPS bundle cleanup must never block destroy; S3 lifecycle 7-day rule is belt-and-suspenders
- [Phase 89]: KMSAliasLister reused from bootstrap.go (same package) — not redeclared in doctor.go
- [Phase 89]: checkSharedSecretsKey: nil-client returns CheckSkipped; missing-own takes precedence over orphan list
- [Phase 89]: SOPS-08-IAM-OPERATOR verified no-op: line 484 has exact kms:* broad grant in km-operator-policy; no code change needed
- [Phase 72]: Wave 0 TDD stubs use pure t.Skip with no non-existent symbol references so go vet passes before production code lands
- [Phase 72]: Manifest template scope list (13 scopes) adds files:read (Phase 75 inbound) and users:read.email (Phase 72) vs reference manifest; golden fixture committed for byte-exact assertion in Wave 1
- [Phase 72-01]: users_not_found maps to (false, nil) not an error — orchestrator branches on boolean, not error inspection
- [Phase 72-01]: already_in_channel swallowed to nil in InviteUserToChannel (idempotent) — matches JoinChannel contract; sentinel deferred to Plan 72-04
- [Phase 72-01]: Email lowercased + trimmed before users.lookupByEmail dispatch per Pitfall 6 in 72-RESEARCH.md
- [Phase 72-02]: emailLooksValid: new permissive regex helper (^[^@\s]+@[^@\s]+\.[^@\s]+$); no prior email validator in pkg/profile; Slack API is the authoritative gate
- [Phase 72-02]: types.go and schema.json updated atomically in Task 1 single commit; closes Pitfall 7 drift risk for notifySlackInviteEmails/useSlackConnect
- [Phase 72-02]: UseSlackConnect *bool: no validator rule; nil=default-true resolved at call time in Plan 72-07 km create loop (AutoConnect = cli.UseSlackConnect == nil || *cli.UseSlackConnect)
- [Phase 72]: Export SlackManifestData for clean test injection without a wrapper helper
- [Phase 72]: Print manifest banner to stderr so stdout remains pure JSON and pipeable
- [Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator]: Option 1 sentinel-error chosen for AlreadyMember detection: ErrAlreadyInChannel + InviteUserToChannelStrict preserves existing idempotent InviteUserToChannel contract
- [Phase 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator]: Interactive=true takes precedence over AutoConnect on lookup miss; AutoConnect only governs non-interactive path
- [Phase 72]: Operator invite AutoConnect=true unconditional — not gated by useSlackConnect; native→conversations.invite, external→Connect; fixes corporate workspace case
- [Phase 72]: channelName (sb-{id}) used in SkippedExternal warning hint rather than opaque Slack ID for operator-usable km slack invite command
- [Phase 72-05]: Used ExitCodeError for SkippedExternal exit code 2; extended SlackAPI interface with LookupUserByEmail + InviteUserToChannelStrict (option a); ConnectFallbackPrompter uses direct bufio.Scanner (SlackPrompter has no Confirm method)
- [Phase 72-08]: checkSlackUsersReadEmailScope placed in doctor_slack_transcript.go alongside checkSlackFilesWriteScope; tests in package cmd (not cmd_test); remediation in CheckResult.Remediation field
- [Phase 72-06]: Interactive=false/AutoConnect=false in RunSlackInit invite: external emails warn to use km slack invite --external
- [Phase 72-06]: users:read.email scope warning is standalone inline check (not added to VerifyEventsAPIScopes) to preserve existing scope tests
- [Phase 72-06]: isSlackProWorkspaceError removed from RunSlackInit call site; orchestrator wrapConnectError owns Pro-tier hint
- [Phase 72]: Phase 72 docs: km slack manifest/invite/notifySlackInviteEmails/useSlackConnect documented in slack-notifications.md; UAT runbook written; awaiting operator live sign-off
- [Phase 72-09]: Operator UAT PASSED 2026-05-30 by KPH — 7/8 rows green; B6 (scope-drift) deferred (covered by TestDoctor_SlackUsersReadEmailScope_Pass + _Warn unit tests); Phase 72 COMPLETE
- [Phase 72-09]: Three production bugs caught during live UAT: ec13e5b (auth.test decode shape), 6cf1deb (nil-ptr in RunSlackInvite lazy-init), 2653bc3 (form-encoded vs JSON for users.lookupByEmail)
- [Phase 72-09]: Reinstall-ejects-bot documented as known Slack platform behavior in docs/slack-notifications.md § Phase 72; operator must re-invite bot or km slack init --force after manifest reinstall
- [Phase 91]: Extended events_handler_test.go in place (single file) rather than creating a sibling to keep all bridge fakes co-located
- [Phase 91]: Wave 0 stub pattern: t.Skip with TODO Plan 91-XX message locks validation contract before implementation lands
- [Phase 91]: Tri-state *bool (nil/&true/&false) for NotifySlackInboundMentionOnly matches UseSlackConnect/SlackArchiveOnDestroy precedent; no default in JSON Schema, Go compiler resolver handles nil
- [Phase 91]: KM_SLACK_MENTION_ONLY emits 'true'/'false' string (not 0/1) to match bridge expectation; gated on NotifySlackEnabled==&true for back-compat
- [Phase 91]: AuthTestWithUserID uses callJSONRaw companion to avoid touching polymorphic SlackAPIResponse decode; bot-user-id SSM Put is non-fatal WARN in both init and rotate-token flows
- [Phase 91]: WireMentionOnly exported helper (not inlined) allows direct unit testing without Lambda cold-start AWS client init
- [Phase 91]: CachedBotUserIDFetcher.PrimeCache no-ops on empty uid to prevent blank cache entries causing lookup-loop confusion
- [Phase 91]: Duplicated resolveMentionOnly logic into anyProfileMentionOnly in doctor.go (not exported from pkg/compiler) to keep compiler package sealed
- [Phase 91]: anyProfileMentionOnly gates checkSlackBotUserIDCached registration so SKIPPED is returned when no local profile activates mention-only
- [Phase 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot]: Phase 91 docs follow Phase 72 structural template: per-mode table + field ref + examples + env vars + doctor + rollout + troubleshooting
- [Phase 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot]: OPERATOR-GUIDE.md gained a new ## Slack notifications top-level section (was absent); mention-only subsection placed before SOPS section
- [Phase 92]: Phase 92 Wave 0: byte-identity goldens (learn.v2 userdata + IAM HCL) captured from pre-change main; Wave 1 IAM rename must keep both GREEN
- [Phase 92]: RED stubs gated by per-wave build tags (phase92_wave2/4/5) so default go test stays green while each wave has a compile-failing target
- [Phase 92]: Phase 92 Wave 1: spec.identity → spec.iam, dead spec.agent/sessionPolicy removed, iam.allowedSecretPaths schema drift closed, apiVersion bumped v1alpha1 → v1alpha2 (STRICT)
- [Phase 92]: 92-02: spec.notification block replaces 14 cli.notify* fields; typed mergeNotificationSpec fixes pointer-merge inheritance bug (VC-7)
- [Phase 92]: 92-02: kept cli.notifySlackInboundReactAlways (15th notify field) on CLISpec — no target home in NotificationSlackInboundSpec; Wave 3 to resolve
- [Phase 92]: Re-homed notifySlackInboundReactAlways off CLISpec into notification.slack.inbound.reactAlways (Wave-2 deferred item closed); CLISpec now exactly NoBedrock/Agent/ClaudeArgs/CodexArgs
- [Phase 92]: NotifyEnv emission outer gate kept at Spec.CLI != nil (KM_AGENT still reads cli.Agent / Wave 4) so learn.v2 userdata stays byte-identical (VC-3)
- [Phase 92]: 92-04: agent.claude.permissions is the only untyped passthrough (map[string]any / additionalProperties:true) per CONTEXT.md locked decision; everything else typed aggressively
- [Phase 92]: 92-04: KM_AGENT keeps its Spec.CLI!=nil emission gate but sources value from agentDefault(p)=spec.agent.default; VC-3 byte-identity holds because learn.v2 carries both cli: and agent: blocks
- [Phase 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating]: Claude settings.json synthesizer emits canonical permissions.allow/deny (Wave 0 Option B), not legacy autoApprove/disallowedTools; passthrough merges into permissions object with typed allow/deny winning on collision.
- [Phase 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating]: VC-3 byte-identity reconciled: strict byte-identity for userdata outside the Claude settings.json blob + proven semantic equivalence (same tool set/trustedDirectories/hooks) for the blob, since canonical permissions.allow intentionally replaces legacy autoApprove. Baseline golden NOT regenerated.
- [Phase 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating]: Codex config.toml is synthesized (synthesizeCodexConfig) byte-identical to the Phase 70 heredoc and kept in its early userdata slot, preserving codex.yaml initCommands override and the byte-identity contract.
- [Phase 93]: Wave 0 stubs use t.Skip as first/only statement so packages compile before RuntimeDesktopSpec exists (93-01)
- [Phase 93]: desktop_test.go bodies are skip-only in Wave 0 to avoid duplicate mock declarations with vscode_test.go (same package cmd)
- [Phase 93-km-desktop]: RuntimeDesktopSpec is opt-in (default false) — opposite of IsVSCodeEnabled default-on; KasmVNC is heavy install
- [Phase 93]: Copied raw AMI ID regex locally into validate.go to avoid pkg/profile→pkg/compiler import cycle
- [Phase 93]: Empty mode defaults to kiosk (valid); empty browsers with kiosk is ERROR; empty browsers with full is OK
- [Phase 93]: Pre-parse DesktopGeometryWidth/Height in generateUserData to avoid template FuncMap; DesktopBrowser0Binary computed field maps chrome→google-chrome-stable; system service (User=sandbox) for kasmvnc.service mirrors km-queue.service pattern
- [Phase 93]: GenerateDesktopCredential exported (not unexported) because create_test.go from 93-04 uses cmd.GenerateDesktopCredential from external test package
- [Phase 93]: desktopStatusScript uses systemctl is-active kasmvnc + test -f ~/.kasmpasswd; parseDesktopStatus returns three distinct error messages per failure mode
- [Phase 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward]: Export GenerateDesktopCredential for testability from package cmd_test; randomPassword extracted as named helper with error return
- [Phase 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward]: desktop-creds.txt S3 upload in runCreateRemote mirrors vscode-pubkey.txt; Lambda subprocess uses KM_DESKTOP_KASM_USER/PASS env vars instead of re-generating
- [Phase 93-06]: profiles/desktop.yaml uses ubuntu-24.04 AMI, kiosk mode, firefox browser; schema required sourceAccess + allowedHosts added
- [Phase 93-06]: Plugin version bumped 0.3.0→0.4.0 in lockstep across plugin.json + marketplace.json
- [Phase 94-01]: Guardrail flags (SetLogRetention, SetS3Lifecycle) excluded from --with-deletes fan-out per design — explicit opt-in required
- [Phase 94-01]: Three new narrow interfaces (CWLogsCleanupAPI, DDBScanDeleteAPI, S3LifecycleAPI) follow existing SSMDeleterAPI/S3CleanupAPI narrow-interface convention
- [Phase 94-02]: logGroupFilterEntry named as package-level type to avoid Go anonymous-struct return-type mismatch; applyLogRetention fetches management groups independently to avoid mixing with sandbox-ID extraction
- [Phase 94]: BUDGET#ai# rows preserved unconditionally via HasPrefix guard in scanBudgetsTable (AI spend history)
- [Phase 94]: sandboxDeletableStatuses map restricts sandboxes purge to failed/nocap statuses only
- [Phase 94]: DDB reserved word 'status' handled via ExpressionAttributeNames in sandboxes ProjectionExpression
- [Phase 94]: smithy.APIError.ErrorCode() pattern for NoSuchLifecycleConfiguration detection (no typed error struct in sdk v1.97.1)
- [Phase 94]: LifecycleRuleFilter.Prefix struct field (not union member) for lifecycle rule filter; deprecated rule.Prefix also checked for coverage scan
- [Phase 94-05]: create-handler IAM ARNs moved in lockstep with audit path (/km/sandboxes/* → /{prefix}/sandboxes/*) — without this the Lambda loses log-write permission
- [Phase 94-05]: SCP v1.0.0 ELSE branch: no resource_prefix var; module unused (live uses v2.0.0 wildcard patterns); added inline comment, no logic change
- [Phase 95-slack-federated-bridge-relay-one-app-many-prefixes]: Use []string + GetStringSlice for PeerBridges (not *bool tri-state); nil == federation off; merge-list entry is CRITICAL to prevent silent viper drop
- [Phase 95]: Broadcast synchronous (WaitGroup.Wait) to prevent Lambda freeze from dropping in-flight relay goroutines
- [Phase 95]: X-KM-Relayed:1 single-header loop guard — relayed miss is TERMINAL, never re-relayed; loops structurally impossible
- [Phase 95]: nil Relayer == federation off == today (byte-identical nil-invariant protected by explicit test)
- [Phase 95-03]: checkSlackPeerBridges is a pure function accepting raw strings; ownBridgeURL resolved lazily in doctor.go wiring closure for testability
- [Phase 95-03]: SLACK-FED-E2E is a manual UAT checkpoint: cannot be automated (needs real Slack App + two live km installs)
- [Phase 96]: DefaultRouter follows *bool tri-state (nil=absent=dormant) mirroring MentionOnly/ReactAlways; dynamodb:Scan added to existing sandboxes-pause-hint IAM policy (not new resource)
- [Phase 96-02]: Rollout-safety LOCKED: any legacy 'ok', HTTP error, or timeout in Broadcast maps to Claimed:true — mixed-version fleets never produce false orphan replies
- [Phase 96-02]: peerRelayResponse as package-private struct shared between relayer.go and events_handler.go (same package, no export needed)
- [Phase 96-02]: Relayed-owned response gated at final return via req.Headers check — clean separation from non-relayed front-door path without threading isRelayed through steps 5-10
- [Phase 96-slack-default-router-orphan-channel-mention-reply]: RouterCooldownStore wraps DynamoNonceStore.Reserve with router-cooldown: prefix — reuses nonces table, no new infra
- [Phase 96-slack-default-router-orphan-channel-mention-reply]: DefaultRouter is plain bool (false zero = dormant, not *bool) for structural Phase 95 byte-identity guarantee
- [Phase 97-02]: Added checks→checks:write verb to CompilePermissions to complete github-inbound write set without bypassing verb abstraction
- [Phase 97-02]: GitHubInboundQueueName owned by plan 02 (not plan 03) to eliminate intra-wave compile dependency
- [Phase 97-02]: drainGithubEnvelope is best-effort: SQS error logs warn but does not fail the create — operator can re-mention
- [Phase 97]: Plan 03: GitHubInboundQueueName added first (plan 02 hadn't landed in Wave-1 concurrent execution); github-review sidecar images must be non-empty per schema minLength constraint
- [Phase 97-01]: JSON-encode github.repos as KM_GITHUB_REPOS (single env var) vs numbered keys — Lambda-parseable, self-describing, no bespoke decode logic
- [Phase 97-01]: Add json tags to GithubRepoEntry alongside mapstructure+yaml so json.Marshal produces snake_case keys matching yaml surface
- [Phase 97-01]: Replace km github shortcut with km github init/manifest/status command tree; km configure github --setup still works for App credential setup
- [Phase 97]: Resolve() uses exact-before-glob two-pass: pass1=exact, pass2=glob; ensures order-independence for exact matches
- [Phase 97]: 200-on-internal-error invariant in Handle(); GitHub redelivers 5xx with NEW delivery GUID bypassing dedup
- [Phase 97]: KM_GITHUB_REPOS as JSON env var (list-of-objects): {repos:[...], default_profile} serialized to single string
- [Phase 97]: Separate binary cmd/km-github mirrors cmd/km-slack pattern; deployed via conditional S3 download in userdata
- [Phase 97]: GitHub context preamble includes worktree-per-PR guidance for concurrent PR review in long-lived sandboxes
- [Phase 97]: APPROVE needs no body; COMMENT/REQUEST_CHANGES require body — validated before HTTP call
- [Phase 97]: GitHub bridge doctor checks are WARN-level (not ERROR) — GitHub is opt-in; silent-skip when github.repos empty + SSM probe absent
- [Phase 97]: checkGitHubReposResolvable is a pure config function (no AWS calls) for fast overlap + missing-profile detection
- [Phase 97-github-comment-trigger-mvp]: GitHub bridge reuses dynamodb-slack-nonces (shared nonce table) with github-delivery: key namespace — no new DynamoDB table
- [Phase 97-github-comment-trigger-mvp]: lambda-github-bridge placed after lambda-slack-bridge in regionalModules() — all bridge Lambdas contiguous before ses
- [Phase 98-github-bridge-expansion]: phase98_wave0 build tag isolates all RED test scaffolding from normal builds — 98-01 through 98-04 remove tags as features ship
- [Phase 98-github-bridge-expansion]: DynamoDB github-threads key schema: hash=repo(S), range=number(N) — number is type N not S, differs from Slack threads
- [Phase 98-github-bridge-expansion]: TestRegionalModulesIncludesGitHubThreads runs in normal suite untagged to continuously gate missing regionalModules() entry (prevents Phase 97 silent non-deploy footgun)
- [Phase 98-github-bridge-expansion]: Use appcfg.GithubRepoEntry (not bridge.RepoEntry) in DetectGitHubAliasIssues — consistent with checkGitHubReposResolvable; avoids importing bridge into doctor.go
- [Phase 98-github-bridge-expansion]: Intentional shared alias (multiple entries same explicit alias:) produces no WARN — supported GH-X-SHARED feature; only implicit-vs-explicit collision warns
- [Phase 98-01]: checkRunOutput.Title uses check name as title (agent-friendly default, no separate flag)
- [Phase 98-01]: runPRCreateWith receives stdout io.Writer for testability — html_url printed to stdout so agent can read new PR URL
- [Phase 98-01]: Byte-identity golden re-captured (not test patched) for preamble expansion — intentional permanent change
- [Phase 98-github-bridge-expansion]: DynamoGitHubThreadClient: separate interface (superset of DynamoQueryPutter + UpdateItem) to avoid widening existing interface and breaking existing fakes
- [Phase 98-github-bridge-expansion]: IAM policy for km-github-threads gated on github_threads_table_arn != '' for backward compat with pre-98-00 installs
- [Phase 98-github-bridge-expansion]: TestHandle_AutoResume stays behind phase98_wave3 build tag until 98-04 implements SandboxResumer + ResolveByAliasWithStatus
- [Phase 98-04]: EC2 IAM scopes StartInstances to km:managed=true tag; SandboxAliasResolverWithStatus type-assertion preserves Phase 97 backward compat; profileSlug/generateGitHubSandboxID local to bridge (no import cycle)
- [Phase 98-github-bridge-expansion]: Export EnvReqs from RegionalModule to enable direct assert of module env requirements in tests
- [Phase 98-github-bridge-expansion]: 5-sub-test deploy-surface test encodes Phase 97/98 deploy footguns as in-process file-level assertions (no live AWS)
- [Phase 98-github-bridge-expansion]: SetStatusRunning uses UpdateItem only (not PutItem) to avoid SandboxMetadata lossy round-trip; DDBSandboxesUpdateItem IAM statement grants only UpdateItem on km-sandboxes
- [Phase 98-github-bridge-expansion]: StatusWriter is non-fatal in resume branch: DDB error logs Warn and enqueue always continues so the prompt is never lost
- [Phase 99]: No new merge-list entry for github.commands: single 'github' entry covers whole block via UnmarshalKey; documented in GithubCommandEntry godoc to prevent future regression
- [Phase 99]: GithubCommandEntry is map[string]GithubCommandEntry (keyed by verb) for O(1) dispatch lookup and clean YAML syntax
- [Phase 99]: Command names are case-SENSITIVE (YAML key = exact match); /help intercepted before defined-command lookup; CommandEntry is bridge-local (no internal/app/config import); RunCommandPass is the IO-free seam Plan 04 wires into Handle()
- [Phase 99]: KM_GITHUB_DEFAULT_COMMAND env for install-wide default: bridge reads os.Getenv at cold start; Plan 03/05 writes this to Lambda env block at km init [SUPERSEDED by SC3a gap closure 2026-06-07: nothing ever wrote KM_GITHUB_DEFAULT_COMMAND so WebhookHandler.DefaultCommand was always "" at runtime; fix folds default_command into the SSM CommandSet envelope — {"commands":{...},"default_command":"..."} — so both travel over the single SSM param (D8 single source of truth); KM_GITHUB_DEFAULT_COMMAND env read removed from main.go; committed 95978591]
- [Phase 99]: Dormancy strict else branch: structural if/else ensures byte-identical Phase 98 path when Commands empty AND DefaultCommand empty
- [Phase 99-03]: Open-Q-1 resolved: Config.ConfigFilePath populated from v2.ConfigFileUsed() in Load(); callers use filepath.Dir(cfg.ConfigFilePath) for @file base dir
- [Phase 99-03]: PublishGitHubCommandsToSSM exported directly (not unexported+wrapper) for clean SSMReadWriteAPI injection in cmd_test
- [Phase 99-03]: SSM commands drift WARN: yaml always wins (no operator 'export' path for SSM); WARN is informational only unlike KM_GITHUB_REPOS env-wins pattern
- [Phase 99]: DoctorConfigProvider interface extended with GetGithubCommands/GetGithubDefaultCommand/GetConfigFilePath; all test stubs updated with zero-value returns
- [Phase 99]: printGitHubCommandsStatus reads live SSM param (not cfg) to show what Lambda actually reads at runtime
- [Phase 99]: Deploy surface: make build-lambdas + km init --dry-run=false ONLY; no sidecars, no sandbox recreate — cross-checked against Plans 03/04, no discrepancies
- [Phase 99.1]: Shared inbound DLQ must be FIFO (FifoQueue=true) — a FIFO source queue cannot redrive to a non-FIFO DLQ
- [Phase 99.1]: RedrivePolicy injected only when dlqARN non-empty (maxReceiveCount=3); empty keeps the inbound Attributes map byte-identical (dormancy invariant)
- [Phase 99.1]: DLQ ARN derived (not API-fetched) at km create call sites from cfg.ApplicationAccountID + region + {GitHub,Slack}InboundDLQName; empty when unresolvable (dormancy)
- [Phase 99.1]: Shared inbound FIFO DLQs (sqs-inbound-dlq module) created once per install at km init, idempotent via TF state
- [Phase 99.1]: No new IAM grant: existing {prefix}-{github,slack}-inbound-*.fifo wildcards already cover -dlq.fifo (RESEARCH Pitfall 6)
- [Phase 99.1-harden-github-slack-inbound-pollers-against-fifo-poison-message-wedge-via-shared-per-install-dlq-redrivepolicy]: km doctor inbound DLQ-depth check (SKIP dormant / OK empty / WARN poison-present) reuses the Slack inbound SQS client; deploy-surface verified (reachability triple, IAM-by-wildcard, no create-handler change, recreate migration)
- [Phase 100]: 100-01: github.peer_bridges decodes via existing UnmarshalKey("github")+"github" merge entry — NO new merge-list entry (proven by TestLoadGithubPeerBridges_Set)
- [Phase 100]: 100-01: lambda-github-bridge v1.1.0 edited in place (additive, default empty) instead of bumping to v1.2.0 — live source line untouched
- [Phase 100]: GitHub PeerRelayer is fire-and-forget (plain-error Broadcast); Phase-96 claim machinery dropped (orphan-repo reply deferred to Phase 101)
- [Phase 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges]: Resolve() reorder is unconditional — doubles as the 700-repo scale fix; byte-identity holds because a github-threads row only exists for owned repos
- [Phase 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges]: Set WebhookHandler.Relayer only when peers configured to avoid typed-nil-into-interface panic in Broadcast
- [Phase 100]: Phase 100 federated relay: km doctor checkGitHubPeerBridges (malformed/self-loop WARN, empty SKIP) mirrors Slack; deterministic checks only (no undeterminable front-door-empty WARN); deploy = make build-lambdas + km init --dry-run=false (NOT --sidecars); v1.1.0 in-place; dormant absent github.peer_bridges
- [Phase 101]: No new merge-list entry for github.default_router — decoded by existing UnmarshalKey("github") call, proven by TestLoadGithubDefaultRouter_Set
- [Phase 101]: In-place v1.1.0 TF module edit with additive default="false" github_default_router var — no version bump (Phase 100 precedent)
- [Phase 101]: PeerClaimResult has no Channels field — GitHub orphan reply has no repo list unlike Slack Phase-96
- [Phase 101]: Rollout safety: legacy 'ok'/non-2xx/timeout all tally Claimed:true; only explicit {claimed:false} counts as unclaimed
- [Phase 101]: jsonClaim helper returns plain JSON body — WebhookResponse has no Headers field; GitHub ignores response headers so Content-Type omission is safe
- [Phase 101]: Reuse DynamoGitHubNonceStore as OrphanCooldown store with gh-router-cooldown: key prefix — no new table, no new interface
- [Phase 101]: Phase 101 doc sections mirror Slack-96 analog in docs/github-bridge.md + OPERATOR-GUIDE.md + CLAUDE.md; 101-UAT.md has Tests A-D for GH-ORPHAN-E2E
- [Phase 102]: InvalidateStaleSession keeps agent_type (does not REMOVE): stale rows retain agent binding across sandbox recreations
- [Phase 102]: agent_type schema-on-write in km-github-threads; old rows return empty string (profile default); no Terraform change
- [Phase 102]: AgentVerb cleared to empty when AgentVerbConflict=true; two-verb conflict short-circuits Handle() before dispatch
- [Phase 102]: ExtractArgs delegates to ExtractArgsWithAgent('') — single stripping implementation for mention+command+agent-verb
- [Phase 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog]: Codex-missing error uses single quotes (not backticks) — Go raw string literals cannot contain backtick characters
- [Phase 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog]: Golden file re-captured to include Phase 102 poller changes — standard per-phase pattern
- [Phase 102]: buildHelpReply/RunCommandPass extended with currentAgentType param; threadCurrentAgentType captured at existing LookupSandbox call site
- [Phase 102]: Reserved-shadow loop over slice [help,claude,codex] replaces single-help check in doctor.go
- [Phase 102]: Step e (/codex on Claude-only sandbox) covered by code+unit (D6 guard userdata.go:2276-2281) with operator-approved skip of 2nd live sandbox
- [Phase 102]: GitHub Codex path never passes a resume arg (pre-existing) — candidate follow-up: codex exec resume for GitHub threads
- [Phase 102]: GH-AGENT-E2E requirement closed: Phase 102 proven deployable via make build-lambdas + km init --dry-run=false (NOT --sidecars), additive-only surface confirmed
- [Phase 103]: Phase 103 Plan 01: took pre-authorized synthetic-fallback for the live HackerOne webhook capture; real Sandbox-program capture + envelope-wrapper confirmation deferred to Plan 10 (Wave 6) E2E
- [Phase 103]: Phase 103 Plan 01: OQ1 program-handle path resolved (data.report.relationships.program.data.attributes.handle); OQ2 state endpoint left LOW-confidence/deferred (km-h1 state may be fast-follow)
- [Phase 103]: 103-02: h1: config merge-list-wired (no silent drop); Resolve(handle) multi-target fanout; CommandEntry single-owner in commands.go; H1ThreadStore keyed reportID+target; H1Commenter internal-by-default
- [Phase 103-hackerone-comment-trigger-bridge]: 103-03: ported VerifyH1Signature byte-identical from GitHub (X-H1-Signature swap); HMAC over base64-DECODED body
- [Phase 103-hackerone-comment-trigger-bridge]: 103-03: payload parser wrapper-tolerant (data.report vs JSON:API double-data); missing handle = hard resolve-miss, never panic
- [Phase 103-hackerone-comment-trigger-bridge]: 103-03: /reply_to_researcher is parse-only intent + always stripped; internal-by-default gate deferred to Plan 04
- [Phase 103]: km h1 init/status forked from github.go, dropping manifest (HackerOne has no App-install model); webhook-secret + api-token SecureString, api-username String under /{prefix}config/h1/*
- [Phase 103]: H1 reply gate enforced at the handler: ComputeReplyToResearcher requires command AND allowlist (deny-by-default); only targets[0] may post externally (never N external replies under fanout)
- [Phase 103]: Multi-target fanout = per-target distinct dedupID (h1-{report_id}-{alias}) + per-(report_id,target) thread row; H1ThreadStore keyed (report_id,target) UpdateItem-shaped
- [Phase 103]: Plan 07: km-h1-bridge Lambda forks the GitHub bridge entry but DROPS all GitHub App machinery (App creds, JWT reactor, relayer, orphan router) — HackerOne uses HTTP Basic Auth + per-program webhook URLs, so no App-install model or federation relay is needed.
- [Phase 103]: H1 deploy wiring: lambda-h1-bridge + dynamodb-h1-threads in regionalModules (threads<bridge<ses); km-h1-bridge in lambdaBuilds/Makefile, km-h1 in sidecarBuilds; KM_H1_* env + merged CommandSet to SSM; h1-inbound FIFO 1800s VT + shared DLQ
- [Phase 103]: Plan 09: km-h1-inbound-poller drains the per-sandbox h1-inbound FIFO; (report_id,target) session resume via UpdateItem with target=KM_SANDBOX_ALIAS; INTERNAL-by-default reply preamble (--reply-to-researcher taught only when envelope flag set); notification.h1.inbound schema field gated by the Wave-0 dormancy golden; profiles/h1-triage.yaml uses sourceAccess:none + api.hackerone.com egress
- [Phase 103]: Phase 103 deploy surface locked by automated guards (TestLambdaBuildsIncludesH1Bridge + TestRegionalModulesH1BridgeOrdering + config merge-list nested-target guard); H1-DEPLOY-WIRING green. The safety-critical reply-VISIBILITY (internal-by-default, allowlisted-external primary-only, non-allowlisted downgrade) remains the operator's live UAT in 103-UAT.md against the HackerOne Sandbox org prodsec_klanker_maker_test_pro_demo — Task 3 awaiting operator. — Deploy-surface guards mean a future refactor cannot silently re-open a Phase-97 gap; live reply-visibility is unobservable outside the HackerOne UI so it is operator UAT, not automated.
- [Phase 104]: SlackMaxScanPages=0 default: scan disabled; name_taken with no stored ID fails fast with km slack adopt guidance
- [Phase 104]: SlackChannelStore interface nil-tolerant; production passes nil until plan 104-03 wires DDB store
- [Phase 104]: Transient conversations.info errors bounded-retried then optimistically trusted; never trigger workspace scan
- [Phase 104]: No TTL on km-slack-channels table: alias→channel_id mapping must persist across sandbox destroy/recreate; stale rows self-heal via channel_not_found recreate path
- [Phase 104]: km-slack-channels table name derives from site.label (not hardcoded prefix): ensures multi-install isolation without collision
- [Phase 104]: No dependency block in create-handler live unit for slack-channels table: IAM grant needs only table name string, static pattern mirrors slack-threads
- [Phase 104]: No TF env var for slack-channels table name: cfg.GetSlackChannelsTableName() derived at runtime in create.go, matching km-slack-threads pattern
- [Phase 104]: slack_channels_table_name added to viper merge-list in config.go: prevents silent ignore of km-config.yaml key (project_config_key_merge_list footgun)
- [Phase 104]: CORRECTION #4: km-slack-channels not added to checkOrphanedDDBRows; alias rows survive destroy by design
- [Phase 104]: DoctorConfigProvider interface extended with GetSlackChannelsTableName() to enable doctor check without type-assertion
- [Phase 104]: Deploy sequence: make build (binary) BEFORE km init for new DDB module; full km init --dry-run=false (not --sidecars) for table+IAM changes
- [Phase 104]: Provider-lock-drift vector: stray infra/modules/**/.terraform.lock.hcl files from bare terraform validate runs conflict with root.hcl provider pin; remove before apply
- [Phase 104]: Archived-channel fast-fail is correct bounded behavior for reuse-after-destroy with archiveOnDestroy profile; does not trigger workspace scan (cache_hit + conversations.info classifier)
- [Phase 105]: Used package cmd_test for init_scoped_test.go matching existing test file convention; 10 stub tests compile and skip at Wave 0 without referencing unimplemented production symbols
- [Phase 105]: ResolveScopedModule exported (not unexported) so cmd_test external package can call it directly; mirrors RegionalModules export pattern
- [Phase 105]: runInitScopedFunc stub var uses package-level-var-seam pattern (like RunInitPlanFunc) so Plan 03 can replace body without touching dispatch code
- [Phase 105]: RunInitScopedWithRunner exported as plain function; scopedGateFunc package var is Plan 04 injection point (no-op in Plan 03); runInitScopedFunc rebound to real runInitScoped
- [Phase 105]: scopedGateFunc uses planModule+planreport.Evaluate (not RunInitPlanFunc) for single-module pre-apply destroy-class gate; ses preflight+Reconfigure wired inside gate before dryRun branch
- [Phase 105-05]: skills/init/SKILL.md updated materially — plugin.json + marketplace.json version bump required at next release (project_plugin_version_gates_cache)
- [Phase 105-05]: Live UAT as behavior-neutral no-op apply proves zero drift without config change; km init --plan follow-up confirmed no residual drift
- [Phase 105-05]: Deferred pre-existing TestRunInitPlan_ModuleOrder failure (expects 17, regionalModules() has 22) — Phase 105 added zero module entries, confirmed not a Phase 105 regression
- [Phase 107]: Tests assert current production behavior; production code (execDockerShell) is authoritative — bash --login login shell and always-present -u sandbox/root user flag
- [Phase 107]: Replaced stale PLACEHOLDER_OPERATOR_KEY with PLACEHOLDER_SIDECAR_ROLE_ARN to maintain placeholder coverage intent; updated runCreateRemote signature with budget params verbatim from create.go:2074
- [Phase 107]: wantOrder slice is exact reverse of regionalModules(); two count consts annotated with regionalModules()==22 for future maintenance
- [Phase 107]: Re-keyed email SSM mock seeds from /sandbox/... to /km/sandbox/... to match SigningKeyPath/EncryptionKeyPath prefix-scoped convention
- [Phase 107-06]: TEST-21 locked: TestLoadEFSOutputs_NotExist asserts only err==nil; S3 fallback return value is unconstrained
- [Phase 107-06]: Dynamic future time in at-list fixture: now.Add(48h) eliminates hardcoded-date staleness permanently
- [Phase 107-reconcile-22-stale-internal-app-cmd-unit-tests]: State-bucket guard tests: inverted negative assertions replace legacy guard expectations — DynamoDB-primary means guard only fires on S3 fallback after ResourceNotFoundException
- [Phase 107-07]: preflightError wrapper struct (not sentinel var) preserves Unwrap() for substring test assertions
- [Phase 107-07]: NewShellCmdWithFetcher RunE discriminates pre-flight vs session-exit errors via errors.As; session-exit errors still swallowed to avoid spurious cobra output

### Roadmap Evolution

- Phase 10 added: SCP Sandbox Containment — org-level EC2 breakout prevention (SCP as account-level backstop for sandbox IAM containment)
- Phase 13 added: GitHub App Token Integration — scoped repo access for sandboxes (short-lived installation tokens via GitHub App, SSM storage, Lambda refresh every 45min)
- Phase 14 added: Sandbox Identity & Signed Email — Ed25519 key pairs for inter-sandbox trust (signed/encrypted email, public keys in DynamoDB, profile-controlled policies)
- Phase 15 added: km doctor — platform health check and bootstrap verification (validates config, AWS creds, bootstrap resources, per-region infra, active sandboxes)
- Phase 16 added: Documentation refresh — update operator guide, user manual, and all docs for Phases 6-15 features (budget, SCP, sidecars, GitHub App, identity, km doctor)
- Phase 17 added: Sandbox Email Mailbox & Access Control — aliases, allow-lists, self-mail, S3 reader (human-friendly sandbox names, profile-driven sender restrictions, mailbox reading library)
- Phase 18 added: Loose Ends — km init deploys all regional infra, km uninit teardown, bootstrap KMS, github-token graceful skip, state_bucket auto-config (discovered during live testing 2026-03-23)
- Phase 21 added: Bug fixes and mini-features — budget precision (4 decimal places), polish, small enhancements
- Phase 99.1 inserted after Phase 99: Harden github/slack inbound pollers against FIFO poison-message wedge via shared per-install DLQ + RedrivePolicy (URGENT — production-risk gap found live in Phase 99 UAT 2026-06-08; poison message head-of-line-blocks FIFO group, wedges poller, only purge recovers)

### Pending Todos

8 pending in `.planning/todos/pending/` (most recent: `2026-05-25-km-init-self-heal-on-provider-checksum-lock-errors.md` — operator-workstation plugin-cache lock drift periodically blocks `km init`; self-heal by retrying init with `-upgrade`).

### Blockers/Concerns

- [Phase 2]: ECS substrate introduces a second Terraform module path (ecs-cluster, ecs-task, ecs-service) — compiler branch logic for substrate selection needs careful design to avoid divergence
- [Phase 3]: On ECS, DNS and HTTP proxy sidecars run as containers in the task definition; iptables DNAT rules used for EC2 interception do not apply — ECS needs a different traffic interception approach (likely environment-variable proxy configuration or VPC endpoint routing)
- [Phase 3]: iptables DNAT interaction with IMDSv2 hop limit not fully resolved on EC2 — research recommends `/gsd:research-phase` before Phase 3 planning
- [Phase 3]: HTTPS proxy mode (SNI-only vs. full MITM) is a security trade-off that needs an explicit decision before Phase 3 implementation
- [Phase 4]: Filesystem policy enforcement mechanism (seccomp, Linux mount namespaces, OverlayFS) not decided — research recommends `/gsd:research-phase` before Phase 4 planning

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 1 | max_sandboxes config limit with operator email | 2026-03-29 | a185a79 | [1-max-sandboxes-config-limit-with-operator](./quick/1-max-sandboxes-config-limit-with-operator/) |
| 2 | upload km-config.yaml to S3 toolchain for create-handler Lambda | 2026-03-29 | c6adc96 | [2-upload-km-config-to-s3-toolchain-for-cre](./quick/2-upload-km-config-to-s3-toolchain-for-cre/) |
| 3 | Fix budget-enforcer-instance-id | 2026-03-29 | 9d884e1 | [3-fix-budget-enforcer-instance-id](./quick/3-fix-budget-enforcer-instance-id/) |
| 4 | Add cache token metering to HTTP proxy Anthropic API cost calculation | 2026-04-19 | b35f9d5 | [4-add-cache-token-metering-to-http-proxy-a](./quick/4-add-cache-token-metering-to-http-proxy-a/) |
| 5 | Plugin snapshot save/restore workflow for Claude plugins via rsync to S3 | 2026-04-19 | 66b303e | [5-plugin-snapshot-save-restore-workflow-fo](./quick/5-plugin-snapshot-save-restore-workflow-fo/) |
| 6 | Fix GitHub App installation-ID resolution for wildcard-only allowedRepos so learn.yaml clones work without prompting | 2026-05-01 | 99f49eb | [6-fix-github-app-installation-id-resolutio](./quick/6-fix-github-app-installation-id-resolutio/) |
| 7 | km doctor --dry-run=false cleans up stale Slack SQS queues and S3 transcript prefixes | 2026-05-04 | afa03a4 | [7-km-doctor-dry-run-false-cleans-up-stale-](./quick/7-km-doctor-dry-run-false-cleans-up-stale-/) |
| 8 | Implement spec: uptime + agent-auth visibility for km status and km list | 2026-06-07 | 5bf71288 | [8-implement-spec-uptime-agent-auth-visibil](./quick/8-implement-spec-uptime-agent-auth-visibil/) |

## Accumulated Context

### Roadmap Evolution
- Phase 22 added: Remote Sandbox Dispatch — km create/destroy/stop/extend --remote via Lambda
- Phase 23 added: Email-Driven Operations — operator inbox, email-to-create, safe phrase auth, EventBridge
- Phase-24 added: Credential Rotation — km roll creds for platform and sandbox secrets (since renumbered; no standalone 24-* dir)
- Phase 25 added: GitHub Source Access Restrictions — deep testing of repo allowlists, deny-by-default
- Phase 26 added + completed: Live Operations Hardening — ~60 commits across 2-day session
- Phase 27 added: Documentation Refresh (Phases 22-26)
- Phase 27 added: Claude Code OTEL Integration — inject built-in Claude Code telemetry env vars into sandboxes, extend OTel Collector sidecar with logs+metrics pipelines, full agent observability to S3
- Phase 28 added: GitHub repo-level MITM filtering in HTTP proxy — MITM GitHub hosts to inspect URL paths and enforce allowedRepos at the network layer, mirroring Bedrock/Anthropic pattern
- Phase 30 added: Sandbox Lifecycle Commands — km pause (EC2 hibernate), km lock/km unlock (safety lock preventing destroy/stop/budget changes)
- Phase 32 added: Profile-scoped rsync paths — move rsync path lists from global config into per-profile YAML with external file list references (e.g. `rsyncFileDetails: "cc-files.yaml"`) and shell wildcard support; remove global rsync_paths
- Phase 31 added: Observable learning-mode sandbox — permissive MITM proxy records all DNS/HTTP/GitHub/Claude traffic, generates minimal allowlist profile from observed behavior
- Phase 33 added: EC2 Storage & AMI Selection — profile-driven root volume sizing, optional additional EBS with auto-mount, hibernation for on-demand, loose AMI spec resolved per-region
- Phase 34 added: Agent Profiles — agent-orchestrator, goose, and codex sandbox profiles
- Phase 39 added: Migrate sandbox metadata from S3 JSON to DynamoDB — km list/status/lock/pause metadata reads/writes move to DynamoDB, artifacts stay in S3
- Phase 50 added: km agent non-interactive execution — fire prompts into sandboxes via SSM send-command, Claude runs with --json --dangerously-skip-permissions, output lands on disk, fetch results with km agent results subcommand, idle reset while running
- Phase 40 added: eBPF TC/cgroup enforcement — kernel-level DNS/HTTP/TLS-SNI allowlisting, toggleable `enforcement: ebpf` in profile, fixes root bypass, proxy sidecars kept for MITM inspection
- Phase 41 added: eBPF SSL uprobe observability — plaintext TLS capture via OpenSSL hooks, toggleable `inspection: uprobe`, replaces MITM proxy for GitHub filtering, Bedrock metering stays userspace
- Phase 42 added: eBPF gatekeeper mode — connect4 DNAT rewrite for selective L7 proxy
- Phase 44 added: km at/schedule — EventBridge Scheduler command for deferred and recurring sandbox operations
- Phase 45 added: km-send/km-recv sandbox scripts & km email send/read CLI — close Phase 14 gap, pure bash in-sandbox scripts + operator Go CLI for signed email with attachments
- Phase 46 added: AI email-to-command — Haiku interprets free-form operator emails, replies with confirmation template, operator replies "yes" to execute
- Phase 48 added: Profile override flags for km create — targeted budget flags and generic --set (renumbered from 47; Phase 47 taken by privileged execution mode)
- Phase 49 added: Prebaked AMI support — custom images with preinstalled toolchains for fast sandbox boot
- Phase 50 added: km agent non-interactive execution — fire prompts via SSM SendCommand, --no-bedrock, --auto-start, S3 fast path, km at agent run
- Phase 51 added: km agent tmux sessions — persistent tmux sessions for live attach, survive disconnects, --interactive mode, km agent attach
- Phase 53 added: Persistent local sandbox numbering — monotonic counter assigned at create time, stored locally, replaces ephemeral positional numbers in km list
- Phase 54 added: Multi-account GitHub App installations — support multiple GitHub users installing the same App, per-account SSM keys, auto-resolve installation from repo owner at create time
- Phase 55 added: Learn mode command capture — record shell commands typed by root and sandbox user during km shell --learn sessions, include in observed state and generated profile output
- Phase 56 added: Learn mode AMI snapshot — --ami flag on km shell --learn snapshots EC2 instance, writes AMI ID into generated profile, km ami list/delete for lifecycle, km doctor stale AMI detection
- Phase 59 added: Email sender allowlist — operator inbox allowlist in km-config.yaml enforced by email-create-handler Lambda, extend sandbox allowedSenders to support external email patterns (user@domain, *@domain)
- Phase 57 added: Email enhancement — km-send --no-sign for external recipients, km-recv RFC5322/multipart fixes, safe phrase validation on inbound Lambda, marketplace plugin email docs
- Phase 58 added: km agent run codex support — add --claude/--codex flags (mirror interactive), parameterize BuildAgentShellCommands by agent type, codex uses --dangerously-bypass-approvals-and-sandbox and --json, gate --no-bedrock to claude only, add spec.cli.codexArgs alongside claudeArgs, pass-through codex output (no normalization)
- Phase 59 plan 01: Email patterns in MatchesAllowList use path.Match with case-insensitive lowering; email patterns identified by @ in pattern string; continue after email branch prevents fallthrough to alias matching
- Phase 60 added: Budget compute accounting excludes paused/hibernated intervals — track pausedAt/resumedAt transitions, accumulate paused seconds in budget row, subtract from elapsed time in calculateComputeCost so hibernated EC2 stops accruing compute spend
- Phase 61 added: km shell Ctrl+C fix — switch interactive SSM sessions from AWS-StartInteractiveCommand to a parameterized Standard_Stream document with runAsDefaultUser=sandbox
- Phase 33.1 inserted after Phase 33: Raw AMI ID support — extend schema, compiler, and Terraform to accept ami-xxxxxxxx IDs alongside slugs (prereq for Phase 56 snapshot lifecycle) (URGENT)
- Phase 63.1 inserted after Phase 63: Slack notify hook gap closure — step 11d runtime injection visibility, km destroy archive auto-trigger, and bridge token rotation hardening (URGENT — Phase 63 UAT followups)
- Phase 67 added: Slack inbound: per-sandbox channel as bidirectional chat with km agent run dispatch
- Phase 62 added: Claude Code operator-notify hook — emit signed emails to operator (or override address) on Notification (permission) and Stop (idle) hook events; profile-driven (`spec.cli.notifyOnPermission/notifyOnIdle/notifyCooldownSeconds/notificationEmailAddress`) with `--notify-on-permission`/`--notify-on-idle` CLI overrides on `km shell`/`km agent run`. Hook installed at compile time; v1 notification-only with v2 closed-loop forward-compatible. Spec: `docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md`
- Phase 56.1 inserted after Phase 56: Bake-loop hardening — fix additionalVolume/AMI BDM collision, non-blocking audit hook, sidecar FIFO-retry + post-env-rewrite restart (URGENT — lessons from Phase 56 e2e: AMI'd sandbox failed to launch due to /dev/sdf BDM collision; once fixed, login shells deadlocked because audit hook FIFO write blocks when km-audit-log starts before /run/km/audit-pipe exists and never retries)
- Phase 56.2 inserted after Phase 56.1: Resume hardening — relocate cgroup scope creation and /run/km bootstrap from cloud-init user-data into a `km-bootstrap.service` systemd unit that runs on every boot, and harden `echo $$ > cgroup.procs` writes against bash redirect-error leaks (URGENT — lessons from Phase 56.1 km resume e2e: on a stop+resume of a baked sandbox, /sys/fs/cgroup is empty so the eBPF scope created during cloud-init vanishes and `bash: cgroup.procs: No such file or directory` leaks past `2>/dev/null` because bash opens the redirect target before applying stderr suppression — eBPF enforcement may be bypassed; /run is tmpfs so /run/km/learn-commands.log disappears on resume but cloud-init only ran once at first boot; fix: km-bootstrap.service oneshot Before=amazon-ssm-agent recreates the cgroup scope + /run/km files every boot, sourcing /etc/profile.d/km-identity.sh so SANDBOX_ID is correct on baked-AMI relaunch, and `{ echo $$ > path; } 2>/dev/null || true` wraps cgroup writes so redirect failures are suppressed)
- Phase 63 added: Slack-notify hook for Claude Code permission and idle events — extends Phase 62's email notify with Slack delivery via klankermaker.ai-owned Pro Slack workspace; bot token in SSM never leaves AWS; sandboxes call new `km-slack-bridge` Lambda with Ed25519-signed payloads (same signing model as `km-send`); operators invited via Slack Connect from their own workspace; hybrid channel mode (default `#km-notifications` shared, opt-in per-sandbox `#sb-{id}` via `notifySlackPerSandbox`); new `notifyEmailEnabled` field for symmetry with `notifySlackEnabled` enables Slack-only delivery while preserving Phase 62 backward compat. Spec: `docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md`
- Phase 64 added: km create reliability and doctor cleanup hardening — bundles operational hygiene gaps from Phase 63.1 with related create-flow reliability todos. CLEAN-1 fix pre-existing `TestUnlockCmd_RequiresStateBucket` failure (predates 63.1, last touched commit 22366b1), CLEAN-2 add `km doctor --auto-fix` to archive stale Slack channels and orphan resources, CLEAN-3 fix `km create` orphan-channel-on-failure (Slack provisioned before infra apply, persists when terraform fails), CLEAN-4 doctor checks sidecar systemd health on active sandboxes (from todo doctor-sidecar-health.md), CLEAN-5 spot capacity multi-AZ retry (from todo spot-multi-az.md)
- Phase 65 added: Four-account config model — split today's `accounts.management` (which conflates AWS Organizations management with the DNS parent-zone owner) into `accounts.organization` (SCP target, blank → skip SCP) and `accounts.dns_parent` (Route53 parent zone owner used by `ensureSandboxHostedZone`). Hard rename, no back-compat alias (pre-1.0). Updates `internal/app/config/config.go`, `bootstrap.go` SCP gate, `init.go` DNS lookup, `infra/live/site.hcl`, `infra/live/management/scp/terragrunt.hcl`, and adds `km doctor` warning when `accounts.organization` is blank ("SCP enforcement disabled — sandbox containment relies on IAM only"). Motivation: upcoming single-account install lacks org-management access; bootstrap must skip SCP cleanly while DNS still works. AWS profile names (`klanker-management` etc) are explicitly out of scope.
- Phase 66 added: Multi-instance support — introduce `resource_prefix` (default `"km"`) and `email_subdomain` (default `"sandboxes"`) in km-config.yaml so multiple km installs can coexist in one AWS account. Threads both knobs through DynamoDB tables (`km-budgets`, `km-sandboxes`, `km-identities`, `km-schedules`, `km-slack-bridge-nonces`), management Lambdas (`km-ttl-handler`, `km-create-handler`, `km-email-create-handler`, `km-slack-bridge`, `km-ecs-spot-handler`), Lambda IAM roles (~12 sub-policies per handler), EventBridge schedule group (`km-at`) and rule (`km-ecs-spot-interruption`), CloudWatch log group prefix (`/km/sandboxes/*`), SSM parameter prefixes (`/km/slack/*`, `/km/config/github/*`, `/km/config/remote-create/*`), TF state backend (`tf-km-state-{region}` + lock table), SES domain identity / Route53 zone (`sandboxes.{domain}`), and the ~25 inline `"sandboxes." + cfg.Domain` call sites (collapse to `Config.GetEmailDomain()`). Defaults preserve existing-install behavior; new installs override at `km init`. Migration of existing installs is explicitly out of scope. Org SCP (`km-sandbox-containment`) reuses the Phase 65 `accounts.organization == ""` skip path since SCPs are org-scoped and only one install per org can deploy. TF resource renames must keep logical names constant (parameterize `name` attribute only) to avoid destroy/create on stateful DynamoDB tables.
- Phase 68 added: Slack transcript streaming (Phase A) — per-turn assistant text + tool one-liner streaming via PostToolUse hook to per-sandbox Slack thread, plus final gzipped JSONL transcript upload via S3 → bridge → Slack files API at Stop. New profile field `notifySlackTranscriptEnabled` gated on `notifySlackEnabled + notifySlackPerSandbox`; CLI overrides `--transcript-stream/--no-transcript-stream` on `km agent run` and `km shell`. New bridge `ActionUpload`, Slack scope `files:write`, DDB table `km-slack-stream-messages` mapping `(channel_id, slack_ts) → (sandbox_id, session_id, transcript_offset)` to enable a future reaction-fork phase (Phase B, deferred). Auto-creates thread parent for operator-initiated runs (no `KM_SLACK_THREAD_TS`). Spec: `docs/superpowers/specs/2026-05-03-slack-transcript-streaming-design.md`. Plan 03 resolved a CONTEXT.md naming ambiguity by choosing `{prefix}-slack-stream-messages` (NOT `{prefix}-km-…`) to match the Phase 67 `{prefix}-slack-threads` convention; CONTEXT.md amended in Plan 12 Task 0.
- Phase 67.1 inserted after Phase 67: Slack inbound ACK reaction — bridge-side fire-and-forget `reactions.add` (👀) after successful SQS enqueue, mirrors existing PauseHinter goroutine pattern; new Slack scope `reactions:write`, scope checks added to `km slack init` and `km doctor`, configurable emoji via `KM_SLACK_ACK_EMOJI` env var (default `eyes`), bridge-only deploy (no sandbox redeploy) (URGENT — Phase 67 UX gap: users get no feedback that Slack message was received until agent boots)
- Phase 72 added: Slack corporate-workspace support with auto-detect invite and manifest generator — install KM Slack app in a corporate workspace (e.g., Corporate) where most invitees are native members, while still supporting external collaborators via Slack Connect. New unified invite orchestrator in `pkg/slack/invite.go` (`EnsureMemberByEmail` returning `InvitedDirect|InvitedConnect|SkippedExternal|AlreadyMember|Failed`) tries `users.lookupByEmail` → `conversations.invite` first, prompts before falling back to `conversations.inviteShared` (interactive only; fail-fast non-interactive). New low-level methods `LookupUserByEmail` + `InviteUserToChannel` alongside existing `InviteShared`. New ad-hoc command `km slack invite <email> [--channel <name|id>] [--external]`. New profile field `spec.cli.notifySlackInviteEmails: []string` for per-sandbox auto-invite at `km create` (fail-soft: skip+warn for Connect-needed addresses since `km create` may run scheduled). New standalone command `km slack manifest` renders an embedded JSON template with deployment-specific `request_url` (bridge Lambda URL from SSM) + resource_prefix-aware display name + full scope list; output to stdout for copy-paste into Slack admin "From manifest" UI. Manifest scope additions vs current production app: `users:read.email` (required for `users.lookupByEmail`). Existing `km slack init` invite path refactored to use the new orchestrator (no behavior change for existing PoC installs). No changes to bridge Lambda, sidecars, signing, Connect transport, existing channels/SSM keys.
- Phase 73 added: km vscode remote session via SSM — new `km vscode start | status` subcommand connects the operator's **local desktop VS Code** (via the Remote-SSH extension) to a sandbox over SSM port-forward. Per-sandbox ed25519 keypair auto-generated on operator's laptop at `km create` time (private key under `~/.km/keys/sb-<id>`, pubkey shipped via userdata to `/home/sandbox/.ssh/authorized_keys`). `km vscode start <sb>` opens foreground SSM port-forward (sandbox port 22 → local 2222), upserts managed Host block in `~/.ssh/config`, prints "F1 → Remote-SSH → km-sb-<id>" instruction. Userdata block (gated on `spec.cli.vscodeEnabled: true`, default true): `systemctl enable --now sshd`, write authorized_keys (0600 sandbox:sandbox), `restorecon -R -v /home/sandbox/.ssh` (AL2023 SELinux mandatory). `km destroy` cleans up ssh-config block + key files. Nothing related to VS Code is installed on the sandbox — Remote-SSH auto-deploys `vscode-server` on first connect. **Original v1 (browser-based `code serve-web`) was rejected after live POC validation in favor of Remote-SSH** because operators want their full local IDE experience (themes, keybindings, extensions) not a browser tab. POC also surfaced the AL2023 SELinux gotcha (`restorecon` mandatory) and the `IdentitiesOnly yes` requirement for ssh-config. Userdata template change ⇒ `make build && km init --sidecars`; existing sandboxes need `km destroy && km create` to pick up. No new IAM, DDB schema, SSM parameters, or Lambda. Spec: `docs/superpowers/specs/2026-05-06-km-vscode-design.md` (rewritten 2026-05-06 after POC).
- Phase 74 added: Slack mrkdwn rendering — robust markdown→Slack mrkdwn transformer for transcript streaming output (Phase 68). Today the streaming hook ships raw CommonMark to Slack, which mangles `**bold**` (renders literal asterisks like `***heading***`), drops `#` ATX headings entirely, and turns pipe-tables into ragged proportional-font garbage. Plan: tokenizer-based pipeline that splits input into `text`/`code-span`/`code-fence` segments and only transforms `text` (so code samples containing `**`, `|...|`, or `<html>` survive byte-for-byte), with HTML-escape for `<`/`>`/`&` in non-code segments to prevent Slack mention/link interpretation. Tier 1 (mrkdwn): collapse `**x**`→`*x*`, `# h`/`## h`/`### h`→`*h*`, `[label](url)`→`<url|label>`, `~~x~~`→`~x~`, drop horizontal rules, wrap pipe-table runs (≥2 contiguous lines) in triple-backtick fences for monospace alignment; italic `*x*`→`_x_` is **explicitly skipped** (lookaround-free Go regex can't reliably distinguish leftover bold artifacts; Claude usually emits `_x_` natively which already passes through). Tier 2 (Block Kit, deferred to plan-time decision): structured `header`/`section`/`divider` blocks for true visual hierarchy on streaming hook output, with per-section 3000-char split and 50-block ceiling fallback to Tier 1. New `pkg/slack/mrkdwn.go` + `pkg/slack/blocks.go`, fixture corpus + property tests (idempotence, code-block byte-preservation, valid Block Kit payload schema) + Go fuzz target. New `--render=plain|mrkdwn|blocks` flag on `km-slack post` (default `plain` so existing Phase 62/63 idle pings are unchanged); streaming hook in `pkg/compiler/userdata.go` flips to `--render=blocks`. Env safety valve `KM_SLACK_RENDER` lets operator downgrade in production without redeploy. Pipeline wrapped in recover so transformer panics fall back to original body. Phasing: Tier 1 ships first as standalone PR with the tokenizer + tests, soak in production for a few days, then Tier 1+Block Kit rollout.
- Phase 75 added: Slack inbound file attachments (images, PDFs) for per-sandbox channels — extend Phase 67 inbound flow so users can paste files into a per-sandbox Slack channel/thread and reference them conversationally ("what's in this picture?"). Bridge Lambda detects `files[]` on inbound message events, downloads each file from `files.slack.com` using the bot token (new `files:read` Slack scope, one-time re-auth), stages to S3 under `slack-inbound/<sandbox-id>/<thread_ts>/<filename>`. Sandbox-side `km-slack-inbound-poller` mirrors staged files to `/workspace/.km-slack/attachments/<thread_ts>/<filename>` (chown sandbox), then prepends a "master prompt" wrapper to the `claude -p` turn enumerating each attachment by absolute path + MIME type so Claude reads them via its Read tool (multimodal for images, native for PDFs). Wrapper added only when `files[]` is non-empty so plain-text turns stay clean. Caps: 25 files per inbound message, 100MB per file (bridge enforces; over-cap files dropped with a warning posted as thread reply). Filenames sanitized for path safety (strip `/`, `..`, non-printable) but original Slack-supplied name preserved in the master-prompt wrapper. Files persist for the session (matches 30-day Slack-thread DDB TTL via S3 lifecycle on the staging prefix). Bridge IAM gains `s3:PutObject` on staging prefix; sandbox role already reads `KM_ARTIFACTS_BUCKET`. No new SSM, DDB, or profile-schema changes; gated under existing `notifySlackInboundEnabled`. Builds on Phase 67 inbound flow + Phase 67.1 ACK reaction.
- Phase 76 added: km vscode rekey — rotate per-sandbox ed25519 keypair on an already-running sandbox. Solves three Phase 73 production gaps: (1) baked-AMI relaunches carry the bake-source sandbox's `authorized_keys` because cloud-init can mark itself "done" and skip the userdata pubkey-write block (architectural fix deferred to a follow-up phase that relocates the write into `km-bootstrap.service`); (2) cross-laptop portability — operators who want to `km vscode start` from a different machine currently have to manually copy `~/.km/keys/<sandbox-id>*`; (3) post-incident key rotation. Implementation: new subcommand under `km vscode`, generates ed25519 keypair (mirroring `km create` logic), atomic local replace via `os.Rename`, pushes new pubkey to sandbox via SSM SendCommand (`cat >`, chmod 600, chown sandbox:sandbox, restorecon for AL2023 SELinux). Order: push to sandbox FIRST, replace local key on success — reverse order can brick local access if SSM fails. No sshd restart needed (sshd reads authorized_keys per-connection); active sessions stay connected, reconnect transparently picks up the new key because the IdentityFile path is unchanged. Pre-flight: refuse if sandbox isn't running (no SSM channel) and emit a `km resume <id>` hint. No userdata template, DDB schema, SSM, or infra/modules changes.
- Phase 68.1 inserted after Phase 68: Fix km agent run PostToolUse hook skip blocking transcript streaming (URGENT — Phase 68 follow-up: non-interactive `claude -p` skips PostToolUse hooks per Claude Code platform behavior, so fire-and-forget agent runs get no per-turn Slack streaming today; only interactive `km shell` works)
- Phase 84.4.1.1 inserted after Phase 84.4.1: Multi-install follow-on gaps (URGENT — surfaced live during 84.4.1 UAT-2 on 2026-05-19) — (1) `km init --plan` skips `buildLambdaZips` so fresh clones fail at `filebase64sha256(build/create-handler.zip)`; (2) `km configure` artifacts_bucket prompt doesn't auto-derive `${prefix}-artifacts-${account_id}` despite `deriveArtifactsBucket` existing (Phase 84.3 partial-pass gap re-confirmed); (3) orphan SCPs not auto-detached by `km uninit`/`km unbootstrap` — `rg-sandbox-containment` from 84.4 UAT-1 blocked tg `create-handler`, same pattern now repeats with `tg-sandbox-containment` post-tg-teardown; (4) `validateArtifactsBucket` too permissive (accepted `tg-km-artifacts-use1-abcd0123` placeholder); (5) suspected TTL / "system in use" detection bug during `km uninit` (operator-flagged, needs repro). Doc-note interim shipped in 341a2e7 for gap (1).
- Phase 77 added: failed sandbox discoverability — persist `failure_reason` in DynamoDB at create-handler fail time, surface it in `km status`, and fall back `km logs` to the create-handler Lambda log group when the per-sandbox log group is missing. Triggered by L2/L3 (`learn-465e52e9`, `learn-ac6f33d2`) failing on archived `#sb-l2`/`#sb-l3` Slack channels — the actionable error existed only in `/aws/lambda/km-create-handler` and neither `km status` nor `km logs` could reach it. Spec: `docs/superpowers/specs/2026-05-10-failed-sandbox-discoverability-design.md`
- Phase 78 added: km agent auth — SSM-mediated OAuth login for claude and codex CLIs inside sandboxes. Two paths confirmed by inspecting the CLIs: (1) `--claude` uses claude CLI's manual paste-the-code flow with NO localhost callback (binary references `MANUAL_REDIRECT_URL: https://platform.claude.com/oauth/code/callback`); operator opens URL on laptop, claude.ai displays a code, operator pastes back into the SSM-attached terminal, CLI exchanges code for tokens and writes `~/.claude/.credentials.json`. (2) `--codex` uses codex's fixed `127.0.0.1:1455` OAuth callback (fallback 1457) — confirmed in `openai/codex` source `codex-rs/login/src/server.rs`; no CLI flag to override the port; tokens persist to `~/.codex/auth.json`. Operator-side flow: spin up SSM port-forward `localhost:1455 ↔ sandbox:1455` (reuse `km shell --ports` SSM primitive), SSM-exec `codex login`, capture URL the CLI prints (xdg-open fails on headless EC2 so it falls back to printing), operator opens URL on laptop, callback flows back through tunnel, tear down on file mtime change or process exit. New Cobra subcommand at `internal/app/cmd/agent_auth.go`. Solves operator pain of manually `claude auth login` after every `km create`, and gives codex parity. Suggested phasing: Wave 1 ship `--claude` (trivial wrapper), Wave 2 ship `--codex` (port-forward + URL relay state). Auto-trigger from `km agent run`/`km shell --no-bedrock` when credentials file is missing — print hint, do NOT silently bootstrap. Does NOT install or update the CLIs themselves (already baked into AMI/userdata).
- Phase 79 added: km-presence daemon — replace bash `_km_heartbeat` with a single systemd-managed liveness service (`km-presence.service`) that ticks every 60s and emits a heartbeat into `/run/km/audit-pipe` if any of five signals is positive: (1) login shells via `who`/utmp (covers SSM and SSH-via-Phase-73-port-forward), (2) attached tmux clients via `tmux list-clients`, (3) recent inbound email via mtime of newest `/var/mail/km/new/` file vs `/run/km/.presence-last-tick`, (4) recent inbound Slack via mtime of `/run/km/last-slack-inbound` (requires one-line `touch` addition in `km-slack-inbound-poller`), (5) headless agent process via `pgrep -af '(^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh'` (covers `km agent run` detached-tmux case). Drop-in replacement for the audit-pipe contract — zero changes to `km-audit-log` sidecar, `IdleDetector` (`sidecars/audit-log/cmd/main.go:327`), EventBridge, `ttl-handler` Lambda, or client-side `computeIdleRemaining` (`internal/app/cmd/status.go:532`); only diagnostic delta is `source:"presence"` instead of `"shell"`. Triggered by orphaned `_km_heartbeat` bug observed on `learn-14853201` (alias L1) on 2026-05-10: IDLE pegged at 60m for 3+ hours despite no active sessions, two orphaned heartbeats found via `aws ssm send-command` (one from a closed pts/0 SSM session, one from a 2h-old `km agent run` tmux that left two nested `exec bash` login shells each running their own heartbeat). New Go binary at `cmd/km-presence/main.go` (~150 LoC), systemd unit installed via userdata, ships through existing `km init --sidecars` pipeline. Removes lines 1056-1080 of `pkg/compiler/userdata.go` (the `_km_heartbeat()` function and its EXIT trap); keeps `_km_audit` per-command hook. New `km doctor` check `presence_daemon_healthy` flags sandboxes whose latest `source:"presence"` event is >5min old. Existing sandboxes don't get retroactively (matches Phases 63/67/68/73 pattern). Spec: `docs/superpowers/specs/2026-05-10-km-presence-daemon-design.md`
- Phase 80.1 inserted after Phase 80: Auto-detect existing OIDC provider in cluster-irsa module, supporting same-account IRSA without manual flags (URGENT)
- Phase 80 added: km cluster — cross-account IRSA for k8s integrations. New `km cluster add/list/rm` Cobra subcommand provisions an IAM role in the klanker AWS account with a trust policy referencing an OIDC provider in a *different* AWS account (the EKS cluster's account, e.g. Corporate `dev-use1-0` in `123456789012`). Solves the secret-free credential problem for a persistent k8s service in another account that needs to invoke `km` against klanker resources — pod assumes role via projected service-account token + `sts.AssumeRoleWithWebIdentity`, no static keys. Implementation: (1) **Extract** `infra/modules/km-operator-policy/v1.0.0/` from the current create-handler inline policy set (`infra/modules/create-handler/v1.0.0/main.tf:28-528` — s3_artifacts, dynamodb, dynamodb_sandboxes, terraform_state, ec2_provisioning, iam_sandbox, ecs_provisioning, scheduler, ssm, ssm_send_command, ses_send, lambda_budget, kms, sqs_slack_inbound — 14 inline statements). (2) Refactor `create-handler/v1.0.0` to consume the shared module via `aws_iam_policy_document` data sources keyed by role_id. (3) New `infra/modules/cluster-irsa/v1.0.0/` consumes the same shared module, with trust policy that accepts `oidc_provider_arn` as input (NOT `data.aws_caller_identity` — that's same-account only); wildcard-aware `sub` condition (StringLike when namespace/SA contains `*`, else StringEquals), mirroring Corporate's `aws_irsa_role` module pattern. (4) CLI generates `infra/live/{region-label}/cluster-{name}/terragrunt.hcl` at runtime from the design-doc template, runs `terragrunt apply --auto-approve` via existing `terragrunt.Runner`, captures `role_arn` output, persists to `km-config.yaml` under new `clusters:` list (new `ClusterConfig` struct in `internal/app/config/config.go`). (5) Handoff: print ServiceAccount YAML with `eks.amazonaws.com/role-arn` + `token-expiration: "3600"` annotations. Phase-close gate: full `--dry-run=false` apply against `klanker-application` profile, verify role in IAM, `km cluster list` shows it, `km cluster rm` cleanly destroys + un-persists. `km doctor` checks (cross-account trust health, stale cluster role detection) deferred to a follow-up phase. Spec: design doc supplied 2026-05-11.
- Phase 67.2 inserted after Phase 67.1: Slack ACK reaction bounded retry — add bounded retry with backoff inside `SlackReactorAdapter.Add` to absorb transient `reactions.add` failures (HTTP 429 honoring Retry-After, HTTP 5xx, Slack JSON errors `internal_error`/`service_unavailable`/`fatal_error`/`request_timeout`, network errors). Classifies responses into Success / Terminal-no-retry (auth-class at Error log, bad-input at Warn) / Transient-retry with ±25% jitter at 200ms→600ms. Unknown error strings default to transient (safer for new Slack codes). Bumps handler-side goroutine context timeout 5s→10s to fit the retry budget; handler-side contract unchanged. Bridge-only deploy (`make build && km init --lambdas`); no sandbox redeploy, no SSM/DDB/profile schema. Triggered by intermittent dropped 👀 reactions during the 2026-05-14 Slack-wide outages — today's code logs and discards the `ErrSlackRateLimited{RetryAfter}` value that the retry layer needs to honor. Spec: `docs/superpowers/specs/2026-05-14-slack-ack-reaction-bounded-retry-design.md` (URGENT — Phase 67.1 robustness gap exposed by Slack flakiness)
- Phase 81 added: GitHub Actions self-hosted runner — sandbox registers as runner for declared repos. New profile field `spec.sourceAccess.github.runner` (`enabled`, `repos`, `labels`, `runnerGroup`, `maxRepos`) — `repos` MUST be subset of `allowedRepos`. At boot, sandbox-side `km-actions-runner` sidecar (invoked from per-repo systemd template units `klanker-actions-runner-register@.service` + `klanker-actions-runner@.service`) calls new `km-actions-runner-token` Lambda via sigv4 to mint registration tokens, runs `config.sh` against each `https://github.com/{owner}/{repo}`, captures `runner_id` → DDB. Reuses existing GitHub App + JWT minting (`pkg/github/token.go`); App permission expansion required (`administration: write`, repo-level). Default labels (not removable): `self-hosted`, `klanker`, `klanker-<sandbox-id>`, `klanker-<alias>`; user labels appended. Failure model: per-repo registration is best-effort (NOT transactional) — `km create` succeeds regardless; failed registrations land as `failed` systemd units recoverable via new `km runner reattach <sandbox> [--repo R | --all-failed]`. Other CLI: `km runner status <sandbox> [--refresh]` (DDB-cached, `--refresh` hits GitHub API), `km runner detach <sandbox>`. `km destroy` belt-and-suspenders: SSM RunCommand → `config.sh remove` (best-effort), PLUS operator-side `DELETE /repos/.../actions/runners/{id}` (always runs, prevents ghost runners). Network allowlist auto-injects 4 new hosts (`*.actions.githubusercontent.com`, `*.pkg.actions.githubusercontent.com`, `objects.githubusercontent.com`, `codeload.github.com`) in `pkg/compiler/`. Presence daemon (Phase 79) gains 6th signal (`pgrep -f Runner.Listener`). Four new doctor checks: `actions_runner_app_perms` (ERROR), `actions_runner_register_failures` / `actions_runner_ghosts` / `actions_runner_drift` (WARN). Scope: repo-level only, persistent long-lived only, EC2 substrate only (Docker out — no systemd), immutable `runner.repos` (destroy+create to change). Rollout matches Phase 63/67/68/73/79/80 pattern (`make build && make sidecars && km init --sidecars && km init`). Spec: `docs/superpowers/specs/2026-05-13-actions-runner-design.md`. Reference patterns: `~/working/defcon.run.34/infra/terraform/modules/github-oidc/v1.0.0/` (instance-profile + Terragrunt v1.0.0 module shape; note: defcon uses OIDC federation, a different primitive from self-hosted runners).
- Phase 82 added: Multi-instance resource_prefix isolation — close the gap between CLAUDE.md's "multiple km installs per AWS account via resource_prefix" promise and reality. Code-inspection audit found ~85% of the platform correctly threads `resource_prefix` (km list / km at / km destroy / km doctor schedule+lambda+KMS+IAM+SSM checks / DynamoDB / Lambda / IAM roles / S3 / SQS / EventBridge / log groups / operator-policy SSM / userdata env injection all prefix-aware), but 3 hard infra blockers + 2 doctor cross-install destruction holes + 1 configure-flow footgun + 4 silent-misroute fallbacks remain. **Hard blockers:** (B1) `infra/modules/ses/v1.0.0/main.tf:62` rule_set_name literal `"km-sandbox-email"` — SES allows only one active receipt rule set per account/region, so second install's `km init` collides or activate-fight silently breaks the first install's inbound mail; (B2) `infra/modules/email-handler/v1.0.0/main.tf:75` IAM policy hardcodes `tf-km/sandboxes/*/metadata.json` so `rg` install's email handler can't read sandbox metadata; (B3) three ECS modules (`ecs-task/v1.0.0/main.tf:156`, `ecs/v1.0.0/main.tf:126`, `ecs-cluster/v1.0.0/main.tf:132`) hardcode `parameter/km/*` in SSM IAM ARN — only matters if ECS substrate used. **Doctor destruction risk:** (D1) sandbox/AMI tags are `km:sandbox-id` + `km:profile` but NOT `km:resource-prefix` (comment at `doctor.go:2740` foreshadows but never implemented); `ListBakedAMIs` (`pkg/aws/ec2_ami.go:184`) and `checkOrphanedEC2` (`doctor.go:1679`) filter `tag:km:sandbox-id=*` → return BOTH installs' resources → other install's sandboxes appear as orphans eligible for cleanup. **Configure footgun:** (C1) `internal/app/cmd/configure.go:140,178,185` silently re-default resource_prefix to `"km"` on re-run — an `rg` operator who reruns `km configure` to rotate operator_email accidentally rewrites their config to point at the `km` install's tables. **Silent fallbacks:** (F1) four `km-…` literal defaults in `cmd/configui/main.go:225` (`km-budgets`), `cmd/km-slack-bridge/main.go:175` (`km-slack-threads`), `pkg/compiler/userdata.go:3315,3331,3346` (`km-slack-threads`, `km-slack-stream-messages`) fire when env vars are missing — defense-in-depth that silently cross-routes instead of failing loudly. **Fix shape (3 waves, ~15-20 file touches, comparable in size to Phase 80):** Wave 1 Go-only — close C1+F1, add `km:resource-prefix` tag at bake time in `pkg/aws/ec2_ami.go`, add tag-filter to doctor's ListBakedAMIs + checkOrphanedEC2, ship `km doctor --backfill-tags` (potentially unified with the analogous proposal in `docs/superpowers/specs/2026-05-09-doctor-tag-based-platform-discrimination.md`). Wave 2 Terraform module additions, backward-compat defaults — fix B1/B2/B3 by adding `resource_prefix` (and `state_prefix` for email-handler) variables. Wave 3 — `km init --dry-run=false` to apply tags + SES rename (sub-10s inbound-mail outage window unless `moved {}` blocks apply cleanly; worth verifying on throwaway account before flipping prod). Open questions in spec: SES `moved {}` behavior, backfill-tags unification, optional positive `multi_install_collision` doctor check, Phase 80 cluster-role name prefixing, state-bucket layout. Code-inspection-only confidence high for all bullets (literal strings in patches); empirical verification only worth it for SES rename behavior. Spec: `docs/superpowers/specs/2026-05-16-multi-instance-resource-prefix-isolation-design.md`. Followup/companion to Phase 66 (which introduced `resource_prefix` but didn't enforce isolation).
- Phase 83 added: km event command for operator-controlled EventBridge (originally planned as Phase 82 in a parallel worktree, renumbered to 83 on 2026-05-16 to avoid collision with main's Phase 82 multi-instance work which landed first). Adds a declarative platform-level scheduling lane that complements (not replaces) the ad-hoc `km at` command. Manifest-driven (`events/*.yaml`), terragrunt-applied, git-reviewable. Shared operator bus `{prefix}-operator-events` (custom EventBridge bus + archive enabled by default, 7-day replay window provisioned but no `km events replay` CLI in v1). New singleton `km-runner` Lambda that has the `km` binary baked in and routes scheduled commands through it (`type: km` target — operator writes `--target-km 'doctor --all-regions'` and the Lambda execs `km doctor --all-regions`); IAM is the same 14-policy bundle from `km-operator-policy/v1.0.0/` (Phase 80 module) so the trust surface stays coherent. Second target type: `type: arn` (free-form `--target <arn>`, repeatable for fanout, service-prefix-derived IAM target role for `lambda:InvokeFunction`/`sqs:SendMessage`/`sns:Publish`). Third target type `code` (operator-supplied Python/Go compiled to a Lambda) explicitly OUT of scope — sandboxes-as-execution-substrate makes `type: km` the universal abstraction (`km create profiles/job.yaml` is the code-execution path). CLI surface modeled on Phase 80 `km cluster add`: `km event add '<NL when>' <km-cmd> --name <slug>` (NL → cron/at expression, generates `events/<slug>.yaml`, runs terragrunt apply, persists to `km-config.yaml` under new `events:` key); `km event apply events/*.yaml` for hand-edited manifests; `km event list` (config + EventBridge `DescribeRule` for status); `km event rm <name>` (terragrunt destroy + config prune); `--hcl <file>` escape hatch drops raw HCL into the live tree for richer-than-flag-soup rules (DLQs, input transformers, multi-target). `km at` cleanup: strip `--cron` flag, reject parser results with `IsRecurring=true` (one-line error pointing operators at `km event`) — no deprecation period, just a hard cut. New `infra/modules/operator-event-bus/v1.0.0/` (one-time: bus + archive + km-runner Lambda) and `infra/modules/operator-event-rule/v1.0.0/` (per-rule). Typed Go struct in `pkg/events/manifest.go` → JSON Schema for IDE completion, same shape as `SandboxProfile`. New `events:` key in `km-config.yaml` (absent = empty slice, no migration needed). New `km doctor` check `operator_event_rules_healthy` verifies every config-registered rule has a matching EventBridge rule. Rollout matches Phase 80 pattern (operator-applied terragrunt from workstation — `make build` only; no `km init --sidecars` because no sandbox-side or management-Lambda-side code changes). Planning artifacts (CONTEXT + RESEARCH + VALIDATION + 7 PLANs) created across 7 commits on a stale `worktree-phase-74-02` branch; squashed-cherry-picked into this `worktree-phase-83-km-event` branch off updated main on 2026-05-16.
- Phase 84.1 inserted after Phase 84: SES upgrade safety — gap closure for in-place v1.0.0 → v2.0.0 cutover. Closes 8 gaps diagnosed by Phase 84's UAT (`.planning/phases/84-…/84-10-UAT.md`) that made the happy-path upgrade fail and required manual recovery: (1) `km bootstrap` does not export `KM_ROUTE53_ZONE_ID` (fatal when `register_domain_identity=true`); (2) `km bootstrap` is not idempotent — foundation's `aws_ses_receipt_rule_set.shared` has `lifecycle.prevent_destroy = true` AND `count = var.register_shared_rule_set ? 1 : 0`, so second invocation auto-detects existing rule set → flag flips 1→0 → terraform plans destroy → blocked; (3) foundation auto-detect operates on AWS reality not state ownership, so in-place upgrade from Phase 82.x lets regional v1.0.0 destroy nuke the domain identity/DKIM/MX/verification TXT/active rule set pointer that no module ended up owning — net data loss; (4) `km init` swallows ses-module errors silently when terraform wedges (no stderr until exit); (5) no timeout on regional module applies — wedged terraform blocks `km init` indefinitely (10+ min before manual Ctrl-C in UAT); (6) foundation does not `terraform import` existing v1.0.0 resources on upgrade — highest-impact gap, any Phase 82.x upgrader hits data loss; (7) `km bootstrap` does not export `KM_ARTIFACTS_BUCKET` (direct-terragrunt operator workflow produces malformed S3 ARNs); (8) state-digest mismatch recovery (S3 vs DynamoDB lock table after Ctrl-C of mid-flight apply) is undocumented — should be documented in OPERATOR-GUIDE.md or detected by `km doctor`. Fix shape: likely (a) `ExportTerragruntEnvVars(cfg)` helper exporting full set (KM_RESOURCE_PREFIX, KM_DOMAIN, KM_EMAIL_SUBDOMAIN, KM_REGION, KM_REGION_LABEL, KM_ROUTE53_ZONE_ID, KM_ARTIFACTS_BUCKET, KM_ACCOUNTS_*) shared by `km bootstrap` + `km init`; (b) replace register-flag count toggle with always-manage semantics post-creation; (c) foundation runs `terraform import` blocks (or `removed {}` in regional v2.0.0) for pre-existing domain identity/DKIM/MX/TXT/active rule set pointer when upgrading from Phase 82.x; (d) `runner.Apply` learns a context-deadline + progress heartbeat that surfaces wedged children as timeout errors; (e) OPERATOR-GUIDE.md adds state-digest recovery runbook and/or `km doctor` learns to detect+offer repair. Plus folding three inline UAT fixes (`7aefed3`, `143798d`, `80b59a3`) back into Phase 84 plan SUMMARYs and re-running UAT closures (a)/(c)/(d) that were skipped because they'd re-trip Gaps 1, 2, 4, 6, 7. (URGENT — Phase 84 upgrade path broken, runtime design sound)
- Phase 84.2 deferred from Phase 84.1 UAT (2026-05-17): six items intentionally not exercised in Plan 84.1-05's minimal UAT scope. (1) **GAP-6 empirical verification** — re-run the in-place v1.0.0 → v2.0.0 cutover against a fresh Phase 82.x install or a synthesized regional state file containing v1.0.0 resources; Phase 84.1's verification was structural only (`terraform validate` + zero-destroy assertion against post-recovery regional state with no v1.0.0 resources to suppress). (2) **GAP-3 auto-detect investigation** — `km bootstrap --shared-ses --dry-run=true` reported `creating` for shared rule set + domain identity on an account that already has them; either `FoundationStateReader` isn't activating in the bootstrap dry-run path, or foundation state doesn't actually own those resources. Probe: `terragrunt state list` against `infra/live/use1/ses-shared-rule-set` + trace `runBootstrapSharedSES` wiring. (3) **UAT closures (a)/(c)/(d) deferred** — re-run the three Phase 84 UAT closures (second-install dry-run, sibling destroy isolation via `km uninit`, bootstrap idempotent re-run); operationally heavy. (4) **DRIFT-B** — exercise `km configure --reset-prefix` → re-derive operator_email loop. (5) **DRIFT-C** — destructively exercise `InitSESPreflight` block-until-rule-set-exists path. (6) **Phase 84.2 `km init --plan` flag** (spec already shipped in commit `78241a8`, `docs/superpowers/specs/2026-05-16-km-init-plan-flag-and-destroy-class-gate-design.md`) would make all of the above trivial by replacing today's static-info `km init --dry-run=true` print with a real `terragrunt plan` + curated destroy-class gate — canonical follow-up for empirical SES upgrade verification. All six tracked in `.planning/phases/84.1-ses-upgrade-safety-gap-closure/84.1-05-UAT.md` § Phase 84.2 gap entries.
- Phase 84.4.1 inserted after Phase 84.4: Multi-install identity/permission gap closure — close 3 TIER-1 gaps from 84.4-08 UAT (ssm-session-doc v2.0.0 migration, SCP AND-composition, Plan 06 SES auto-import) plus 6+ fresh-clone DX gaps (Lambda zip Makefile drift, region.hcl prereq, configure state_bucket UX, unbootstrap DDB orphan, Phase 84.3 closure-e regression, terraform binary cache invalidation); thesis re-verified via fresh-prefix UAT-2 against a second probe install (URGENT — 84.4 closed PARTIAL PASS, multi-install thesis not production-safe)
- Phase 85 added: doctor: orphan state-lock digest sweeper + report cleanup — Phase 84.1 added `checkStateLockDigest` which flags DDB lock rows whose sibling S3 state object is gone; on this account ~275 such orphans have accumulated from a mix of `km destroy` leaks and manual S3 cleanup during 84.x multi-install testing. Today there's no cleanup path (only a hand-run `aws dynamodb update-item` per row) and the warn dumps all rows into a single unreadable line. **In scope:** (1) new cleanup category for orphan rows in the Terragrunt state-lock table; (2) gate via new `--delete-state-digests` per-category flag, also folded into `--with-deletes` umbrella (matches existing `--delete-{ebs,sqs,s3,lambdas,ssh,ssm}` pattern); (3) detection safety — delete iff (a) S3 HEAD on the sibling `terraform.tfstate` returns a definitive NoSuchKey (any other error skips, logged as "could not verify") AND (b) DDB row age > 24h (configurable, matches existing in-flight-create age guard); (4) output format — top-line `state digest mismatch in N item(s) (M orphan: state object missing, K other)`, first 10 items inline, `… and N more (use --json for full list)` (matches Stale Lambdas formatting); (5) performance — parallelize the per-item S3 HEAD scan (worker pool ~10) + BatchWriteItem deletes (25/batch); target doctor full run < 30s on current data (currently ~1:40, digest check dominates); (6) TDD unit tests in `doctor_state_digest_test.go`: orphan+age-passes deletes, orphan+age-fails skips, S3 HEAD non-404 error skips (never deletes), non-orphan mismatch type never deletes, batch > 25 splits correctly. **Out of scope (follow-up phase):** plug the underlying leak in `km destroy` / `km uninit` so the `terraform.tfstate-md5` DDB row is deleted alongside the S3 state object — sweeper here is post-hoc only; sweeper for other mismatch types (live S3 + stale MD5) — reported, never auto-deleted. **Acceptance:** `km doctor` ≤30s wall clock on current account; warning summarized to one readable line + 10-item preview, full list via `--json`; `km doctor --with-deletes --dry-run=false` cleans the ~275 orphans, leaves any live-state mismatches untouched, leaves the running sandbox's lock row untouched. (Brainstormed 2026-05-18; design captured in `.planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/BRIEF.md`.) User wanted Phase 84.5 — landed as Phase 85 because `/gsd:add-phase` allocates integer phases (use `/gsd:insert-phase` for decimals); roadmap position is equivalent.
- Phase 86 added: km-create-prompt-queue — operator-side `--prompt <text-or-@file>` flag on `km create`, repeatable, queues prompts on-box at `/workspace/.km-agent/queue/`, drains them sequentially once Claude auth is available. Composition over existing `km agent run` primitives — no schema changes, no Lambda code changes, no Terragrunt churn. Linear chain semantics: indefinite auth wait (Bedrock invoke-model probe or `~/.claude/credentials.json` existence check), prompt-failure stops the chain (remaining marked `skipped`), runner is a ~80-line bash script under a `km-queue.service` systemd unit (`Restart=always`) wrapping a tmux session named `km-queue` for attach-ability. Reconcile step on runner start resets stuck `running` entries back to `pending` for clean reboot recovery. Adds `km agent list --queue` view. Three open questions deferred to planning: (1) bash vs Go runner (default bash), (2) seed via `configFiles` vs AMI bake (default configFiles), (3) `--prompt` on `km resume` too (default no, v1 is create-only). Out of scope (deferred to future specs): profile-embedded `spec.agent.prompts: []`, standalone `km play plan.yaml` with DAG/conditions/templating, per-prompt timeout/retry. Spec at `docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md`; brief at `.planning/phases/86-km-create-prompt-queue/BRIEF.md`. Brainstormed + spec'd 2026-05-19 in a single session with the user; brainstorming choices: option A (CLI flag) + option D (auto-detect single vs repeatable, `--wait` blocks, default queue-and-return) + option A (auth wait forever, fail stops chain). User dispatched autonomous research/plan/execute/UAT cycle after spec approval.
- Phase 87 added: additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile. New optional `spec.runtime.additionalSnapshots: [...]` list of `(snapshotId, mountPoint, device?, encrypted?, size?)` tuples parallel to existing `additionalVolume` (coexists, separate field, both can be set). Each entry materialises a fresh `aws_ebs_volume` from an existing EBS snapshot (`snapshot_id` set), attaches it via `aws_volume_attachment` on `/dev/sd[f-p]` (auto-allocated by extending `pickAdditionalVolumeDevice` to accept a per-list `claimed` set, or pinned explicitly), and mounts via userdata-detected filesystem type (loop refactor of current single `{{- if .AdditionalVolumeMountPoint }}` block — emits one mount stanza per `additionalVolume`+`additionalSnapshots` entry; `blkid -s TYPE -o value` instead of hard-coded ext4 in fstab). Source snapshot never modified; materialised volume destroyed with sandbox (same lifecycle as `additionalVolume`). **Layered validation:** Layer 1 schema at `km validate` (snapshotId `^snap-[0-9a-f]{8,17}$`, mountPoint absolute + safe + unique + not colliding with EFS/workspace/additionalVolume, device `/dev/sd[f-p]` + unique, size >=1, EC2-only). Layer 2 AWS pre-flight at `km create` (single batched `DescribeSnapshots` — all snapshots exist + `State=completed` + size override >= snapshot.VolumeSize; IAM-missing degrades to WARN + terragrunt fallback). **Schema nuance:** `Encrypted *bool` (pointer, not bool — omitted ≠ false; nil emits `null` to terraform so AWS inherits snapshot encryption). **Module versioning:** additive change to versioned module → new `infra/modules/ec2spot/v1.1.0/` (copy of v1.0.0 + `additional_snapshots` variable using `optional()` types + `for_each` `aws_ebs_volume.snapshot`/`aws_volume_attachment.snapshot` resources); old sandboxes on v1.0.0 untouched; compiler emits v1.1.0 path. Userdata change is byte-identical for `additionalVolume`-only profiles save for `ext4` → `${FSTYPE}` substitution. EC2-only (rejects ECS/Docker like today's `additionalVolume`). 8 UAT scenarios (auto-device, multi-entry+additionalVolume coexistence, explicit device pin, AMI BDM collision rotation, missing snapshot, wrong region, size grow, size shrink rejected). Out of scope: `preserveOnDestroy`/`snapshotOnDestroy` knobs, KMS key selection, unified `additionalVolumes` list (deprecates `additionalVolume`), cross-region snapshot copy, learnMode integration. Spec: `docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`. Brainstormed + spec'd 2026-05-21; user approved schema after schema-section presentation, then said "write the spec and make the GSD phase".
- Phase 88 added: Codex/OpenAI budget metering — http-proxy interceptor for `api.openai.com` + price table + IncrementAISpend wiring (mirrors Anthropic pipeline). Today `sidecars/http-proxy/httpproxy/anthropic.go` + `bedrock.go` are the only intercept paths that feed `BUDGET#ai#{modelID}` rows; OpenAI/Codex traffic flows through unmetered, so Codex-only sandboxes report `$0` AI spend and `spec.budget` enforcement (IAM revoke + proxy 403) never fires for them. This phase adds a third interceptor at `api.openai.com` with the same response-stream parse → token-count → `pkg/aws/budget.IncrementAISpend(ctx, …, modelID, in, out, costUSD)` shape, plus a hard-coded OpenAI price table (research current $/1K tokens — at minimum the gpt-5 family, gpt-4o family, o1 reasoning models, and any codex-specific endpoints). No DynamoDB schema change, no budget-enforcer change — just an additional intercept path that writes into the same per-model rows the enforcer already reads. Acceptance: a Codex sandbox with `spec.budget` set sees per-model AI spend rows accrue exactly like a Claude sandbox does, and existing exceeds-budget enforcement fires for Codex the same way. Out of scope: budget UI changes, OTEL/MLflow spend reporting changes, anything non-OpenAI (Google/Mistral/etc. deferred). Companion to Phase 6 (which introduced budget enforcement) and Phase 70 (Codex parity). Surfaced 2026-05-24 in conversation triggered by the question "Does the budget consider Codex outputs too?" — answer is no, this phase closes that gap. User authorized autonomous GSD planning before checking in tomorrow.
- Phase 89 added: SOPS secret injection for sandboxes — declarative `spec.secrets.sopsFile: ./secrets/*.enc.yaml` schema field; sandbox instance profile gets `kms:Decrypt` on a shared `klanker-sandbox-secrets` KMS key (provisioned by `km bootstrap`, idempotent like shared SES rule set); compiler uploads encrypted bundle to S3 (`s3://km-artifacts-*/sandboxes/<id>/secrets.enc.yaml`); initCommand fetches + decrypts via `sops` Rust binary at boot into `/etc/sandbox-secrets.env` (root:root 0400); sandbox user picks up via `/etc/profile.d/zz-sandbox-secrets.sh` or systemd EnvironmentFile. Multi-key bundles native (OpenAI + Anthropic + future providers). Motivated by Phase 88 UAT — no automated `OPENAI_API_KEY` injection path exists today; UAT used out-of-band SSM SecureString + temp file + SSM send-command. v1 scope: one shared KMS key for the install (any sandbox can decrypt any bundle if it gets the file). v2 (deferred): per-profile or per-sandbox KMS key isolation via key policy + `aws:PrincipalTag`; secret rotation without sandbox recreate; `km secrets edit <profile>` ergonomic wrapper around `sops`. Acceptance: a Codex sandbox declares `spec.secrets.sopsFile: ./secrets/codex.enc.yaml`, boots with `OPENAI_API_KEY` populated, Phase 88 meter fires automatically against `api.openai.com` (no operator action post-create). Architecture chosen 2026-05-26 after Phase 88 completion: Option B (sandbox decrypts at boot) over Option A (operator decrypts + injects resolved env) — operational independence wins over lower blast radius for v1; compiled-in profile path over git-clone-on-boot or SSM-as-blob. Open design questions for planner: (1) where do encrypted SOPS files live in-repo — `secrets/` dir gitignored except `*.enc.yaml`, or always-gitignored encrypted-or-not?; (2) does `km bootstrap` accept `--shared-secrets-key` like `--shared-ses`, or fold under `--all`?; (3) does `sops` get installed via initCommand in every profile, or baked into common AMIs?
- Phase 79.1 inserted after Phase 79: audit-pipe FIFO recreation on resumed sandboxes — systemd-tmpfiles drop-in + audit-log self-heal when path exists as non-FIFO regular file. Phase 79's km-presence daemon (root, started at boot) emits heartbeats via bash `tee /run/km/audit-pipe`, which CREATES the path as a regular `root:root 0644` file when the FIFO is absent. On resumed sandboxes (`/run` is tmpfs → wiped on stop+start; cloud-init does NOT re-run on second boot), the FIFO is gone. km-presence's first tick beats audit-log's `openAuditPipeWithRetry` (which uses `O_RDWR` to avoid the FIFO open-block) — audit-log gets `EACCES` on the regular file, gives up after 10 retries, and no `source:"presence"` events ship to CloudWatch. Doctor's `presence_daemon_healthy` check then falsely WARNs because the underlying audit→CW pipeline is broken on resume. Fix has two layers: **Layer 1 (root cause):** add `/usr/lib/tmpfiles.d/km.conf` heredoc to userdata template (`pkg/compiler/userdata.go` near the existing `mkfifo /run/km/audit-pipe` block) so `systemd-tmpfiles --create` recreates the FIFO with `km-sidecar:km-sidecar 0666` at every boot — fresh AND resumed. **Layer 2 (defense in depth):** modify `sidecars/audit-log/cmd/main.go::openAuditPipeWithRetry` (line 226-255) so when `os.Stat` succeeds but `info.Mode().Type() != fs.ModeNamedPipe`, unlink + `syscall.Mkfifo` before opening. Validation: stop+resume of a fresh sandbox produces audit-log journal `reading from audit pipe pipe=/run/km/audit-pipe` (not `permission denied`) AND `km doctor` reports `✓ Presence daemon healthy` within 5 minutes of a real liveness signal (tmux attach, login shell, agent process). Pre-existing manual recovery: `rm /run/km/audit-pipe && mkfifo /run/km/audit-pipe && chown km-sidecar:km-sidecar /run/km/audit-pipe && chmod 666 /run/km/audit-pipe && systemctl restart km-audit-log` — used to confirm root cause end-to-end on sandbox learn-14f484c7 (2026-05-16). Discovered during Phase 79 live UAT post-resume; original Phase 79 VERIFICATION was done on a fresh `km create`, never on stop+resume cycle. (URGENT — Phase 79 stop+resume integration gap; doctor check falsely WARNs without it)
- Phase 90 added: km init self-healing provider locks via reconfigure-upgrade per module — `km init` (apply loop + `--plan` path) runs `terragrunt init -reconfigure -upgrade` per regional module so stale `.terraform.lock.hcl` from an upgraded old install (observed on km 0.2.x; operator had to manually `init -reconfigure -upgrade` to "move a lock version on") is moved forward to root.hcl's exact pins (aws 6.46.0, tls 4.3.0) automatically. New `ReconfigureUpgrade` method in `pkg/terragrunt/runner.go` + `InitRunner` interface; replaces the ses-only `Reconfigure` branch in `init.go` with a universal per-module call; `planModule` runs it before `PlanWithOutput`. Destroy paths (uninit, cluster) and bootstrap foundation apply stay on plain `Reconfigure` (out of scope). Accepted tradeoffs: registry round-trip + few seconds per module on every init; lock drift becomes invisible. Brainstormed + spec'd 2026-05-27; original one-line "add -upgrade to shared Reconfigure" proposal corrected after finding Reconfigure is ses-only in `km init` (would not have healed other modules). Spec: `docs/superpowers/specs/2026-05-27-km-init-self-healing-locks-design.md`.
- Phase 91 added: Slack inbound @-mention-only mode for shared/override channels — stop the km-slack bridge from forwarding every message in subscribed channels; only react when message text contains `<@{bot_user_id}>`. Smart per-channel-mode defaults: per-sandbox `#sb-{id}` channels keep current every-message behaviour (bot is primary participant); shared (Mode 1) + operator-controlled override (Mode 3) channels default to @-mention-only. New `cli.notifySlackInboundMentionOnly *bool` profile field lets operators force on/off, otherwise mode-derived default applies. Implementation surfaces: (1) `pkg/profile/types.go` + JSON schema + `validate.go` new CLISpec field; (2) bridge handler detects mention via `<@{bot_user_id}>` substring scan; bot_user_id cached in SSM at `{prefix}slack/bot-user-id` (verify caching path in `km slack init` against current auth.test response handling); (3) `pkg/compiler/userdata.go` emits `KM_SLACK_MENTION_ONLY` env var into bridge config from resolved profile + mode-derived default; (4) `docs/slack-notifications.md` operator-facing doc + decision matrix table; (5) `km doctor` sanity check that bot_user_id is cached when at least one profile has mention-only enabled. Out of scope: per-channel runtime overrides (slash command), display-name mentions without `<@U...>` form, reactions-as-actions integration. Origin: raised by operator during Phase 72 UAT 2026-05-30 — corporate-workspace install where shared `#km-notifications` would be too noisy if the bot 👀-reacted to every team message. Initial design captured in `.planning/todos/pending/2026-05-30-slack-inbound-mention-only-mode.md`. Depends on Phase 72 (uses `bot_user_id` from `auth.test` response shape established in 72-01; reuses `notifySlackEnabled`/`notifySlackPerSandbox`/`notifySlackChannelOverride` mode dispatch from `create_slack.go`).
- Phase 92 added: Profile spec restructure — notification block + iam rename + dead-field removal + structured agent tool gating. Consolidates four user-facing changes in a single phase with 7 waves: (1) new top-level `spec.notification:` block owns the 14 `notify*` fields currently scattered through `spec.cli:`, with sub-blocks (`notification.events`, `notification.email`, `notification.slack.{inbound,transcript,invites}`); (2) `spec.identity:` → `spec.iam:` rename (section is AWS IAM, not "identity"); (3) dead fields removed (`identity.sessionPolicy`, entire dead `spec.agent:` block with `maxConcurrentTasks`/`taskTimeout`/`allowedTools` confirmed unused); (4) structured `spec.agent:` block fills the reclaimed slot with `agent.{default,claude,codex}` typed tool-gating — compiler synthesizes `/home/sandbox/.claude/settings.json` and `~/.codex/config.toml` from typed fields, eliminating inlined-JSON-in-YAML antipattern. Plus three correctness fixes that fall out: inheritance bug fix (`pkg/profile/inherit.go` doesn't merge pointer sections — child profile setting one notify field silently loses parent's other notify settings; typed mergers added for Notification + Agent), schema drift fix (`allowedSecretPaths` is wired in compiler but missing from JSON schema), and `vscodeEnabled` relocated from `cli:` to `runtime.vscode.enabled` (it's userdata provisioning, not CLI defaulting). User constraints: zero running sandboxes, no backwards compatibility required, all 20 profile YAMLs (12 in `profiles/` + 8 in `pkg/profile/builtins/`) rewritten atomically per wave. Pre-phase cleanup already done by user 2026-05-30: `profiles/uat/` tree, `profiles/hermes.open.yaml`, `profiles/secrets/codex.enc.yaml` deleted. Wave layout: Wave 0 (research + RED tests), Wave 1 (iam rename + dead-field removal + schema drift fix), Wave 2 (notification types/schema/inherit), Wave 3 (notification compiler + CLI + fixtures + docs), Wave 4 (agent types/schema/inherit + mixed-mode validator), Wave 5 (Claude settings.json + Codex config.toml synthesizers + fixture rewrite + docs), Wave 6 (operator UAT). Brainstormed 2026-05-30 starting from "I want to plan a comprehensive restructuring of the profiles spec"; user picked Claude-Code-settings.json tool gating layer (not kernel/proxy enforcement) and full Approach B (notification + agent restructure together). Context: `.planning/todos/pending/2026-05-30-phase-92-profile-spec-restructure-CONTEXT.md` (PRD-ready, all 14 implementation decisions locked).
- Phase 93 added: km desktop — KasmVNC-backed browser/XFCE remote session over SSM port-forward. Give an operator a graphical session — default a single maximized browser (kiosk mode), optionally a full XFCE desktop — rendered in their **local** browser over an SSM port-forward, so web-browser-based interactions (Chrome/Firefox/Brave) run remotely inside the sandbox EC2. New `spec.runtime.desktop` block: `enabled` (default **false** — opt-in, opposite of `vscode`'s default-on, because the install is heavy), `mode: kiosk|full` (default kiosk), `browsers` ⊆ {firefox,chromium,chrome,brave} (default `[firefox]`; kiosk launches `browsers[0]`, full installs all), optional `geometry` (default 1920x1080). Engine is **KasmVNC** (web-native VNC server with a built-in HTML5 client + seamless bidirectional clipboard) — one component replacing TigerVNC+websockify+noVNC; chosen specifically because *proper clipboard* is a hard requirement. `km desktop start/status <id>` mirrors `km vscode` (loopback-only bind, SSM port-forward is the sole access path; reuses `sendSSMAndWait`/`buildPortForwardCmd`/`extractResourceID`/`ResolveSandboxID` + the dep-injection pattern). Per-sandbox KasmVNC credential generated at `km create`, stored at `~/.km/desktop/<id>`, seeded into `~/.kasmpasswd` fresh at boot and **never baked** so one AMI serves all sandboxes. Idempotent, AMI-bakeable userdata (install if absent, skip if baked; XFCE for full mode, matchbox-window-manager for kiosk). Posture-agnostic networking — inherits the profile's `spec.network` egress enforcement unchanged. SSL disabled at the KasmVNC layer (justified by loopback bind + encrypted SSM tunnel). Compiler threads new `Desktop*` fields through `service_hcl.go` like `VSCodeSSHPubKey`; new `IsDesktopEnabled(*RuntimeDesktopSpec)` helper defaults **false**. **v1 target distro: Ubuntu 24.04/22.04 only** (KasmVNC's official builds); `km validate` guards non-Ubuntu AMI when desktop enabled. Deliverables: `profiles/desktop.yaml` kiosk-Firefox example (added to `scripts/validate-all-profiles.sh`) + a `klanker:desktop` skill (bump plugin.json/marketplace.json per the cache-gate). Rollout: schema addition → `make build && km init --sidecars` to refresh management Lambdas; existing sandboxes need `km destroy && km create`. Out of scope (v1): GNOME/KDE, audio, multi-monitor, session recording, Amazon Linux 2023, web-based file transfer as a headline feature. Brainstormed + spec'd 2026-06-02 starting from "I want to add an apache guacamole setup … xfce/gnome start … full system linux in a browser"; user steered away from Guacamole (gateway value duplicates the SSM tunnel) to KasmVNC, and from full-desktop-first toward browser-first/kiosk-default. Spec: `docs/superpowers/specs/2026-06-02-km-desktop-remote-browser-design.md`. Depends on Phase 92 (`spec.runtime` schema shape + RuntimeVSCodeSpec/`IsVSCodeEnabled` sibling pattern; `km vscode` CLI/SSM helpers reused).
- Phase 94 added: km doctor leaked per-sandbox debris cleanup (log groups, DDB rows, S3 lifecycle). Teach `km doctor` to detect, reclaim, and prevent the orphaned per-sandbox debris teardown leaves behind, found in a live crawl of the `kph` install: ~271 retention-less CloudWatch log groups across three families (`/aws/lambda/{prefix}-budget-enforcer-{id}` ~91, `/aws/lambda/{prefix}-github-token-refresher-{id}` ~100, the per-sandbox sandbox log-group family ~90 — AWS never deletes a Lambda's log group when the Lambda is deleted), leaked DynamoDB rows (`{prefix}-budgets` ~251 incl. per-sandbox rows, `-identities` ~85, `-slack-threads` ~53, plus `status=failed` `-sandboxes` rows from failed creates), and an artifacts S3 bucket with no lifecycle expiry on transient prefixes (`logs/`, `remote-create/`, `agent-runs/`, `slack-inbound/`). Approach A (chosen over B=one mega-check, C=separate `km reap`/`km gc` command): three new checks following the established `checkStale*`/`checkOrphaned*` contract (list → group by sandbox-id → diff against `SandboxLister` active set → WARN with `use --delete-X` hint → reclaim only under `--dry-run=false --delete-X`, the same safety model as `--delete-ssm`/`--delete-ebs`/`--delete-s3`): (1) `checkStaleLogGroups` in new `doctor_log_groups.go` — `--delete-logs` cleanup + `--set-log-retention` guardrail (set `retentionInDays` on management + live-sandbox groups lacking it, idempotent); (2) `checkOrphanedDDBRows` in new `doctor_ddb_rows.go` — `--delete-ddb-rows`, **preserving AI-model `BUDGET#ai#{modelID}` rows** (Phase 88 metering shape) and guarding in-flight creates (sandbox rows purged only when `status=failed` AND missing `instance_id`); (3) `checkS3LifecyclePolicy` extending `doctor_artifacts.go` — `--set-s3-lifecycle` guardrail expiring only the transient prefixes, never build artifacts (`toolchain/`/`sidecars/`/`rsync/`). Two config knobs `doctor_log_retention_days` / `doctor_s3_expire_days` (default 30) via the five-touchpoint pattern (field + SetDefault + merge-list entry + accessor + clamp — merge-list per `project_config_key_merge_list`). New mocked-API interfaces (`CWLogsCleanupAPI`, `DDBScanDeleteAPI`, `S3LifecycleAPI`) mirroring `SSMDeleterAPI`/`S3CleanupAPI`. Multi-install honored (`--ignore-prefix`/`doctor_ignore_prefixes`); full pagination; idempotent guardrails. Operator-side binary change only — `make build`, no Lambda/terragrunt deploy. Out of scope: orphan EBS snapshots (manual operator backups — too risky to auto-touch). Two open research items deferred to planning rather than guessed: (a) exact CloudWatch log-group name templates, esp. the per-sandbox sandbox log-group base (crawl shows `/km/sandboxes/`, profile declares `/klanker-maker/sandboxes`) — derive from compiler/create-handler; (b) exact DDB key schemas for the four tables — derive from `pkg/aws` definitions. Discovered + brainstormed 2026-06-04 during a live read-only resource crawl after debugging the `l1` desktop sandbox (an unrelated Ubuntu cert-path initCommand bug). Overlaps known teardown gaps in `project_ttl_handler_ignores_retain_and_lock`. Spec: `docs/superpowers/specs/2026-06-04-km-doctor-debris-cleanup-design.md`. Depends on Phase 93.
- Phase 95 added: Slack federated bridge relay — one Slack App serving many `resource_prefix` installs and operators in one AWS account. A Slack App has exactly one Events Request URL; today each install runs its own `{prefix}-slack-bridge` (own Function URL + own bot token + own signing secret), so each install effectively needs its own App. Phase 95 lets the operator install ONE App, point its single Request URL at any one install's bridge ("the front door"), and have that bridge relay events it doesn't own to sibling bridges. Mechanism (brainstormed 2026-06-05, pivoted twice): NOT a shared registry/shared-SSM-credentials design (rejected) — instead an opt-in static per-install list `slack.peer_bridges []string` in `km-config.yaml`, plumbed to the bridge Lambda as `KM_SLACK_PEER_BRIDGES` mirroring `slack.mention_only` end-to-end (config struct + v2→v merge-list per `project_config_key_merge_list` + `init.go` env export with env-wins drift WARN → terragrunt `get_env` → TF var → Lambda env → bridge parses). Each install keeps its own per-prefix SSM paths; the operator pastes the SAME App's xoxb + signing secret into each install's normal (unchanged) `km slack init`, so every bridge can verify Slack's HMAC and re-verify a forwarded request. Runtime: every bridge runs single-hop broadcast-on-miss — verify signature, `FetchByChannel` against own `{prefix}-sandboxes`; on a HIT process as today; on a MISS POST the verbatim body + `X-Slack-Signature` + `X-Slack-Request-Timestamp` + `X-KM-Relayed:1` to all peer `/events` URLs (parallel, bounded ~2.5s, synchronous before returning 200 so Slack's 3s ack holds), then 200. A relayed request is TERMINAL — processed if owned, dropped (`slack_relay_no_owner`) otherwise, NEVER re-relayed — so `X-KM-Relayed` is the entire loop guard and loops are structurally impossible (single-hop chosen over multi-hop chain). Correctness invariant: channel name/alias uniqueness across all installs/operators (per-sandbox `#sb-{id}` and single-owner channels route unambiguously; multi-install shared channels like `#km-notifications` stay notify-only). New `pkg/slack/bridge/relayer.go` (`PeerRelayer`/`HTTPPeerRelayer`) injected into `EventsHandler` (nil ⇒ federation off ⇒ byte-identical to today; injection point is the `FetchByChannel` miss at `events_handler.go:189`). `km doctor` gains peer-bridge validity / self-loop / empty-list-on-front-door checks. No shared infra, no SandboxProfile schema change, no sandbox recreate; deploy is a Lambda env-block change ⇒ `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`), same constraint as `slack.mention_only` (`project_km_init_lambdas_doesnt_deploy`, `project_km_init_skips_existing_lambda_zips`). Spec: `docs/superpowers/specs/2026-06-05-slack-federated-bridge-relay-design.md`. Depends on Phase 94.
- Phase 96 added: Slack default router — helpful reply for orphan-channel @-mentions. Builds on Phase 95. Problem: after Phase 95 an @-mention of the shared bot in a channel no install owns is silently dropped (`slack_relay_no_owner`); the human gets no response. Goal: a designated `slack.default_router` install posts one threaded reply explaining no sandbox is bound, showing the `#sb-{alias}-{profile}` convention, and listing currently-running sandbox channels across ALL installs as `<#CID>` mentions. Brainstormed 2026-06-05; key realizations that shaped it: (1) one App ⇒ only the front door receives raw Slack events, so only the front door can detect an orphan + reply — `slack.default_router` is effectively a front-door toggle; (2) to know "nobody owns it" the Phase 95 fire-and-forget broadcast must become a CLAIM-AWARE SCATTER-GATHER — relayed-request handler returns `200 {claimed:bool, channels:[…]}` (non-owner peer also returns its running sandbox channels via `km-sandboxes` state=running query), front door tallies, zero claims ⇒ true orphan; (3) realistic trigger is member-channels-only (Slack delivers message events only for channels the bot is in), so in-channel `chat:write` suffices — NO new scopes, NO app_mention, NO manifest change. Trigger requires ALL of: bot-mention (reuse Phase 91 detection) + local FetchByChannel miss + zero peer claims. Threaded reply with per-channel cooldown (reuse pause-hint mechanism, 3600s). ROLLOUT SAFETY: a peer still on Phase-95 code returns legacy plain `"ok"`; front door treats any unparseable/legacy/error response as `claimed:true` (never post a false "no sandbox here") → mixed-version fleet safe. `default_router` defaults false ⇒ dormant/byte-identical to Phase 95 when off. Config plumbed `slack.default_router *bool` → `KM_SLACK_DEFAULT_ROUTER` mirroring `slack.mention_only` (struct + merge-list per `project_config_key_merge_list` + init.go export + drift WARN → terragrunt → TF → Lambda env). Anti-loop: existing self-message/`isBotLoop` filter drops the bot's own reply. OUT OF SCOPE (deferred): agentic self-serve create (`@bot spin me up profiles/patch.yaml` → bridge triggers `km create` via EventBridge — the north star, separate future phase), non-member channels (`app_mention`+`chat:write.public`), DM fallback (`im:write`). No schema change, no sandbox recreate; deploy `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`), all installs before relying on cross-install lists. Spec: `docs/superpowers/specs/2026-06-05-slack-default-router-design.md`. Depends on Phase 95.
- Phase 97 added: GitHub comment-trigger MVP — @-mention dispatch to a per-repo sandbox (PR review). The GitHub-shaped twin of the Slack inbound path: a new `km-github-bridge` Lambda receives `issue_comment` (action=created) webhooks, verifies `X-Hub-Signature-256` over the raw body (constant-time), drops bot/self events (loop guard) + dedupes on `X-GitHub-Delivery` (nonces table), detects an @-mention of the cached bot-login, authorizes `sender.login` against a deny-by-default per-repo allowlist, resolves `owner/repo → {alias, profile}` from a new `github.repos:` block in `km-config.yaml` (exact-before-glob, alias defaults `gh-{owner}-{repo}`), looks up the sandbox via the `alias-index` GSI, and either enqueues to a per-sandbox `github-inbound` FIFO (warm) or publishes a `SandboxCreate` EventBridge event carrying the pending prompt (cold, Phase 86 prompt-queue), then mints an installation token (reuses `pkg/github/token.go`), posts a 👀 reaction, returns 200 in the ~10s window. Brainstormed 2026-06-06 from "@ the bot in a GitHub message → dispatch to an alias'd sandbox → claude/workflow actions → PR edits/comments, create the sandbox if missing"; merged with an operator-provided spec (`klanker-maker-github-app-pr-review-spec.md`) that pinned the event model (can't assign an App bot ⇒ @-mention; `issue_comment` not `pull_request`; distinguish PR via `payload.issue.pull_request`; loop guard on `comment.user.type==Bot`). KEY LEVERAGE: the GitHub App auth already exists — `km configure github` stores `/km/config/github/{app-client-id,private-key,installation-id}`, `pkg/github/token.go` mints App JWT → installation token, `generateAndStoreGitHubToken` writes a per-sandbox token to SSM `{prefix}sandbox/{id}/github-token`, and the git credential helper (`pkg/compiler/userdata.go`) already does `git push` — so Phase 97 EXTENDS the existing App (adds issues/pulls/contents/checks write scopes + `issue_comment` webhook in one reconfigure; check/PR-create verbs deferred to 98) rather than building GitHub auth from scratch. New `spec.notification.github.inbound.enabled *bool` provisions the per-sandbox queue + source-aware poller (clone of `create_slack_inbound.go`); poller builds a GitHub context preamble (repo/PR/branch/head + worktree-per-PR guidance) + dispatches to the agent. New `km-github` sandbox helper (`comment`, `review` verbs) uses the per-sandbox installation token. Lean built-in `github-review` profile + `km github init/manifest/status` + `km doctor` checks. Absent `github:` config ⇒ fully dormant. Deploy: `make build-lambdas` + `km init --dry-run=false` (new Lambda + EventBridge + env-block ⇒ full apply) + `km init --sidecars` (schema field for create-handler); existing sandboxes need `km destroy && km create`. Sibling of the Phase 96 "agentic self-serve create" north star, reached via GitHub instead of Slack. Spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md`. Depends on Phase 96.
- Phase 98 added: GitHub bridge expansion — richer write-backs, thread continuity, shared-alias. Builds on Phase 97. Extends `km-github` with `check` (check-run gating) and `pr create` (open PR / push new branch) verbs + push-commit hardening (App write scopes already landed in 97); adds thread/session continuity (`(repo, number) → {sandbox_id, agent_session_id}` mapping, generalizing `km-slack-threads` or a sibling table) so follow-up @-mentions continue the same agent session, plus a thread-bypass for replies in known threads (mirrors Phase 91.3); adds shared-alias across repos (several `github.repos:` entries → one larger shared sandbox with worktree-per-PR isolation + `km doctor` overlap/collision warnings). Optionally widens the trigger to `pull_request_review_comment` inline-diff comments (deferred from 97). Spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md`. Depends on Phase 97.
- Phase 99 added: GitHub bridge commands — config-defined `/commands` mapping to prompt templates + env routing. Builds on Phases 97/98. A `github.commands:` block in `km-config.yaml` defines named commands, each bundling a prompt template (inline or `@file` loaded at `km init` time), an optional routing override (`alias`/`profile`), and an optional per-command user allowlist. A user invokes one with `@klanker-maker … /name …` placed ANYWHERE in a PR/issue comment. Brainstormed 2026-06-07 from "map a comment verb like `/patch` to a defined prompt+behavior and route between envs/sandboxes"; key decisions: command CONTROLS prompt + routing only (guardrails out — `spec.agent.tools` on the target profile already gates read-only vs write); routing model is COMMAND-OVERRIDES-REPO (`command.alias||repo.alias` / `command.profile||repo.profile||default_profile`), so the command pass just swaps which `{alias,profile,prompt}` tuple feeds Phase 98's existing warm/resume/cold-create dispatch UNCHANGED; parse-ANYWHERE (not anchored) after stripping fenced/inline code, scan whitespace-bounded `/name` tokens, embedded-slash tokens like `/usr/bin/patch` excluded; SINGLE-command-per-comment (>1 distinct known command = error reply, no dispatch; same-command repeats deduped); LENIENT on unknown tokens (treated as plain text → free-form dispatch, no help spam — paired deliberately with parse-anywhere to avoid false-positive friction); `{{args}}` = comment minus mention + command tokens, whitespace-normalized; AUTH is deny-by-default outer (`repo.allow` gates engagement, silent drop) + inner narrowing (`command.allow` intersects — effective `repo.allow ∩ command.allow`, known-user-fails gets a polite "not authorized" reply); built-in `/help` lists commands. Command-less comments run an optional configurable `default_command` (per-repo `repos[].default_command` overrides install-wide `github.default_command`; `{{args}}` = comment minus mention; unset ⇒ free-form passthrough — added 2026-06-07 follow-up), with `km init`/`km doctor` erroring when a `default_command` names an undefined command. Command set (with `@file` templates inlined) published to SSM `{prefix}/config/github/commands` — NOT a Lambda env var — to dodge the 4 KB env ceiling (`KM_GITHUB_REPOS` already eats into it); bridge reads at cold start alongside existing `{prefix}/config/github/{webhook-secret,bot-login,bridge-url}`. Config plumbed via struct + v2→v merge-list (`project_config_key_merge_list`) + `km init` `@file` resolution/drift-WARN; `km doctor` validates `@file` existence, profile resolvability, `help`-shadow, command↔repo alias overlap (extends 98-03), SSM-param presence; `km github status` lists commands. NO SandboxProfile schema change ⇒ no `km init --sidecars`, no sandbox recreate; absent `github.commands` ⇒ byte-identical to Phase 98. Deploy: `make build-lambdas` + `km init --dry-run=false`. Spec: `docs/superpowers/specs/2026-06-07-github-bridge-commands-design.md`. Depends on Phase 98.
- Phase 100 added: GitHub bridge federated relay — one GitHub App serving many `resource_prefix` installs (e.g. kph + sec). Direct analog of Slack Phase 95 (`slack.peer_bridges`). Brainstormed 2026-06-07 from "how does GitHub work across kph/sec prefix namespaces — can certain requests map to kph and others to sec?". Problem: a GitHub App has exactly ONE webhook URL, but each install runs its own `{prefix}-github-bridge` (own Function URL + own SSM `/{prefix}/config/github/` + own `github.repos:`, dispatches only into its own `{prefix}-sandboxes`). Two supported patterns: (A) TWO GitHub Apps — one per install, each webhook → its own bridge, repos split by which App is installed where (works today, documented in `docs/github-bridge.md` § Multi-install); (B) ONE App + federated relay (Phase 100). Mechanism (mirrors Slack 95): opt-in `github.peer_bridges []string` in km-config.yaml → `KM_GITHUB_PEER_BRIDGES` env (struct + v2→v merge-list per `project_config_key_merge_list` + init.go export + drift WARN → terragrunt → TF → Lambda env); operator points the App webhook at one "front door" install whose bridge, on a `Resolve()` ownership MISS (`webhook_handler.go:216` `!matched`), broadcasts the verbatim webhook to peer bridge Function URLs; the install whose `github.repos:` matches processes it. KEY DECISIONS: scope = routing relay ONLY (Phase-95 analog) — the orphan-repo helpful-PR-comment (Phase-96 analog, claim-aware scatter-gather) DEFERRED to a future Phase 101; routing-by-command-on-same-repo-to-different-prefix OUT entirely (no cross-prefix dispatch). ONE structural delta vs Slack drop-in: `Resolve()` ownership moves AHEAD of the mention/thread filter so a peer-owned known-thread follow-up with NO @-mention (Phase 98 thread-bypass) still relays; mention/thread/auth/dedupe/dispatch run only on the matched path; each peer re-runs full `Handle()` incl. its own thread-bypass. This reorder is UNCONDITIONAL (applies even with `peer_bridges` empty) and FOLDS IN the 700-repo scale finding (QQ 2026-06-07): today the thread-continuity `LookupSandbox` DDB GetItem runs for every created PR comment BEFORE the mention/ownership gates, so an App on ~700 repos does a DDB read per PR comment org-wide even for repos never in `github.repos`; after the reorder an unconfigured/unowned-repo comment short-circuits at the in-memory config match with NO DDB read (byte-identical dispatch — a thread row only ever exists for an owned repo — just removes wasted reads; single-install benefits equally). Requirement GH-FED-SCALE + success criterion 10 added. Relay forwards body + `X-Hub-Signature-256` + `X-GitHub-Event` + `X-GitHub-Delivery` + `X-KM-Relayed:1`; operator pastes the SAME App webhook secret into each install's `km github init` so peers re-verify HMAC (GitHub sigs have no timestamp → no skew window). Loop guard `X-KM-Relayed:1` = single-hop terminal (processed if owned, dropped `github_relay_no_owner` otherwise, never re-relayed). Per-install `{prefix}-nonces` dedupe the forwarded GUID; OWNER posts the single 👀 (front door on miss just relays, no reaction). Synchronous bounded broadcast (~5s; GitHub's ~10s ack window roomier than Slack's 3s). Correctness invariant: each repo owned by exactly one install (unique `github.repos:` match) — documented not enforced (doctor can't read peers' configs), mirrors Slack channel-uniqueness. `km doctor` peer-URL-validity / self-loop / empty-on-front-door checks. NO schema change ⇒ no `--sidecars`, no recreate; absent `github.peer_bridges` ⇒ byte-identical to 97/98. Deploy `make build-lambdas` + `km init --dry-run=false` (env-block ⇒ full apply). Independent of Phase 99 (depends on 97/98). Spec: `docs/superpowers/specs/2026-06-07-github-bridge-peer-relay-design.md`. Depends on Phase 98.
- Phase 101 added: GitHub bridge orphan-repo helpful reply — the deferred Phase-96 analog, registered 2026-06-07 to make the "future Phase 101" references in the Phase 100 spec/docs concrete. After federation (Phase 100), an @-mention on a repo NO install owns is silently dropped (`github_relay_no_owner`); goal is the front-door install posts ONE helpful PR/issue comment (no sandbox bound + how to wire `github.repos:`). Mechanism (mirrors Slack 96): upgrade Phase 100's fire-and-forget broadcast to CLAIM-AWARE SCATTER-GATHER (peers return `200 {claimed:bool}`, zero claims ⇒ true orphan ⇒ comment); per-(repo,number) cooldown via nonces table; rollout-safe (Phase-100 peer returns bodiless 200 → treat as claimed:true, never a false "nobody owns this"); front-door-only toggle `github.default_router`. Dormant by default ⇒ byte-identical to Phase 100. Depends on Phase 100. Spec to be written at plan time (mirrors `2026-06-05-slack-default-router-design.md` + Phase 100 deferred note). Requirements GH-ORPHAN-CLAIM/REPLY/COOLDOWN/ROLLOUT/E2E.
- Phase 102 added: GitHub bridge agent verbs — `/claude` & `/codex` per-thread agent selection (Slack Phase 70 analog). User ask 2026-06-07: "add a /codex verb that makes the thread codex, similar to slack." Today GitHub dispatch always runs the profile-default agent (`userdata.go:2248` `EFFECTIVE_AGENT="$AGENT"`), though the poller ALREADY has the Claude/Codex dispatch fork + captures both session types (`userdata.go:2253-2317`) — so this is a small lift. KEY DECISIONS: slash verbs `/claude` `/codex` (consistent w/ Phase 99 `/command` tokens, parsed anywhere; NOT Slack's `codex:` colon prefix), reserved built-ins like `/help`; SEPARATE AXIS from Phase 99 template commands that COMPOSES (`/codex /patch …` = Codex + patch template); PERSISTENT per-thread (new `agent_type` column on `km-github-threads`, schema-on-write so no TF/migration; follow-ups continue with it); precedence verb > thread `agent_type` > profile default; ≤1 agent verb (two = error reply); SINGLE `agent_session_id` column RESET on cross-agent switch (fresh session + overwrite; switching back = fresh — NO Slack-style 8-step new-top-level handoff because the PR IS the thread); `/codex` needs a Codex-capable profile (lean `github-review` is Claude-only) → documented precondition + runtime helpful-error comment, not a stranded turn; `claude`/`codex`/`help` reserved (github.commands shadow → doctor WARN). Plumbing: `GitHubEnvelope` gains `agent`; bridge parses+strips verb; poller computes `EFFECTIVE_AGENT` + writes `agent_type` back. No verb ⇒ byte-identical. Deploy: poller in `userdata.go` ⇒ `make build-lambdas` + `km init --dry-run=false` + existing sandboxes `km destroy && km create`; bridge verb-parse same redeploy. Depends on Phase 99 (parser) + Phase 98 (threads); independent of 100/101. Spec: `docs/superpowers/specs/2026-06-07-github-bridge-agent-verbs-design.md`. Requirements GH-AGENT-VERB/PERSIST/SWITCH/POLLER/PROFILE/E2E.
- Phase 103 added: HackerOne comment-trigger bridge — `km-h1-bridge` Lambda (GitHub-bridge Phase 97-102 analog for HackerOne). User ask 2026-06-09: "HackerOne webhook → build an h1-bridge that responds/triggers a sandbox prompt just like the @github concept." A single Lambda Function URL receives HackerOne program webhooks, HMAC-verifies `X-H1-Signature` (`sha256=<hex>` HMAC-SHA256 of raw body — same scheme as GitHub's `X-Hub-Signature-256`, so `VerifyGitHubSignature()` ports verbatim with a header swap), dedupes by `X-H1-Delivery` GUID in the shared `{prefix}-slack-bridge-nonces` table, resolves the report's PROGRAM HANDLE (in `data.report` relationships) to `{alias, profile, allow}` from a new `h1.programs:` block in km-config.yaml (analog of `github.repos:`), and dispatches a sandbox agent turn via the existing 3-way warm-FIFO / cold-EventBridge-SandboxCreate / resume path, with REPORT-ID-keyed thread continuity. KEY DECISIONS / user requirements (2026-06-09): (1) BOTH trigger models required — event-driven auto-triage on `report_created` (+ configurable lifecycle events) AND comment-keyword on `report_comment_created` (scan body for trigger token / slash-command, analog of GitHub @-mention scan). (2) CONFIG-DRIVEN event→prompt mappings in km-config.yaml — map H1 event type → prompt kind/template, with potentially DIFFERENT `/command` sets for the comment context vs the report-generation (auto-triage) context. (3) REPLY VISIBILITY — replies are INTERNAL (team-only) BY DEFAULT; an explicit `/reply_to_researcher` command is required to post a researcher-visible (non-internal) reply; `km-h1` helper supports both internal + public flags, default internal to avoid accidentally messaging hackers. Back-channel is far SIMPLER than GitHub: HackerOne customer API Basic Auth (API username+token in SSM), `POST /reports/{id}/comments` (with internal flag), `PATCH /reports/{id}/state`, `GET /reports/{id}` — NO App-JWT/installation-token dance, NO per-sandbox token refresher. New CLI: `km h1 init` (mint+store webhook secret + Basic Auth creds in SSM, print Function URL + secret to paste into the H1 program UI — NO App manifest since H1 has no App-install model) + `km h1 status`. New `pkg/h1/bridge` (port of `pkg/github/bridge`), `cmd/km-h1-bridge` Lambda, `cmd/km-h1` sandbox helper, `infra/modules/lambda-h1-bridge` TF module added to `regionalModules()` in init.go + `lambdaBuilds()` (remember `make build` the km binary before `km init` per `project_make_build_precedes_km_init`). Federated relay (Phase 100/101 analog) OUT OF SCOPE. Sources: HackerOne webhooks docs + customer API. Spec to be written at plan time.
- Phase 104 added: Slack channel O(1) resolution on alias reuse — kill the unbounded `conversations.list` scan that wedges create. Incident 2026-06-10: bringing up the `github-bot` warm box (`profiles/github-review.yaml`, per-sandbox Slack, `archiveOnDestroy:false` ⇒ every recreate hits `conversations.create → name_taken`) hung the create-handler for the full 900s and stranded the sandbox in `starting`; `slack.enabled:false` made create succeed in ~2 min ⇒ the hang is entirely in Slack channel resolution (Step 6c). Root cause confirmed against code: `resolveExistingChannelID` (`create_slack.go:152`) gates the SSM by-name cache hit on `conversations.info(cachedID)==nil-err`, and `ChannelInfo` (`client.go:749`) returns the RAW error with no `channel_not_found` classification — so ANY transient info error (a momentary `ratelimited`/5xx/context blip) falls through to `FindChannelByName` (`client.go:605`), a bare `for{}` with `limit:1000`, `exclude_archived:true`, NO page cap and NO sub-deadline (freshly-created channels sort LAST ⇒ walks every page); the client's per-request timeout is only 10s (`NewClient(token, nil)` at `create.go:597`) so the 15-min wedge is the many-page walk + retries, not a single hung request. KEY FINDINGS that shaped the design: (1) the incident is NOT a cache MISS — the by-name value was present+correct (`/sec/slack/channel-id-by-name/sb-github-bot-sec`=`C0B91RA9CPR`); the defeater is the info-gate fall-through, so the fix is "bound + classify," not "populate cache." (2) The existing SSM by-name cache ALREADY survives destroy (per-sandbox SSM deletes in `destroy.go:374/720` are keyed by `sandbox_id`; the by-name key is keyed by channel NAME ⇒ never cleaned) and is alias-derived ⇒ it is already a durable cross-recreate mapping. (3) Despite (2), operator chose a DEDICATED `km-slack-channels` DDB table (PK `alias`, no TTL) over hardening SSM alone — rejected storing on `km-sandboxes` because destroy DELETES that row (`destroy.go:583/779`) and `ListAllSandboxesByDynamo` SCANS the table (`sandbox_dynamo.go:518`) so a synthetic item would pollute `km list`. DESIGN (spec `docs/superpowers/specs/2026-06-10-slack-channel-reuse-o1-resolution-spec.md`, A+C+B layered): P0 wall-clock sub-context (`KM_SLACK_RESOLVE_BUDGET`=45s) + page-capped scan (`KM_SLACK_MAX_SCAN_PAGES`, default 0=OFF ⇒ fail-fast with `km slack adopt`/`channelOverride` guidance, never unbounded); P1 lookup-first BEFORE `conversations.create` + classify info errors (only `channel_not_found` invalidates; transient ⇒ bounded-retry 2× then optimistic-use, never enumerate); P2 durable `km-slack-channels` table read-first + write-through on create/resolve (SSM by-name kept as back-compat fallback) + `km slack adopt <alias> <channelID>` (validate `^C[A-Z0-9]+$` + bot membership + DDB/SSM write-through). Observability: `slack_resolve path=cache_hit|cache_optimistic|created|scan_capped|failfast ms=…` (ships the spec's Phase 0 defeater-confirmation inline). Bridge is NOT a consumer ⇒ no `lambda-slack-bridge` changes; NO SandboxProfile schema change ⇒ no `--sidecars`, existing sandboxes unaffected (create-time fix). Deploy surface (new table): TF module `dynamodb-slack-channels/v1.0.0` + live unit + `init.go` regionalModules entry + create-handler IAM (km-operator-policy var+policy → create-handler var+wiring → live input) + `GetSlackChannelsTableName` config getter (+ v2→v merge-list per `project_config_key_merge_list`) + `pkg/aws.SlackChannelStore` helper; `make build` the km binary BEFORE `km init` (`project_make_build_precedes_km_init`), then `make build-lambdas` + `km init --dry-run=false` (new table + IAM + env-block ⇒ full apply, NOT `--sidecars`). 5 plans (104-01 P0+P1 core → 104-02 table+live+init.go → 104-03 IAM+config+store+wiring → 104-04 adopt+doctor → 104-05 docs+deploy-audit+live UAT). Implementation plan: `docs/superpowers/plans/2026-06-10-slack-channel-o1-resolution.md`. Independent of Phase 103.
- Phase 103.1 added (in-place FOLLOWUP, planned 2026-06-10): `km doctor` HackerOne bridge checks. Operator-requested fast-follow after Phase 103 docs flagged the gap (`docs/h1-bridge.md` § Troubleshooting). New `internal/app/cmd/doctor_h1.go` mirroring the GitHub doctor group (`doctor.go::checkGitHub*`): `checkH1WebhookSecret` / `checkH1APICreds` (api-username+api-token) / `checkH1BridgeURL` (valid HTTPS) / `checkH1ProgramsValidity` (pure-config: handle+≥1 target, default_command names a real command, comment-keyword-only program has a bot_handle). All gated DORMANT — SKIP silently when `h1.programs` empty (mirror `doctor.go:265`). Adds `GetH1Programs()` to DoctorConfigProvider + adapter. Inbound DLQ already covered generically by `doctor_inbound_dlq.go` (confirm at exec). Plan: `.planning/phases/103-*/103-103.1-FOLLOWUP.md`. NOT yet executed.
- Phase 105 added: Scoped `km init` for bridge config — `km init --only <module>` (+ sugar `--github` / `--slack` / `--h1`) applies ONE terragrunt module instead of the full ~27-module loop. User ask 2026-06-11: after the Slack-auto-start discussion, "is there a way to have an update just focus on the github or h1 sections… those don't need full sidecars, it's mostly forcing a cold start + update SSM?" Investigation (this session) established the real mechanism: for the github/h1/slack CONFIG keys (`github.repos`/`peer_bridges`/`default_router`/`default_profile`, `h1.programs`/`bot_handle`/`default_profile`, `slack.mention_only`/`react_always`/`peer_bridges`/`default_router`) the only thing a full `km init` does is recompute a handful of `KM_*` env vars and write them into ONE Lambda's `environment.variables` block — NOT SSM (github/h1 config lives in the Lambda env block; SSM is a Slack-token thing). Pipeline: `km-config.yaml → ExportTerragruntEnvVars(cfg) → KM_GITHUB_*/KM_H1_*/KM_SLACK_* (OS env) → terragrunt.hcl get_env() → Lambda module var → env block` (env-var export at `init.go:1071-1360`; github env block `infra/modules/lambda-github-bridge/v1.1.0/main.tf:272-290`; h1 `infra/modules/lambda-h1-bridge/v1.0.0/main.tf:297-313`; slack `lambda-slack-bridge/v1.0.0/main.tf`). KEY DECISION: chose Option A (SCOPED TERRAGRUNT APPLY — filter the existing `regionalModules()` loop at `init.go:205-372` to one module + still run `ExportTerragruntEnvVars` so the module recomputes its env block) over Option B (direct `UpdateFunctionConfiguration` via the existing private `upsertLambdaEnvVar()` at `init.go:3420`, used today by `--sidecars` for `TOOLCHAIN_VERSION` + `km slack rotate-token` for `TOKEN_ROTATION_TS`). Rationale: Option A stays fully inside terraform state ⇒ ZERO drift (Option B creates benign-but-real drift, safe ONLY if it derives values from the same yaml→KM_* pipeline so a later full apply is a no-op — an invariant easy to violate), picks up IAM changes too (relevant: the deferred Slack-auto-start `ec2:StartInstances` permission would ride along), generalizes to any module, and is LESS new surface (filter an existing loop vs a parallel deploy path). BOUNDARY (must document): scoped apply covers env + IAM for that module but NOT a stale code zip (still `make build-lambdas` + `--lambdas`) and NOT new resources/wiring (new table/queue ⇒ full `km init`). Surface: new `--only <module>` flag + `--github`/`--slack`/`--h1` aliases resolving to `lambda-github-bridge`/`lambda-slack-bridge`/`lambda-h1-bridge` in `NewInitCmd()` (`init.go:569`); routing guard in the flag-dispatch block (`init.go:583-601`) — mutually exclusive with `--sidecars`/`--lambdas`/`--plan`; a `runInitScoped()` that reuses the existing module loop (`RunInitWithRunner` `init.go:1794-1999`) filtered to the selected module dir (upstream `outputs.json` already exist on a live install ⇒ deps resolve from mocks). SCOPE DECISION (operator, 2026-06-11): `--only` is validated against a CURATED ALLOWLIST, NOT all ~27 `regionalModules()`; unknown/out-of-allowlist value errors and prints the allowed set. Operator goal is fast iteration on slack/github/h1/email config edits; full-fleet access is "too much rope." TWO-TIER allowlist (operator chose "email-handler + gated ses" 2026-06-11): TIER 1 CHEAP (env+IAM, no destroy-class resources, no confirmation) = `lambda-github-bridge`/`lambda-slack-bridge`/`lambda-h1-bridge`/`email-handler`, exposed via sugar `--github`/`--slack`/`--h1`/`--email` — all four are Lambdas with an `environment { variables }` block (confirmed `email-handler/v1.0.0/main.tf:247`) so identical safety. TIER 2 GATED = `ses` (DNS/identities/receipt rules), reachable ONLY via explicit `--only ses` with NO cheap alias, MUST route through the destroy-class safety gate (`ses/v2.0.0/main.tf` manages `aws_ses_domain_identity`/`aws_ses_domain_dkim`/`aws_route53_record`{dkim,mx,ses_verification}/`aws_ses_receipt_rule`/`aws_s3_bucket_policy`; it's also the LAST module + owns the consolidated bucket policy ⇒ scoped-alone apply has a dependency-freshness wrinkle on that policy — note at plan time). `--email` ⇒ `email-handler` ONLY; the SES/DNS layer is the separate `--only ses` path. Tier-2 reuses the `km init --plan` curated destroy-class trip-block (`RunInitPlanFunc`) but as a PRE-APPLY gate (refuse protected destroy/replace without `--i-accept-destroys`), not a standalone plan — confirm wiring at plan time. Implement as two named slices (cheap vs gated) so future targets are a one-line add + tier choice. No SandboxProfile schema change, no new TF resource ⇒ operator-side CLI-only change; deploy = `make build` the km binary. Spec to be written at plan time. Depends on nothing structural; relates to the Slack-auto-start idea discussed same session (separate future phase).
- Phase 106 added: Session-resume hint on GitHub + HackerOne bridge replies (post-on-mint). User ask 2026-06-11: "add logic to github replies that include the --resume details for the given bot claude/codex" + "where on the github-bot sandbox would I run the resume from? I believe the slacks are now in /home/sandbox". Brainstormed to a settled design. GOAL: after a bridge agent turn, post ONE extra collapsed `<details>` comment carrying the operator resume handle (run-from dir + agent-correct resume command + sandbox id + session id) so the operator can re-attach without querying DDB. SCOPE: GitHub + HackerOne pollers ONLY — Slack deliberately EXCLUDED (operator can ask interactively in the Slack chat; no value pushing it into every reply). KEY FACTS established this session: (1) run-from dir is `/workspace` NOT `/home/sandbox` — every dispatch does `cd /workspace` (`userdata.go:2329/2305` GH, `2616/2627/2639` H1) but `HOME=/home/sandbox` (`:3208`, SANDBOX_HOME `:3089`); session transcript FILES live at `/home/sandbox/.claude/projects/-workspace/<id>.jsonl` but Claude keys the project bucket off CWD ⇒ `--resume` MUST run from `/workspace` or "No conversation found". So the operator's "/home/sandbox" hunch is half-right (storage) but the hint must say `/workspace` (run-from). (2) Injection point = the POLLER, not the agent — the agent posts its own reply mid-run via `km-github comment`/`km-h1` BEFORE the session id exists; the authoritative `NEW_GITHUB_SESSION`/`NEW_H1_SESSION` is extracted only post-run (`userdata.go:2375-2380` GH; H1 ~`2660`), so the poster of the hint must be the poller right after extraction/DDB write-back (~`2391`). ⇒ a SECOND collapsed comment per qualifying turn. Alternatives rejected: pre-mint `claude --session-id` (Codex can't pre-set thread_id for fresh threads → inconsistent); PATCH the agent's comment (needs its comment id, not surfaced). (3) FREQUENCY = POST-ON-MINT, not every-turn and not strictly turn-1: post the fold only when the id is newly minted — no prior stored session (true first turn) OR `NEW_*` differs from the pre-run stored value (Gap-E/cross-box re-mint, `userdata.go:2336-2347`). Stable common case (Claude keeps id on `--resume`; Codex thread_id stable) ⇒ fires exactly once per thread; self-heals if it ever rotates. Poller holds old+new at the write-back site ⇒ one-line `if`. Operator chose post-on-mint over every-turn (noise) after noting resumed posts already carry full thread context (evidence resume works / id stable). (4) Agent branch on `EFFECTIVE_AGENT`: Claude → `claude --resume <id>`, Codex → `codex exec resume <id>`. (5) H1 SAFETY BONUS: `km-h1` posts INTERNAL by default ⇒ resume hint lands on the internal/team comment, never visible to the external researcher; GitHub PR comments ARE visible to all collaborators but the collapsed fold is the agreed mitigation (ids not exploitable without AWS/SSM). (6) Sandbox id for the hint comes from the poller's own env (already known — `sandbox_id` written to threads tables); confirm exact var at plan time. SURFACE: `pkg/compiler/userdata.go` GH poller block (~`2382`) + H1 poller block (~`2660`); NO change to `km-github`/`km-h1` Go helpers (pollers call as-is with constructed body); Slack poller (`1535-2085`) byte-identical. Best-effort `|| true` (failed hint never blocks SQS ack). Deploy: poller compiled into userdata ⇒ existing sandboxes need `km destroy && km create`; bridge Lambdas/IAM/TF UNAFFECTED; `make build-lambdas` (create-handler compiles userdata) + recreate; confirm at plan time whether GH/H1 userdata byte-identity golden tests need a deliberate golden refresh. No SandboxProfile schema change, no new TF resource, no DDB schema change (reuses `agent_session_id`/`agent_type`). Depends on Phases 97/102 (GH session extraction + `km-github-threads`) and 103 (`km-h1` INTERNAL-default + session continuity); no structural dep on 105.
- Phase 107 added: Reconcile 22 stale `internal/app/cmd` unit tests with current production behavior (test-hygiene only). Discovered 2026-06-11 during Phase 105 close-out: a piped exit code (`go test … | tail`) masked a real `go test` FAIL — the cmd package full suite is RED. Investigation (this session) established 22 deterministic failures across 12 subsystems (shell ×5, email ×4, uninit ×3, state-bucket guards ×4, create/docker ×2, agent-auth/at/efs/learn ×4), VERIFIED PRE-EXISTING — identical failing set on `a0e33fa8` (Phase 104 complete, pre-105 origin tip) and post-105 HEAD `97899062`; Phase 105 introduced ZERO of them. Root cause = STALE ASSERTIONS from production drift (e.g. `TestShellDockerContainerName` expects `/bin/bash`, code emits `bash --login`; `TestShellDockerNoRootFlag` expects no `-u`, code always adds `-u sandbox`; `TestListCmd_EmptyStateBucketError` expects an error on empty bucket, code returns nil; `TestLockCmd_RequiresStateBucket` hits a different error path). KEY DECISION (operator ask 2026-06-11 "make a hygiene phase for the 22 stale tests"): TEST-HYGIENE ONLY — production behavior is the source of truth, update each stale assertion to match current code; do NOT change production code to satisfy an old test. CAVEAT: a few may be REAL lost guards not stale tests — esp. `TestListCmd_EmptyStateBucketError` / the *RequiresStateBucket guards (code returning nil instead of erroring could be a genuinely-dropped validation). Per-test TRIAGE first: stale-test ⇒ fix test here; code-regression ⇒ do NOT silently fix, ESCALATE to operator as a separate bug. Verify via `go test ./internal/app/cmd/ -count=1` reading go test's OWN exit (not a pipe — see memory feedback_check_go_test_exit_not_pipe); confirm no NEW failures + Phase 105 TestScoped*/TestRunInitPlan_ModuleOrder still green. Parallelizable by subsystem (~10 separate *_test.go files, no shared fixtures). Independent of Phases 105/106 (touches only `internal/app/cmd/*_test.go`). NOTE: rostered while a PARALLEL-session Phase 106 sits uncommitted in ROADMAP.md/STATE.md — 107's roadmap/state edits share those files; commit handling deferred to operator (see session note).

## Session Continuity

Last session: 2026-06-12T02:10:06.818Z
Stopped at: Completed 107-07-PLAN.md — shell pre-flight error fix; TestShellCmd_Stopped/Unknown/MissingInstance all PASS
Resume file: None
