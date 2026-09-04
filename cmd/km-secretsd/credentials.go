package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

const (
	// chainedSessionSeconds is the ceiling STS allows for role chaining —
	// assuming a role using credentials that are themselves a role's. Asking for
	// more is a hard error, not a silent clamp, so this is a real bound rather
	// than a preference.
	chainedSessionSeconds = 3600

	// refreshMargin is how long before expiry a cached credential stops being
	// handed out. A consumer that receives one needs time to finish using it;
	// handing out a credential with seconds left produces an expiry failure
	// inside somebody else's request, far from this code.
	refreshMargin = 5 * time.Minute
)

// STSAPI is the slice of STS the broker uses, as an interface so the tests can
// prove the ARN derivation and the session policy without an AWS account.
type STSAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	AssumeRole(context.Context, *sts.AssumeRoleInput, ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// mintCredentials returns credentials that are the instance role minus the two
// grants which open the secrets bundle (see secrets.SessionPolicy).
//
// The BROKER does this, not km-creds, for a structural reason: km-creds runs as
// uid sandbox and is therefore on the far side of the fence, with no way to
// reach IMDS for the credentials an AssumeRole call needs. The broker is root
// and unfenced.
func (s *Server) mintCredentials(ctx context.Context) (*secrets.Credentials, error) {
	if !s.FenceEnabled {
		// Never fall through to un-narrowed credentials. A box whose profile did
		// not ask for the fence has no v1.7.0 self-assume trust either, so the
		// AssumeRole would fail regardless — refusing here makes the reason
		// legible instead of surfacing as an opaque STS AccessDenied.
		return nil, errors.New("credentials: the IMDS fence is not enabled on this sandbox")
	}

	s.credMu.Lock()
	defer s.credMu.Unlock()
	if s.cachedCreds != nil && time.Until(s.cachedExpiry) > refreshMargin {
		return s.cachedCreds, nil
	}

	api, err := s.stsAPI(ctx)
	if err != nil {
		return nil, err
	}

	who, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("credentials: whoami: %w", err)
	}
	roleARN, err := roleARNFromCallerARN(aws.ToString(who.Arn))
	if err != nil {
		return nil, err
	}

	policy, err := secrets.SessionPolicy(s.ResourcePrefix, s.ArtifactsBucket, s.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	out, err := api.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleARN),
		RoleSessionName: aws.String(sessionName(s.SandboxID)),
		Policy:          aws.String(policy),
		DurationSeconds: aws.Int32(chainedSessionSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: assume %s: %w "+
			"(the self-assume trust ships in ec2spot v1.7.0 — was this sandbox created "+
			"after km init applied it?)", roleARN, err)
	}
	if out.Credentials == nil {
		return nil, errors.New("credentials: STS returned no credentials")
	}

	exp := aws.ToTime(out.Credentials.Expiration)
	creds := &secrets.Credentials{
		Version:         1,
		AccessKeyID:     aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken),
		Expiration:      exp.UTC().Format(time.RFC3339),
	}
	s.cachedCreds, s.cachedExpiry = creds, exp
	return creds, nil
}

// sessionName builds an STS role session name. STS caps it at 64 characters and
// rejects anything longer outright, so a long sandbox id must truncate rather
// than fail every credential request on the box.
func sessionName(sandboxID string) string {
	n := "km-fenced-" + sandboxID
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// roleARNFromCallerARN turns the caller's assumed-role ARN into the IAM role ARN
// it came from.
//
// Deriving this instead of configuring it is deliberate: the answer is by
// construction the role the instance is actually running as, so it cannot drift
// from what Terraform provisioned. A misparse would make the broker assume some
// OTHER role, so every unexpected shape is an error rather than a best guess.
//
//	arn:aws:sts::ACCT:assumed-role/NAME/SESSION  ->  arn:aws:iam::ACCT:role/NAME
func roleARNFromCallerARN(caller string) (string, error) {
	parts := strings.Split(caller, ":")
	if len(parts) != 6 || parts[2] != "sts" {
		return "", fmt.Errorf("credentials: caller %q is not an STS ARN — km-secretsd "+
			"must run under the instance role", caller)
	}
	res := strings.Split(parts[5], "/")
	if len(res) < 2 || res[0] != "assumed-role" || res[1] == "" {
		return "", fmt.Errorf("credentials: caller %q is not an assumed role", caller)
	}
	return fmt.Sprintf("arn:%s:iam::%s:role/%s", parts[1], parts[4], res[1]), nil
}

// stsAPI returns the injected client, or builds a real one on first use.
func (s *Server) stsAPI(ctx context.Context) (STSAPI, error) {
	if s.STS != nil {
		return s.STS, nil
	}
	s.stsOnce.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			s.stsErr = fmt.Errorf("credentials: load AWS config: %w", err)
			return
		}
		s.STS = sts.NewFromConfig(cfg)
	})
	return s.STS, s.stsErr
}

// runFenceProbe executes assertion 6's three clauses against the live box.
//
// Every clause runs AS UID SANDBOX (runuser), because uid sandbox is what the
// fence is about; running any of them as root would prove nothing at all.
// runuser rather than sudo, matching the rest of the platform: sudo does not
// preserve the cgroup and skips the proxy environment.
func (s *Server) runFenceProbe() (imdsBlocked, stsWorks, decryptDenied bool, detail string) {
	var notes []string

	// 1. IMDS must FAIL for uid sandbox. A short --connect-timeout keeps a
	//    misconfigured DROP (rather than REJECT) from stalling the boot for the
	//    SDK's full retry budget.
	err := exec.Command("runuser", "-u", "sandbox", "--", "curl", "-sS", "-o", "/dev/null",
		"--connect-timeout", "3", "-X", "PUT",
		"http://169.254.169.254/latest/api/token",
		"-H", "X-aws-ec2-metadata-token-ttl-seconds: 60").Run()
	imdsBlocked = err != nil
	notes = append(notes, fmt.Sprintf("imds-blocked=%v", imdsBlocked))

	// 2. The helpers must still work — this is the clause that catches a fence
	//    that took km-github/km-slack/km-h1 down with it. It exercises the whole
	//    credential_process chain: ~/.aws/config -> km-creds -> broker -> STS.
	err = exec.Command("runuser", "-u", "sandbox", "--",
		"aws", "sts", "get-caller-identity").Run()
	stsWorks = err == nil
	notes = append(notes, fmt.Sprintf("sts:getcalleridentity-ok=%v", stsWorks))

	// 3. THE NEGATIVE CONTROL. The narrowed credentials must FAIL to decrypt the
	//    bundle. A success here means the session-policy Deny is not matching and
	//    the whole fence buys nothing — which no other clause can detect, and
	//    which an IAM simulator would report as a pass.
	out, err := exec.Command("runuser", "-u", "sandbox", "--",
		"/opt/km/bin/sops", "--decrypt", s.CiphertextPath).CombinedOutput()
	decryptDenied = err != nil
	if decryptDenied {
		notes = append(notes, "decrypt-denied=true")
	} else {
		// Never let recovered plaintext reach a log line or an audit event.
		zero(out)
		notes = append(notes, "decrypt-denied=false (the bundle DECRYPTED as uid sandbox)")
	}

	return imdsBlocked, stsWorks, decryptDenied, strings.Join(notes, " ")
}
