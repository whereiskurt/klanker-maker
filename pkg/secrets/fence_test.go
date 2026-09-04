package secrets_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

type policyDoc struct {
	Version   string `json:"Version"`
	Statement []struct {
		Sid       string                       `json:"Sid"`
		Effect    string                       `json:"Effect"`
		Action    json.RawMessage              `json:"Action"`
		Resource  json.RawMessage              `json:"Resource"`
		Condition map[string]map[string]string `json:"Condition"`
	} `json:"Statement"`
}

func parsePolicy(t *testing.T, s string) policyDoc {
	t.Helper()
	var d policyDoc
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("session policy is not valid JSON: %v\n%s", err, s)
	}
	return d
}

// A session policy with no Allow grants nothing at all: session policies
// INTERSECT with the role's identity policies, so the Allow must be there or the
// narrowed credentials can do nothing and every helper breaks instantly.
func TestSessionPolicy_HasAnAllowOrItGrantsNothing(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "km-artifacts-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	var allows int
	for _, s := range parsePolicy(t, got).Statement {
		if s.Effect == "Allow" {
			allows++
		}
	}
	if allows == 0 {
		t.Fatal("no Allow statement: narrowed credentials would grant nothing and " +
			"every km helper would break")
	}
}

func TestSessionPolicy_DeniesTheSecretsKMSAlias(t *testing.T) {
	got, err := secrets.SessionPolicy("km2", "b", "s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range parsePolicy(t, got).Statement {
		if s.Effect != "Deny" || !strings.Contains(string(s.Action), "kms:Decrypt") {
			continue
		}
		if got := s.Condition["StringEquals"]["kms:ResourceAliases"]; got != "alias/km2-sandbox-secrets" {
			t.Fatalf("kms Deny condition = %q, want alias/km2-sandbox-secrets", got)
		}
		return
	}
	t.Fatal("no Deny on kms:Decrypt")
}

// The Deny must be CONDITIONED, never blanket. An unconditional kms:Decrypt Deny
// would also kill the SSM SecureString reads km-github, km-slack and km-h1 depend
// on — a different key, conditioned on kms:ViaService=ssm — which is precisely
// the breakage the fence exists to avoid.
func TestSessionPolicy_KMSDenyIsNotBlanket(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "b", "s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range parsePolicy(t, got).Statement {
		if s.Effect == "Deny" && strings.Contains(string(s.Action), "kms:Decrypt") && len(s.Condition) == 0 {
			t.Fatal("unconditional kms:Decrypt Deny would break every helper's SSM " +
				"SecureString read")
		}
	}
}

func TestSessionPolicy_DeniesTheBundleObjectExactly(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "km-artifacts-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:s3:::km-artifacts-1/sandboxes/abc123/secrets.enc.yaml"
	if !strings.Contains(got, want) {
		t.Fatalf("session policy does not deny %s\n%s", want, got)
	}
	if strings.Contains(got, "sandboxes/*") {
		t.Fatal("the bundle Deny is wildcarded; it must name this sandbox's object only")
	}
}

// An empty component would interpolate into a Deny that matches nothing — a
// fence that reports success and does not fence.
func TestSessionPolicy_RejectsEmptyInputs(t *testing.T) {
	for _, c := range []struct{ prefix, bucket, id string }{
		{"", "b", "s"}, {"km", "", "s"}, {"km", "b", ""},
	} {
		if _, err := secrets.SessionPolicy(c.prefix, c.bucket, c.id); err == nil {
			t.Errorf("SessionPolicy(%q,%q,%q) returned no error", c.prefix, c.bucket, c.id)
		}
	}
}
