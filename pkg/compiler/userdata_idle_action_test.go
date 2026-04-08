package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TestIdleActionHibernateInUserdata verifies that IDLE_ACTION=hibernate appears
// in generated userdata when IdleAction is "hibernate" in userDataParams.
func TestIdleActionHibernateInUserdata(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate: %v", err)
	}

	params := userDataParams{
		SandboxID:          "sb-test01",
		IdleTimeoutMinutes: 30,
		IdleAction:         "hibernate",
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template Execute: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "IDLE_ACTION=hibernate") {
		t.Errorf("expected IDLE_ACTION=hibernate in userdata when IdleAction=hibernate, got:\n%s", out)
	}
}

// TestIdleActionAbsentWhenEmpty verifies that IDLE_ACTION env var is NOT emitted
// when IdleAction is "" (the default/destroy case).
func TestIdleActionAbsentWhenEmpty(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate: %v", err)
	}

	params := userDataParams{
		SandboxID:          "sb-test02",
		IdleTimeoutMinutes: 30,
		IdleAction:         "", // default — no env var should appear
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template Execute: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "IDLE_ACTION") {
		t.Errorf("expected IDLE_ACTION to be absent from userdata when IdleAction is empty, got:\n%s", out)
	}
}

// TestIdleActionAbsentWhenNoIdleTimeout verifies that IDLE_ACTION env var is NOT emitted
// when IdleTimeout is 0 (no idle detection configured).
func TestIdleActionAbsentWhenNoIdleTimeout(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate: %v", err)
	}

	params := userDataParams{
		SandboxID:          "sb-test03",
		IdleTimeoutMinutes: 0,           // no idle timeout
		IdleAction:         "hibernate", // even if hibernate, no idle = no env var
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template Execute: %v", err)
	}
	out := buf.String()

	// IDLE_TIMEOUT_MINUTES also should not appear when IdleTimeoutMinutes is 0.
	if strings.Contains(out, "IDLE_TIMEOUT_MINUTES") {
		t.Errorf("expected IDLE_TIMEOUT_MINUTES absent when IdleTimeoutMinutes=0")
	}
	if strings.Contains(out, "IDLE_ACTION") {
		t.Errorf("expected IDLE_ACTION absent when IdleTimeoutMinutes=0")
	}
}

// TestIdleActionFromProfileTTLZero verifies that idleActionFromProfile returns "hibernate"
// when TTL is empty (--ttl 0 sentinel) and IdleTimeout is set.
func TestIdleActionFromProfileTTLZero(t *testing.T) {
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Lifecycle: profile.LifecycleSpec{
				TTL:         "", // --ttl 0 sentinel
				IdleTimeout: "30m",
			},
		},
	}
	got := idleActionFromProfile(p)
	if got != "hibernate" {
		t.Errorf("idleActionFromProfile: got %q, want %q", got, "hibernate")
	}
}

// TestIdleActionFromProfileTTLSet verifies that idleActionFromProfile returns ""
// when TTL is non-empty (normal case — one-shot destroy).
func TestIdleActionFromProfileTTLSet(t *testing.T) {
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Lifecycle: profile.LifecycleSpec{
				TTL:         "3h",
				IdleTimeout: "30m",
			},
		},
	}
	got := idleActionFromProfile(p)
	if got != "" {
		t.Errorf("idleActionFromProfile: got %q, want %q (empty)", got, "")
	}
}

// TestIdleActionFromProfileNoIdleTimeout verifies that idleActionFromProfile returns ""
// when IdleTimeout is not set (regardless of TTL).
func TestIdleActionFromProfileNoIdleTimeout(t *testing.T) {
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Lifecycle: profile.LifecycleSpec{
				TTL:         "", // --ttl 0 sentinel
				IdleTimeout: "", // no idle timeout
			},
		},
	}
	got := idleActionFromProfile(p)
	if got != "" {
		t.Errorf("idleActionFromProfile: got %q, want %q (empty)", got, "")
	}
}

// TestIdleActionGenerateUserDataTTLZero verifies that IDLE_ACTION=hibernate appears
// in full generated userdata when profile has TTL="" and IdleTimeout set.
func TestIdleActionGenerateUserDataTTLZero(t *testing.T) {
	p := baseProfile()
	p.Spec.Lifecycle.TTL = ""       // --ttl 0 sentinel
	p.Spec.Lifecycle.IdleTimeout = "30m"

	out, err := generateUserData(p, "sb-idle01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if !strings.Contains(out, "IDLE_ACTION=hibernate") {
		t.Errorf("expected IDLE_ACTION=hibernate in userdata for TTL=0 profile, got output did not contain it")
	}
	if !strings.Contains(out, "IDLE_TIMEOUT_MINUTES=30") {
		t.Errorf("expected IDLE_TIMEOUT_MINUTES=30 in userdata")
	}
}

// TestIdleActionGenerateUserDataTTLSet verifies that IDLE_ACTION is absent
// when profile TTL is non-empty (normal destroy-on-idle case).
func TestIdleActionGenerateUserDataTTLSet(t *testing.T) {
	p := baseProfile()
	p.Spec.Lifecycle.TTL = "3h"
	p.Spec.Lifecycle.IdleTimeout = "30m"

	out, err := generateUserData(p, "sb-idle02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if strings.Contains(out, "IDLE_ACTION") {
		t.Errorf("expected IDLE_ACTION absent from userdata when TTL=3h")
	}
}
