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

## 📬 Inbound email accepted anything, from anyone

Any stranger who mailed `{sandbox-id}@<your domain>` had their text delivered into the
sandbox mailbox and rendered to the agent as ordinary mail. For an autonomous agent that is
an unauthenticated route into its context, and three independent defects each opened it:

- Both shell gates — the mail poller and `km-recv` — wrapped the check in
  `if [ -n "${KM_ALLOWED_SENDERS:-}" ]`. An **empty value meant "no gate"**, not "no senders".
  The ingress failed open.
- `joinAllowedSenders` returned `""` whenever a profile omitted `spec.email.allowedSenders` —
  precisely the value that disabled the gate above.
- `profiles/base/platform.yaml` shipped `allowedSenders: ["*"]`, and nearly every profile
  extends it, so even a working gate was wide open fleet-wide.

An unset allowlist now yields a closed default: `self`, `*@<your install email domain>`, and
your own operator address. That last one matters — it usually lives at an unrelated domain,
and without it, replying to a sandbox from a normal mail client would silently stop working.
Both gates enforce unconditionally, so a hand-cleared value denies rather than admits
everyone. An explicit `allowedSenders` is still honoured verbatim, `"*"` included.

The wildcard was **removed** from `base/platform.yaml` rather than narrowed there. Inheritance
unions lists, so a value in that fragment can only ever be widened by a leaf, never narrowed —
keeping the default in the compiler is what makes a tighter per-profile allowlist expressible
at all.

**Existing sandboxes keep the old policy until `km destroy && km create`.**

## 🔎 km doctor could not see a total inbound-mail outage

SES allows exactly one *active* receipt rule set per account and region. If anything else in
the account holds that slot — a sibling install, an unrelated Terraform project — every message
to your domain is evaluated against rules that match nothing, and is dropped. Sending is
unaffected, because outbound never consults receipt rules, so it presents as a receive-side
bug in a component that is working perfectly.

Nothing caught it. The existing check inspects the *contents* of `sandbox-email-shared` and
reported `SES rules healthy (2 rules for prefix "km")` throughout, while terraform does not
manage the activation pointer at all on an install where `register_shared_rule_set` is false —
that resource is `count = 0`, with a comment saying the rule set is *assumed* already active.
Nothing rechecked the assumption.

`km doctor` now reports it, and the two checks read together:

```
x SES active rule set  "other-email" is active, not "sandbox-email-shared"
                       - inbound email for this install is dropped (sending is unaffected)
v SES rules            SES rules healthy (2 rules for prefix "km")
```

The remediation deliberately does not stop at `set-active-receipt-rule-set`: activating your
shared set stops SES evaluating the other set's rules, so it names the other set and tells you
to migrate them in first.

## 📋 Upgrading

`make build` + `make build-lambdas` + `km init --dry-run=false`. The doctor check is
operator-side and live on `make build` alone; the allowlist rides in create-handler-rendered
userdata, so existing sandboxes keep the old policy until they are recreated.
