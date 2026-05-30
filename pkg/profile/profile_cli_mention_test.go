package profile

import "testing"

// TestCLISpec_NotifySlackInboundMentionOnly stubs the Wave 0 contract for POL-01.
// Plan 91-01 will implement the real test once CLISpec.NotifySlackInboundMentionOnly *bool
// is added to types.go — round-trip parse from YAML + JSON, assert nil/&true/&false distinguishable.
func TestCLISpec_NotifySlackInboundMentionOnly(t *testing.T) {
	t.Skip("TODO Plan 91-01: implement after CLISpec.NotifySlackInboundMentionOnly *bool field is added — round-trip parse from YAML + JSON, assert nil/&true/&false distinguishable")
}

// TestSchema_NotifySlackInboundMentionOnly stubs the Wave 0 contract for POL-02.
// Plan 91-01 will implement the real test once sandbox_profile.schema.json gains
// notifySlackInboundMentionOnly — assert valid bool accepted, non-bool rejected.
func TestSchema_NotifySlackInboundMentionOnly(t *testing.T) {
	t.Skip("TODO Plan 91-01: implement after sandbox_profile.schema.json gains notifySlackInboundMentionOnly — assert valid bool accepted, non-bool rejected")
}
