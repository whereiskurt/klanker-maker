package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInfoShowsNewAccountFields verifies that km info prints "Organization:" and
// "DNS parent:" lines, and does NOT print "Management:" label.
func TestInfoShowsNewAccountFields(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	cfgContent := `domain: test.example.com
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  terraform: "333333333333"
  application: "444444444444"
sso:
  start_url: https://sso.example.com/start
  region: us-east-1
region: us-east-1
`
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}

	out, err := runKMArgsInDir(km, dir, "", "info")
	if err != nil {
		t.Fatalf("km info: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Organization:") {
		t.Errorf("km info output missing 'Organization:' line; output:\n%s", out)
	}
	if !strings.Contains(out, "111111111111") {
		t.Errorf("km info output missing organization account ID '111111111111'; output:\n%s", out)
	}
	if !strings.Contains(out, "DNS parent:") {
		t.Errorf("km info output missing 'DNS parent:' line; output:\n%s", out)
	}
	if !strings.Contains(out, "222222222222") {
		t.Errorf("km info output missing DNS parent account ID '222222222222'; output:\n%s", out)
	}
	if strings.Contains(out, "Management:") {
		t.Errorf("km info output must NOT contain 'Management:' label (renamed to Organization/DNS parent); output:\n%s", out)
	}
}
