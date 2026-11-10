package wizoutpost

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func valid() Settings {
	return Settings{TrustRoleARN: "arn:aws:iam::111122223333:role/wiz-dc", OrchestratorAccountID: "444455556666",
		ExternalID: "abcdef12-3456-7890-abcd-ef1234567890", RoleSuffix: "km"}
}

func TestParameters_DefaultsMirrorTemplate(t *testing.T) {
	want := map[string]string{
		"ExternalId": "abcdef12-3456-7890-abcd-ef1234567890", "RoleARN": "arn:aws:iam::111122223333:role/wiz-dc",
		"OrchestratorAccountID": "444455556666", "RoleSuffix": "km", "WizRoleName": "WizAccess-Role",
		"isOrg": "Disabled", "CloudCostScanning": "Enabled", "DataScanning": "Disabled", "EKSScanning": "Disabled",
		"LightsailScanning": "Disabled", "RDSLogsScanning": "Enabled", "S3AWSLogsScanning": "Enabled",
		"S3KMSDecrypt": "Enabled", "ServerlessScanning": "Enabled", "TerraformBucketScanning": "Enabled",
	}
	if got := valid().Parameters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestParameters_ToggleOverrides(t *testing.T) {
	tr, fa := true, false
	s := valid()
	s.Scanning.EKS = &tr
	s.Scanning.CloudCost = &fa
	s.AccessRoleName = "Custom"
	p := s.Parameters()
	if p["EKSScanning"] != "Enabled" || p["CloudCostScanning"] != "Disabled" || p["WizRoleName"] != "Custom" {
		t.Fatalf("overrides not applied: %v", p)
	}
}

func TestValidate_RejectsEachMissingOrMalformedField(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Settings)
		key  string
	}{
		{"empty arn", func(s *Settings) { s.TrustRoleARN = "" }, "wiz.outpost.trust_role_arn"},
		{"bad arn", func(s *Settings) { s.TrustRoleARN = "not-an-arn" }, "wiz.outpost.trust_role_arn"},
		{"short acct", func(s *Settings) { s.OrchestratorAccountID = "1234" }, "wiz.outpost.orchestrator_account_id"},
		{"alpha acct", func(s *Settings) { s.OrchestratorAccountID = "11112222333a" }, "wiz.outpost.orchestrator_account_id"},
		{"empty acct", func(s *Settings) { s.OrchestratorAccountID = "" }, "wiz.outpost.orchestrator_account_id"},
		{"empty ext", func(s *Settings) { s.ExternalID = "" }, "wiz.outpost.external_id"},
		{"bad ext", func(s *Settings) { s.ExternalID = "not-a-uuid" }, "wiz.outpost.external_id"},
		{"empty suffix", func(s *Settings) { s.RoleSuffix = "" }, "wiz.outpost.role_suffix"},
	}
	for _, c := range cases {
		s := valid()
		c.mut(&s)
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s: err=%v, want mention of %s", c.name, err, c.key)
		}
	}
}

type tmpl struct {
	Parameters map[string]struct {
		AllowedValues  []string
		AllowedPattern string
	}
	Outputs map[string]json.RawMessage
}

func loadTemplate(t *testing.T) ([]byte, tmpl) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", TemplateRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var tp tmpl
	if err := json.Unmarshal(raw, &tp); err != nil {
		t.Fatal(err)
	}
	return raw, tp
}

func TestTemplateContract_ParametersAndOutputsExist(t *testing.T) {
	_, tp := loadTemplate(t)
	for _, k := range RequiredParameters {
		if _, ok := tp.Parameters[k]; !ok {
			t.Errorf("template lacks parameter %s", k)
		}
	}
	for _, k := range RequiredOutputs {
		if _, ok := tp.Outputs[k]; !ok {
			t.Errorf("template lacks output %s", k)
		}
	}
	for k, v := range valid().Parameters() {
		p := tp.Parameters[k]
		if len(p.AllowedValues) > 0 {
			found := false
			for _, a := range p.AllowedValues {
				found = found || a == v
			}
			if !found {
				t.Errorf("%s=%q not in AllowedValues %v", k, v, p.AllowedValues)
			}
		}
		if p.AllowedPattern != "" && !regexp.MustCompile("^(?:"+p.AllowedPattern+")$").MatchString(v) {
			t.Errorf("%s=%q does not match AllowedPattern %s", k, v, p.AllowedPattern)
		}
	}
	// Our own validators must be no looser than the template's patterns for valid input.
	if p := tp.Parameters["ExternalId"].AllowedPattern; p == "" {
		t.Error("ExternalId has no AllowedPattern in template")
	}
}

func TestTemplateContract_MinifiedUnderInlineLimit(t *testing.T) {
	raw, _ := loadTemplate(t)
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	if buf.Len() >= MaxInlineTemplateBytes {
		t.Fatalf("minified template %d >= %d", buf.Len(), MaxInlineTemplateBytes)
	}
}

func TestExportJSON_Stable(t *testing.T) {
	a, err := ExportJSON(valid())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ExportJSON(valid())
	if a != b || !strings.HasPrefix(a, `{"parameters":{`) {
		t.Fatalf("unstable or malformed: %s", a)
	}
}

func TestStackAndRoleNames(t *testing.T) {
	if StackName("km") != "km-wiz-outpost-connector" {
		t.Fatal("stack name")
	}
	if valid().ScannerRoleName() != "WizScannerRole-km" {
		t.Fatal("scanner role name")
	}
	if valid().AccessRoleNameOrDefault() != "WizAccess-Role" {
		t.Fatal("access default")
	}
}

func TestTemplateVersion(t *testing.T) {
	v, err := TemplateVersion(filepath.Join("..", ".."))
	if err != nil || v != "2025-10-07" {
		t.Fatalf("got %q, %v", v, err)
	}
	if _, err := TemplateVersion(t.TempDir()); err == nil {
		t.Fatal("expected error for missing template")
	}
}
