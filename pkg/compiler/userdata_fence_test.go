package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// renderFenceUserdata renders a bundle-carrying profile with the fence on or off.
// The bundle is always present, so a dormant assertion cannot pass merely because
// there was no secrets block at all.
func renderFenceUserdata(t *testing.T, fence bool) string {
	t.Helper()
	p := baseProfile()
	p.Spec.Secrets = &profile.SecretsSpec{
		SopsFile:  "./secrets/test.enc.yaml",
		FenceIMDS: &fence,
	}
	out, err := generateUserData(p, "sb-fence01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// Dormant: a bundle-carrying profile that does not ask for the fence must render
// no fence artefacts at all.
func TestUserdata_NoFenceArtefactsWhenUnset(t *testing.T) {
	got := renderFenceUserdata(t, false)
	for _, forbidden := range []string{"km-imds-fence", "km-creds", "credential_process", "KM_FENCE_IMDS"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("dormant userdata contains %q", forbidden)
		}
	}
}

// The rule must live in the FILTER table. The nat-table IMDS rule in section 6 is
// a different rule with the opposite purpose (it RETURNs IMDS so IMDSv2 keeps
// working), and it only exists under enforcement: proxy — a fence written there
// would be silently absent under ebpf and both.
func TestUserdata_FenceIsInTheFilterTable(t *testing.T) {
	got := renderFenceUserdata(t, true)
	if !strings.Contains(got, "iptables -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT") {
		t.Error("no filter-table REJECT for uid sandbox to 169.254.169.254")
	}
	if strings.Contains(got, "-t nat -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox") {
		t.Error("the fence was written into the nat table, where it would be absent " +
			"under ebpf/both enforcement")
	}
}

// REJECT, never DROP: an SDK probing IMDS against a DROP waits out a full connect
// timeout on every call, which reads as a hang rather than a policy.
func TestUserdata_FenceRejectsRatherThanDrops(t *testing.T) {
	if strings.Contains(renderFenceUserdata(t, true), "--uid-owner sandbox -j DROP") {
		t.Error("the fence DROPs; it must REJECT so the SDK fails fast")
	}
}

// The fence must survive a resume. There is no iptables persistence anywhere in
// this repo, but km-secretsd.service and every shim ARE enabled units and do come
// back — so a userdata-only rule leaves a resumed box looking healthy with no
// fence at all.
func TestUserdata_FenceIsASystemdUnit(t *testing.T) {
	got := renderFenceUserdata(t, true)
	if !strings.Contains(got, "km-imds-fence.service") {
		t.Fatal("the fence is not a systemd unit: it would vanish on km resume")
	}
	if !strings.Contains(got, "systemctl enable --now km-imds-fence.service") {
		t.Error("km-imds-fence.service is never enabled, so it will not run on resume")
	}
}

// Assertion 6 must not race the rule it asserts.
func TestUserdata_SecretsCheckIsOrderedAfterTheFence(t *testing.T) {
	got := renderFenceUserdata(t, true)
	i := strings.Index(got, "KMSECRETSCHECK")
	if i < 0 {
		t.Fatal("km-secrets-check unit heredoc not found")
	}
	end := i + 900
	if end > len(got) {
		end = len(got)
	}
	if !strings.Contains(got[i:end], "After=km-imds-fence.service") {
		t.Error("km-secrets-check.service is not ordered after km-imds-fence.service: " +
			"assertion 6 can run before the rule exists")
	}
}

// credential_process is what keeps km-github/km-slack/km-h1 working behind the
// fence. Without it the fence is not a boundary, it is an outage.
func TestUserdata_WritesTheSandboxAWSConfig(t *testing.T) {
	got := renderFenceUserdata(t, true)
	if !strings.Contains(got, "/home/sandbox/.aws/config") {
		t.Fatal("no ~/.aws/config for the sandbox user")
	}
	if !strings.Contains(got, "credential_process = /opt/km/bin/km-creds") {
		t.Error("~/.aws/config does not name km-creds as its credential_process")
	}
}

func TestUserdata_InstallsKMCreds(t *testing.T) {
	if !strings.Contains(renderFenceUserdata(t, true), "sidecars/km-creds") {
		t.Error("km-creds is never fetched from S3; the credential_process would be a " +
			"missing binary and every AWS call as uid sandbox would fail")
	}
}

// The broker needs the fence flag and the two session-policy interpolation
// inputs, or mintCredentials refuses.
func TestUserdata_BrokerUnitCarriesFenceEnv(t *testing.T) {
	got := renderFenceUserdata(t, true)
	i := strings.Index(got, "KMSECRETSD")
	if i < 0 {
		t.Fatal("km-secretsd unit heredoc not found")
	}
	end := i + 2200
	if end > len(got) {
		end = len(got)
	}
	unit := got[i:end]
	for _, want := range []string{"KM_FENCE_IMDS=true", "KM_RESOURCE_PREFIX=", "KM_ARTIFACTS_BUCKET="} {
		if !strings.Contains(unit, want) {
			t.Errorf("km-secretsd.service is missing %s", want)
		}
	}
}

// BOTH selftest invocations need the fence env, or assertion 6 is silently
// skipped — the Server it builds reads KM_FENCE_IMDS, and Selftest only runs the
// fence assertion when FenceEnabled is true.
//
// The resume unit is the one that matters most: km-imds-fence.service coming back
// is exactly what a resumed box has to prove, and a selftest that skips the
// assertion would report a clean pass over a box with no fence.
func TestUserdata_BothSelftestInvocationsCarryFenceEnv(t *testing.T) {
	got := renderFenceUserdata(t, true)

	// Boot-time: a plain userdata command under set -euo pipefail.
	boot := strings.Index(got, "Running secrets self-test")
	if boot < 0 {
		t.Fatal("boot selftest invocation not found")
	}
	end := boot + 400
	if end > len(got) {
		end = len(got)
	}
	if !strings.Contains(got[boot:end], "KM_FENCE_IMDS=true") {
		t.Error("the boot selftest does not carry KM_FENCE_IMDS: assertion 6 would be " +
			"skipped and the boot would pass over an unfenced box")
	}

	// Resume: km-secrets-check.service.
	unit := strings.Index(got, "KMSECRETSCHECK")
	if unit < 0 {
		t.Fatal("km-secrets-check unit heredoc not found")
	}
	end = unit + 1200
	if end > len(got) {
		end = len(got)
	}
	for _, want := range []string{"KM_FENCE_IMDS=true", "KM_RESOURCE_PREFIX=", "KM_ARTIFACTS_BUCKET="} {
		if !strings.Contains(got[unit:end], want) {
			t.Errorf("km-secrets-check.service is missing %s: assertion 6 would be "+
				"skipped on every resume, the exact path where the fence unit "+
				"coming back is what needs proving", want)
		}
	}
}
