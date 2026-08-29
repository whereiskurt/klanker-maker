package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// maxLifetimeProfile builds a complete v1alpha2 profile by taking the shipped
// `hardened` builtin and swapping in the given lifecycle block. Basing the fixture
// on a real profile rather than a hand-written minimal one keeps these tests from
// failing for reasons that have nothing to do with maxLifetime — and means they
// keep exercising a currently-valid profile as the schema evolves.
//
// The tests deliberately go through ValidateSchema on YAML bytes rather than
// setting fields on the struct. That distinction is the whole point here:
// spec.otp has four green unit tests that all set p.Spec.OTP directly, and it has
// never once been reachable through the schema. maxLifetime was in exactly that
// state until this change, and a struct-level test would not have caught it.
func maxLifetimeProfile(t *testing.T, lifecycle string) []byte {
	t.Helper()
	base, err := os.ReadFile("builtins/hardened.yaml")
	if err != nil {
		t.Fatalf("read hardened builtin: %v", err)
	}
	const oldLifecycle = `  lifecycle:
    ttl: "4h"
    idleTimeout: "1h"
    teardownPolicy: destroy
`
	s := string(base)
	if !strings.Contains(s, oldLifecycle) {
		t.Fatalf("hardened.yaml lifecycle block changed shape; update this fixture")
	}
	return []byte(strings.Replace(s, oldLifecycle, "  lifecycle:\n"+lifecycle+"\n", 1))
}

// validateMaxLifetime runs the same schema-then-semantic sequence `km validate`
// runs, and returns only hard errors (warnings are not failures here).
func validateMaxLifetime(t *testing.T, lifecycle string) []profile.ValidationError {
	t.Helper()
	raw := maxLifetimeProfile(t, lifecycle)
	p, err := profile.Parse(raw)
	if err != nil {
		// A schema-rejected value can still parse; only a malformed document fails here.
		t.Fatalf("parse failed: %v", err)
	}
	var all []profile.ValidationError
	all = append(all, profile.ValidateSchema(raw)...)
	all = append(all, profile.ValidateSemantic(p)...)

	var errs []profile.ValidationError
	for _, e := range all {
		if !e.IsWarning {
			errs = append(errs, e)
		}
	}
	return errs
}

// hasErrorAt reports whether any error is anchored at the given profile path.
// Asserting on the path (not merely on "some error occurred") is what stops these
// tests from passing for an unrelated reason.
func hasErrorAt(errs []profile.ValidationError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}

const lifeOK = `    ttl: "4h"
    idleTimeout: "1h"
    teardownPolicy: destroy`

// TestMaxLifetime_AcceptedBySchema is the regression that matters. maxLifetime was
// implemented in CheckMaxLifetime and threaded through all three km create paths,
// but was absent from the JSON schema while spec.lifecycle is
// additionalProperties: false — so every profile that set it failed validation with
// "additional properties 'maxLifetime' not allowed" and the feature was unreachable.
func TestMaxLifetime_AcceptedBySchema(t *testing.T) {
	errs := validateMaxLifetime(t, lifeOK+"\n    maxLifetime: \"72h\"")
	if len(errs) != 0 {
		t.Fatalf("expected maxLifetime to validate, got errors: %v", errs)
	}
}

// TestMaxLifetime_DaySuffixAccepted pins the "d" suffix, which the ttl and
// idleTimeout patterns already allow. A lifetime cap is naturally written in days,
// so this is the common case rather than an edge one.
func TestMaxLifetime_DaySuffixAccepted(t *testing.T) {
	errs := validateMaxLifetime(t, "    ttl: \"8h\"\n    idleTimeout: \"1h\"\n    teardownPolicy: destroy\n    maxLifetime: \"3d\"")
	if len(errs) != 0 {
		t.Fatalf("expected day-suffixed maxLifetime to validate, got errors: %v", errs)
	}
}

