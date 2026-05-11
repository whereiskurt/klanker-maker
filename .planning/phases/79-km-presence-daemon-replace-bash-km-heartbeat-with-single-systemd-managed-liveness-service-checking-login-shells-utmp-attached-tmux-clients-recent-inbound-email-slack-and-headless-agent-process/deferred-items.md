# Phase 79 Deferred Items

Items discovered during Phase 79 execution that are out of scope for this phase.
Logged per deviation scope-boundary rule: do NOT fix pre-existing issues in unrelated files.

---

## DEFERRED-79-01: km-presence missing from `buildAndUploadSidecars` Go-side path

**Status:** RESOLVED — fixed in-phase before PR merge.
**Resolution:** Added `{name: "km-presence", srcDir: "cmd/km-presence"}` to the `sidecars` slice in `buildAndUploadSidecars` (`internal/app/cmd/init.go`). `km init --sidecars` now ships km-presence to S3 alongside the other sidecars.

**Discovered during:** Plan 79-05 UAT (Task 3)
**Severity:** Medium — operators using `km init --sidecars` without `make` access will miss km-presence
**Workaround (pre-fix):** `make sidecars` (correctly uploads km-presence via Makefile target added in Plan 79-03)

### What's wrong

`internal/app/cmd/init.go` function `buildAndUploadSidecars` maintains an explicit list of
sidecar binaries to fetch and upload to S3. As of Phase 79, this list does NOT include
`km-presence`.

The Makefile `sidecars` target (in `Makefile`, updated by Plan 79-03) correctly builds and
uploads `km-presence` to S3. However, the Go CLI path — which operators invoke via
`./km init --sidecars` — pulls its binary list from `buildAndUploadSidecars`, NOT the
Makefile. So running `km init --sidecars` on a fresh operator laptop (where `make sidecars`
was never run) will leave km-presence absent from S3, silently breaking `km create` for
Phase-79-enabled profiles.

### Fix recipe

File: `internal/app/cmd/init.go`
Function: `buildAndUploadSidecars` (search for `km-dns-proxy` or `km-audit-log` to find it)

Add `"km-presence"` to the sidecar binary list, following the same pattern as other sidecars
in the list (typically: build cross-compiled Linux amd64 binary from `./cmd/km-presence/`,
upload to `s3://{artifactsBucket}/sidecars/km-presence`).

The binary is already buildable via `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath
-ldflags '-s -w' -o build/km-presence ./cmd/km-presence/`.

### How to verify the fix

```bash
# After adding km-presence to buildAndUploadSidecars:
./km init --sidecars --dry-run=false
aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-presence  # must exist
```

### Priority

Fix before the next phase that modifies `internal/app/cmd/init.go` or documents `km init
--sidecars` as the canonical sidecar deploy path. Until fixed, operators should use
`make sidecars` as the authoritative upload step (as documented in CLAUDE.md § Presence
daemon (Phase 79)).
