package aws

import (
	"testing"
)

func TestIsManagedIdentityEnv(t *testing.T) {
	cases := []struct {
		name    string
		envVars map[string]string
		want    bool
	}{
		{
			name:    "operator laptop — no managed-identity signals",
			envVars: map[string]string{},
			want:    false,
		},
		{
			name:    "EKS pod — KUBERNETES_SERVICE_HOST set by kubelet",
			envVars: map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"},
			want:    true,
		},
		{
			name:    "Lambda runtime — AWS_LAMBDA_FUNCTION_NAME set",
			envVars: map[string]string{"AWS_LAMBDA_FUNCTION_NAME": "km-create-handler"},
			want:    true,
		},
		{
			name:    "ECS / CodeBuild — AWS_EXECUTION_ENV set",
			envVars: map[string]string{"AWS_EXECUTION_ENV": "AWS_ECS_FARGATE"},
			want:    true,
		},
		{
			name: "multiple signals — any one is enough",
			envVars: map[string]string{
				"KUBERNETES_SERVICE_HOST": "10.0.0.1",
				"AWS_EXECUTION_ENV":       "AWS_ECS_FARGATE",
			},
			want: true,
		},
	}

	// Unset every signal up front so the laptop case is honest even when the
	// host environment leaks one of them in (e.g. a dev running these tests
	// from inside a container).
	signals := []string{"KUBERNETES_SERVICE_HOST", "AWS_LAMBDA_FUNCTION_NAME", "AWS_EXECUTION_ENV"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range signals {
				t.Setenv(k, "")
			}
			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}
			if got := isManagedIdentityEnv(); got != tc.want {
				t.Errorf("isManagedIdentityEnv() = %v, want %v (env=%v)", got, tc.want, tc.envVars)
			}
		})
	}
}
