package compiler

// TestActionLimitsEmission (CMP-01) — Phase 121 Plan 07.
//
// Behaviour under test:
//  1. A profile with spec.limits (at least one configured window) causes the compiler
//     to emit both KM_QUOTA_TABLE and KM_ACTION_LIMITS into the proxy systemd drop-in.
//  2. A profile with no spec.limits emits NEITHER line — userdata is dormant (byte-identical
//     to a build without quota support).
//  3. The install-level defaults (passed via NetworkConfig.InstallLimits) are also
//     resolved and emitted when the profile has no limits of its own.
//  4. Profile wins over install default per-window.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"github.com/whereiskurt/klanker-maker/pkg/quota"
)

// int64Ptr is a helper to get a pointer to an int64 value.
func int64Ptr(v int64) *int64 { return &v }

// TestActionLimitsEmission verifies the proxy drop-in emission rules for CMP-01.
func TestActionLimitsEmission(t *testing.T) {
	t.Run("limits_present_emits_both_env_lines", func(t *testing.T) {
		p := baseProfile()
		lifetime := int64(100)
		perHour := int64(15)
		p.Spec.Limits = &profile.LimitsSpec{
			GithubPR: &profile.ActionLimitSpec{
				Lifetime: &lifetime,
				PerHour:  &perHour,
				OnBreach: "freeze",
			},
		}

		out, err := generateUserData(p, "sb-quota-01", nil, "my-bucket", false, nil)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}

		if !strings.Contains(out, "KM_QUOTA_TABLE") {
			t.Errorf("expected KM_QUOTA_TABLE in proxy drop-in when limits are set\n--- snippet ---\n%s", abbreviateUD(out))
		}
		if !strings.Contains(out, "KM_ACTION_LIMITS") {
			t.Errorf("expected KM_ACTION_LIMITS in proxy drop-in when limits are set\n--- snippet ---\n%s", abbreviateUD(out))
		}

		// The JSON must be valid and contain github_pr with the configured windows.
		limitsStart := strings.Index(out, "KM_ACTION_LIMITS=")
		if limitsStart < 0 {
			t.Fatal("KM_ACTION_LIMITS not found in output")
		}
		// Extract the JSON value (rest of the line after the "=")
		lineRest := out[limitsStart+len("KM_ACTION_LIMITS="):]
		lineEnd := strings.IndexByte(lineRest, '\n')
		if lineEnd > 0 {
			lineRest = lineRest[:lineEnd]
		}
		lineRest = strings.TrimSpace(lineRest)

		var resolved quota.Limits
		if err := json.Unmarshal([]byte(lineRest), &resolved); err != nil {
			t.Fatalf("KM_ACTION_LIMITS is not valid JSON: %v\nvalue: %q", err, lineRest)
		}
		al, ok := resolved[quota.ActionGithubPR]
		if !ok {
			t.Fatalf("github_pr missing from resolved limits JSON: %v", resolved)
		}
		if al.Lifetime == nil || *al.Lifetime != 100 {
			t.Errorf("github_pr.lifetime: got %v, want 100", al.Lifetime)
		}
		if al.PerHour == nil || *al.PerHour != 15 {
			t.Errorf("github_pr.perHour: got %v, want 15", al.PerHour)
		}
		if al.OnBreach != quota.BreachFreeze {
			t.Errorf("github_pr.onBreach: got %q, want %q", al.OnBreach, quota.BreachFreeze)
		}
	})

	t.Run("no_limits_emits_neither_line", func(t *testing.T) {
		p := baseProfile()
		// No spec.limits set — dormant.

		out, err := generateUserData(p, "sb-quota-02", nil, "my-bucket", false, nil)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}

		if strings.Contains(out, "KM_QUOTA_TABLE") {
			t.Errorf("expected no KM_QUOTA_TABLE in proxy drop-in when no limits configured")
		}
		if strings.Contains(out, "KM_ACTION_LIMITS") {
			t.Errorf("expected no KM_ACTION_LIMITS in proxy drop-in when no limits configured")
		}
	})

	t.Run("install_defaults_only_emits_lines", func(t *testing.T) {
		p := baseProfile()
		// No profile limits — but install defaults provided via NetworkConfig.
		perDay := int64(50)
		network := &NetworkConfig{
			InstallLimits: quota.Limits{
				quota.ActionEmailSend: quota.ActionLimit{
					PerDay:   &perDay,
					OnBreach: quota.BreachWarn,
				},
			},
		}

		out, err := generateUserData(p, "sb-quota-03", nil, "my-bucket", false, network)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}

		if !strings.Contains(out, "KM_QUOTA_TABLE") {
			t.Errorf("expected KM_QUOTA_TABLE when install defaults are set")
		}
		if !strings.Contains(out, "KM_ACTION_LIMITS") {
			t.Errorf("expected KM_ACTION_LIMITS when install defaults are set")
		}
	})

	t.Run("profile_wins_over_install_default_per_window", func(t *testing.T) {
		p := baseProfile()
		// Profile sets perHour=5; install default sets perHour=20.
		// Resolved: perHour=5 (profile wins).
		profilePerHour := int64(5)
		installPerHour := int64(20)
		p.Spec.Limits = &profile.LimitsSpec{
			GithubComment: &profile.ActionLimitSpec{
				PerHour: &profilePerHour,
			},
		}
		network := &NetworkConfig{
			InstallLimits: quota.Limits{
				quota.ActionGithubComment: quota.ActionLimit{
					PerHour: &installPerHour,
				},
			},
		}

		out, err := generateUserData(p, "sb-quota-04", nil, "my-bucket", false, network)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}

		limitsStart := strings.Index(out, "KM_ACTION_LIMITS=")
		if limitsStart < 0 {
			t.Fatal("KM_ACTION_LIMITS not found in output")
		}
		lineRest := out[limitsStart+len("KM_ACTION_LIMITS="):]
		lineEnd := strings.IndexByte(lineRest, '\n')
		if lineEnd > 0 {
			lineRest = lineRest[:lineEnd]
		}

		var resolved quota.Limits
		if err := json.Unmarshal([]byte(strings.TrimSpace(lineRest)), &resolved); err != nil {
			t.Fatalf("KM_ACTION_LIMITS is not valid JSON: %v", err)
		}
		al := resolved[quota.ActionGithubComment]
		if al.PerHour == nil || *al.PerHour != 5 {
			t.Errorf("profile perHour=5 should win over install default 20; got %v", al.PerHour)
		}
	})
}
