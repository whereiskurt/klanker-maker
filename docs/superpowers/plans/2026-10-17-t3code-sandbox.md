# `km t3` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `km t3 start|stop|status|<connect>` runs T3 Code as a background service on a sandbox, reached only over SSM, with km-presence signal 9 keeping the box awake only while a client is connected or a turn is running.

**Architecture:** Operator-side Cobra verbs (`internal/app/cmd/t3.go`) drive one POSIX-sh SSM script per verb, the herdr pattern. The server is a km-owned system unit written by `start`. km-presence gains signal 9 (port-3773 socket OR an authenticated `/api/orchestration/shell` busy probe) and drops t3-cgroup pids from signal 5.

**Tech Stack:** Go, Cobra, aws-sdk-go-v2 SSM, systemd, `ss`, t3 v0.1.0.

**Spec:** `docs/superpowers/specs/2026-10-17-t3code-sandbox-design.md`

## Global Constraints

- Remote port `3773`, a single Go const per package (`t3RemotePort` in cmd, `t3Port` in km-presence), pinned equal by a test.
- Default local port `3774`. Pinned t3 version `0.1.0`.
- Unit name `km-t3code.service`; presence token path `/etc/km/t3/presence.token` (dir 0700, file 0600, root).
- Installer URL: `https://raw.githubusercontent.com/pingdotgg/t3code/v<ver>/scripts/install.sh`, run with `T3CODE_VERSION=<ver>`.
- SSM scripts are POSIX sh (`sendSSMAndWait` runs them under dash). Anything as the sandbox user goes through `runuser -u sandbox -- bash -lc`.
- No long-lived credential in SSM output; the only token printed is the 5-minute pairing link.
- Signal checks fail idle on every error path.
- Deploy: `make build` (CLI); `km init --sidecars` (km-presence). No TF/IAM/schema/userdata change.

## Review Focus

1. **A browser left open on a server with no turns** — signal 9 true via socket only; the HTTP probe must not be called once the socket check is positive (pinned in Task 2).
2. **Server stopped / never started** — signal 9 false, no error spam, ~ms cost (Task 2: connection-refused ⇒ false).
3. **t3's claude child alive 30 min after a turn** — signal 5 must not count it (Task 1), and signal 9 must read idle (Task 2 idle-threads test).
4. **Locked egress box** — `start` fails with the named libatomic message, not a raw dnf error (Task 3 marker → error test).
5. **Connect on a box where the server is down** — refuse before opening a forward, naming `km t3 start` (Task 4).

---

### Task 1: Signal 5 ignores t3's agent children

**Files:**
- Modify: `cmd/km-presence/runner.go` (`checkAgentProcess`)
- Test: `cmd/km-presence/t3_test.go` (new)

**Interfaces:**
- Produces: package var `procRoot = "/proc"`; `func inT3Unit(pid string) bool` (reads `procRoot/<pid>/cgroup`, true iff it contains `/km-t3code.service`; false on read error).

- [ ] **Step 1: Failing tests** in `t3_test.go`, using a `t.TempDir()` as `procRoot` with `<pid>/cgroup` files:
  - `TestSignal5_ExcludesT3UnitChildren`: pgrep output `"101 /usr/bin/claude --output-format stream-json\n"`, `101/cgroup` = `0::/system.slice/km-t3code.service\n` ⇒ `checkAgentProcess` false.
  - `TestSignal5_CountsAgentsOutsideT3Unit`: same plus `202 claude -p hi`, `202/cgroup` = `0::/user.slice/user-1001.slice/session-3.scope\n` ⇒ true.
  - `TestSignal5_UnreadableCgroupStillCounts`: pid with no cgroup file ⇒ true.
- [ ] **Step 2:** `cd cmd/km-presence && go test -run TestSignal5 .` → FAIL.
- [ ] **Step 3:** In `checkAgentProcess`, after pgrep, keep a line iff its first field's pid is not `inT3Unit`. Add a comment stating why (spec F5) next to the existing pgrep commentary.
- [ ] **Step 4:** `go test .` (whole package) → PASS.
- [ ] **Step 5:** Commit `feat(km-presence): signal 5 ignores agents inside km-t3code.service`.

### Task 2: Signal 9 — t3 in use

**Files:**
- Create: `cmd/km-presence/t3.go`
- Modify: `cmd/km-presence/runner.go` (`tick`: `s9`, "nine signals" comments), `cmd/km-presence/main.go` (header comment count)
- Test: `cmd/km-presence/t3_test.go`

**Interfaces:**
- Produces: `const t3Port = 3773`; `const t3TokenPath = "/etc/km/t3/presence.token"`; `type t3Getter func(url, token string) ([]byte, int, error)`; package var `t3GetFn t3Getter = t3HTTPGet` (2 s timeout, `DisableKeepAlives: true`, headers `Authorization: Bearer <tok>` and `x-t3-orchestration-protocol: 2`); `func t3ThreadsBusy(raw []byte) bool`; `func checkT3InUse(r commandRunner, get t3Getter, tokenPath string) bool`. `tick` calls `checkT3InUse(r, t3GetFn, t3TokenPath)` — signature of `tick` unchanged.