// TestMaxLifetime_RejectsMalformed keeps the pattern honest. An unsuffixed or
// unknown-unit value must be caught at validate time rather than surviving to
// become a parse error inside km extend — which is the failure mode this change
// exists to close.
func TestMaxLifetime_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{"72", "72 hours", "forever", "-4h", "4w", ""} {
		errs := validateMaxLifetime(t, lifeOK+"\n    maxLifetime: \""+bad+"\"")
		if !hasErrorAt(errs, "spec.lifecycle.maxLifetime") {
			t.Errorf("maxLifetime %q should have been rejected at spec.lifecycle.maxLifetime, got: %v", bad, errs)
		}
	}
}

// TestMaxLifetime_MustNotBeShorterThanTTL — a cap below the initial ttl makes the
// sandbox un-extendable from the moment it boots, which is never what an operator
// means. The cap is there to bound extension, not to contradict the ttl.
func TestMaxLifetime_MustNotBeShorterThanTTL(t *testing.T) {
	errs := validateMaxLifetime(t, "    ttl: \"24h\"\n    idleTimeout: \"1h\"\n    teardownPolicy: destroy\n    maxLifetime: \"4h\"")
	if !hasErrorAt(errs, "spec.lifecycle.maxLifetime") {
		t.Fatalf("expected maxLifetime < ttl to be rejected, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if e.Path == "spec.lifecycle.maxLifetime" && strings.Contains(e.Message, "must not be shorter than ttl") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the shorter-than-ttl message, got: %v", errs)
	}
}

// TestMaxLifetime_EqualToTTLAllowed — equal means "no extensions permitted", which
// is a legitimate thing to configure and must not be an error.
func TestMaxLifetime_EqualToTTLAllowed(t *testing.T) {
	errs := validateMaxLifetime(t, lifeOK+"\n    maxLifetime: \"4h\"")
	if len(errs) != 0 {
		t.Fatalf("maxLifetime == ttl must be allowed, got: %v", errs)
	}
}

// TestMaxLifetime_DayVsHourComparison guards the cross-unit comparison. "1d" is
// longer than "8h"; a same-unit or string comparison would get this backwards.
func TestMaxLifetime_DayVsHourComparison(t *testing.T) {
	errs := validateMaxLifetime(t, "    ttl: \"8h\"\n    idleTimeout: \"1h\"\n    teardownPolicy: destroy\n    maxLifetime: \"1d\"")
	if len(errs) != 0 {
		t.Fatalf("maxLifetime 1d vs ttl 8h must be allowed, got: %v", errs)
	}
	errs = validateMaxLifetime(t, "    ttl: \"2d\"\n    idleTimeout: \"1h\"\n    teardownPolicy: destroy\n    maxLifetime: \"12h\"")
	if !hasErrorAt(errs, "spec.lifecycle.maxLifetime") {
		t.Fatalf("maxLifetime 12h vs ttl 2d must be rejected, got: %v", errs)
	}
}

// TestMaxLifetime_OmittedIsStillValid — the field is optional; absence means no cap
// and must not turn into a missing-required-property error.
func TestMaxLifetime_OmittedIsStillValid(t *testing.T) {
	errs := validateMaxLifetime(t, lifeOK)
	if len(errs) != 0 {
		t.Fatalf("expected profile without maxLifetime to validate, got: %v", errs)
	}
}

// TestMaxLifetime_TTLZeroSkipsComparison — ttl "0" is the `--ttl 0` sentinel for
// "no auto-destroy". There is nothing meaningful to compare a cap against, so the
// rule must stay silent rather than emit a nonsense "shorter than 0" error.
func TestMaxLifetime_TTLZeroSkipsComparison(t *testing.T) {
	errs := validateMaxLifetime(t, "    ttl: \"0\"\n    idleTimeout: \"1h\"\n    teardownPolicy: destroy\n    maxLifetime: \"4h\"")
	if hasErrorAt(errs, "spec.lifecycle.maxLifetime") {
		t.Fatalf("maxLifetime rule must not fire when ttl is the 0 sentinel, got: %v", errs)
	}
}
