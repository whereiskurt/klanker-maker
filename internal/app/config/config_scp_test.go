// scp: block — merge-list registration. If "scp" is missing from the v2→v
// merge-list the block is silently dropped, and the next SCP apply would remove
// every exemption it lists from the live policy (project_config_key_merge_list).
package config_test

import (
	"strings"
	"testing"
)

func TestLoadSCP_MergeListGuard(t *testing.T) {
	cfg := loadFromYAML(t, `
domain: example.com
region: us-east-1
scp:
    trusted_roles:
        - arn:aws:iam::111122223333:role/ci-all
    trusted_iam_roles:
        - arn:aws:iam::111122223333:role/kmv-github-deploy
    trusted_ssm_roles:
        - arn:aws:iam::111122223333:role/ops-runbook
    extra_regions: [ca-central-1]
`)
	got := strings.Join([]string{
		strings.Join(cfg.SCP.TrustedRoles, ","),
		strings.Join(cfg.SCP.TrustedIAMRoles, ","),
		strings.Join(cfg.SCP.TrustedSSMRoles, ","),
		strings.Join(cfg.SCP.ExtraRegions, ","),
	}, "|")
	want := "arn:aws:iam::111122223333:role/ci-all|arn:aws:iam::111122223333:role/kmv-github-deploy|arn:aws:iam::111122223333:role/ops-runbook|ca-central-1"
	if got != want {
		t.Fatalf("scp block = %q, want %q (is \"scp\" in the merge-list?)", got, want)
	}
}

func TestLoadSCP_AbsentIsEmpty(t *testing.T) {
	cfg := loadFromYAML(t, `
domain: example.com
region: us-east-1
`)
	if len(cfg.SCP.TrustedRoles)+len(cfg.SCP.TrustedIAMRoles)+len(cfg.SCP.TrustedSSMRoles)+len(cfg.SCP.ExtraRegions) != 0 {
		t.Fatalf("absent scp: block should be empty, got %+v", cfg.SCP)
	}
}
