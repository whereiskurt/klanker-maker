package main

import "testing"

// The env var wins when set — that is how the deployed function is configured, and
// why the hardcoded fallback below was latent rather than actively breaking.
// The fallback is what bites when a tfvar is ever forgotten.
func TestTTLHandlerName_PrefixFallback(t *testing.T) {
	tests := []struct {
		name        string
		envName     string
		envPrefix   string
		wantHandler string
		wantRole    string
	}{
		{
			name:        "explicit env var wins",
			envName:     "sec-ttl-handler",
			envPrefix:   "sec",
			wantHandler: "sec-ttl-handler",
			wantRole:    "sec-ttl-scheduler",
		},
		{
			name:        "fallback derives from resource prefix, not a hardcoded km-",
			envPrefix:   "sec",
			wantHandler: "sec-ttl-handler",
			wantRole:    "sec-ttl-scheduler",
		},
		{
			name:        "default install still resolves to km-",
			wantHandler: "km-ttl-handler",
			wantRole:    "km-ttl-scheduler",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KM_TTL_HANDLER_NAME", tc.envName)
			t.Setenv("KM_TTL_SCHEDULER_ROLE", "")
			t.Setenv("KM_RESOURCE_PREFIX", tc.envPrefix)

			if got := ttlHandlerName(); got != tc.wantHandler {
				t.Errorf("ttlHandlerName() = %q, want %q", got, tc.wantHandler)
			}
			if got := ttlSchedulerRole(); got != tc.wantRole {
				t.Errorf("ttlSchedulerRole() = %q, want %q", got, tc.wantRole)
			}
		})
	}
}
