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

## 🩹 Exec capture: four fixes, all of them found by running it

v0.8.12 shipped `km-netpolicy execs | who | execs save`. Everything below was found by
actually using it on live sandboxes and measuring the result, not by reading the code —
each one had already survived review.

**`execs save` works when you run it by hand.** It read its S3 target straight from the
process environment, which only ever exists inside the `km-execlog.service` unit. From a
`km shell --root` session it exited `KM_ARTIFACTS_BUCKET is not set`. `/etc/km/netpolicy.env`
now carries `KM_EXEC_DIR`, `KM_ARTIFACTS_BUCKET`, `KM_SANDBOX_ID` and `AWS_REGION`, and the
verb resolves all four through its normal environment → env-file fallback.

**A graceful stop no longer loses its own upload to DNS.** Under `ebpf`/`both` enforcement
`/etc/resolv.conf` points at the resolver `km-ebpf-enforcer` provides. systemd stops units
in reverse `After=` order, and `km-execlog` declared only `After=network-online.target` —
so on a real shutdown the enforcer went down first and the trace upload died resolving the
S3 hostname. `systemctl stop` on a running box never exposes this, because the resolver is
still alive; only a real shutdown orders it down first.

**`km destroy` saves the trace before it tears anything down.** The `ExecStopPost` hook does
**not** cover an EC2 terminate — that was measured, not assumed. `km destroy` and the
ttl-handler's expiry path now run `execs save` on the instance over SSM while it is still
running, before terraform removes its networking. Best-effort by construction: a forensics
artifact must never be able to block a teardown.

**And the region that made the above fail anyway.** SSM's `AWS-RunShellScript` runs a bare,
non-login shell that never sources `/etc/profile.d` — so a region an interactive root login
takes for granted simply isn't there, and the save died on *"Invalid region: region was not
a valid DNS name."* Fixed at three independent points, because the sidecar binary and the
userdata that renders its env file ship on different cadences and either can be older.

If you are on v0.8.12, `km destroy` is silently discarding the exec trace of every sandbox
you tear down. That is the fix worth taking.

## 🛡️ A local `goreleaser` run can no longer leak your Terraform state config

`make release` / a local goreleaser run archived `infra/` wholesale — including any
`.terragrunt-cache/` and `.terraform/` directories left behind by a previous `km init`.
Measured on a real tree: **528 cached files**, carrying the state bucket name, the DynamoDB
lock table, and per-module state keys, headed for a public release archive. CI was never
affected (it builds from a clean checkout), so nothing published has ever carried this — but
anyone cutting a release from a working directory was one command away from it.
`scripts/check-release-tree.sh` now runs first in the build and refuses outright.
