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

This release is about **getting km into your hands and keeping sandboxes alive while you use
them** — a container distribution that needs nothing but Docker, and a fix for interactive
sandboxes being reaped as idle out from under you.

### 🐳 Run km from a container — nothing to install but Docker

```bash
docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  ghcr.io/whereiskurt/klanker-maker/km:latest
```

Multi-arch (amd64 + arm64), published to GHCR on every release. It lands you in a shell at
`/klanker-maker` with `km`, your config, and the whole profile library in place — so `./km create
profiles/spot.yaml --remote` just works. Append a command instead to run one-shot:
`docker run ... km ./km list` exits with km's own exit code and prints nothing extra, so it pipes
cleanly into `jq`.

- **Bundles what km shells out to but never shipped**: `aws` CLI v2, `session-manager-plugin`,
  `sops`, plus git/jq/ssh. That was previously a manual per-operator install.
- **Credentials are never baked in.** `~/.aws` is mounted read-write at runtime, so the SSO token
  cache is shared with the host — if you're logged in on the host, you're logged in in the
  container. The startup banner reports whether each mount actually landed.
- **Scoped to `--remote` sandbox work**: create, list, status, shell, agent, logs, otel, destroy,
  doctor, capacity, validate — all verified live end to end. Platform provisioning (`km init`,
  `km bootstrap`, `--local` create/destroy) stays on a native install; it needs the `infra/live/`
  terragrunt tree no release archive carries. `terraform`/`terragrunt` are deliberately left out of
  the image for the same reason (and 152 MB lighter for it).
- **Prefer to build it yourself?** Every release archive now carries the Dockerfile, entrypoint,
  profile library, and `km-config.example.yaml` at their repo-relative paths — extract the tarball
  and `docker build -f containers/operator/Dockerfile .` in place, no clone required.

Details: `containers/operator/README.md`.

### 🖥️ Sandboxes no longer reaped while you're working in them

A sandbox someone was actively using through **KasmVNC or VS Code Remote-SSH** could be torn down by
`spec.lifecycle.idleTimeout`, with an empty CloudWatch audit stream as the only trace.

The root cause was **utmp, not the timer**: `sshd` writes a utmp record only for **PTY** sessions.
KasmVNC runs as a systemd service and never logs in, and VS Code Remote-SSH runs `vscode-server`
over a **non-PTY** exec channel — so a connected user looked idle on all five liveness signals.
The VS Code case was intermittent in the worst way: opening the integrated terminal allocates a PTY
and makes the session visible again, so survival depended on whether an unrelated UI panel was open.

`km-presence` now ORs **seven** signals, adding *KasmVNC viewer attached* and *SSH session
established*. Both detect the **live socket, never the process** — `pgrep vscode-server` is the
obvious check and is wrong, since VS Code deliberately leaves that server running after a client
disconnects and would latch the sandbox awake forever.

⚠️ **Consequence:** an open browser tab or VS Code window now pins a sandbox alive indefinitely, the
same tradeoff as leaving a `km shell` open. `spec.lifecycle.ttl` is unaffected and is now the real
backstop against forgotten sessions.

See `docs/desktop.md` § Idle timeout and `docs/vscode.md` § Idle timeout.

### 🎮 70B-class models on a single GPU — FP8 on one L40S

4-GPU `g6e` shapes have been capacity-walled, while single-GPU shapes stayed available. **FP8
quantization halves the weights** enough to fit Qwen3.8-27B-OBLITERATED onto **one L40S**
(`g6e.4xlarge`), turning a blocked deploy into a routine one — plus an S3-staged BF16 variant for
where the capacity exists.

### 🔧 Also in this release

- **codex can run bash tools on the GPU DLAMI again** — the image was missing `bubblewrap`, which
  codex requires for sandboxed tool execution.
- Single-GPU FP8 deployment notes, including the six distinct failure modes it surfaced
  (`docs/gpu-model-serving.md`).
