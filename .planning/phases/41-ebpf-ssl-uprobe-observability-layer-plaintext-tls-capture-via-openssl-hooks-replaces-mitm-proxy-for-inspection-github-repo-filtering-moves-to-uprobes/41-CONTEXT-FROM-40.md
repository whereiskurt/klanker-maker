# Phase 41 Context: Lessons from Phase 40 E2E

**Captured:** 2026-04-01 after Phase 40 completion (14 E2E iterations)

## Build Pipeline

- **bpf2go runs in Docker** via `make generate-ebpf` (Dockerfile.ebpf-generate). Uses system libbpf headers (`<linux/bpf.h>`, `<bpf/bpf_helpers.h>`) NOT hand-rolled vmlinux.h — the hand-rolled approach caused BPF verifier rejection on AL2023 kernel 6.1
- **`-target amd64`** flag on bpf2go produces `_x86_bpfel.{go,o}` files. Must commit generated files since `make build` doesn't depend on `make generate-ebpf`
- **arm64 stubs required** — Lambda runs on Graviton (arm64). All eBPF Go files need `//go:build linux && amd64` and corresponding `_stub.go` with `//go:build linux && !amd64` providing no-op implementations
- **km binary upload to S3** — the `km` binary must be cross-compiled for linux/amd64 and uploaded to `s3://bucket/sidecars/km`. User-data downloads it. `make sidecars` now includes the km binary

## Kernel / Verifier

- **AL2023 kernel 6.1** — has BTF, CO-RE, ring buffer, cgroup BPF, LPM trie. Does NOT have TCX (needs 6.6+)
- **BPF verifier is strict** — hand-rolled struct layouts (even slightly wrong) cause `R6 invalid mem access 'scalar'`. Always use system headers
- **Ring buffer works** — `BPF_MAP_TYPE_RINGBUF` with `bpf_ringbuf_reserve`/`submit` pattern. 16MB buffer. Go reads via `ringbuf.NewReader(objs.Events)`

## Cgroup Architecture

- **Sandbox cgroup**: `/sys/fs/cgroup/km.slice/km-{sandbox-id}.scope` — created in user-data, `chown root:sandbox` on `cgroup.procs` for write access
- **Wrapper shell**: `/usr/local/bin/km-sandbox-shell` — set as sandbox user's login shell via `usermod -s`. Moves `$$` into cgroup then `exec /bin/bash --login "$@"`. The `--login` is critical for profile.d env vars (HTTPS_PROXY etc.)
- **SSM commands run as root** — NOT in the sandbox cgroup. For testing eBPF enforcement via SSM, must explicitly `echo $$ > cgroup.procs` first. Real `km shell` sessions run as sandbox user and go through the wrapper

## DNS Resolution

- **resolv.conf override** only in pure `ebpf` mode — `nameserver 127.0.0.1` routes DNS through enforcer's resolver on :53
- **In `both` mode**: DNS proxy sidecar on :5353 + iptables DNAT handles DNS. Enforcer skips DNS resolver (`--dns-port 0`)
- **Pre-seeding the BPF trie** is essential — `net.LookupHost()` on all `--allowed-hosts` at startup. Without it, first connection to any allowed host fails because connect4 blocks before DNS can populate the trie
- **VPC DNS (169.254.169.253) and link-local (169.254.0.0/16) must be in the allowlist** — glibc's resolver connects to VPC DNS, which BPF intercepts

## Enforcement Modes

- **`ebpf`**: BPF programs in `block` mode. DNS resolver on :53. No iptables, no proxy env vars. Pure kernel enforcement
- **`both`**: BPF programs in `log` mode (observe only). Proxy + iptables handle enforcement. BPF connect4 fires BEFORE iptables DNAT — if BPF blocks, iptables never fires. This is why `both` mode MUST use `log` not `block`
- **`proxy`**: No eBPF at all. Existing behavior

## For Phase 41 Uprobe Specifics

- **ecapture is the primary reference** — Go CLI + cilium/ebpf + uprobes on OpenSSL/GnuTLS/NSS/Go. Same library we use
- **`link.OpenExecutable(path).Uprobe(symbol, prog, nil)`** — the cilium/ebpf API for attaching uprobes
- **The enforcer process (`km ebpf-attach`) runs as a systemd service** — uprobes should be attached in the same process/service, extending the existing `ebpf-attach` command
- **Library discovery**: scan `/proc/<pid>/maps` for loaded `.so` files. On AL2023, OpenSSL is at `/usr/lib64/libssl.so.3`
- **Go crypto/tls uretprobe is broken** — must find all RET offsets and attach uprobe at each one
- **Budget metering integration**: captured plaintext should be routed through existing `ExtractBedrockTokens()`/`ExtractAnthropicTokens()` + `IncrementAISpend()` DynamoDB path
