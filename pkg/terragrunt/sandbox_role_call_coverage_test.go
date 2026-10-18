package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// cliActionMap maps an `aws <svc> <op>` CLI call found in the rendered userdata
// to the IAM action(s) it needs. An op absent from the map fails the test, so a
// new CLI call in userdata.go forces someone to decide its grant.
var cliActionMap = map[string][]string{
	"s3 cp":                         {"s3:GetObject", "s3:PutObject"}, // direction is not knowable from text; both must be granted somewhere
	"s3 ls":                         {"s3:ListBucket"},
	"s3 rm":                         {"s3:DeleteObject"},
	"s3 sync":                       {"s3:GetObject", "s3:ListBucket"},
	"ssm get-parameter":             {"ssm:GetParameter"},
	"ssm delete-parameter":          {"ssm:DeleteParameter"},
	"sqs receive-message":           {"sqs:ReceiveMessage"},
	"sqs delete-message":            {"sqs:DeleteMessage"},
	"sqs change-message-visibility": {"sqs:ChangeMessageVisibility"},
	"dynamodb get-item":             {"dynamodb:GetItem"},
	"dynamodb update-item":          {"dynamodb:UpdateItem"},
	"dynamodb put-item":             {"dynamodb:PutItem"},
	"sesv2 send-email":              {"ses:SendEmail"},
	"sts get-caller-identity":       {}, // needs no permission
}

// sdkActions are the calls the Go sidecars and helpers make through the SDK,
// with the source that makes them. Maintained by hand because the SDK call
// sites are spread over several binaries; the census file names each.
var sdkActions = map[string]string{
	"logs:CreateLogGroup":                   "pkg/aws/cloudwatch.go EnsureLogGroup (km-audit-log)",
	"logs:PutRetentionPolicy":               "pkg/aws/cloudwatch.go EnsureLogGroup",
	"logs:CreateLogStream":                  "pkg/aws/cloudwatch.go EnsureLogGroup",
	"logs:PutLogEvents":                     "pkg/aws/cloudwatch.go PutLogEvents",
	"logs:GetLogEvents":                     "pkg/lifecycle/idle.go via cloudwatch.go",
	"events:PutEvents":                      "sidecars/audit-log/cmd/main.go",
	"dynamodb:Query":                        "pkg/aws/budget.go (http-proxy metering)",
	"dynamodb:PutItem":                      "cmd/km-slack record-mapping (stream-messages table)",
	"kms:Decrypt":                           "SSM SecureString reads; cmd/km-secretsd/bundle.go (sops)",
	"kms:DescribeKey":                       "cmd/km-secretsd/bundle.go",
	"sts:AssumeRole":                        "cmd/km-secretsd/credentials.go (IMDS fence)",
	"bedrock:InvokeModel":                   "the agent on Bedrock",
	"bedrock:InvokeModelWithResponseStream": "the agent on Bedrock",
	"bedrock-mantle:CreateInference":        "Bifrost gpt-oss routes",
}

var awsCLIRe = regexp.MustCompile(`\baws\s+(s3|s3api|ssm|sqs|dynamodb|sesv2|ses|logs|sts|kms|ec2)\s+([a-z-]+)`)

func allowedActions(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	actRe := regexp.MustCompile(`"([a-z0-9-]+:[A-Za-z*]+)"`)
	out := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		seen := map[int]bool{}
		for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
			a, b := enclosingBlock(s, m[0])
			if a == -1 || seen[a] {
				continue
			}
			seen[a] = true
			stmt := s[a:b]
			if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) {
				continue
			}
			for _, am := range actRe.FindAllStringSubmatch(stmtField(stmt, "Action"), -1) {
				out[am[1]] = true
			}
		}
	}
	return out
}

