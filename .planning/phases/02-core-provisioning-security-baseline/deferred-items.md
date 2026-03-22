# Deferred Items - Phase 02

## pkg/compiler build failures (pre-existing, not caused by 02-02)

**Found during:** Plan 02-02 full test suite run
**Status:** Pre-existing before 02-02 changes

The `pkg/compiler` package was broken before plan 02-02:
- Before 02-02: failed due to missing go.sum entry for `github.com/google/uuid`
- After 02-02 (go.mod updated with AWS SDK): now fails with `undefined: SGRule` and `undefined: IAMSessionPolicy`

The `security.go` file references `SGRule` and `IAMSessionPolicy` types that are not defined in the compiler package.
These types likely need to be defined in a `types.go` file in `pkg/compiler/` — this is scope for Plan 02-01 (compiler).

**Action:** Will be resolved when Plan 02-01 (profile compiler) is implemented.
