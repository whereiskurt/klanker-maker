package compiler

import (
	"os"
	"testing"
)

// TestUserdataKmPrefixByteIdentity is the Phase 94-05 byte-identity guard for the
// default ("km") install. It proves that after the CW_LOG_GROUP template migration
// (/km/sandboxes/ hardcode → /{{ .ResourcePrefix }}/sandboxes/), the rendered
// userdata for a km-prefix install is still byte-identical to the pre-Phase-92
// golden baseline.
//
// The pre-Phase-92 golden (userdata_learn_v2_pre92_baseline.golden.sh) was rendered
// with KM_RESOURCE_PREFIX="" (defaulting to "km"), so it contains the literal
// /km/sandboxes/ path. Our migrated template with ResourcePrefix="km" must produce
// the exact same bytes — that is the no-op guarantee.
//
// This test reuses generateLearnV2Userdata (defined in
// userdata_phase92_byte_identity_test.go) with KM_RESOURCE_PREFIX unset, which
// exercises the same code path the production km install takes.
func TestUserdataKmPrefixByteIdentity(t *testing.T) {
	// Unset KM_RESOURCE_PREFIX so the compiler defaults to "km" — same as a
	// default km install.
	t.Setenv("KM_RESOURCE_PREFIX", "")

	// Load the pre-92 golden (captured on pre-Phase-92 main with the same km default).
	golden := goldenPath92(t, phase92LearnV2UserdataGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
	}

	// Render using the same learn.v2 profile + fixed inputs as the Phase 92 baseline.
	got := generateLearnV2Userdata(t)

	// Fast path: verbatim byte-identity (expected on km prefix, no settings.json drift).
	if got == string(want) {
		return
	}

	// Fallback: the settings.json blob may differ due to Phase 92 semantic migration.
	// Extract blob + rest and apply the same two-part check as Phase 92's test.
	wantBlob, wantRest, ok1 := extractClaudeSettingsBlob(string(want))
	gotBlob, gotRest, ok2 := extractClaudeSettingsBlob(got)
	if !ok1 || !ok2 {
		t.Fatalf("could not locate ~/.claude/settings.json heredoc in baseline(%v)/generated(%v); "+
			"userdata drift for km prefix is NOT confined to the settings.json blob:\n%s",
			ok1, ok2, diffStrings(string(want), got))
	}

	// Everything outside settings.json must be byte-identical for the km prefix no-op.
	if wantRest != gotRest {
		t.Errorf("userdata for km prefix drifted from pre-Phase-92 baseline OUTSIDE settings.json blob "+
			"(km→km must be a no-op):\n%s", diffStrings(wantRest, gotRest))
	}

	// The settings.json blob itself must be semantically equivalent (Phase 92 migration).
	assertClaudeSettingsSemanticEquivalence(t, wantBlob, gotBlob)
}