// onBoxCLISources returns the Go files whose string literals carry shell that
// runs ON the sandbox as the instance role: the userdata template, and the SSM
// RunCommand payloads the operator verbs (km agent run, km rsync save, …) and
// the ttl-handler send. Test files are excluded.
func onBoxCLISources(t *testing.T) []string {
	t.Helper()
	root := findRepoRoot(t)
	files := []string{filepath.Join(root, "pkg", "compiler", "userdata.go")}
	for _, dir := range []string{filepath.Join(root, "internal", "app", "cmd"), filepath.Join(root, "cmd", "ttl-handler")} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// operatorHintCalls are `aws` commands that appear in operator-side files as
// PRINTED text (km doctor remediation, a monitor hint): the operator runs them
// on their own workstation with their own credentials, so they need no grant on
// the sandbox role. Keyed by "<file> <svc> <op>", so the same call inside an SSM
// payload in any other file is still checked. A stale entry fails the test.
var operatorHintCalls = map[string]string{
	"remote_publisher.go logs tail":                 "printed Monitor: hint",
	"doctor_inbound_dlq.go sqs purge-queue":         "doctor remediation",
	"doctor_inbound_dlq.go sqs receive-message":     "doctor remediation",
	"doctor_webhooks.go sqs purge-queue":            "doctor remediation",
	"doctor_webhooks.go sqs receive-message":        "doctor remediation",
	"doctor_webhooks.go ssm put-parameter":          "doctor remediation",
	"doctor_ebs.go ec2 delete-volume":               "doctor remediation",
	"doctor_ebs.go ec2 delete-snapshot":             "doctor remediation",
	"doctor_access_params.go ssm delete-parameters": "doctor remediation",
	"doctor.go ses set-active-receipt-rule-set":     "doctor remediation",
	"doctor.go ses describe-receipt-rule-set":       "doctor remediation",
	"doctor.go ses delete-receipt-rule":             "doctor remediation",
	"doctor.go ec2 delete-volume":                   "doctor remediation (target-account creds)",
}

func TestSandboxRole_EveryUserdataCLICallHasAGrant(t *testing.T) {
	allowed := allowedActions(t)
	calls := map[string]map[string]bool{} // call -> files it appears in (hints excluded)
	userdataCalls := map[string]bool{}
	hintSeen := map[string]bool{}
	for _, f := range onBoxCLISources(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(f)
		for _, line := range strings.Split(string(raw), "\n") {
			if tl := strings.TrimSpace(line); strings.HasPrefix(tl, "#") || strings.HasPrefix(tl, "//") {
				continue // a comment is not a call
			}
			for _, m := range awsCLIRe.FindAllStringSubmatch(line, -1) {
				call := m[1] + " " + m[2]
				if base == "userdata.go" {
					userdataCalls[call] = true
				}
				if _, hint := operatorHintCalls[base+" "+call]; hint {
					hintSeen[base+" "+call] = true
					continue
				}
				if calls[call] == nil {
					calls[call] = map[string]bool{}
				}
				calls[call][base] = true
			}
		}
	}
	if len(userdataCalls) < 8 {
		t.Fatalf("found only %d distinct aws CLI calls in userdata.go; the regex drifted", len(userdataCalls))
	}
	for k := range operatorHintCalls {
		if !hintSeen[k] {
			t.Errorf("operatorHintCalls entry %q matched nothing; remove it", k)
		}
	}
	var missing []string
	for call, files := range calls {
		actions, known := cliActionMap[call]
		if !known {
			var fs []string
			for f := range files {
				fs = append(fs, f)
			}
			sort.Strings(fs)
			missing = append(missing, call+" in "+strings.Join(fs, ",")+" (not in cliActionMap — decide its grant, or list it in operatorHintCalls if it is printed text)")
			continue
		}
		for _, a := range actions {
			if !allowed[a] {
				missing = append(missing, call+" needs "+a)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("on-box CLI calls without a grant in %s:\n  %s", pinnedEC2SpotModuleDir(t), strings.Join(missing, "\n  "))
	}
}

func TestSandboxRole_EverySDKActionHasAGrant(t *testing.T) {
	allowed := allowedActions(t)
	var missing []string
	for a, src := range sdkActions {
		if !allowed[a] {
			missing = append(missing, a+" ("+src+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("SDK actions without a grant in the pinned module:\n  %s", strings.Join(missing, "\n  "))
	}
}
