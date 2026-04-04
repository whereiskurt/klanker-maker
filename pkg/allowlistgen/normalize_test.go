package allowlistgen_test

import (
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
)

func TestNormalizeSuffixes(t *testing.T) {
	// githubusercontent.com is itself a public suffix (in the PSL), so
	// raw.githubusercontent.com collapses to .raw.githubusercontent.com.
	// api.github.com and github.com both collapse to .github.com.
	input := []string{"api.github.com", "github.com", "raw.githubusercontent.com"}
	got := allowlistgen.CollapseToDNSSuffixes(input)
	want := []string{".github.com", ".raw.githubusercontent.com"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: expected %q, got %q", i, w, got[i])
		}
	}
}

func TestNormalizeSuffixes_NoOverPermissive(t *testing.T) {
	input := []string{"foo.com", "bar.com"}
	got := allowlistgen.CollapseToDNSSuffixes(input)
	want := []string{".bar.com", ".foo.com"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: expected %q, got %q", i, w, got[i])
		}
	}
}

func TestNormalizeSuffixes_CoUK(t *testing.T) {
	input := []string{"app.example.co.uk"}
	got := allowlistgen.CollapseToDNSSuffixes(input)
	want := []string{".example.co.uk"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if got[0] != want[0] {
		t.Errorf("expected %q, got %q", want[0], got[0])
	}
}

func TestNormalizeSuffixes_SingleDomain(t *testing.T) {
	input := []string{"example.com"}
	got := allowlistgen.CollapseToDNSSuffixes(input)
	want := []string{".example.com"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if got[0] != want[0] {
		t.Errorf("expected %q, got %q", want[0], got[0])
	}
}

func TestNormalizeSuffixes_Sorted(t *testing.T) {
	input := []string{"z.example.com", "a.other.com", "m.foo.com"}
	got := allowlistgen.CollapseToDNSSuffixes(input)
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("output not sorted: %v", got)
			break
		}
	}
}

func TestNormalizeSuffixes_Empty(t *testing.T) {
	got := allowlistgen.CollapseToDNSSuffixes([]string{})
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}
