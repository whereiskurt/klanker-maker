package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ec2spotGrantsModuleDir is switched to the live pin in Task 2 of the
// least-privilege plan; until then it names the module under construction.
const ec2spotGrantsModuleDir = "infra/modules/ec2spot/v1.8.0"

func readEC2SpotMainTF(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(ec2spotGrantsModuleDir), "main.tf"))
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	return tfCommentRe.ReplaceAllString(string(raw), "")
}

// Every IAM statement block in the file: the innermost {...} around each Action/NotAction key.
func iamStatements(t *testing.T, s string) []string {
	t.Helper()
	seen := map[int]bool{}
	var out []string
	for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
		a, b := enclosingBlock(s, m[0])
		if a == -1 || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, s[a:b])
	}
	if len(out) == 0 {
		t.Fatal("no IAM statements found; the parser has drifted from the module")
	}
	return out
}

func TestEC2SpotRole_NoAllowStar(t *testing.T) {
	s := readEC2SpotMainTF(t)
	allowStar := regexp.MustCompile(`Action\s*=\s*"\*"`)
	for _, stmt := range iamStatements(t, s) {
		if strings.Contains(stmt, `Effect = "Deny"`) || strings.Contains(stmt, `Effect   = "Deny"`) {
			continue
		}
		if allowStar.MatchString(stmt) {
			t.Errorf("an Allow statement grants Action \"*\": %s", stmt)
		}
	}
	if strings.Contains(s, `"ec2spot_region_lock"`) {
		t.Error("ec2spot_region_lock (the Allow-star) must not exist in v1.8.0")
	}
	if strings.Contains(s, "AmazonSSMManagedInstanceCore") {
		t.Error("the managed AmazonSSMManagedInstanceCore attachment grants ssm:GetParameter* on *; v1.8.0 carries an inline copy without it")
	}
}

func TestEC2SpotRole_RegionDenyShape(t *testing.T) {
	s := readEC2SpotMainTF(t)
	var deny string
	for _, stmt := range iamStatements(t, s) {
		if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) && strings.Contains(stmt, "aws:RequestedRegion") {
			deny = stmt
		}
	}
	if deny == "" {
		t.Fatal("no Deny statement conditioned on aws:RequestedRegion")
	}
	if !strings.Contains(deny, "NotAction") || !strings.Contains(deny, "StringNotEquals") {
		t.Errorf("region deny must be NotAction + StringNotEquals: %s", deny)
	}
	for _, exempt := range []string{`"sts:*"`, `"iam:*"`, `"bedrock:*"`, `"bedrock-mantle:*"`, `"aws-marketplace:*"`} {
		if !strings.Contains(deny, exempt) {
			t.Errorf("region deny NotAction is missing %s (cross-region Bedrock inference and the IMDS-fence self-assume both need it)", exempt)
		}
	}
}

func TestEC2SpotRole_SSMParameterReadsAreScoped(t *testing.T) {
	s := readEC2SpotMainTF(t)
	getParam := regexp.MustCompile(`"ssm:GetParameters?"`)
	for _, stmt := range iamStatements(t, s) {
		if !getParam.MatchString(stmtField(stmt, "Action")) {
			continue
		}
		res := stmtField(stmt, "Resource")
		if strings.Contains(res, `"*"`) || (!strings.Contains(res, "parameter") && !strings.Contains(res, "local.ssm_param_arns")) {
			t.Errorf("ssm:GetParameter must be granted on parameter ARNs only, never *: %s", stmt)
		}
	}
	kmsVia := false
	for _, stmt := range iamStatements(t, s) {
		if strings.Contains(stmt, `"kms:Decrypt"`) && strings.Contains(stmt, "kms:ViaService") {
			kmsVia = true
			if !strings.Contains(stmt, "kms:EncryptionContext:PARAMETER_ARN") {
				t.Errorf("the SSM-path kms:Decrypt must be pinned to kms:EncryptionContext:PARAMETER_ARN: %s", stmt)
			}
		}
	}
	if !kmsVia {
		t.Error("no kms:Decrypt statement conditioned on kms:ViaService = ssm (SecureString reads would fail)")
	}
}

func TestEC2SpotRole_NewInputsDeclared(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(ec2spotGrantsModuleDir), "variables.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{`variable "email_domain"`, `variable "otp_secret_paths"`} {
		if !strings.Contains(string(raw), v) {
			t.Errorf("variables.tf missing %s", v)
		}
	}
}
