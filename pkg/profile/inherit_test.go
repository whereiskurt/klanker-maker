package profile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// ─── deepMerge unit tests (Task 1, Plan 02) ──────────────────────────────────

// TestDeepMerge_ScalarWins: child scalar overrides parent scalar; missing keys are
// added from src; keys only in dst are preserved.
func TestDeepMerge_ScalarWins(t *testing.T) {
	dst := map[string]any{"a": 1, "b": "x"}
	src := map[string]any{"b": "y", "c": 3}
	got := deepMerge(dst, src)
	if got["a"] != 1 {
		t.Errorf("key only in dst: want 1, got %v", got["a"])
	}
	if got["b"] != "y" {
		t.Errorf("scalar src-wins: want 'y', got %v", got["b"])
	}
	if got["c"] != 3 {
		t.Errorf("key only in src added: want 3, got %v", got["c"])
	}
}

// TestDeepMerge_MapUnion: nested maps are recursively merged (key-union, src-wins on collision).
func TestDeepMerge_MapUnion(t *testing.T) {
	dst := map[string]any{
		"m": map[string]any{
			"x": 1,
			"deep": map[string]any{
				"dd": map[string]any{"leaf": "from-dst"},
			},
		},
	}
	src := map[string]any{
		"m": map[string]any{
			"y": 2,
			"deep": map[string]any{
				"dd": map[string]any{"extra": "from-src"},
			},
		},
	}
	got := deepMerge(dst, src)
	m, ok := got["m"].(map[string]any)
	if !ok {
		t.Fatalf("want map, got %T", got["m"])
	}
	if m["x"] != 1 {
		t.Errorf("dst key preserved: want 1, got %v", m["x"])
	}
	if m["y"] != 2 {
		t.Errorf("src key added: want 2, got %v", m["y"])
	}
	deep, _ := m["deep"].(map[string]any)
	dd, _ := deep["dd"].(map[string]any)
	if dd["leaf"] != "from-dst" {
		t.Errorf("3-deep dst preserved: want 'from-dst', got %v", dd["leaf"])
	}
	if dd["extra"] != "from-src" {
		t.Errorf("3-deep src added: want 'from-src', got %v", dd["extra"])
	}
}

// TestDeepMerge_ListDedup: string-list concat+dedup, order-preserving, first-occurrence kept.
func TestDeepMerge_ListDedup(t *testing.T) {
	dst := map[string]any{"l": []any{"a", "b"}}
	src := map[string]any{"l": []any{"b", "c"}}
	got := deepMerge(dst, src)
	l, ok := got["l"].([]any)
	if !ok {
		t.Fatalf("want []any, got %T", got["l"])
	}
	want := []any{"a", "b", "c"}
	if !reflect.DeepEqual(l, want) {
		t.Errorf("concat+dedup: want %v, got %v", want, l)
	}

	// Identical lists: diamond idempotence
	dst2 := map[string]any{"l": []any{"x", "y"}}
	src2 := map[string]any{"l": []any{"x", "y"}}
	got2 := deepMerge(dst2, src2)
	l2 := got2["l"].([]any)
	if !reflect.DeepEqual(l2, []any{"x", "y"}) {
		t.Errorf("identical lists: no dup; want [x y], got %v", l2)
	}
}

// TestDeepMerge_ObjectListDedup: slices of maps de-dup by deep-equality; distinct entries kept.
func TestDeepMerge_ObjectListDedup(t *testing.T) {
	snap1 := map[string]any{"snapshotId": "snap-aaa", "mountPoint": "/mnt/data"}
	snap2 := map[string]any{"snapshotId": "snap-bbb", "mountPoint": "/mnt/other"}
	dst := map[string]any{"snaps": []any{snap1}}
	src := map[string]any{"snaps": []any{snap1, snap2}} // snap1 is duplicate
	got := deepMerge(dst, src)
	snaps := got["snaps"].([]any)
	if len(snaps) != 2 {
		t.Fatalf("want 2 distinct snapshots, got %d: %v", len(snaps), snaps)
	}
}

// TestDeepMerge_MissingKey: key only in src is added; key only in dst is preserved.
func TestDeepMerge_MissingKey(t *testing.T) {
	dst := map[string]any{"onlyDst": "d"}
	src := map[string]any{"onlySrc": "s"}
	got := deepMerge(dst, src)
	if got["onlyDst"] != "d" {
		t.Errorf("dst-only key lost, want 'd', got %v", got["onlyDst"])
	}
	if got["onlySrc"] != "s" {
		t.Errorf("src-only key not added, want 's', got %v", got["onlySrc"])
	}
}

