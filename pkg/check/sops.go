package check

// sops.go — deploy-time SOPS secret unpacking for km check (Phase 116 follow-on).
//
// A check exposes a SOPS-encrypted secrets file's values to its Lambda snippet as
// env vars WITHOUT any KMS-decrypt path inside the Lambda. The operator decrypts
// the file at `km check deploy --sops` time (where sops + the KMS key already
// work) and unpacks each value into a per-check SSM SecureString parameter at
//
//	/{prefix}/checks/{check}/{key}
//
// Those param paths are appended to the check's secret paths (KM_CHECK_SECRET_PATHS),
// and the existing bootstrap (_km_check_bootstrap.py) fetches each WithDecryption at
// invoke time and exposes it as an env var keyed by the LAST path segment UPPERCASED:
//
//	/{prefix}/checks/wiz-audit/wiz_token  ⇒  $WIZ_TOKEN
//
// The {prefix}/checks/* namespace already matches the check-runner role's
// ssm:GetParameter grant, so no IAM change is needed. Decryption never happens
// inside the Lambda; secrets transit the operator machine + SSM (SecureString,
// KMS-encrypted at rest) — the same trust model as km bootstrap secret handling.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// envVarKeyRE matches a valid POSIX-ish environment variable name. SOPS keys
// must satisfy this because the bootstrap exposes each as an env var (UPPERCASED).
// Rejecting dashes/dots/spaces/leading-digits up front gives the operator a clear
// error at deploy time instead of a silently broken env var at runtime.
var envVarKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// sopsDecryptRaw decrypts a SOPS file operator-side and returns the plaintext as
// JSON bytes. It is a package var so tests can inject a canned decrypt without a
// real sops binary or KMS access.
var sopsDecryptRaw = defaultSopsDecrypt

