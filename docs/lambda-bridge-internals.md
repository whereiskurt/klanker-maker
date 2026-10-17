# Bridge Lambdas, read in code: what runs when Slack or HackerOne calls km

This is a code walkthrough of the boundary between "a SaaS sent us an HTTP request" and "an agent
on a sandbox started working." It follows one Slack message and one HackerOne webhook from the
moment they reach AWS to the moment the sandbox replies, and links each step to the lines that
run it.

Every link is a permalink to commit
[`373071b6`](https://github.com/whereiskurt/klanker-maker/commit/373071b60675882248f21e79451dacf8f6ea1977)
on `main`, so the line numbers stay correct after the code moves. If you are reading this much
later, replace the commit hash in a link with `main` to see the same file today.

Companion material:

- [`docs/diagrams/security/04-bridge-trust-boundary.html`](diagrams/security/04-bridge-trust-boundary.html):
  one picture of the trust boundary. This doc is the prose and code behind it.
- [`docs/slack-notifications.md`](slack-notifications.md) and [`docs/h1-bridge.md`](h1-bridge.md):
  the operator runbooks (setup, config keys, troubleshooting). This doc doesn't repeat them.
- The GitHub and webhook (Wiz) bridges follow the same pattern
  ([`cmd/km-github-bridge/main.go`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-github-bridge/main.go), [`cmd/km-webhook-bridge/main.go`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-webhook-bridge/main.go)). They aren't traced here.

> **Scope:** this is `main`. On the `feat/slack-default-route` branch the Slack bridge gains a
> routing ladder that runs where the channel lookup misses (step 5 below), plus cold-create, DMs,
> and replies to the originating channel. See `docs/slack-default-route.md` on that branch. The
> parts this doc covers (cold start, signature check, enqueue, poller, deploy) are unchanged there.

---

## 1. The mental model

A bridge Lambda is **an ordinary Go `main` package**. Nothing about it is special except who
calls `main`:

- AWS starts a Linux micro-VM, runs the binary once, and keeps it warm. Everything in `init()`
  happens once per **cold start**: config is loaded, clients are built, a few secrets are cached.
- `lambda.Start(handle)` hands control to the AWS runtime, which calls `handle()` once per HTTP
  request. Slack: [`cmd/km-slack-bridge/main.go:211`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L211). HackerOne: [`cmd/km-h1-bridge/main.go:324`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L324).
- Requests arrive through a **Function URL**: a public HTTPS endpoint AWS puts in front of the
  function. Slack and HackerOne are given that URL once, at setup.
- Between requests AWS **freezes** the process. Any goroutine still running when `handle()`
  returns is paused mid-flight. That is why the handlers do everything synchronously (see
  [§7](#7-gotchas-that-explain-the-shape-of-the-code)).

There are **two halves**, and they never talk to each other directly:

| | Bridge Lambda | Sandbox poller |
|---|---|---|
| Runs on | AWS Lambda, arm64, **no VPC attachment** | the sandbox's EC2 instance |
| Started by | an inbound HTTP request | systemd at boot, `Restart=always` |
| Code | `cmd/km-{slack,h1}-bridge` + `pkg/{slack,h1}/bridge` | bash rendered into userdata by `pkg/compiler/userdata.go` |
| Identity | its own IAM role, defined in `infra/modules/lambda-*-bridge` | the sandbox instance role |
| Job | authenticate, filter, dedupe, route, **enqueue** | **long-poll** the queue, run the agent, post the reply |

The handoff point is a **per-sandbox SQS FIFO queue**. The Lambda has no network path into the
sandbox subnet. Work reaches a box only because the box pulled it off its own queue.

```mermaid
sequenceDiagram
    autonumber
    participant S as Slack / HackerOne
    participant L as Bridge Lambda<br/>(Function URL, no VPC)
    participant D as DynamoDB<br/>(sandboxes, nonces, threads)
    participant Q as SQS FIFO<br/>per sandbox
    participant P as Poller on sandbox<br/>(systemd, root)
    participant A as Agent<br/>(claude / codex, uid sandbox)
    S->>L: POST (HMAC-signed)
    L->>L: verify signature → 401 if bad
    L->>D: who owns this? already seen?
    L->>Q: SendMessage (group = thread / report)
    L-->>S: 200 (+ 👀 on Slack, internal "On it" on H1)
    loop every 20s long-poll
        P->>Q: ReceiveMessage
    end
    P->>A: runuser -u sandbox -- claude -p ...
    A-->>P: output.json
    P->>S: reply (Slack: via the bridge; H1: direct API)
    P->>Q: DeleteMessage (only after the turn succeeded)
```

---

## 2. How the code gets into AWS

"Deploying a Lambda" here means: build a Linux binary, zip it, and have Terraform upload the zip.

### 2.1 Build: a binary called `bootstrap`

`make build-lambdas` cross-compiles each bridge for `linux/arm64` into a file named `bootstrap`
and zips it into `build/`:

- Slack bridge: [`Makefile:298-299`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/Makefile#L298-L299)
- HackerOne bridge: [`Makefile:302-303`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/Makefile#L302-L303)

The name `bootstrap` is required. The function uses the OS-only runtime `provided.al2023` with
`handler = "bootstrap"`, and that runtime simply executes `/var/task/bootstrap`
([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:377-379`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L377-L379)). `zip -j` puts it at the zip root.

`km init` has its own copy of the same build in Go, `lambdaBuilds()` / `buildLambdaZips()`
([`internal/app/cmd/init.go:3569-3592`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L3569-L3592), [`internal/app/cmd/init.go:3609`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L3609)). It must stay in
lockstep with the Makefile.

### 2.2 Upload: Terraform notices the zip changed

The Terraform module points at the zip on disk and hashes it:

```hcl
filename         = var.lambda_zip_path
source_code_hash = filebase64sha256(var.lambda_zip_path)
```

([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:375-376`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L375-L376),
[`infra/modules/lambda-h1-bridge/v1.0.0/main.tf:356-357`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-h1-bridge/v1.0.0/main.tf#L356-L357)). The terragrunt unit sets the path to
`build/km-slack-bridge.zip` ([`infra/live/use1/lambda-slack-bridge/terragrunt.hcl:88`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/live/use1/lambda-slack-bridge/terragrunt.hcl#L88)) or
`build/km-h1-bridge.zip` ([`infra/live/use1/lambda-h1-bridge/terragrunt.hcl:84`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/live/use1/lambda-h1-bridge/terragrunt.hcl#L84)).

**The hash is the change detector.** On any `terragrunt apply` of that unit, a zip whose bytes
differ from the deployed one is uploaded. Nothing else ships code. That one fact explains the deploy
table below.

The same module also defines:

- **The env block**, which is how `km-config.yaml` reaches the running code. For example,
  `slack.allow` → `KM_SLACK_ALLOW` is exported by `km init` ([`internal/app/cmd/init.go:1999-2006`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L1999-L2006)),
  read by terragrunt with `get_env()` ([`infra/live/use1/lambda-slack-bridge/terragrunt.hcl:139`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/live/use1/lambda-slack-bridge/terragrunt.hcl#L139)),
  put into the env block ([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:427`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L427)), and read by the
  Go code at cold start ([`cmd/km-slack-bridge/main.go:310-321`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L310-L321)).
- **The IAM role**, with one inline policy per capability (§3.5, §4.4).
- **The Function URL**, with `authorization_type = "NONE"`
  ([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:465-474`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L465-L474),
  [`infra/modules/lambda-h1-bridge/v1.0.0/main.tf:428-437`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-h1-bridge/v1.0.0/main.tf#L428-L437)), plus an explicit public
  `lambda:InvokeFunctionUrl` permission so a function replacement doesn't start returning 403
  ([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:477-487`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L477-L487)).
- **The log group** `/aws/lambda/{prefix}-slack-bridge` / `/aws/lambda/{prefix}-h1-bridge`, with
  30-day retention ([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:447-455`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L447-L455)).

`authorization_type = "NONE"` sounds alarming. It means AWS doesn't check who the caller is, and
**the code does**: the first thing either handler does with a request is verify the sender's HMAC.

### 2.3 Which command actually deploys what

| Command | Bridge code | Bridge env block | Bridge IAM |
|---|---|---|---|
| `make build-lambdas` | rebuilt **locally only** | — | — |
| `km init --lambdas` | rebuilt **locally only** ([`internal/app/cmd/init.go:1733-1741`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L1733-L1741); its cold-start poke targets only the create-handler, [`internal/app/cmd/init.go:4729-4739`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L4729-L4739)) | — | — |
| `km init --sidecars` | — | — | — |
| `km init --slack` / `--h1` / `--only lambda-*-bridge` (with `--dry-run=false`) | uploaded **if the local zip changed**: no rebuild, but it is a real apply, so the hash diff ships whatever zip is on disk | yes | yes |
| `km init --dry-run=false` | rebuilt + uploaded | yes | yes |

The full apply runs one `terragrunt apply -auto-approve` per module in `regionalModules()` order
([`internal/app/cmd/init.go:2821`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L2821), [`pkg/terragrunt/runner.go:91-94`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/terragrunt/runner.go#L91-L94)). The bridges are
[`internal/app/cmd/init.go:715-717`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L715-L717) and [`internal/app/cmd/init.go:748-750`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L748-L750).

Two traps:

- **A failed zip build doesn't stop the full apply.** It prints `[warn]` and applies whatever zip
  is already in `build/` ([`internal/app/cmd/init.go:1521-1522`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L1521-L1522)). Check that `km --version`'s
  commit matches what you think you deployed.
- **`km init` doesn't publish the Slack or H1 bridge URL.** `km slack init` reads the Terraform
  output and writes SSM `{prefix}slack/bridge-url` ([`internal/app/cmd/slack.go:410-433`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/slack.go#L410-L433)).
  `km slack manifest` appends `/events` to it for the Slack App ([`internal/app/cmd/slack_manifest.go:78`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/slack_manifest.go#L78)).
  For HackerOne you pass `km h1 init --bridge-url` yourself and paste the printed Payload URL into
  the program's webhook settings ([`internal/app/cmd/h1.go:125-143`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/h1.go#L125-L143)).

---

## 3. Slack: a message arrives

### 3.1 Cold start: `init()` and `wireEventsHandler()`

The Slack bridge serves **two directions** from one function. `/events` is Slack → sandbox, and
every other path is sandbox → Slack. So it builds two handlers:

- `init()` ([`cmd/km-slack-bridge/main.go:79-196`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L79-L196)) reads table names and SSM paths from env
  ([`cmd/km-slack-bridge/main.go:97-100`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L97-L100)), and builds the AWS clients and the signed-envelope
  `Handler` for the outbound path ([`cmd/km-slack-bridge/main.go:129-136`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L129-L136)). It fetches the **bot
  token** eagerly, with a 15-minute cache ([`cmd/km-slack-bridge/main.go:165`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L165),
  [`pkg/slack/bridge/aws_adapters.go:207`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L207)).
- `wireEventsHandler()` ([`cmd/km-slack-bridge/main.go:217-417`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L217-L417)) builds the inbound
  `EventsHandler`. It **refuses to start** without `KM_SLACK_THREADS_TABLE`, because a default
  name would route into another install's table ([`cmd/km-slack-bridge/main.go:563-571`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L563-L571)). It also
  reads the federation, allowlist, and mention-only knobs
  ([`cmd/km-slack-bridge/main.go:288-341`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L288-L341), [`cmd/km-slack-bridge/main.go:428-442`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L428-L442)). The
  **signing secret** is fetched lazily on the first `/events` request
  ([`cmd/km-slack-bridge/main.go:235-239`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L235-L239)).

### 3.2 Per request: `handle()` routes on the path

[`cmd/km-slack-bridge/main.go:469-511`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L469-L511): the body is base64-decoded if needed, then
`switch ev.RawPath`. `/events` goes to `eventsHandler.Handle` with lowercased headers. **Any other
path** goes to the signed-envelope `handler.Handle` (§3.4). The handlers never return a Go error;
the HTTP status travels in the response struct.

### 3.3 The inbound decision ladder: `EventsHandler.Handle`

[`pkg/slack/bridge/events_handler.go:187-641`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L187-L641). The response-code policy is stated at the top
([`pkg/slack/bridge/events_handler.go:174-186`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L174-L186)): **400 only for malformed JSON, 401 only for a bad
signature, 200 for everything else, internal errors included.** A 5xx makes Slack retry, and
the retry storm is worse than a dropped message.

| # | Step | Code | If it rejects |
|---|---|---|---|
| 0 | Parse the outer JSON | [`pkg/slack/bridge/events_handler.go:188-192`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L188-L192) | 400 |
| 1 | `url_verification`: echo the challenge (Slack's one-time URL handshake, answered before the signature check by design) | [`pkg/slack/bridge/events_handler.go:197-204`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L197-L204) | — |
| 2 | **Verify the signature**: HMAC-SHA256 over `v0:{timestamp}:{body}`, constant-time compare, ±300 s skew | call [`pkg/slack/bridge/events_handler.go:207-219`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L207-L219), impl [`pkg/slack/bridge/events_handler.go:824-850`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L824-L850) | **401** |
| 3 | Only `event_callback` → inner `message` | [`pkg/slack/bridge/events_handler.go:222-236`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L222-L236) | 200, silent |
| 4 | Drop bots, our own echo, and odd subtypes (`isBotLoop`) | [`pkg/slack/bridge/events_handler.go:658-684`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L658-L684) | 200, silent |
| 5 | **Which sandbox owns this channel?** A `Query` on the `slack_channel_id-index` GSI of `{prefix}-sandboxes` | call [`pkg/slack/bridge/events_handler.go:251-255`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L251-L255), impl [`pkg/slack/bridge/aws_adapters.go:1040-1132`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L1040-L1132) | 200 |
| 5′ | Miss: relay to peer installs (Phase 95), or post the orphan-channel hint (Phase 96) | [`pkg/slack/bridge/events_handler.go:256-324`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L256-L324), [`pkg/slack/bridge/relayer.go:65-105`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/relayer.go#L65-L105) | 200 |
| 5a | Trigger allowlist by Slack user id (Phase 118) | [`pkg/slack/bridge/events_handler.go:338-347`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L338-L347) | 200, silent |
| 5b | Mention-only, with the known-thread bypass (Phase 91) | [`pkg/slack/bridge/events_handler.go:361-388`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L361-L388) | 200, silent |
| 5c | Sandbox frozen by an action quota → post 🛑 | [`pkg/slack/bridge/events_handler.go:395-414`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L395-L414) | 200 |
| 6 | **Dedup** on `event:{event_id}`, a conditional `PutItem` on the nonces table, 24 h TTL | [`pkg/slack/bridge/events_handler.go:418-426`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L418-L426), [`pkg/slack/bridge/aws_adapters.go:145-163`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L145-L163) | 200 |
| 7 | Upsert the thread row in `{prefix}-slack-threads` | [`pkg/slack/bridge/events_handler.go:429-436`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L429-L436), [`pkg/slack/bridge/aws_adapters.go:996-1025`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L996-L1025) | best-effort |
| 8 | Download any attached files to S3 `slack-inbound/…` (synchronous) | [`pkg/slack/bridge/events_handler.go:452-503`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L452-L503), [`pkg/slack/bridge/file_downloader.go:117`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/file_downloader.go#L117) | warns in-thread |
| 8 | **Enqueue** to the sandbox's FIFO queue | [`pkg/slack/bridge/events_handler.go:504-524`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L504-L524) | 200 (logged) |
| 9 | Sandbox paused or stopped? `StartInstances` and post "waking up" (Phase 114) | [`pkg/slack/bridge/events_handler.go:537-577`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L537-L577), [`pkg/slack/bridge/aws_adapters.go:1580-1694`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L1580-L1694) | never fails the request |
| 10 | 👀 reaction on the triggering message | [`pkg/slack/bridge/events_handler.go:591-622`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L591-L622) | logged |
| — | Done | [`pkg/slack/bridge/events_handler.go:640`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L640) | 200 `ok` |

The enqueue itself is `SQSAdapter.Send` ([`pkg/slack/bridge/aws_adapters.go:838-852`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L838-L852)):

- `MessageGroupId = thread_ts`. FIFO keeps one thread's turns in order and lets different
  threads run in parallel (Phase 119).
- `MessageDeduplicationId = event_id`. SQS drops a duplicate send for five minutes.

The message body is `InboundQueueBody` ([`pkg/slack/bridge/events_types.go:39-56`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_types.go#L39-L56)): `channel`,
`thread_ts`, `text`, `user`, `event_ts`, and an optional `attachments` list of S3 keys. The queue,
`{prefix}-slack-inbound-{sandbox_id}.fifo`, is created by `km create`, not by the Lambda
([`pkg/aws/sqs.go:187-189`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/aws/sqs.go#L187-L189), [`pkg/aws/sqs.go:205`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/aws/sqs.go#L205)).

### 3.4 The other direction: the sandbox posts back through the same Lambda

A sandbox **never holds the Slack bot token**. `km-slack` builds an envelope, signs it with the
sandbox's own Ed25519 key, and POSTs it to the bridge's root path:

- The key seed is read from SSM `/{prefix}/sandbox/{id}/signing-key`
  ([`cmd/km-slack/main.go:1038-1073`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack/main.go#L1038-L1073)). The public half lives in `{prefix}-identities`
  ([`pkg/aws/identity.go:295-314`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/aws/identity.go#L295-L314)).
- The envelope and signing: [`pkg/slack/payload.go:168-188`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/payload.go#L168-L188). The POST with `X-KM-Sender-ID` /
  `X-KM-Signature`: [`pkg/slack/client.go:967-973`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/client.go#L967-L973).

`Handler.Handle` ([`pkg/slack/bridge/handler.go:110-578`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/handler.go#L110-L578)) checks, in order: shape and version →
the sender header matches the body → timestamp skew → nonce (10-minute replay window) → public key
lookup → **Ed25519 signature** → authorization. Only the operator may `archive`/`test`, and **a
sandbox may post only to its own channel** (`403 channel_mismatch`,
[`pkg/slack/bridge/handler.go:245-282`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/handler.go#L245-L282)). Only then does it fetch the bot token and call Slack. Action
quotas are counted here, on the way out ([`pkg/slack/bridge/handler.go:300-303`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/handler.go#L300-L303)); the inbound
path only checks the frozen latch.

### 3.5 What the Slack bridge is allowed to do

The IAM role in [`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:12-365`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L12-L365) is the complete list.
Each statement maps to a step above:

| Policy | Grants | Used by |
|---|---|---|
| `ssm_bot_token`, `SSMSigningSecret` | read two SSM params | cold start, step 2 |
| `PublicKeyAndChannelLookup` | `GetItem` on identities + sandboxes | §3.4 |
| `DDBSandboxesChannelGSI` | `Query` on `slack_channel_id-index` | step 5 |
| `NonceReplayProtection` | `PutItem` on nonces | step 6, §3.4 |
| `DDBSlackThreads` | Get/Put/Query on threads | steps 5b, 7 |
| `S3PutSlackInboundFiles` | `PutObject` on `slack-inbound/*` | step 8 |
| `SQSSendInbound` | `SendMessage` on `{prefix}-slack-inbound-*.fifo` | step 8 |
| `ec2_resume` | `StartInstances`, **only on instances tagged with this prefix** | step 9 |
| `S3GetTranscripts` | `GetObject` on `transcripts/*` | §3.4 `upload` |

What's missing matters just as much: no `vpc_config`, no `ssm:SendCommand`, and no way to run
anything on a sandbox.

---

## 4. HackerOne: a webhook arrives

Same skeleton, different authentication and routing. HackerOne signs with a shared secret but
has **no timestamp**, so replay protection rests entirely on the delivery GUID. Routing is by
**program**, not channel. One report can fan out to several sandboxes.

### 4.1 Cold start

[`cmd/km-h1-bridge/main.go:79-270`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L79-L270):

- `KM_H1_PROGRAMS` is the whole routing table, as JSON rendered by `km init` from the `h1:` block
  ([`cmd/km-h1-bridge/main.go:122-142`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L122-L142); exported at [`internal/app/cmd/init.go:2160-2194`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L2160-L2194)). If it
  is unset the bridge is **dormant** and drops everything.
- The HackerOne **API** username and token (for the bridge's own "On it" comment) are read once
  from SSM ([`cmd/km-h1-bridge/main.go:276-301`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L276-L301)).
- The **webhook secret** is fetched lazily and cached for 15 minutes
  ([`cmd/km-h1-bridge/main.go:153-157`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L153-L157)).
- `handle()` ([`cmd/km-h1-bridge/main.go:331-356`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L331-L356)) decodes the body, lowercases the headers, and
  calls `WebhookHandler.Handle`.

### 4.2 The pipeline: `WebhookHandler.Handle`

[`pkg/h1/bridge/webhook_handler.go:164-409`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L164-L409). The same contract: 401 for a bad signature, 400 for
bad base64, **200 for everything else**, so HackerOne never redelivers with a new GUID.

| # | Step | Code | If it rejects |
|---|---|---|---|
| 0 | *Opt-in* raw capture to S3 `h1-captures/{guid}.json`, **before** the HMAC check on purpose, so a mis-pasted secret still leaves evidence | [`pkg/h1/bridge/webhook_handler.go:168-172`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L168-L172), [`pkg/h1/bridge/capture.go:49-86`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/capture.go#L49-L86) | — |
| 1 | **Verify** `X-H1-Signature: sha256=<hex>`, HMAC-SHA256 over the raw body, constant-time | [`pkg/h1/bridge/webhook_handler.go:175-186`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L175-L186), [`pkg/h1/bridge/payload.go:275-287`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/payload.go#L275-L287) | **401** |
| 2 | Parse the JSON:API body (`data.activity`, `data.report`) | [`pkg/h1/bridge/webhook_handler.go:195-199`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L195-L199), [`pkg/h1/bridge/payload.go:134-183`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/payload.go#L134-L183) | 200 |
| 3 | Loop guard: drop activity authored by our own API user | [`pkg/h1/bridge/webhook_handler.go:205-209`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L205-L209) | 200 |
| 4 | **Program → targets** from `KM_H1_PROGRAMS` | [`pkg/h1/bridge/webhook_handler.go:212-217`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L212-L217), [`pkg/h1/bridge/resolve.go:88-117`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/resolve.go#L88-L117) | 200 |
| 4′ | Is this a configured auto-triage event, or a comment? Anything else drops | [`pkg/h1/bridge/webhook_handler.go:220-226`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L220-L226) | 200 |
| 5A | **Auto-triage**: the operator's `events:` entry is the authorization. No handle or allowlist check. `reply: none` is captured here | [`pkg/h1/bridge/webhook_handler.go:260-277`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L260-L277) | — |
| 5B | **Comment**: must contain the bot handle (unless the thread is already known)… | [`pkg/h1/bridge/webhook_handler.go:240-251`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L240-L251), [`pkg/h1/bridge/webhook_handler.go:281-284`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L281-L284) | 200 |
| 6 | …and come from an allowlisted actor (deny-by-default) | [`pkg/h1/bridge/webhook_handler.go:287-291`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L287-L291) | 200, silent |
| 6′ | `/help`, `/claude`, `/codex`, `/reply_to_researcher`, and per-program commands | [`pkg/h1/bridge/webhook_handler.go:294-334`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L294-L334), [`pkg/h1/bridge/commands.go:190-247`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/commands.go#L190-L247) | internal reply, 200 |
| 7 | **Dedup** on `h1-delivery:{X-H1-Delivery}` (shared nonces table, 24 h) | [`pkg/h1/bridge/webhook_handler.go:338-350`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L338-L350), [`pkg/h1/bridge/aws_adapters.go:171-188`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L171-L188) | 200 |
| 8 | External reply only with `/reply_to_researcher` **and** an allowlisted actor | [`pkg/h1/bridge/webhook_handler.go:357`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L357), [`pkg/h1/bridge/webhook_handler.go:624-626`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L624-L626) | — |
| 9 | **Fan out**: one envelope per target, `dispatchTarget` each | [`pkg/h1/bridge/webhook_handler.go:364-393`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L364-L393) | logged, loop continues |
| 10 | One **internal** "On it" comment, unless `reply: none` | [`pkg/h1/bridge/webhook_handler.go:402-406`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L402-L406), [`pkg/h1/bridge/aws_adapters.go:808-847`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L808-L847) | non-fatal |

### 4.3 Warm, resume, or cold: `dispatchTarget`

[`pkg/h1/bridge/webhook_handler.go:416-487`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L416-L487) looks the target alias up on the `alias-index` GSI
([`pkg/h1/bridge/aws_adapters.go:214-246`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L214-L246)). There are three outcomes:

- **Running** → `enqueueAndUpsert` ([`pkg/h1/bridge/webhook_handler.go:495-575`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L495-L575)). It runs the
  action-quota gate ([`pkg/h1/bridge/webhook_handler.go:518-558`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L518-L558)), reads the queue URL off the
  sandbox row, `SendMessage`s, and only after a successful send writes the thread row.
  - `MessageGroupId = h1-{report}-{alias}`, so turns on one report are serialized per target.
  - `MessageDeduplicationId = {delivery}-{group}`, so N fanout targets stay distinct
    ([`pkg/h1/bridge/webhook_handler.go:387-388`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L387-L388)).
- **Stopped or paused** → `StartInstances` ([`pkg/h1/bridge/aws_adapters.go:434-520`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L434-L520)), then
  enqueue. If the instance is **gone** (`ErrNoResumableInstance`), the stale row is deleted and the
  target falls through to a cold create (Phase 109 self-heal,
  [`pkg/h1/bridge/webhook_handler.go:454-471`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L454-L471)).
- **Absent** → `PutEvents` a `SandboxCreate` event onto EventBridge, carrying the envelope
  ([`pkg/h1/bridge/aws_adapters.go:340-386`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L340-L386)). The **create-handler** Lambda provisions the box
  and drains the envelope into its new queue ([`cmd/create-handler/main.go:477-482`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/create-handler/main.go#L477-L482)). The bridge
  itself never creates anything.

The queued `H1Envelope` ([`pkg/h1/bridge/payload.go:234-264`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/payload.go#L234-L264)) carries `program`, `report_id`,
`kind`, `actor`, the expanded prompt `body`, `agent`, `reply_to_researcher`, and `reply_mode`.

### 4.4 What the HackerOne bridge is allowed to do

[`infra/modules/lambda-h1-bridge/v1.0.0/main.tf:42-346`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-h1-bridge/v1.0.0/main.tf#L42-L346):

- `SSMH1Config`: read `/{prefix}/config/h1/*`.
- `NonceReplayProtection`: `PutItem`.
- On `{prefix}-sandboxes`: `Query` on `alias-index` plus `Get`/`Update`/`DeleteItem`. There is no
  `PutItem`; only `km create` writes sandbox rows.
- Read/write on `{prefix}-h1-threads` and `{prefix}-action-quota`.
- `SQSSendH1Inbound` on `{prefix}-h1-inbound-*.fifo`.
- Prefix-tag-conditioned `StartInstances`.
- `EventBridgePutSandboxCreate`.
- `H1RawCaptureWrite` on `h1-captures/*`, which exists only when debug capture is on.

---

## 5. The sandbox side: the poller

Both pollers are bash scripts rendered into userdata and run by a systemd unit as root. They are
inside the Go source as template text, so they're easy to miss.

**Slack** (`km-slack-inbound-poller`, [`pkg/compiler/userdata.go:1964-2669`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L1964-L2669); unit
[`pkg/compiler/userdata.go:3935-3960`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L3935-L3960)):

1. Find the queue URL: env, else SSM `…/sandbox/{id}/slack-inbound-queue-url`
   ([`pkg/compiler/userdata.go:2038-2053`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2038-L2053)).
2. **Long-poll**: `aws sqs receive-message --wait-time-seconds 20`
   ([`pkg/compiler/userdata.go:2111-2117`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2111-L2117)). Up to `KM_SLACK_MAX_CONCURRENCY` turns run at once,
   one per thread ([`pkg/compiler/userdata.go:2106`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2106), [`pkg/compiler/userdata.go:2144-2147`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2144-L2147)).
3. A visibility heartbeat every 120 s keeps a long turn from being redelivered mid-run
   ([`pkg/compiler/userdata.go:2160-2170`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2160-L2170)).
4. An idempotency guard skips an `event_ts` this thread already processed
   ([`pkg/compiler/userdata.go:2217-2228`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2217-L2228)).
5. **Drop privileges and run the agent.** `dispatch_as_sandbox` moves the subshell into the
   enforcement cgroup, then `runuser -u sandbox`
   ([`pkg/compiler/userdata.go:2025-2031`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2025-L2031)). Claude runs at
   [`pkg/compiler/userdata.go:2516-2530`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2516-L2530) and Codex at [`pkg/compiler/userdata.go:2477-2503`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2477-L2503),
   resuming the thread's session.
6. Reply with `km-slack post`, which goes back through the bridge (§3.4)
   ([`pkg/compiler/userdata.go:2621-2630`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2621-L2630)).
7. **Ack only after success**: `delete-message` runs after the reply
   ([`pkg/compiler/userdata.go:2645-2648`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2645-L2648)). A failed turn is left on the queue to redeliver
   ([`pkg/compiler/userdata.go:2652-2654`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L2652-L2654)).

**HackerOne** (`km-h1-inbound-poller`, [`pkg/compiler/userdata.go:3091-3464`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L3091-L3464); unit
[`pkg/compiler/userdata.go:3987-4005`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L3987-L4005)) has the same shape, but serial, one message at a time
([`pkg/compiler/userdata.go:3189-3194`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/compiler/userdata.go#L3189-L3194)). The difference is how it replies. The agent runs
`km-h1 comment`, which calls the **HackerOne API directly** with Basic Auth credentials it reads
from SSM ([`cmd/km-h1/main.go:137-145`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1/main.go#L137-L145), [`cmd/km-h1/main.go:409-452`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1/main.go#L409-L452),
[`cmd/km-h1/main.go:211-212`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1/main.go#L211-L212)). Replies are internal unless `--reply-to-researcher` is passed
([`cmd/km-h1/main.go:157-182`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1/main.go#L157-L182)). The bridge's loop guard (step 3) is what stops those replies
from re-triggering the bot: they're authored by the same API user.

---

## 6. Debugging a message that "didn't arrive"

Work along the path. Each hop leaves its own evidence.

| Question | Where to look |
|---|---|
| Did the request reach AWS at all? | `/aws/lambda/{prefix}-slack-bridge` or `-h1-bridge` in CloudWatch. Every drop logs a reason (`events: …` for Slack). No log line at all → wrong URL, or the env-var KMS grant broke (`km doctor` checks `checkBridgeEnvKMSKey`). |
| 401s? | Wrong signing secret / webhook secret in SSM, or (Slack) a clock skew over 300 s. For H1, turn on `h1.debug_capture` to see the raw delivery. |
| 200 but nothing queued? | Find the step in the tables above whose log line appears last. Most common: no owning sandbox (step 5), allowlist, mention-only, or a duplicate. |
| Queued but no reply? | On the box: `journalctl -u km-slack-inbound-poller` (or `km-h1-inbound-poller`); the run's output is under `/workspace/.km-agent/runs/<RUN_ID>/`. Repeated failures move the message to the shared inbound DLQ (`km doctor` probes it). |
| Code change "didn't take"? | §2.3. `make build-lambdas` alone uploads nothing. Compare the function's `CodeSha256` with your zip. |

---

## 7. Gotchas that explain the shape of the code

- **Everything is synchronous.** Relaying, file downloads, `StartInstances` and reactions all happen
  before `handle()` returns ([`pkg/slack/bridge/relayer.go:96`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/relayer.go#L96) even comments the `wg.Wait()`),
  because a frozen Lambda pauses background goroutines. The cost is latency: Slack retries anything
  slower than ~3 s, and those retries are absorbed by dedup (step 6). Both bridges have a 60 s
  timeout ([`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:386`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-slack-bridge/v1.0.0/main.tf#L386),
  [`infra/modules/lambda-h1-bridge/v1.0.0/main.tf:363`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-h1-bridge/v1.0.0/main.tf#L363)).
- **200 on internal errors is deliberate** (see the top of each table). The alternative is a retry
  storm with fresh delivery ids that dedup can't catch.
- **An unknown Slack path is not a 404.** It is the signed-envelope path
  ([`cmd/km-slack-bridge/main.go:498`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-slack-bridge/main.go#L498)), which then fails its own checks.
- **The queues belong to sandboxes, not to the bridge.** `km create` makes them and writes their
  URL onto the sandbox row (Slack: `slack_inbound_queue_url`; H1: `h1_inbound_queue_url`,
  [`internal/app/cmd/create_h1_inbound.go:85-115`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/create_h1_inbound.go#L85-L115)). A row with no queue URL reads as "not ours."
- **The sandbox tag is always `km:sandbox-id`**, whatever the install prefix. The resume code
  filters on it ([`pkg/h1/bridge/aws_adapters.go:415-439`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/aws_adapters.go#L415-L439)), and `StartInstances` is additionally
  conditioned on `km:resource-prefix`.

### Things noticed while writing this (unverified live)

These come from reading `373071b6`. Each is a one-line pointer for someone to confirm, not a
diagnosis.

- **Slack: a failed enqueue can lose a message.** The `event:` nonce is reserved
  ([`pkg/slack/bridge/events_handler.go:419`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L419)) *before* the SQS send. If the send fails, the handler
  returns 200 ([`pkg/slack/bridge/events_handler.go:516-522`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/events_handler.go#L516-L522)), and Slack's retry, which carries
  the same `event_id`, is then dropped as a duplicate.
- **Slack: the Phase 96 running-channel list filters on `state`**
  ([`pkg/slack/bridge/aws_adapters.go:1474-1484`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/slack/bridge/aws_adapters.go#L1474-L1484)), the attribute Phase 114 found was never
  written for the channel lookup. The orphan hint may always list no channels.
- **H1: `FrozenCheck` is never wired.** `WireActionQuota` sets the quota, limits and freezer
  ([`cmd/km-h1-bridge/main.go:309-321`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/cmd/km-h1-bridge/main.go#L309-L321)) but not the frozen-dispatch gate the handler checks
  ([`pkg/h1/bridge/webhook_handler.go:498`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L498)). A frozen sandbox is still enqueued to on later turns.
- **H1: dedup runs after the command gates** ([`pkg/h1/bridge/webhook_handler.go:338`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/pkg/h1/bridge/webhook_handler.go#L338)), so a
  redelivered `/help` posts its reply again.
- **`km init --lambdas`'s own advice is wrong.** The scoped-apply hint says
  `make build-lambdas && km init --lambdas` for stale code ([`internal/app/cmd/init.go:396`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/internal/app/cmd/init.go#L396)), and
  per §2.3 that deploys nothing.
- **Stale comments:** [`infra/live/use1/lambda-slack-bridge/terragrunt.hcl:108-109`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/live/use1/lambda-slack-bridge/terragrunt.hcl#L108-L109) says
  `km init --sidecars` refreshes the env block, and it doesn't. The H1 module's `function_url` output
  says `km init` stores it in SSM ([`infra/modules/lambda-h1-bridge/v1.0.0/outputs.tf:11-14`](https://github.com/whereiskurt/klanker-maker/blob/373071b60675882248f21e79451dacf8f6ea1977/infra/modules/lambda-h1-bridge/v1.0.0/outputs.tf#L11-L14)), and it
  doesn't.