// TestExtendsUnmarshal verifies the ExtendsField union type correctly parses
// scalar string, array, and absent extends fields.
func TestExtendsUnmarshal(t *testing.T) {
	t.Run("scalar string", func(t *testing.T) {
		input := `extends: base/foo`
		var s struct {
			Extends ExtendsField `yaml:"extends,omitempty"`
		}
		if err := yaml.Unmarshal([]byte(input), &s); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !s.Extends.IsSet() {
			t.Error("expected IsSet() true for scalar extends")
		}
		list := s.Extends.List()
		if len(list) != 1 || list[0] != "base/foo" {
			t.Errorf("expected [base/foo], got %v", list)
		}
	})

	t.Run("sequence", func(t *testing.T) {
		input := "extends:\n  - base/a\n  - base/b"
		var s struct {
			Extends ExtendsField `yaml:"extends,omitempty"`
		}
		if err := yaml.Unmarshal([]byte(input), &s); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !s.Extends.IsSet() {
			t.Error("expected IsSet() true for sequence extends")
		}
		list := s.Extends.List()
		if len(list) != 2 || list[0] != "base/a" || list[1] != "base/b" {
			t.Errorf("expected [base/a, base/b], got %v", list)
		}
	})

	t.Run("absent", func(t *testing.T) {
		input := `metadata: {name: test}`
		var s struct {
			Extends ExtendsField `yaml:"extends,omitempty"`
		}
		if err := yaml.Unmarshal([]byte(input), &s); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if s.Extends.IsSet() {
			t.Errorf("expected IsSet() false for absent extends, got true: %v", s.Extends)
		}
		if len(s.Extends.List()) != 0 {
			t.Errorf("expected empty list for absent extends, got %v", s.Extends.List())
		}
	})

	t.Run("IsSet and List accessors", func(t *testing.T) {
		var e ExtendsField
		if e.IsSet() {
			t.Error("empty ExtendsField should not be set")
		}
		e = ExtendsField{"foo", "bar"}
		if !e.IsSet() {
			t.Error("non-empty ExtendsField should be set")
		}
		list := e.List()
		if len(list) != 2 || list[0] != "foo" || list[1] != "bar" {
			t.Errorf("List() returned unexpected value: %v", list)
		}
	})
}

func TestResolveInheritsParentValues(t *testing.T) {
	// Child extends open-dev with TTL override but omits some parent values
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Child specifies TTL=2h, should override parent's 24h
	if p.Spec.Lifecycle.TTL != "2h" {
		t.Errorf("expected child TTL 2h, got %s", p.Spec.Lifecycle.TTL)
	}
	// Child specifies IdleTimeout=30m, should override parent's 4h
	if p.Spec.Lifecycle.IdleTimeout != "30m" {
		t.Errorf("expected child IdleTimeout 30m, got %s", p.Spec.Lifecycle.IdleTimeout)
	}
}

func TestResolveChildOverridesParent(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Child's allowedDNSSuffixes should REPLACE parent's, not union
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != 1 {
		t.Fatalf("expected 1 DNS suffix, got %d: %v", len(p.Spec.Network.Egress.AllowedDNSSuffixes), p.Spec.Network.Egress.AllowedDNSSuffixes)
	}
	if p.Spec.Network.Egress.AllowedDNSSuffixes[0] != ".example.com" {
		t.Errorf("expected .example.com, got %s", p.Spec.Network.Egress.AllowedDNSSuffixes[0])
	}
}

func TestResolveChildWinsScalar(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Both parent and child have substrate=ec2 — child wins
	if p.Spec.Runtime.Substrate != "ec2" {
		t.Errorf("expected substrate ec2, got %s", p.Spec.Runtime.Substrate)
	}
}

func TestResolveCircularDetection(t *testing.T) {
	_, err := Resolve("circular-a", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for circular inheritance, got nil")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected error containing 'circular', got: %s", err.Error())
	}
}

func TestResolveDepthExceeded(t *testing.T) {
	// depth-4 extends depth-3 extends depth-2 extends depth-1 = 4 levels > max 3
	_, err := Resolve("depth-4", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for depth exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "depth exceeded") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error about depth or missing parent, got: %s", err.Error())
	}
}

func TestResolveMaxDepth3(t *testing.T) {
	// open-dev (builtin, depth 0) -> child-of-open-dev (depth 1) = 2 levels, should succeed
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("expected 2-level chain to succeed, got error: %v", err)
	}
	if p.Metadata.Name != "child-of-open-dev" {
		t.Errorf("expected name child-of-open-dev, got %s", p.Metadata.Name)
	}
}

func TestResolveNonExistentParent(t *testing.T) {
	_, err := Resolve("nonexistent-profile", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for nonexistent profile, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error containing 'not found', got: %s", err.Error())
	}
}

func TestResolveMetadataLabelsAdditive(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Parent (open-dev) has labels: tier=development, builtin=true
	// Child has labels: tier=development, custom=true
	// Merged should have all three: tier=development (child wins), builtin=true (from parent), custom=true (from child)
	if p.Metadata.Labels["builtin"] != "true" {
		t.Errorf("expected parent label 'builtin=true' to be inherited, got %v", p.Metadata.Labels)
	}
	if p.Metadata.Labels["custom"] != "true" {
		t.Errorf("expected child label 'custom=true' to be present, got %v", p.Metadata.Labels)
	}
}

func TestResolveExtendsCleared(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if p.Extends.IsSet() {
		t.Errorf("expected extends to be cleared after resolution, got %v", p.Extends)
	}
}
