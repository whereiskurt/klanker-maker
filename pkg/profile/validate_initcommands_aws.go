package profile

import (
	"fmt"
	"regexp"
	"strings"
)

// The v1.8.0 sandbox role grants only what km's own code calls. An operator
// `aws …` line in initCommands used to work through the Allow-star; now it
// fails under set -e in km-init.sh and aborts the remaining steps. This is a
// best-effort textual check: it cannot see indirect AWS use inside installers.
var initAWSCallRe = regexp.MustCompile(`(?:^|[\s;&|(])aws\s+(s3|s3api|ssm|dynamodb|sqs|logs|ses|sesv2|kms|sts|ec2|iam|secretsmanager|lambda|events)\s+([a-z-]+)([^\n;&|]*)`)

var initAWSContinuationRe = regexp.MustCompile(`\\[ \t]*\r?\n[ \t]*`)

var initAWSS3GrantedPrefixRe = regexp.MustCompile(`s3://[^/\s"']+/(models|sidecars|binaries|rsync)/`)

func validateInitCommandAWSCalls(p *SandboxProfile) []ValidationError {
	var errs []ValidationError
	allowedSSM := map[string]bool{}
	for _, s := range p.Spec.IAM.AllowedSecretPaths {
		allowedSSM[s] = true
	}
	if p.Spec.OTP != nil {
		for _, s := range p.Spec.OTP.Secrets {
			allowedSSM[s] = true
		}
	}
	check := func(path string, cmds []string) {
		for i, cmd := range cmds {
			// Join backslash-newline continuations so a continued command is one command.
			cmd = initAWSContinuationRe.ReplaceAllString(cmd, " ")
			for _, m := range initAWSCallRe.FindAllStringSubmatch(cmd, -1) {
				svc, op, rest := m[1], m[2], m[3]
				switch {
				case svc == "sts" && op == "get-caller-identity":
					continue
				case svc == "s3" && (op == "cp" || op == "sync" || op == "ls") && initAWSS3GrantedPrefixRe.MatchString(rest):
					continue
				case svc == "s3" && (op == "cp" || op == "sync" || op == "ls") && !strings.Contains(rest, "s3://"):
					// The target is built in a shell variable (e.g. SRC="s3://.../models/..."
					// then `aws s3 sync "$SRC" ...`); a textual check cannot resolve it, so
					// stay quiet rather than warn on a granted call.
					continue
				case svc == "ssm" && op == "get-parameter" && ssmNameListed(rest, allowedSSM):
					continue
				}
				errs = append(errs, ValidationError{
					Path: fmt.Sprintf("%s[%d]", path, i),
					Message: fmt.Sprintf("calls `aws %s %s`: the sandbox role grants only what km itself needs, so this step will fail (and abort the remaining initCommands) unless the target is covered. "+
						"For an SSM parameter add it to spec.iam.allowedSecretPaths; for S3 use the artifacts bucket's models/, sidecars/, binaries/, rsync/ prefixes; anything else needs a profile grant field — ask for one.", svc, op),
					IsWarning: true,
				})
			}
		}
	}
	check("spec.execution.initCommands", p.Spec.Execution.InitCommands)
	check("spec.execution.initCommandsAppend", p.Spec.Execution.InitCommandsAppend)
	return errs
}

func ssmNameListed(rest string, allowed map[string]bool) bool {
	fields := strings.Fields(rest)
	for i, f := range fields {
		if f == "--name" && i+1 < len(fields) {
			return allowed[strings.Trim(fields[i+1], `"'`)]
		}
		if strings.HasPrefix(f, "--name=") {
			return allowed[strings.Trim(strings.TrimPrefix(f, "--name="), `"'`)]
		}
	}
	return false
}
