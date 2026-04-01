# Phase 41 Lessons: eBPF SSL Uprobe Observability

**Captured:** 2026-04-01 after 3 E2E iterations on AL2023 kernel 6.18

## BPF Verifier on Kernel 6.18

- **AL2023 now ships kernel 6.18** (not 6.1 as the research assumed). The verifier is stricter.
- **`R2 min value is negative`** error on `bpf_probe_read_user(buf, len, src)` — the verifier traces `len` back to `PT_REGS_PARM3(ctx)` which is `unsigned long` but treated as potentially negative by the verifier.
- **if-clamping is NOT sufficient** — `if (len > MAX) len = MAX` doesn't satisfy the verifier because the signed origin taints the register.
- **Fix: bitwise AND at assignment** — `copy_len = len & 0x3FFF` immediately bounds the value so the verifier statically knows `0 <= copy_len <= 16383`. Accept losing 1 byte vs MAX_PAYLOAD_LEN (16384).
- **`& 0x7FFF` applied inside the helper call is too late** — the verifier checks args before the call, not the expression within it.

## BPF Map Type Marshaling

- **cilium/ebpf `Map.Put(key, val)` is strict about types** — if the BPF map declares `__type(key, __u8)` and `__type(value, __u8)`, Go must use `uint8` not `uint32`. A uint32 won't marshal to 1 byte; the put silently fails with a logged warning.
- The `lib_enabled` map uses `__u8` key/value. The initial code used `uint32` → "marshal value: uint32 doesn't marshal to 1 bytes". Fixed to `uint8`.

## Uprobe Attachment

- **Uprobes are global to the library, not cgroup-scoped** — attaching to `/usr/lib64/libssl.so.3` fires for ALL processes system-wide, not just sandbox cgroup processes. This means the uprobe consumer sees system noise (SSM agent, yum, etc).
- **`/lib64/` and `/usr/lib64/` are the same** — `/lib64` is a symlink to `/usr/lib64` on AL2023. The discovery code finds `/usr/lib64/libssl.so.3` which matches what `ldd` reports as `/lib64/libssl.so.3`.
- **8 probes attach successfully**: SSL_write entry, SSL_read entry+return, SSL_write_ex entry, SSL_read_ex entry+return, plus 2 kprobes for connect/accept4.
- **Optional `_ex` variants succeed on AL2023** — OpenSSL 3.2.2 exports both `SSL_write` and `SSL_write_ex`.

## Connection Correlation Gap

- **`remote: 0.0.0.0:0` on all events** — the SSL_write/SSL_read BPF programs use `fd=0` as a hardcoded placeholder because the fd is not directly available from `SSL_write(SSL *ssl, void *buf, int num)` arguments.
- **The kprobe on `__sys_connect` populates conn_map correctly**, but the lookup key uses `fd=0` which never matches.
- **Three approaches to fix** (for a future phase):
  1. Extract fd from SSL struct (`ssl->wbio->num`) — version-specific offsets, ecapture pattern
  2. Syscall correlation — hook `write`/`sendto` and match by `pid_tgid` (Pixie pattern)
  3. Userspace `/proc/net/tcp` lookup — simplest but race-prone

## HTTP/2 vs HTTP/1.1

- **GitHub uses HTTP/2** — curl to `https://api.github.com/` sends HTTP/2 frames at the SSL layer. The captured plaintext contains HPACK-compressed binary, not parseable `GET /repos/... HTTP/1.1` text.
- **`ParseHTTPRequest()` only handles HTTP/1.1** — by design, per research. HTTP/2 header parsing requires maintaining per-connection HPACK dynamic table state.
- **git-smart-HTTP (git clone/push) uses HTTP/1.1 over TLS** — these requests WILL be captured and parsed correctly by the GitHub audit handler.
- **The MITM proxy already handles HTTP/2** — it terminates TLS and gets plaintext HTTP/2 streams via goproxy. The uprobe layer is additive, not replacement.

## Architecture: Uprobe Layer vs MITM Proxy

- **They work in series, not parallel**: eBPF `connect4` redirects traffic → MITM proxy terminates TLS → proxy inspects plaintext → proxy enforces policy.
- **Uprobes sit alongside, not in the path**: They observe the same plaintext passively, without interception or TLS termination.
- **The real value is coverage the proxy can't see**: Any traffic not redirected to the proxy (direct connections, new endpoints not in proxy-hosts list) is invisible to the proxy but visible to uprobes.
- **Budget metering stays in the proxy**: Token counts are in HTTP/2 response bodies (DATA frames), which uprobes can capture raw but can't parse without a full HTTP/2 demuxer. The proxy already does this.
- **GitHub enforcement stays in the proxy**: Active 403 blocking requires being in the request path. Uprobes observe but cannot block.

## Build / Deploy Pipeline

- Same as Phase 40: `make generate-ebpf` → Docker-based bpf2go → commit generated `.o`/`.go` files → `make build` → `make sidecars` (uploads km binary to S3) → `km init` → `km create`
- **3 create/test/destroy cycles** needed to get from verifier rejection to working events
- **Each cycle takes ~3 minutes** (create → wait for running → test via SSM)
