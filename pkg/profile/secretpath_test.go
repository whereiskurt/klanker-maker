package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func TestValidateSecretPaths(t *testing.T) {
	tests := []struct {
		name    string
		paths   []string
		wantErr bool
		wantSub string // substring expected in the message
	}{
		{
			name:    "prefix-relative path is accepted",
			paths:   []string{"{{prefix}}/wiz/wiz-api-client-id"},
			wantErr: false,
		},
		{
			name:    "multiple prefix-relative paths are accepted",
			paths:   []string{"{{prefix}}/wiz/wiz-api-client-id", "{{prefix}}/wiz/wiz-api-client-secret"},
			wantErr: false,
		},
		{
			name:    "empty list is accepted",
			paths:   nil,
			wantErr: false,
		},
		{
			name:    "absolute path is rejected",
			paths:   []string{"/km/wiz/wiz-api-client-id"},
			wantErr: true,
			wantSub: "must start with",
		},
		{
			name:    "path escaping the install namespace is rejected",
			paths:   []string{"/*"},
			wantErr: true,
			wantSub: "must start with",
		},
		{
			name:    "unknown token is rejected",
			paths:   []string{"{{prefix2}}/wiz/x"},
			wantErr: true,
			wantSub: "unknown token",
		},
		{
			name:    "token in a later segment is rejected",
			paths:   []string{"{{prefix}}/wiz/{{sandbox}}"},
			wantErr: true,
			wantSub: "unknown token",
		},
		{
			name:    "bare token with no trailing slash is rejected",
			paths:   []string{"{{prefix}}"},
			wantErr: true,
			wantSub: "must start with",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.ValidateSecretPaths(tc.paths)
			if tc.wantErr && len(errs) == 0 {
				t.Fatalf("ValidateSecretPaths(%q) = no errors, want an error", tc.paths)
			}
			if !tc.wantErr && len(errs) != 0 {
				t.Fatalf("ValidateSecretPaths(%q) = %+v, want no errors", tc.paths, errs)
			}
			if tc.wantErr {
				if errs[0].IsWarning {
					t.Errorf("error must be blocking, not a warning: %+v", errs[0])
				}
				if !strings.Contains(errs[0].Message, tc.wantSub) {
					t.Errorf("message = %q, want substring %q", errs[0].Message, tc.wantSub)
				}
				if errs[0].Path != "spec.iam.allowedSecretPaths" {
					t.Errorf("Path = %q, want %q", errs[0].Path, "spec.iam.allowedSecretPaths")
				}
			}
		})
	}
}

func TestInterpolateSecretPaths(t *testing.T) {
	t.Run("token expands to a leading-slash prefix segment", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-id"}, "km")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/km/wiz/wiz-api-client-id"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %q, want [%q]", got, want)
		}
	})

	t.Run("non-default prefix", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-secret"}, "km2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/km2/wiz/wiz-api-client-secret"
		if got[0] != want {
			t.Errorf("got %q, want %q", got[0], want)
		}
	})

	t.Run("empty prefix with a token present is an ERROR, never a default", func(t *testing.T) {
		_, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-id"}, "")
		if err == nil {
			t.Fatal("expected an error when resourcePrefix is empty, got nil — " +
				"defaulting would render an IAM policy for the wrong install")
		}
		if !strings.Contains(err.Error(), "KM_RESOURCE_PREFIX") {
			t.Errorf("error should name the env var, got: %v", err)
		}
	})

	t.Run("empty path list with empty prefix is fine", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %q, want empty", got)
		}
	})
}
