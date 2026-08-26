package profile_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestValidateSchema_MITM verifies the spec.network.mitm.intercepts JSON
// schema subtree (Phase 127 plan 01): a name-plus-enabled-only override
// entry validates clean (hosts/action are semantic requirements owned by
// plan 04, not schema requirements), a full redirect/respond rule set
// validates clean, and an unknown key anywhere in the subtree is rejected.
func TestValidateSchema_MITM(t *testing.T) {
	t.Run("name and enabled only validates clean (disable-only override)", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rickroll
          enabled: false
`)
		errs := profile.Validate(data)
		if len(errs) != 0 {
			t.Errorf("expected no validation errors for name+enabled-only intercept, got %d:", len(errs))
			for _, e := range errs {
				t.Logf("  - %s", e.Error())
			}
		}
	})

	t.Run("full redirect and respond intercepts validate clean", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rickroll
          hosts: [".google.com"]
          action:
            redirect: https://www.youtube.com/watch?v=dQw4w9WgXcQ
        - name: chaos
          hosts: ["api.example.com"]
          action:
            respond:
              status: 503
              contentType: text/plain
              body: "maintenance window"
`)
		errs := profile.Validate(data)
		if len(errs) != 0 {
			t.Errorf("expected no validation errors for full intercept set, got %d:", len(errs))
			for _, e := range errs {
				t.Logf("  - %s", e.Error())
			}
		}
	})

	t.Run("unknown key on an intercept item is rejected", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rickroll
          bogusKey: true
`)
		errs := profile.Validate(data)
		if len(errs) == 0 {
			t.Error("expected validation errors for unknown intercept key 'bogusKey', got none — additionalProperties:false may have been loosened")
		}
	})

	t.Run("unknown key under mitm block itself is rejected", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      bogusMitmKey: true
`)
		errs := profile.Validate(data)
		if len(errs) == 0 {
			t.Error("expected validation errors for unknown mitm key 'bogusMitmKey', got none — additionalProperties:false may have been loosened")
		}
	})

	t.Run("block action is not a recognised schema key", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rickroll
          hosts: [".google.com"]
          action:
            block: true
`)
		errs := profile.Validate(data)
		if len(errs) == 0 {
			t.Error("expected validation errors for 'block' action key, got none — a block action must never be schema-legal")
		}
	})

	t.Run("omitting mitm block entirely validates clean", func(t *testing.T) {
		data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
`)
		errs := profile.Validate(data)
		if len(errs) != 0 {
			t.Errorf("expected no validation errors when mitm block is absent, got %d:", len(errs))
			for _, e := range errs {
				t.Logf("  - %s", e.Error())
			}
		}
	})
}
