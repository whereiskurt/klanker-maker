package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestParseSourcesEnv(t *testing.T) {
	raw := `{"sources":[{"name":"wiz","auth":{"type":"bearer","secret_path":"/p"},
"rules":[{"alias":"ir-bot","prompt":"go"}]}],"rate_limit":{"max_dispatches":5,"window_seconds":60}}`
	cfg, err := parseSourcesEnv(raw)
	if err != nil {
		t.Fatalf("parseSourcesEnv: %v", err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "wiz" {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
	if cfg.RateLimit == nil || cfg.RateLimit.MaxDispatches != 5 {
		t.Errorf("rate_limit: %+v", cfg.RateLimit)
	}
}

// An absent env var must leave the bridge dormant, not crash the cold start.
func TestParseSourcesEnv_EmptyIsDormant(t *testing.T) {
	cfg, err := parseSourcesEnv("")
	if err != nil {
		t.Fatalf("empty env must not error: %v", err)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("sources: got %d, want 0", len(cfg.Sources))
	}
}

// Malformed JSON must warn and stay dormant rather than panic the Lambda.
func TestParseSourcesEnv_MalformedIsDormant(t *testing.T) {
	cfg, err := parseSourcesEnv(`{not json`)
	if err == nil {
		t.Fatal("malformed JSON should report an error to the caller")
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("sources: got %d, want 0", len(cfg.Sources))
	}
}

func TestLowercaseHeaders(t *testing.T) {
	got := lowercaseHeaders(map[string]string{"Authorization": "Bearer x", "X-Foo": "y"})
	if got["authorization"] != "Bearer x" || got["x-foo"] != "y" {
		t.Errorf("got %+v", got)
	}
}

func TestEnvOr(t *testing.T) {
	os.Setenv("KM_TEST_X", "set") //nolint:errcheck
	defer os.Unsetenv("KM_TEST_X")
	if envOr("KM_TEST_X", "fallback") != "set" {
		t.Error("set var must win")
	}
	if envOr("KM_TEST_UNSET", "fallback") != "fallback" {
		t.Error("unset var must fall back")
	}
}

var _ = json.Marshal

// ============================================================
// Nil-safety: every injected Handler dependency must survive init(), by name.
// ============================================================
//
// Task 6's review flagged this explicitly: there is no nil-guard anywhere in
// Handle() and no recover() in the request path. A nil dependency field panics
// on the very first request that reaches it, the Lambda returns non-200, and
// the sender redelivers with a FRESH delivery id that walks straight past
// dedup — exactly the failure the "always return 200" design exists to
// prevent, arriving through the back door. This test enumerates the fields by
// NAME (not just "every non-zero-value field via reflection is fine") so that
// a future field added to Handler and left unwired here fails loudly, naming
// the field, rather than silently passing because reflection only checked the
// fields the test already knew about.
//
// handler is package-initialized by init() (this file's package, main), which
// runs before any test in this binary — no additional setup is required.
func TestHandlerFieldsAllWired(t *testing.T) {
	if handler == nil {
		t.Fatal("package-level handler is nil after init() — cold start did not run")
	}

	checks := []struct {
		name string
		nilp bool
	}{
		{"Secrets", handler.Secrets == nil},
		{"Resolver", handler.Resolver == nil},
		{"Queue", handler.Queue == nil},
		{"Resumer", handler.Resumer == nil},
		{"Status", handler.Status == nil},
		{"Cold", handler.Cold == nil},
		{"Nonces", handler.Nonces == nil},
		{"Rates", handler.Rates == nil},
		{"Now", handler.Now == nil},
	}
	for _, c := range checks {
		if c.nilp {
			t.Errorf("bridge.Handler.%s is nil after init() — a request reaching this "+
				"dependency will panic, the Lambda will return non-200, and the sender "+
				"will redeliver with a fresh id that bypasses dedup", c.name)
		}
	}

	// Belt-and-suspenders: reflect over every field on bridge.Handler and fail if
	// any pointer/interface/func-typed field is nil that the table above didn't
	// name — catches a NEW field added to Handler in the future that this test
	// (and the wiring in init()) was never updated for. Sources/RateLimit are
	// legitimate data fields (not dependencies) and are allowed to be zero-value.
	// Quota/Limits/Freezer are Task 9A optional dependencies: dormant (nil)
	// unless KM_QUOTA_TABLE is set on the Lambda env, which it is not in this
	// test process — see TestWireActionQuota_* in main_quota_test.go for the
	// wired-when-set half of this contract.
	allowedZero := map[string]bool{
		"Sources": true, "RateLimit": true,
		"Quota": true, "Limits": true, "Freezer": true,
	}
	v := reflect.ValueOf(*handler)
	tp := v.Type()
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if allowedZero[f.Name] {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Ptr, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
			if fv.IsNil() {
				t.Errorf("bridge.Handler.%s is nil after init() (reflection sweep) — "+
					"add wiring in cmd/km-webhook-bridge/main.go init() and to the named "+
					"checks above", f.Name)
			}
		}
	}
}

// Now must be a REAL clock, not merely non-nil — a stub that always returns 0
// would pass the nil check above yet silently break CheckRate's window bucketing.
func TestHandlerNowIsRealClock(t *testing.T) {
	if handler == nil {
		t.Fatal("package-level handler is nil after init()")
	}
	got := handler.Now()
	if got <= 0 {
		t.Fatalf("handler.Now() = %d, want a real positive unix timestamp", got)
	}
}
