package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kms:ResourceAliases is a MULTIVALUED condition key: it compares the SET of
// aliases on a KMS key, and AWS requires a ForAnyValue/ForAllValues set operator
// for it. A bare StringEquals never matches. ec2spot v1.8.0 and v1.9.0 wrote
// exactly that, so the sandbox role's kms:Decrypt grant on the SOPS bundle key
// authorized nothing and every sandbox on those roles booted without secrets
// (2026-10-22). The policy simulator reports an unsatisfiable condition
// identically to a missing statement, so only a live decrypt or this guard can
// catch the shape. The guard is name-agnostic: every .tf under the PINNED ec2spot
// module and every non-test Go file under pkg/ and cmd/ is scanned, so a new
// statement or a new session policy is covered without an edit.
func TestKMSResourceAliasesAlwaysUnderASetOperator(t *testing.T) {
	root := "../.."
	tmpl, err := os.ReadFile(filepath.Join(root, "infra/templates/sandbox/terragrunt.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`ec2spot\s*=\s*"(v[0-9.]+)"`).FindStringSubmatch(string(tmpl))
	if m == nil {
		t.Fatal("cannot find the ec2spot pin in the sandbox template")
	}
	var files []string
	tf, _ := filepath.Glob(filepath.Join(root, "infra/modules/ec2spot", m[1], "*.tf"))
	files = append(files, tf...)
	for _, dir := range []string{"pkg", "cmd"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(tf) == 0 {
		t.Fatalf("no .tf files under the pinned module %s", m[1])
	}
	keyUse := regexp.MustCompile(`"kms:ResourceAliases"\s*[=:]`) // a policy key, not a prose mention
	setOp := regexp.MustCompile(`"?(ForAnyValue|ForAllValues):\w+"?\s*[=:]`)
	bareOp := regexp.MustCompile(`"?(StringEquals|StringLike|StringEqualsIgnoreCase|StringNotEquals)"?\s*[=:]`)
	seen := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if !keyUse.MatchString(l) || strings.HasPrefix(strings.TrimSpace(l), "//") || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			seen++
			// The operator is the nearest preceding condition-operator line.
			ok := false
			for j := i - 1; j >= 0 && j >= i-4; j-- {
				if setOp.MatchString(lines[j]) {
					ok = true
					break
				}
				if bareOp.MatchString(lines[j]) {
					break
				}
			}
			if !ok {
				t.Errorf("%s:%d: kms:ResourceAliases without a ForAnyValue/ForAllValues set operator (a bare StringEquals never matches a multivalued key)", f, i+1)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("expected to find kms:ResourceAliases in the pinned ec2spot module and the fence session policy; found %d", seen)
	}
}
