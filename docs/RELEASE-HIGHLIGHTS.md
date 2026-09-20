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

  Drafted from CLAUDE.md phase blocks since v0.8.23 via
  scripts/draft-release-highlights.sh, then curated.
-->


## 🛡️ Hibernate/resume no longer cross-writes additional EBS volumes

On resume from hibernation the additional NVMe namespaces could come back **cross-wired** —
the kernel re-read each namespace's size but kept its cached identity and page cache, so the
fstab mount by UUID succeeded against the wrong disk and flushed both volumes onto each
other. Reproduced on the first `km pause`/`km resume` of a two-volume box: a 1 MiB known file
failed its checksum 33 s after resume. Only a **live** NVMe Identify told the truth; sysfs and
`blkid` both lied.

New sidecar **`km-volumes`** owns every additional volume: a first-boot manifest of physical
identity, a validated mount on **every** boot (resolve by live serial — never BDM letter or
UUID — then size, fs UUID, superblock), an unmount before hibernate, and an unconditional PCI
re-probe of every non-root controller after resume. A persistent mismatch is **refused**
(marker + state, loud in `km status`/`km resume`/`km doctor`, and the on-box agent is told
not to misdiagnose it) or, with `spec.runtime.onVolumeMismatch: reboot`, rebooted once for
headless boxes. `km-volumes repair` recovers already cross-written snapshot volumes from
their backup superblocks. Existing sandboxes need a recreate; `hibernation: false` is the
mitigation until then. See `docs/hibernate-volumes.md`.

## 🔑 Sandboxes are shareable between analysts

`km vscode|desktop|herdr|tunnel start` from a laptop that never ran `km create` now just
works. The per-sandbox SSH key and KasmVNC password live in SSM (`/{prefix}/access/<id>/`);
`~/.km/keys` and `~/.km/desktop` are a cache that `start` pulls, refreshes after someone
else's `rekey`, or publishes if SSM is empty — so every existing sandbox joins on the first
`start` from a laptop that holds its key. `rekey` publishes box → local → SSM; the
vscode/herdr pre-flight names `km vscode rekey` when the box's key disagrees with the shared
one. Upgrade every laptop before anyone rekeys.

## 🩺 `km status` no longer reports a working claude as logged out

The auth probes trusted profile.d to put `/opt/km/shims` first on PATH; nvm's prepend wins
in a login shell, so on a brokered-secrets box the probe asked the real binary (no key →
`loggedIn:false`) while every agent turn worked. The three probes now prepend the shim dir
explicitly, like every dispatch site, with a source guard so a fourth can't ship without it.

## ✨ Small things

- `km version` is an alias for `km --version`.
- `km list --wide`: an expired TTL renders `exp.` and the ⏸/⏹ icons are measured at their real
  one-column width, so stopped/paused rows line up with the rest.
- RedHat base profile installs `gh`.
