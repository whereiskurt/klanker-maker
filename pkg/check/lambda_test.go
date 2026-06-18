package check

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambdapkg "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// mockLambdaConfig is a minimal LambdaClient for exercising the env-merge helpers
// (UpdateTriggerEnv / UpdateSecretPathsEnv). Only GetFunction +
// UpdateFunctionConfiguration are wired; the rest panic if unexpectedly called.
type mockLambdaConfig struct {
	existingEnv map[string]string
	gotCfgIn    *lambdapkg.UpdateFunctionConfigurationInput
}

func (m *mockLambdaConfig) GetFunction(_ context.Context, _ *lambdapkg.GetFunctionInput, _ ...func(*lambdapkg.Options)) (*lambdapkg.GetFunctionOutput, error) {
	return &lambdapkg.GetFunctionOutput{
		Configuration: &lambdatypes.FunctionConfiguration{
			Environment: &lambdatypes.EnvironmentResponse{Variables: m.existingEnv},
		},
	}, nil
}

func (m *mockLambdaConfig) UpdateFunctionConfiguration(_ context.Context, in *lambdapkg.UpdateFunctionConfigurationInput, _ ...func(*lambdapkg.Options)) (*lambdapkg.UpdateFunctionConfigurationOutput, error) {
	m.gotCfgIn = in
	return &lambdapkg.UpdateFunctionConfigurationOutput{}, nil
}

func (m *mockLambdaConfig) CreateFunction(context.Context, *lambdapkg.CreateFunctionInput, ...func(*lambdapkg.Options)) (*lambdapkg.CreateFunctionOutput, error) {
	panic("unexpected CreateFunction")
}
func (m *mockLambdaConfig) UpdateFunctionCode(context.Context, *lambdapkg.UpdateFunctionCodeInput, ...func(*lambdapkg.Options)) (*lambdapkg.UpdateFunctionCodeOutput, error) {
	panic("unexpected UpdateFunctionCode")
}
func (m *mockLambdaConfig) DeleteFunction(context.Context, *lambdapkg.DeleteFunctionInput, ...func(*lambdapkg.Options)) (*lambdapkg.DeleteFunctionOutput, error) {
	panic("unexpected DeleteFunction")
}
func (m *mockLambdaConfig) Invoke(context.Context, *lambdapkg.InvokeInput, ...func(*lambdapkg.Options)) (*lambdapkg.InvokeOutput, error) {
	panic("unexpected Invoke")
}

// TestUpdateSecretPathsEnv verifies that KM_CHECK_SECRET_PATHS is set on the
// merged env while existing (non-secret) vars are preserved.
func TestUpdateSecretPathsEnv(t *testing.T) {
	m := &mockLambdaConfig{
		existingEnv: map[string]string{
			"KM_CHECK_NAME":         "wiz-audit",
			"KM_ARTIFACTS_BUCKET":   "km-artifacts-123",
			"KM_CHECK_SECRET_PATHS": `["/km/checks/wiz-audit/OLD"]`,
		},
	}

	newJSON := `["/km/checks/wiz-audit/A","/km/checks/wiz-audit/B"]`
	if err := UpdateSecretPathsEnv(context.Background(), m, "km-check-wiz-audit", newJSON); err != nil {
		t.Fatalf("UpdateSecretPathsEnv: %v", err)
	}

	if m.gotCfgIn == nil || m.gotCfgIn.Environment == nil {
		t.Fatal("UpdateFunctionConfiguration was not called with an Environment")
	}
	vars := m.gotCfgIn.Environment.Variables

	if got := vars["KM_CHECK_SECRET_PATHS"]; got != newJSON {
		t.Errorf("KM_CHECK_SECRET_PATHS = %q, want %q", got, newJSON)
	}
	// Non-secret vars preserved.
	if vars["KM_CHECK_NAME"] != "wiz-audit" {
		t.Errorf("KM_CHECK_NAME not preserved: %q", vars["KM_CHECK_NAME"])
	}
	if vars["KM_ARTIFACTS_BUCKET"] != "km-artifacts-123" {
		t.Errorf("KM_ARTIFACTS_BUCKET not preserved: %q", vars["KM_ARTIFACTS_BUCKET"])
	}
	if aws.ToString(m.gotCfgIn.FunctionName) != "km-check-wiz-audit" {
		t.Errorf("FunctionName = %q, want km-check-wiz-audit", aws.ToString(m.gotCfgIn.FunctionName))
	}
}

// TestUpdateSecretPathsEnv_EmptyRemoves verifies an empty/[] list removes the var.
func TestUpdateSecretPathsEnv_EmptyRemoves(t *testing.T) {
	m := &mockLambdaConfig{
		existingEnv: map[string]string{
			"KM_CHECK_NAME":         "c",
			"KM_CHECK_SECRET_PATHS": `["/km/checks/c/OLD"]`,
		},
	}
	if err := UpdateSecretPathsEnv(context.Background(), m, "km-check-c", "[]"); err != nil {
		t.Fatalf("UpdateSecretPathsEnv: %v", err)
	}
	if _, present := m.gotCfgIn.Environment.Variables["KM_CHECK_SECRET_PATHS"]; present {
		t.Error("KM_CHECK_SECRET_PATHS should be removed for an empty list")
	}
	if m.gotCfgIn.Environment.Variables["KM_CHECK_NAME"] != "c" {
		t.Error("KM_CHECK_NAME not preserved")
	}
}
