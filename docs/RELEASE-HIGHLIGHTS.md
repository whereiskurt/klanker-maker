<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->
## ✨ Major additions highlighted

This release is about **where a sandbox sits on the network and what it can reach** — private
placement, egress deny lists, and letting a running sandbox tighten its own policy.

### 🕸️ Per-profile private-subnet sandboxes with per-AZ NAT gateways (Phase 125)

> **Live UAT pending.** Code-complete and unit-verified; the full seven-step live runbook in
> `docs/private-subnet-nat.md` has not been executed end to end.

A sandbox's ENI can now land in a **private subnet** behind NAT instead of a public subnet with a
public IPv4 — controlled by two decoupled, **dormant-by-default** toggles:

- **`network.nat_gateway`** (install-level, `km-config.yaml`) decides whether NAT/EIP
  infrastructure *exists*. **`spec.network.privateSubnet`** (per-profile) decides whether *this*
  sandbox lands private. Both absent ⇒ byte-identical to before.
- **One NAT gateway + one EIP per AZ**, each in the public subnet of the AZ it serves — not one
  shared NAT. The Phase 124 AZ-failover sweep rotates launches across all four AZs, so a shared
  NAT would add cross-AZ data-transfer charges to every byte and make one AZ a SPOF for the whole
  install's internet access.
- **`network.private_subnet_count`** (1–4, absent = all four) sizes the private topology so you can
  trade AZ-rotation breadth against the NAT bill.
- ⚠️ **Cost:** roughly **$132/month** for four AZs plus **$0.045/GB** processed — a GPU profile
  pulling 300 GB of weights is ~$13.50 in NAT processing alone. This is exactly why the toggle is
  reversible: disabling NAT leaves the private subnets as free, routeless islands.
- **Guards:** `km create` fails fast when a private profile meets a NAT-less install; `km init`
  refuses to disable NAT while a private sandbox is running; `km doctor` warns on NAT-enabled-but-idle
  and on private-sandbox-without-NAT.

Full runbook, reversal procedure, and the one-time route-table split: `docs/private-subnet-nat.md`.

### 🚫 Egress deny lists — subtract known-bad from a wide-open profile

The egress policy was allowlist-only, with `*` as an allow-everything wildcard. That made the
natural authoring loop inexpressible: run wide open under `learnMode`, subtract the known-bad as you
find it, then replace `*` with the generated allowlist. The middle step had no allowlist-shaped
answer, because narrowing `*` means enumerating the very list learn mode is being run to discover.

```yaml
spec:
  network:
    egress:
      allowedDNSSuffixes: ["*"]
      deniedDNSSuffixes: ["evil.example.com", ".tracker.example.net"]
      deniedHosts: ["evil.example.com"]
```

- **A deny beats every allow** — including the `*` wildcard, the GitHub repo-filter carve-out, the
  OpenAI budget path, and the Bedrock/SES/Anthropic MITM interceptors. The deny gate is registered
  ahead of all of them, because goproxy dispatches first-match and a deny evaluated later would be
  silently bypassable.
- **Enforced at every layer** — the DNS proxy (NXDOMAIN), the HTTP proxy (403), and the eBPF
  resolver, which also refuses to seed a denied host's IPs into the BPF trie.
- Deny matching is deliberately **broader** than allow matching: a bare entry covers subdomains, so
  `evil.example.com` also blocks `api.evil.example.com`. Strictness on an allowlist permits less;
  the same strictness on a denylist would fail open.
- **Dormant by default** — a profile declaring no denies renders byte-identical user-data.

### 🔒 Runtime egress narrowing — a sandbox can tighten its own policy

`spec.network.egress.runtimeDeny: true` lets a **running** sandbox add denies to itself from
user-land, with no operator round-trip and no restart:

```console
$ km-netpolicy deny telemetry.example.com
denied telemetry.example.com
1 runtime deny entry now in force (takes effect within ~1s)
```

Narrowing is **one-way, and enforced rather than promised**:

- **The data structure.** The only operation is *append to a deny list*, which can only ever shrink
  the reachable set. There is no removal verb — `km-netpolicy` has exactly `deny` and `list`.
- **The kernel.** The deny file is created with `chattr +a`. An append-only file cannot be
  truncated, unlinked, renamed, or have the attribute cleared without `CAP_LINUX_IMMUTABLE`.

Widening still requires what it always did: a new profile and a fresh sandbox. Denies live under
`/var/lib` (not `/run`) so they survive reboot — a reboot that dropped them would *widen* the policy.

Verified end to end on a live EC2 sandbox: append-only enforced, all four widening attempts refused,
a reachable host blocked with no restart, unrelated hosts still reachable, and denies still enforced
after a reboot. Caveat worth knowing: a `*` allowlist with denies layered on is an **open** box with
holes plugged, not a closed one. Details and limits: `docs/egress-deny-lists.md`.

### ✍️ `km-github commit` — verified, bot-attributed signed commits from a sandbox

The only path that produces a **GitHub-signed**, `klanker-maker[bot]`-attributed commit from inside
a sandbox (`verified:true reason:valid`). A sandbox's local `git commit` is unsigned, and the
low-level REST path is bot-attributed but `verified:false`. Uses the GraphQL `createCommitOnBranch`
mutation, supports multi-file commits, and attributes to the token's own identity so it stays
portable across installs.

```bash
km-github commit --repo O/R --branch BR --message-file MSG -- path/a path/b
```

⚠️ Needs a token with `contents:write`, which is only minted when the profile's GitHub permissions
include **`push`**. See `docs/github-app-permissions.md`.

### 🩹 Notable fixes

- **apt now points at `archive.ubuntu.com`**, not the frequently-degraded EC2 regional mirror —
  this was breaking Ubuntu sandbox bootstrap outright.
- **Slack alias reuse unarchives** the existing channel instead of failing the create.
- **`km status` reconciles stale DynamoDB state against live EC2 in both directions**, so a row that
  drifted out of sync no longer blocks `km start` / `km resume`.
- **Two ttl-handler IAM gaps closed:** remote `km extend` / `km resume` silently no-op'd (the role
  couldn't `GetFunction` on itself), and `km destroy` left the per-sandbox `github-token` Lambda,
  schedule, and IAM roles alive — the cleanup code ran but 403'd and logged the failure as
  non-fatal.
- **GitHub installation tokens** are minted with `actions` + `workflows` write.
- **`km doctor --delete-ddb-rows`** no longer sweeps away the operator identity row.
