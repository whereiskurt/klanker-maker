package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readEC2SpotMainTF(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)), "main.tf"))
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
	allowStar := regexp.MustCompile(`Action\s*=\s*(\[\s*)?"\*"`)
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
	want := []string{"iam:*", "sts:*", "organizations:*", "support:*", "health:*", "trustedadvisor:*",
		"cloudfront:*", "waf:*", "shield:*", "route53:*", "route53domains:*", "budgets:*",
		"ce:*", "cur:*", "globalaccelerator:*", "networkmanager:*", "pricing:*",
		"bedrock:*", "bedrock-mantle:*", "aws-marketplace:*",
		"s3:GetAccountPublicAccessBlock", "s3:ListAllMyBuckets", "s3:PutAccountPublicAccessBlock"}
	i := strings.Index(deny, "NotAction")
	j := strings.Index(deny[i:], "]")
	if j < 0 {
		t.Fatalf("cannot parse NotAction list: %s", deny)
	}
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(deny[i:i+j], -1) {
		got[m[1]] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
		if !got[w] {
			t.Errorf("region deny NotAction is missing %s", w)
		}
	}
	for g := range got {
		if !wantSet[g] {
			t.Errorf("region deny NotAction has unexpected extra entry %s", g)
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
	if i := strings.Index(s, "ssm_param_arns = concat("); i < 0 {
		t.Error("locals.ssm_param_arns not found")
	} else {
		k, depth := strings.Index(s[i:], "(")+i, 0
		end := len(s)
		for n := k; n < len(s); n++ {
			if s[n] == '(' {
				depth++
			} else if s[n] == ')' {
				depth--
				if depth == 0 {
					end = n
					break
				}
			}
		}
		for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(s[k:end], -1) {
			if !strings.Contains(m[1], ":parameter") {
				t.Errorf("ssm_param_arns literal %q does not name a parameter ARN", m[1])
			}
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
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)), "variables.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{`variable "email_domain"`, `variable "otp_secret_paths"`} {
		if !strings.Contains(string(raw), v) {
			t.Errorf("variables.tf missing %s", v)
		}
	}
}