- [ ] **Step 1: Failing tests:**
  - `TestT3ThreadsBusy`: table — `{"threads":[]}` false; `{"threads":[{"activeRunId":null,"pendingBackgroundTasks":[]}]}` false (**the long-lived-idle-session negative**); `activeRunId:"r1"` true; `pendingBackgroundTasks:[{}]` true; `archivedThreads` with activeRunId only ⇒ false; malformed ⇒ false.
  - `TestSignal9_ClientSocketWins`: fake `ss -tnH state established ( sport = :3773 )` returns one line; getter that `t.Fatal`s if called ⇒ true.
  - `TestSignal9_BusyTurnNoClient`: empty ss; token file `"tok\n"`; getter asserts token `tok` and URL `http://127.0.0.1:3773/api/orchestration/shell`, returns busy JSON, 200 ⇒ true.
  - `TestSignal9_IdleServerIsIdle`: empty ss; idle JSON 200 ⇒ false.
  - `TestSignal9_FailsIdle`: subtests — token absent; token empty; getter error; status 401; ss error ⇒ all false.
  - `TestTick_EmitWhenOnlyT3Active`: mirror of `TestTick_EmitWhenOnlySSHActive` with only signal 9 positive (swap `t3GetFn`).
- [ ] **Step 2:** `go test -run 'T3|Signal9' .` → FAIL.
- [ ] **Step 3:** Implement. ss argv is exactly `ss -tnH state established ( sport = :3773 )` (one arg per token, built from `t3Port`). Socket check first; return before HTTP when positive. Token is `strings.TrimSpace` of the file.
- [ ] **Step 4:** `go test .` → PASS; `go vet .`.
- [ ] **Step 5:** Commit `feat(km-presence): signal 9 — t3 client connected or turn running`.

### Task 3: `km t3 start|stop|status`

**Files:**
- Create: `internal/app/cmd/t3.go`, `internal/app/cmd/t3_test.go`
- Modify: `internal/app/cmd/root.go` (`root.AddCommand(NewT3Cmd(cfg))`)

**Interfaces:**
- Produces: consts `t3RemotePort = 3773`, `t3DefaultLocalPort = 3774`, `t3Version = "0.1.0"`, `t3UnitName = "km-t3code.service"`, `t3UnitFile` (spec §5.3 verbatim); `type t3BoxState struct{ Path, Version string; Active, Enabled, Ready, PresenceToken, Signal9, LibatomicMissing bool; Providers []string }`; `func parseT3Status(out string) t3BoxState` (reuses `sectionOf`); `func t3StartScript(version string) string`; `const t3StopScript`, `const t3StatusScript`; `func t3StatusReport(w io.Writer, sandboxID string, st t3BoxState) error`; `func NewT3Cmd(cfg *config.Config) *cobra.Command` and `newT3CmdInternal(cfg, fetcher, execFn, ssmClient)`; package var `t3Run` (connect entry, for routing tests, herdrRun-style).

Markers (shared by start/status output): `=== t3 path ===`, `=== t3 version ===`, `=== unit active ===`, `=== unit enabled ===`, `=== ready ===`, `=== providers ===`, `=== presence token ===`, `=== presence signal9 ===`, `=== libatomic ===` (value `missing` when the repo install failed).

- [ ] **Step 1: Failing tests:**
  - `TestParseT3Status_*`: all-present; not installed; chatty version `t3 v0.1.0 (abc)` → `0.1.0`; libatomic missing; providers `"/usr/bin/claude\n/usr/local/bin/codex\n"`.
  - `TestT3UnitFile_LoadBearingLines`: contains `User=sandbox`, `bash -lc 'exec t3 serve --host 127.0.0.1 --port 3773 --no-browser /workspace'`, `UnsetEnvironment=XDG_RUNTIME_DIR`, `T3CODE_TELEMETRY_ENABLED=false`, `Restart=on-failure`, `StandardOutput=append:/home/sandbox/.t3/km-t3code.log`; does NOT contain `0.0.0.0`, `journal`, `auto-bootstrap`.
  - `TestT3StartScript_NeverEchoesPresenceToken`: script contains `--token-only` only in a line ending `> /etc/km/t3/presence.token.tmp`; contains no `cat /etc/km/t3`; contains `orchestration:read` and `--ttl 365d`; contains the pinned installer URL for the given version and `T3CODE_VERSION=0.1.0`; contains `systemctl enable --now km-t3code.service`.
  - `TestT3StopScript`: contains `systemctl disable --now km-t3code.service`.
  - `TestT3PortPinnedAcrossSites`: unit file contains `--port ` + strconv(t3RemotePort); reads `../../../cmd/km-presence/t3.go` and asserts `t3Port = 3773` equals `t3RemotePort`.
  - `TestT3StatusReport_*`: running+signal9 ⇒ nil, prints `✓ t3 v0.1.0 running`; not installed ⇒ error naming `km t3 start`; installed but inactive ⇒ error naming `km t3 start`; no signal 9 ⇒ warning text contains `km destroy` and `30 min`; no providers ⇒ warning names `km agent auth`; libatomic missing ⇒ error contains `allowedDNSSuffixes`.
  - `TestT3Cmd_Routing`: parent with one arg calls `t3Run`; `start`/`stop`/`status` are subcommands; `--local-port` default 3774 reaches `t3Run`.
