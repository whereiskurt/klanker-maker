package planreport

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to load fixture %s: %v", name, err)
	}
	return b
}

func TestParse_Clean(t *testing.T) {
	b := loadFixture(t, "ses-clean.json")
	r, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if r.ParseFailed {
		t.Errorf("ParseFailed should be false for valid JSON")
	}
	if len(r.Adds) != 0 || len(r.Changes) != 0 || len(r.Destroys) != 0 || len(r.Replaces) != 0 {
		t.Errorf("expected all slices empty, got adds=%d changes=%d destroys=%d replaces=%d",
			len(r.Adds), len(r.Changes), len(r.Destroys), len(r.Replaces))
	}
}

func TestParse_AddOnly(t *testing.T) {
	b := loadFixture(t, "ses-rule-add.json")
	r, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if r.ParseFailed {
		t.Errorf("ParseFailed should be false")
	}
	if len(r.Adds) != 2 {
		t.Errorf("expected 2 adds, got %d", len(r.Adds))
	}
	if len(r.Destroys) != 0 {
		t.Errorf("expected 0 destroys, got %d", len(r.Destroys))
	}
	if len(r.Changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(r.Changes))
	}
	if len(r.Replaces) != 0 {
		t.Errorf("expected 0 replaces, got %d", len(r.Replaces))
	}
}

func TestParse_Phase84Destroy(t *testing.T) {
	b := loadFixture(t, "ses-82to84-destroy.json")
	r, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if r.ParseFailed {
		t.Errorf("ParseFailed should be false")
	}
	if len(r.Destroys) != 3 {
		t.Fatalf("expected 3 destroys, got %d", len(r.Destroys))
	}
	if len(r.Adds) != 0 {
		t.Errorf("expected 0 adds, got %d", len(r.Adds))
	}

	wantAddresses := map[string]bool{
		"aws_ses_domain_identity.sandboxes": false,
		"aws_ses_domain_dkim.sandboxes":     false,
		"aws_route53_record.dkim[0]":        false,
	}
	wantTypes := map[string]bool{
		"aws_ses_domain_identity": false,
		"aws_ses_domain_dkim":     false,
		"aws_route53_record":      false,
	}
	for _, d := range r.Destroys {
		if _, ok := wantAddresses[d.Address]; ok {
			wantAddresses[d.Address] = true
		} else {
			t.Errorf("unexpected destroy address %q", d.Address)
		}
		if _, ok := wantTypes[d.Type]; ok {
			wantTypes[d.Type] = true
		} else {
			t.Errorf("unexpected destroy type %q", d.Type)
		}
	}
	for addr, found := range wantAddresses {
		if !found {
			t.Errorf("expected destroy address %q not found", addr)
		}
	}
}

func TestParse_LambdaReplace(t *testing.T) {
	b := loadFixture(t, "lambda-replace.json")
	r, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if r.ParseFailed {
		t.Errorf("ParseFailed should be false")
	}
	if len(r.Replaces) != 1 {
		t.Fatalf("expected 1 replace, got %d", len(r.Replaces))
	}
	if r.Replaces[0].Type != "aws_lambda_function" {
		t.Errorf("expected aws_lambda_function replace, got %q", r.Replaces[0].Type)
	}
	if len(r.Destroys) != 0 {
		t.Errorf("expected 0 destroys, got %d", len(r.Destroys))
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	b := loadFixture(t, "malformed.json")
	_, err := Parse(b)
	if err == nil {
		t.Fatal("Parse should return error for malformed JSON")
	}
}

func TestParse_FormatVersionCompat(t *testing.T) {
	cases := []struct {
		version string
		wantErr bool
	}{
		{"1.0", false},
		{"1.1", false},
		{"1.99", false},
		{"2.0", true},
		{"", true},
		{"0.1", true},
		{"3.0", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("version_"+tc.version, func(t *testing.T) {
			jsonStr := `{"format_version":"` + tc.version + `","terraform_version":"1.8.2","resource_changes":[]}`
			if tc.version == "" {
				jsonStr = `{"terraform_version":"1.8.2","resource_changes":[]}`
			}
			_, err := Parse([]byte(jsonStr))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for version %q, got nil", tc.version)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for version %q, got: %v", tc.version, err)
			}
		})
	}
}

func TestClassifyAction_Permutations(t *testing.T) {
	cases := []struct {
		actions []string
		want    string
	}{
		{[]string{"no-op"}, "no-op"},
		{[]string{"create"}, "create"},
		{[]string{"update"}, "update"},
		{[]string{"delete"}, "delete"},
		{[]string{"read"}, "read"},
		{[]string{"forget"}, "forget"},
		{[]string{"delete", "create"}, "replace"},
		{[]string{"create", "delete"}, "replace"},
		{[]string{}, "no-op"},
	}
	for _, tc := range cases {
		tc := tc
		got := classifyAction(tc.actions)
		if got != tc.want {
			t.Errorf("classifyAction(%v) = %q, want %q", tc.actions, got, tc.want)
		}
	}
}
