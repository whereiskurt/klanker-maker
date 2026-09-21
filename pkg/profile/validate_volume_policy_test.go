package profile_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// volumePolicyProfile renders a minimal valid profile with the given runtime
// extras. Tested through YAML → schema, never by setting the struct: a field
// absent from the JSON schema is dead under additionalProperties:false even
// when its struct tests are green (the spec.otp lesson).
func volumePolicyProfile(runtimeExtra string) string {
	return fmt.Sprintf(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: volpolicy-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
%s
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: none
  network:
    egress:
      allowedDNSSuffixes:
        - "*"
      allowedHosts:
        - "*"
  iam:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest
    tracing:
      enabled: true
      image: km-tracing:latest
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /klanker-maker/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klanker-maker/network
`, runtimeExtra)
}

func TestOnVolumeMismatch_SchemaAcceptsEnumRejectsOther(t *testing.T) {
	withVolume := `    hibernation: true
    additionalVolume:
      size: 10
      mountPoint: /data
    onVolumeMismatch: %s`
	for _, v := range []string{"refuse", "reboot"} {
		y := volumePolicyProfile(fmt.Sprintf(withVolume, v))
		if errs := profile.ValidateSchema([]byte(y)); len(errs) != 0 {
			t.Errorf("%s: schema rejected a valid value: %v", v, errs)
		}
		p, err := profile.Parse([]byte(y))
		if err != nil {
			t.Fatalf("%s: parse: %v", v, err)
		}
		if p.Spec.Runtime.OnVolumeMismatch != v {
			t.Errorf("parsed OnVolumeMismatch = %q, want %q", p.Spec.Runtime.OnVolumeMismatch, v)
		}
	}
	errs := profile.ValidateSchema([]byte(volumePolicyProfile(fmt.Sprintf(withVolume, "panic"))))
	found := false
	for _, e := range errs {
		if strings.Contains(e.Path, "onVolumeMismatch") {
			found = true
		}
	}
	if !found {
		t.Errorf("a bad enum must be rejected naming the field; got %v", errs)
	}
}

func TestOnVolumeMismatch_WarnsWithoutAdditionalVolumes(t *testing.T) {
	y := volumePolicyProfile("    onVolumeMismatch: reboot")
	errs := profile.Validate([]byte(y))
	var warned bool
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("onVolumeMismatch without volumes must warn, not fail: %v", e)
		}
		if e.IsWarning && strings.Contains(e.Path, "onVolumeMismatch") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a dead-field warning for onVolumeMismatch, got %v", errs)
	}
}

func TestOnVolumeMismatch_AbsentIsEmptyAndSilent(t *testing.T) {
	y := volumePolicyProfile(`    additionalVolume:
      size: 10
      mountPoint: /data`)
	for _, e := range profile.Validate([]byte(y)) {
		if strings.Contains(e.Path, "onVolumeMismatch") {
			t.Errorf("absent field must produce nothing: %v", e)
		}
	}
}
