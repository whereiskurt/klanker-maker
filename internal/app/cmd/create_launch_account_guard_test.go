package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	yaml "gopkg.in/yaml.v3"
)

// TestSelectRemoteProfileYAML_LaunchAccountForcesMerged is the live-UAT regression
// guard for the phase-126 remote-path bug (sb-8d5820ce).
//
// `--launch-account` is a CLI override written back onto
// resolvedProfile.Spec.Runtime.LaunchAccount. It therefore exists ONLY in the
// marshaled form — the operator's raw profile bytes say nothing about it. If the
// raw-bytes shortcut is taken, the create-handler Lambda re-compiles a profile with
// no launchAccount, the sandbox terragrunt template's
// `try(local.svc_config.locals.launch_account, "")` yields "", and the sandbox is
// silently built in the HOME account while every command reports success.
func TestSelectRemoteProfileYAML_LaunchAccountForcesMerged(t *testing.T) {
	// Raw bytes deliberately carry NO launchAccount — exactly the flag-supplied case.
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: gpu\nspec:\n  runtime:\n    substrate: ec2\n")

	resolved := &profile.SandboxProfile{}
	resolved.Spec.Runtime.LaunchAccount = "gpuman"

	// No extends, no ttl/idle overrides — the raw-bytes path would be taken but for
	// the launch account.
	out, err := selectRemoteProfileYAML(false /*extendsSet*/, resolved, raw, false, "", "")
	if err != nil {
		t.Fatalf("selectRemoteProfileYAML returned error: %v", err)
	}
	if !strings.Contains(out, "launchAccount: gpuman") {
		t.Fatalf("uploaded profile MUST carry launchAccount — without it the Lambda builds the "+
			"sandbox in the home account.\nGot:\n%s", out)
	}

	// And it must genuinely be the marshaled form, not the raw bytes.
	var round profile.SandboxProfile
	if err := yaml.Unmarshal([]byte(out), &round); err != nil {
		t.Fatalf("uploaded profile is not valid YAML: %v", err)
	}
	if round.Spec.Runtime.LaunchAccount != "gpuman" {
		t.Errorf("round-tripped launchAccount = %q, want %q", round.Spec.Runtime.LaunchAccount, "gpuman")
	}
}

// TestAssertLaunchAccountEmitted is the fail-closed guard for the structurally
// fail-open dormancy mechanism. A missing launch_account local in service.hcl is
// indistinguishable from "no cross-account launch requested" once terragrunt runs,
// and both mean "use home credentials" — so km must refuse before applying.
func TestAssertLaunchAccountEmitted(t *testing.T) {
	target := &LaunchTarget{LinkName: "gpuman", AccountID: "481723467561"}

	t.Run("nil target is always fine", func(t *testing.T) {
		if err := assertLaunchAccountEmitted("locals {\n}\n", nil); err != nil {
			t.Errorf("nil target must not error: %v", err)
		}
	})

	t.Run("emitted local passes", func(t *testing.T) {
		hcl := "locals {\n  launch_account       = \"gpuman\"\n}\n"
		if err := assertLaunchAccountEmitted(hcl, target); err != nil {
			t.Errorf("correctly emitted service.hcl must pass: %v", err)
		}
	})

	t.Run("alternate whitespace still passes", func(t *testing.T) {
		hcl := "locals {\n  launch_account = \"gpuman\"\n}\n"
		if err := assertLaunchAccountEmitted(hcl, target); err != nil {
			t.Errorf("whitespace variation must still pass: %v", err)
		}
	})

	t.Run("missing local FAILS CLOSED", func(t *testing.T) {
		// This is the exact sb-8d5820ce shape: a valid-looking service.hcl with no
		// launch_account at all.
		hcl := "locals {\n  substrate_module = \"ec2spot\"\n}\n"
		err := assertLaunchAccountEmitted(hcl, target)
		if err == nil {
			t.Fatal("a resolved launch account missing from service.hcl MUST abort — " +
				"continuing silently builds the sandbox in the home account")
		}
		for _, want := range []string{"gpuman", "481723467561", "HOME account"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error must mention %q so the operator understands the stakes, got:\n%s", want, err)
			}
		}
	})

	t.Run("wrong link name FAILS CLOSED", func(t *testing.T) {
		hcl := "locals {\n  launch_account       = \"someotherlink\"\n}\n"
		if err := assertLaunchAccountEmitted(hcl, target); err == nil {
			t.Error("a service.hcl naming a DIFFERENT link must abort")
		}
	})
}
