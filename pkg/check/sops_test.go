package check

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ─────────────────────────────────────────────────────────────────────────────
// parseFlatSecretMap
// ─────────────────────────────────────────────────────────────────────────────

func TestParseFlatSecretMap_FlatStrings(t *testing.T) {
	raw := []byte(`{"wiz_token":"abc123","API_KEY":"sk-xyz"}`)
	got, err := parseFlatSecretMap(raw)
	if err != nil {
		t.Fatalf("parseFlatSecretMap: %v", err)
	}
	want := map[string]string{"wiz_token": "abc123", "API_KEY": "sk-xyz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFlatSecretMap_CoercesScalars(t *testing.T) {
	// Numbers and bools are common in YAML; coerce to string verbatim.
	raw := []byte(`{"port":"8443","retries":5,"enabled":true,"ratio":1.5}`)
	got, err := parseFlatSecretMap(raw)
	if err != nil {
		t.Fatalf("parseFlatSecretMap: %v", err)
	}
	want := map[string]string{"port": "8443", "retries": "5", "enabled": "true", "ratio": "1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFlatSecretMap_RejectsNestedMap(t *testing.T) {
	raw := []byte(`{"db":{"host":"x","port":5432}}`)
	if _, err := parseFlatSecretMap(raw); err == nil {
		t.Fatal("expected error for nested map value, got nil")
	}
}

func TestParseFlatSecretMap_RejectsArray(t *testing.T) {
	raw := []byte(`{"hosts":["a","b"]}`)
	if _, err := parseFlatSecretMap(raw); err == nil {
		t.Fatal("expected error for array value, got nil")
	}
}

func TestParseFlatSecretMap_RejectsNull(t *testing.T) {
	raw := []byte(`{"empty":null}`)
	if _, err := parseFlatSecretMap(raw); err == nil {
		t.Fatal("expected error for null value, got nil")
	}
}

func TestParseFlatSecretMap_RejectsBadKey(t *testing.T) {
	cases := []string{
		`{"has-dash":"x"}`,  // dashes aren't valid env var names
		`{"has space":"x"}`, // spaces
		`{"1leading":"x"}`,  // leading digit
		`{"a.b":"x"}`,       // dots
		`{"":"x"}`,          // empty key
	}
	for _, c := range cases {
		if _, err := parseFlatSecretMap([]byte(c)); err == nil {
			t.Errorf("expected error for bad key in %s, got nil", c)
		}
	}
}

func TestParseFlatSecretMap_SkipsSopsMeta(t *testing.T) {
	// `sops decrypt` strips the metadata block, but be defensive: a residual
	// "sops" or "_meta" key must never become a secret param.
	raw := []byte(`{"real_key":"v","sops":{"kms":"..."},"_meta":"x"}`)
	got, err := parseFlatSecretMap(raw)
	if err != nil {
		t.Fatalf("parseFlatSecretMap: %v", err)
	}
	want := map[string]string{"real_key": "v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFlatSecretMap_Empty(t *testing.T) {
	if _, err := parseFlatSecretMap([]byte(`{}`)); err == nil {
		t.Fatal("expected error for zero-key map, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CheckSecretParamPath
// ─────────────────────────────────────────────────────────────────────────────

func TestCheckSecretParamPath(t *testing.T) {
	got := CheckSecretParamPath("km", "wiz-audit", "WIZ_TOKEN")
	want := "/km/checks/wiz-audit/WIZ_TOKEN"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// mock SSM client
// ─────────────────────────────────────────────────────────────────────────────

type mockSSM struct {
	puts        []*ssm.PutParameterInput
	byPathPages [][]ssmtypes.Parameter
	deleted     [][]string
	putErr      error
	deleteErr   error
}

func (m *mockSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	m.puts = append(m.puts, in)
	return &ssm.PutParameterOutput{}, nil
}

func (m *mockSSM) GetParametersByPath(_ context.Context, in *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	// Page index is encoded in NextToken ("1","2",...); nil/"" = first page.
	idx := 0
	if in.NextToken != nil && *in.NextToken == "1" {
		idx = 1
	}
	if idx >= len(m.byPathPages) {
		return &ssm.GetParametersByPathOutput{}, nil
	}
	out := &ssm.GetParametersByPathOutput{Parameters: m.byPathPages[idx]}
	if idx+1 < len(m.byPathPages) {
		next := "1"
		out.NextToken = &next
	}
	return out, nil
}

func (m *mockSSM) DeleteParameters(_ context.Context, in *ssm.DeleteParametersInput, _ ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	m.deleted = append(m.deleted, in.Names)
	return &ssm.DeleteParametersOutput{DeletedParameters: in.Names}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UnpackSopsToSSM
// ─────────────────────────────────────────────────────────────────────────────

func TestUnpackSopsToSSM(t *testing.T) {
	// Inject a canned decrypt so no real sops binary is needed.
	orig := sopsDecryptRaw
	sopsDecryptRaw = func(string) ([]byte, error) {
		return []byte(`{"WIZ_TOKEN":"t-1","SLACK_HOOK":"h-2"}`), nil
	}
	defer func() { sopsDecryptRaw = orig }()

	m := &mockSSM{}
	paths, err := UnpackSopsToSSM(context.Background(), m, "km", "wiz-audit", "secrets.enc.yaml")
	if err != nil {
		t.Fatalf("UnpackSopsToSSM: %v", err)
	}

	wantPaths := []string{"/km/checks/wiz-audit/SLACK_HOOK", "/km/checks/wiz-audit/WIZ_TOKEN"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("returned paths = %v, want %v (sorted)", paths, wantPaths)
	}

	if len(m.puts) != 2 {
		t.Fatalf("expected 2 PutParameter calls, got %d", len(m.puts))
	}
	for _, p := range m.puts {
		if p.Type != ssmtypes.ParameterTypeSecureString {
			t.Errorf("param %s Type = %s, want SecureString", aws.ToString(p.Name), p.Type)
		}
		if p.Overwrite == nil || !*p.Overwrite {
			t.Errorf("param %s Overwrite must be true", aws.ToString(p.Name))
		}
	}
}

func TestUnpackSopsToSSM_DecryptError(t *testing.T) {
	orig := sopsDecryptRaw
	sopsDecryptRaw = func(string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	defer func() { sopsDecryptRaw = orig }()

	m := &mockSSM{}
	if _, err := UnpackSopsToSSM(context.Background(), m, "km", "c", "f.yaml"); err == nil {
		t.Fatal("expected decrypt error to propagate, got nil")
	}
	if len(m.puts) != 0 {
		t.Errorf("no params should be written when decrypt fails, got %d", len(m.puts))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteCheckSecretParams
// ─────────────────────────────────────────────────────────────────────────────

func TestDeleteCheckSecretParams_Paginates(t *testing.T) {
	mkParam := func(name string) ssmtypes.Parameter {
		return ssmtypes.Parameter{Name: aws.String(name)}
	}
	m := &mockSSM{
		byPathPages: [][]ssmtypes.Parameter{
			{mkParam("/km/checks/c/A"), mkParam("/km/checks/c/B")},
			{mkParam("/km/checks/c/C")},
		},
	}
	deleted, err := DeleteCheckSecretParams(context.Background(), m, "km", "c")
	if err != nil {
		t.Fatalf("DeleteCheckSecretParams: %v", err)
	}
	sort.Strings(deleted)
	want := []string{"/km/checks/c/A", "/km/checks/c/B", "/km/checks/c/C"}
	if !reflect.DeepEqual(deleted, want) {
		t.Errorf("deleted = %v, want %v", deleted, want)
	}
}

func TestDeleteCheckSecretParams_None(t *testing.T) {
	m := &mockSSM{} // no params under the path
	deleted, err := DeleteCheckSecretParams(context.Background(), m, "km", "c")
	if err != nil {
		t.Fatalf("DeleteCheckSecretParams: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected zero deleted, got %v", deleted)
	}
	if len(m.deleted) != 0 {
		t.Errorf("expected no DeleteParameters call, got %d", len(m.deleted))
	}
}