// defaultSopsDecrypt shells out to `sops decrypt --output-type json <file>`,
// mirroring how the sandbox userdata invokes sops. JSON output gives a flat object
// for a flat YAML/JSON secrets file and strips the SOPS metadata block.
func defaultSopsDecrypt(sopsFile string) ([]byte, error) {
	if _, err := exec.LookPath("sops"); err != nil {
		return nil, fmt.Errorf("sops binary not found on PATH (required for --sops): %w", err)
	}
	cmd := exec.Command("sops", "decrypt", "--output-type", "json", sopsFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sops decrypt %s failed: %w\n%s", sopsFile, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// parseFlatSecretMap parses decrypted JSON into a flat map[string]string.
//
//   - Scalar values (string/number/bool) are coerced to their verbatim string form.
//   - Nested maps, arrays, and null are rejected with a clear error (only flat
//     top-level scalars map cleanly to env vars).
//   - Keys must be valid env var names ([A-Za-z_][A-Za-z0-9_]*).
//   - Reserved keys "sops"/"_meta" are skipped (defensive — sops -d strips them).
//   - An empty result (zero usable keys) is an error.
func parseFlatSecretMap(raw []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve integer/float text exactly (no float64 rounding)

	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse decrypted sops output as a flat JSON object: %w", err)
	}

	out := make(map[string]string, len(doc))
	for k, rawVal := range doc {
		if k == "sops" || k == "_meta" {
			continue
		}
		if !envVarKeyRE.MatchString(k) {
			return nil, fmt.Errorf("sops key %q is not a valid env var name (allowed: letters, digits, underscore; no leading digit)", k)
		}

		var v interface{}
		d := json.NewDecoder(bytes.NewReader(rawVal))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return nil, fmt.Errorf("sops key %q: decode value: %w", k, err)
		}

		switch tv := v.(type) {
		case string:
			out[k] = tv
		case json.Number:
			out[k] = tv.String()
		case bool:
			out[k] = strconv.FormatBool(tv)
		case nil:
			return nil, fmt.Errorf("sops key %q has a null value (not allowed)", k)
		default: // map or slice
			return nil, fmt.Errorf("sops key %q has a nested value; only flat scalar values are supported", k)
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("sops file decrypted to zero usable keys")
	}
	return out, nil
}

// DecryptSopsToMap decrypts a SOPS file operator-side and returns a flat
// map[string]string of its scalar key/value pairs.
func DecryptSopsToMap(sopsFile string) (map[string]string, error) {
	raw, err := sopsDecryptRaw(sopsFile)
	if err != nil {
		return nil, err
	}
	return parseFlatSecretMap(raw)
}

// CheckSecretParamPath returns the SSM parameter path for a check secret:
// /{prefix}/checks/{check}/{key}. This namespace matches the check-runner role's
// ssm:GetParameter grant (no IAM change needed).
func CheckSecretParamPath(prefix, checkName, key string) string {
	return fmt.Sprintf("/%s/checks/%s/%s", prefix, checkName, key)
}

// SSMSecretsAPI is the narrow SSM interface used to unpack and clean up per-check
// SOPS secrets. Satisfied by *ssm.Client; an interface for test injection.
type SSMSecretsAPI interface {
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
	DeleteParameters(ctx context.Context, in *ssm.DeleteParametersInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error)
}

// NewSSMSecretsClient constructs an SSM client from an aws.Config.
func NewSSMSecretsClient(awsCfg aws.Config) SSMSecretsAPI {
	return ssm.NewFromConfig(awsCfg)
}

// UnpackSopsToSSM decrypts sopsFile operator-side and writes each scalar value to
// an SSM SecureString parameter at /{prefix}/checks/{check}/{key} (Overwrite=true).
// It returns the created parameter paths in sorted order (stable for downstream
// hashing / display). On any decrypt or PutParameter error it returns immediately
// without writing further params.
func UnpackSopsToSSM(ctx context.Context, client SSMSecretsAPI, prefix, checkName, sopsFile string) ([]string, error) {
	secrets, err := DecryptSopsToMap(sopsFile)
	if err != nil {
		return nil, fmt.Errorf("unpack sops %s: %w", sopsFile, err)
	}

	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	paths := make([]string, 0, len(keys))
	for _, k := range keys {
		path := CheckSecretParamPath(prefix, checkName, k)
		_, err := client.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(path),
			Value:     aws.String(secrets[k]),
			Type:      ssmtypes.ParameterTypeSecureString,
			Overwrite: aws.Bool(true),
		})
		if err != nil {
			return nil, fmt.Errorf("put ssm secret %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// DeleteCheckSecretParams deletes every SSM parameter under the check's namespace
// /{prefix}/checks/{check}/ (recursive, paginated). Returns the deleted paths.
// Used by km check rm so SOPS-derived secrets do not leak after teardown.
func DeleteCheckSecretParams(ctx context.Context, client SSMSecretsAPI, prefix, checkName string) ([]string, error) {
	pathPrefix := fmt.Sprintf("/%s/checks/%s/", prefix, checkName)

	var names []string
	var nextToken *string
	for {
		out, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:       aws.String(pathPrefix),
			Recursive:  aws.Bool(true),
			NextToken:  nextToken,
			MaxResults: aws.Int32(10), // SSM GetParametersByPath max page size
		})
		if err != nil {
			return nil, fmt.Errorf("list ssm secrets under %s: %w", pathPrefix, err)
		}
		for _, p := range out.Parameters {
			names = append(names, aws.ToString(p.Name))
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	if len(names) == 0 {
		return nil, nil
	}

	// DeleteParameters accepts at most 10 names per call.
	var deleted []string
	for i := 0; i < len(names); i += 10 {
		end := i + 10
		if end > len(names) {
			end = len(names)
		}
		batch := names[i:end]
		out, err := client.DeleteParameters(ctx, &ssm.DeleteParametersInput{Names: batch})
		if err != nil {
			return deleted, fmt.Errorf("delete ssm secrets %v: %w", batch, err)
		}
		deleted = append(deleted, out.DeletedParameters...)
	}
	return deleted, nil
}
