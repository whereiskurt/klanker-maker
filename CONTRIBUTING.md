# Contributing to Klanker Maker

Thanks for your interest. Klanker Maker is the personal project of Kurt
Hundeck (see [NOTICE.md](NOTICE.md)). Outside contributions are welcome under
the terms below.

## Before you start

- This is a side project. Reviews and merges happen on side-project time.
  Please don't expect commercial-grade response times.
- For substantial changes, **open an issue first** so we can talk through the
  design before you write a lot of code. Most planning lives in `.planning/`
  (GSD-style phase plans) and the architecture is opinionated - your time is
  better spent if we align early.
- Small fixes (typos, broken links, clearly-wrong code) are welcome as direct
  PRs without prior discussion.

## Developer Certificate of Origin (DCO)

All contributions must be submitted under the
[Developer Certificate of Origin 1.1](https://developercertificate.org/).

Every commit must carry a `Signed-off-by:` trailer:

```
Signed-off-by: Your Real Name <your@email.example>
```

Add it automatically with:

```bash
git commit -s
```

The name and email must correspond to a real identity (no anonymous or
pseudonymous contributions). By signing off, you certify the four DCO
clauses, reproduced here for clarity:

> By making a contribution to this project, I certify that:
>
> (a) The contribution was created in whole or in part by me and I have the
>     right to submit it under the open source license indicated in the file;
>     or
>
> (b) The contribution is based upon previous work that, to the best of my
>     knowledge, is covered under an appropriate open source license and I
>     have the right under that license to submit that work with
>     modifications, whether created in whole or in part by me, under the
>     same open source license (unless I am permitted to submit under a
>     different license), as indicated in the file; or
>
> (c) The contribution was provided directly to me by some other person who
>     certified (a), (b) or (c) and I have not modified it.
>
> (d) I understand and agree that this project and the contribution are
>     public and that a record of the contribution (including all personal
>     information I submit with it, including my sign-off) is maintained
>     indefinitely and may be redistributed consistent with this project or
>     the open source license(s) involved.

Full text: https://developercertificate.org/

## Contributor warranty (please read carefully)

In addition to the DCO, by submitting a pull request you represent and
warrant that:

1. **You have the right to make this contribution.** You either own the
   contribution outright or have explicit, documented permission from the
   copyright holder to submit it under the [MIT License](LICENSE).

2. **No employer or third-party claim.** The contribution is not the work
   product of any employer, contracting entity, or other party with a claim
   on your work, *unless* you have a written waiver, IP-assignment release,
   or open-source-contribution policy from that party that specifically
   authorizes you to make this contribution to this project under the MIT
   License.

3. **No confidential or proprietary material.** The contribution does not
   contain confidential information, proprietary code, trade secrets, customer
   data, or unpublished material belonging to any third party.

4. **No covered IP.** The contribution does not infringe any patent,
   copyright, trademark, or trade secret of any third party that you are
   aware of.

If you are contributing during work hours, on work equipment, or in a domain
that overlaps with your employer's business, **please verify with your
employer's open-source policy first.** Many employers have a clear process
(a one-line approval, a personal-projects exception, or a published OSS
contribution policy). It is not the maintainer's responsibility to enforce
your employer's IP rules; however, the maintainer reserves the right to
decline or revert any contribution where the contributor's right to submit
it is in doubt.

## License of contributions

By contributing, you agree that your contribution will be licensed under the
same [MIT License](LICENSE) that covers the rest of the project, and that the
project may be sublicensed and distributed under those terms.

## Style and conventions

- **Go**: must be `gofmt` clean and `go vet` clean. Run `make build` to
  verify - bare `go build` skips ldflags.
- **Commit messages**: present tense, scoped where useful
  (`fix(slack): handle empty thread_ts`). Match the existing style in
  `git log`.
- **Tests**: meaningful coverage for new behavior. Existing patterns in
  `*_test.go` show the house style.
- **Documentation**: if you add a CLI flag, profile field, sidecar, or AWS
  resource, update `README.md` and the relevant doc in `docs/`.
- **Security defaults**: changes that weaken the default security model
  (eBPF, SCP, signed email, allowlist-first networking) need strong
  justification and will get extra scrutiny.
- **No new SaaS dependencies** without prior discussion.

## What is unlikely to be merged

- Large refactors without prior discussion in an issue.
- New substrates (EKS, Nomad, etc.) that haven't been planned in `.planning/`.
- Changes that introduce a new mandatory third-party SaaS dependency.
- Cosmetic-only renames or restructures.
- Generated AI code that the contributor has not personally reviewed and
  understood line-by-line. (LLM-assisted contributions are fine; LLM-only
  contributions are not.)

## Code of Conduct

By participating, you agree to abide by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Questions

Open an issue, or email **whereiskurt@gmail.com**.
