package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// .yml must work everywhere .yaml does. It did not: the CLI derived a leaf
// name with TrimSuffix(base, ".yaml"), so "spot.yml" stayed "spot.yml" and
// the resolver — which only ever tried name+".yaml" — looked for
// "spot.yml.yaml" and reported "profile not found". Both halves are fixed
// here and pinned together: LeafName strips either extension, and Resolve
// tries .yml when .yaml is absent.
func TestLeafName_StripsEitherExtension(t *testing.T) {
	for in, want := range map[string]string{
		"profiles/spot.yaml":    "spot",
		"profiles/spot.yml":     "spot",
		"/abs/dc34.ami.yaml":    "dc34.ami",
		"/abs/dc34.ami.yml":     "dc34.ami",
		"noext":                 "noext",
		"weird.yaml.bak":        "weird.yaml.bak", // only a trailing extension is stripped
		"base/network/safe.yml": "safe",
	} {
		if got := LeafName(in); got != want {
			t.Errorf("LeafName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolve_FindsYmlProfilesAndBases(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A .yml base, extended by a .yml leaf.
	write("base/frag.yml", `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: frag
  abstract: true
spec:
  network:
    egress:
      allowedDNSSuffixes: [".example.com"]
`)
	write("leaf.yml", `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: leaf
extends: base/frag
spec:
  runtime:
    substrate: ec2
`)
	p, err := Resolve("leaf", []string{dir})
	if err != nil {
		t.Fatalf("Resolve(.yml leaf extending .yml base): %v", err)
	}
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != 1 {
		t.Fatalf("base fragment did not merge: %+v", p.Spec.Network)
	}

	// .yaml still wins when both exist, so nothing that worked before changes.
	write("both.yaml", "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: from-yaml\nspec: {}\n")
	write("both.yml", "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: from-yml\nspec: {}\n")
	p, err = Resolve("both", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "from-yaml" {
		t.Errorf(".yaml must take precedence over .yml for the same name; got %q", p.Metadata.Name)
	}
}
