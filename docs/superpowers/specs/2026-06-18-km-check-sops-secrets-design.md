# `km check` SOPS secrets — design (deploy-time unpack to SSM)

**Status:** Design (ready to implement next session)
**Date:** 2026-06-18

## Goal

Let a check expose a SOPS-encrypted secrets file's values to its Lambda snippet as env
vars (like sandboxes do via `spec.secrets.sopsFile`), WITHOUT adding a KMS-decrypt path
inside the Lambda. Reuse the existing check secret mechanism (SSM `{prefix}/checks/*` →
bootstrap fetch → env var) by **unpacking the SOPS file into per-check SSM SecureString
params at `km check deploy` time** (operator-side, where SOPS already works).

## Approach (operator chose this)

`km check deploy snippet.py --sops secrets.enc.yaml [--secret …] [--env …]`

At deploy:
1. **Decrypt** operator-side: `sops -d <file>` → flat YAML/JSON `map[string]string`.
   (Mirror how sandboxes / `km bootstrap --shared-secrets-key` invoke sops; reuse any
   existing sops wrapper in pkg/. Fail clearly if `sops` missing or decrypt fails.)
2. For each `(k, v)`: `ssm PutParameter` Name=`/{prefix}/checks/{checkName}/{k}`
   Type=SecureString Value=v Overwrite=true. (Namespacing per check under
   `{prefix}/checks/{check}/` keeps `km check rm` cleanup scoped + matches the role's
   `{prefix}/checks/*` grant — no IAM change.)
3. Add those param paths to the check's `secretPaths` (the existing `--secret` list) so
   they land in `KM_CHECK_SECRET_PATHS`. The bootstrap already fetches each with
   `WithDecryption=True` and exposes it as an env var keyed by the **last path segment,
   uppercased** → `/{prefix}/checks/{check}/wiz_token` ⇒ `$WIZ_TOKEN`.

Result: snippet sees every SOPS key (uppercased) as an env var. No Lambda-side KMS, no
SOPS in the bootstrap, role scope unchanged.

## Touch points

- `internal/app/cmd/check.go` — add `--sops <file>` flag to `deploy` (and `sync`).
- `pkg/check/` — a `UnpackSopsToSSM(ctx, ssmClient, prefix, checkName, sopsFile) ([]string, error)`
  helper (decrypt → flat map → PutParameter loop → return the param paths). Reuse the
  existing sops-decrypt helper if one exists (grep `sops -d` / `pkg` SOPS usage).
- DDB row: record the SOPS-derived param paths in `secret_paths` like any `--secret`
  (already "paths only, never values" — good). Optionally tag which came from SOPS so
  `km check rm` deletes the `/{prefix}/checks/{check}/*` params it created.
- `km check rm` — `ssm DeleteParameters` for the per-check namespace (GetParametersByPath
  `/{prefix}/checks/{check}/` → delete) so secrets don't leak after teardown.
- IAM: the check-runner role already reads `{prefix}/checks/*`. The OPERATOR running
  `km check deploy` needs `ssm:PutParameter` (+ the SOPS KMS key decrypt locally) — the
  operator already has broad perms; confirm `km check rm` operator path has
  `ssm:DeleteParameter`/`GetParametersByPath`.
- `source_hash` — fold the decrypted-keys set (NOT values) into the hash so `km check ls`
  flags drift when the SOPS file changes; `km check sync` re-unpacks.
- `km doctor` — optional: warn on orphan `/{prefix}/checks/{check}/*` params with no DDB row.

## Notes / gotchas

- Env-var key = last SSM path segment UPPERCASED (bootstrap `ssm_path.split("/")[-1].upper()`).
  So SOPS keys map to UPPERCASE env vars; document this (a SOPS key `apiKey` → `$APIKEY`,
  not `$apiKey`). Consider normalizing/validating keys to `[A-Z0-9_]`.
- Nested SOPS YAML: only flat top-level string values map cleanly to env vars; flatten or
  reject nested maps with a clear error.
- Secrets transit through the operator's machine + SSM (SecureString, KMS-encrypted at
  rest). That's the same trust model as `km bootstrap` secret handling.
- This is `km check` (Phase 116) follow-on, NOT a Phase-116 gap. New branch.

## Out of scope

- Decrypting SOPS inside the Lambda (rejected — needs KMS grant + bootstrap SOPS step).
- Auto-rotation; operators re-run `km check deploy --sops` / `km check sync` to refresh.