- [ ] **Step 2:** `go test -run 'T3' ./internal/app/cmd/` → FAIL. (Per memory, if the worktree guard refuses that form, use `cd internal/app/cmd && go test -run T3 .`.)
- [ ] **Step 3:** Implement. Start script order (spec §5.2/5.3/5.5): libatomic (root; on failure print `=== libatomic ===\nmissing` and `exit 0`) → install-if-version-differs (sandbox) → `install -m 0600 -o sandbox -g sandbox /dev/null` the log if absent → write unit via quoted heredoc → `daemon-reload` → `enable --now` → wait ≤30 s for `curl -fsS http://127.0.0.1:3773/.well-known/t3/environment` → revoke prior `km-presence` sessions (parse `t3 auth session list --json` with python3, as herdr-adjacent code already relies on) → issue token to `.tmp`, chmod, mv → then append `t3StatusScript`. On not-ready, print the log tail through the token-redacting `sed` from spec §5.3. Start/stop/status RunE: `resolveVSCodeDeps` → `ResolveSandboxID` → fetch → `extractResourceID(…, ":instance/")` → `sendSSMAndWait` → parse → report. `start` prints `Next: km t3 <ref>`; `--version` flag overrides `t3Version` (validate `^\d+\.\d+\.\d+$`, reject otherwise).
- [ ] **Step 4:** tests PASS; `go vet`.
- [ ] **Step 5:** Commit `feat(t3): km t3 start|stop|status`.

### Task 4: `km t3 <id>` connect

**Files:** Modify `internal/app/cmd/t3.go`, `internal/app/cmd/t3_test.go`

**Interfaces:**
- Consumes: Task 3 consts, `t3StatusScript` markers, `buildPortForwardCmd`, `runReconnectingPortForward`, `httpTunnelProbe`.
- Produces: `func t3PairScript(localPort int, sandboxID string) string` (prints `=== pair url ===` then only `pairUrl`); `func runT3Connect(ctx, cfg, fetcher, execFn, ssmClient, sandboxID string, localPort int, noPair bool) error`.

- [ ] **Step 1: Failing tests:**
  - `TestT3PairScript`: contains `t3 auth pairing create --base-url http://localhost:3774 --json --ttl 5m --label km-t3-sb-1` and prints only the `pairUrl` field.
  - `TestRunT3Connect_RefusesWhenNotRunning`: fake SSM returns status with `unit active` = `inactive`; execFn must not be called; error contains `km t3 start`.
  - `TestRunT3Connect_NoPairSendsNoPairCommand`: fake SSM records commands; with `noPair` no command contains `pairing create`.
  - `TestRunT3Connect_PortInUse`: occupy the local port; error contains `--local-port`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement: local-port probe (same message shape as `connectPrep`), fetch/instance/region, one SSM call = `t3StatusScript` (+ `t3PairScript` unless `noPair`), refuse if not active, banner per spec §5.8, then `runReconnectingPortForward(ctx, execFn, func(c) { buildPortForwardCmd(c, iid, region, strconv.Itoa(lp), "3773") }, httpTunnelProbe(lp), true, os.Stdout)`. **Step 4:** PASS. **Step 5:** Commit `feat(t3): km t3 <id> connects over a reconnecting SSM forward`.

### Task 5: Docs

**Files:** Create `docs/t3code.md`; modify `CLAUDE.md` (dated block at top of the feature list, CLI list entries, "Where to look" row).

- [ ] **Step 1:** `docs/t3code.md`: what it is, the four verbs, first-time pairing, desktop app (Add environment; SSH mode unsupported-may-work), signal 9 + signal 5 exclusion semantics, locked-profile libatomic recipe, pairing link in SSM output (accepted, why), deploy surface + existing-box behaviour, troubleshooting (`~/.t3/km-t3code.log`, `km t3 status`), UAT record (Task 6 fills it).
- [ ] **Step 2:** CLAUDE.md entries. **Step 3:** Commit `docs(t3): runbook + CLAUDE.md`.

### Task 6: Live UAT (spec §7, steps 1–6)

- [ ] Build (`make build`), `km t3 start` on a fresh `profiles/herdr.yaml` box; hot-swap the new km-presence onto that box only (`aws s3 cp` to a scratch key is NOT used — copy via SSM from a presigned URL or base64 chunk; never `km init --sidecars`).
- [ ] Run steps 1–6, record results + any fixes in `docs/t3code.md` § UAT; destroy the test sandboxes (`km destroy <id> --remote --yes`), including the spike box `herdr-ca3c8843` and clean up the orphan `/km/access/herdr-31f4bd0e/ssh-key` param from the failed first create.
- [ ] Commit, push branch, open PR (do not merge).
