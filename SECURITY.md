# Security Policy

Klanker Maker is a personal project (see [NOTICE.md](NOTICE.md)). There is no
commercial support, no SLA, and no formal security team. The notes below
describe how to report vulnerabilities and what to expect in return.

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security-sensitive reports.**

Email **whereiskurt@gmail.com** with `[klanker-maker-security]` in the subject and
include:

- A description of the vulnerability
- Steps to reproduce (proof-of-concept code or commands welcome)
- Affected versions, commits, or default profiles
- Your assessment of impact and any suggested mitigations
- Whether you'd like to be credited in any eventual release notes

I will acknowledge receipt within **7 days** and aim to provide a more detailed
response within **30 days** indicating next steps and a rough timeline. As a
side project this may slip; please feel free to email a polite nudge if you
haven't heard back.

If you'd like to encrypt the report, ask in your first email and I will
respond with a public key.

## Scope

**In scope:**

- The `km` CLI and any binaries built from this repository
- Sidecar binaries (DNS proxy, HTTP proxy, audit log, tracing, `km-slack`)
- Lambda function source under `cmd/*-handler/`, `cmd/*-refresher/`, and the
  Slack bridge
- Terraform / Terragrunt modules in `infra/`
- The eBPF programs in `pkg/ebpf/`
- The default profiles shipped in `profiles/`

**Out of scope:**

- Vulnerabilities in third-party services Klanker Maker integrates with
  (AWS, Anthropic, Slack, GitHub, OpenAI). Please report those to the
  respective vendors.
- Issues that require operator misconfiguration to exploit. The threat model
  assumes the operator (the human running `km`) is trusted; the agents
  running inside sandboxes are not.
- Theoretical attacks against forks where the fork has materially changed the
  default security model (e.g., disabled the SCP, removed the eBPF enforcer,
  bypassed the proxy).
- Bugs in unmerged branches.

The defense-in-depth model is documented in
[README.md § Security Model](README.md#security-model) and
[docs/security-model.md](docs/security-model.md). Reports demonstrating gaps
in the documented controls are most useful.

## Disclosure

This is a personal project; I will work with reporters in good faith on
coordinated disclosure but cannot commit to a fixed embargo or guarantee any
particular timeline. As a rough guide:

- Critical issues with active exploit potential: I'll prioritize a fix before
  public disclosure where possible.
- Lower-severity issues: I may publish the fix and a brief advisory together.

If you intend to publish independently after a reasonable period (e.g., 90
days from acknowledgement), please tell me up front so I can plan around it.

## No Bug Bounty

There is no monetary bug bounty. Reports are accepted out of goodwill, and
credit is given in release notes if the reporter would like to be named.

## Supported Versions

Only the `main` branch and the most recent tagged release receive security
updates. Older versions are unsupported.

## See Also

- [LICENSE](LICENSE) - the warranty disclaimer that applies to this software.
- [NOTICE.md](NOTICE.md) - personal-project status, no employer affiliation.
- [CONTRIBUTING.md](CONTRIBUTING.md) - DCO and contributor warranty terms.
