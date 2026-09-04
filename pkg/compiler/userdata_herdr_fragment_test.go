package compiler

import (
	"strings"
	"testing"
)

// herdrFetchLine is the initCommand the base/tools/herdr fragment appends.
const herdrFetchLine = `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr" /usr/local/bin/herdr`

// profileDumpSection returns the body of the Phase 113 /opt/km/.km-profile.yaml
// heredoc — the yaml.Marshal of userDataParams that userdata.go renders via
// {{ .ProfileYAML }}. Returns ok=false if the block is absent.
func profileDumpSection(ud string) (string, bool) {
	const open = "cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'\n"
	i := strings.Index(ud, open)
	if i < 0 {
		return "", false
	}
	rest := ud[i+len(open):]
	j := strings.Index(rest, "\nKM_PROFILE_EOF")
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// TestInitCommandsAppend_DeltaConfinedToProfileDump is the guard the herdr
// fragment's deploy story rests on.
//
// Adding an initCommand DOES change rendered userdata — Phase 113 sets
// params.ProfileYAML to a yaml.Marshal of the whole userDataParams struct
// (userdata.go:7070) and renders it verbatim into the /opt/km/.km-profile.yaml
// heredoc. Because that happens by reflection, no template token names
// InitCommands and grepping for it cannot find the reference. An earlier
// version of this test asserted the two renders were byte-identical; that was
// false and it failed, which is how the interaction was found.
//
// What actually matters is narrower and stronger: the delta must be CONFINED to
// that profile dump, so no executable bootstrap logic changes. If a future edit
// ever leaks initCommands into runnable script, this test fails.
func TestInitCommandsAppend_DeltaConfinedToProfileDump(t *testing.T) {
	base := baseProfile()
	base.Spec.Execution.InitCommands = []string{"echo one"}

	withHerdr := baseProfile()
	withHerdr.Spec.Execution.InitCommands = []string{"echo one", herdrFetchLine}

	a, err := generateUserData(base, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	b, err := generateUserData(withHerdr, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render with herdr: %v", err)
	}

	dumpA, okA := profileDumpSection(a)
	dumpB, okB := profileDumpSection(b)
	if !okA || !okB {
		t.Fatal("the Phase 113 /opt/km/.km-profile.yaml block is absent from rendered userdata; " +
			"this test would otherwise pass vacuously")
	}

	// The fetch line must appear in the dump...
	if !strings.Contains(dumpB, "binaries/herdr") {
		t.Errorf("expected the herdr fetch line inside the profile dump; it was not there")
	}
	// ...and NOWHERE else in the rendered bootstrap.
	outsideB := strings.Replace(b, dumpB, "", 1)
	if strings.Contains(outsideB, "binaries/herdr") {
		t.Errorf("the herdr initCommand leaked OUTSIDE the profile dump into executable bootstrap")
	}

	// The decisive assertion: strip each render's dump and the remaining
	// bootstrap must be byte-identical.
	outsideA := strings.Replace(a, dumpA, "", 1)
	if outsideA != outsideB {
		t.Fatalf("adding an initCommand changed executable bootstrap outside the Phase 113 " +
			"profile dump; the fragment's no-create-handler-rebuild claim needs re-examining")
	}
}

// TestInitCommandsGate_EmptyToNonEmptyDoesChangeUserdata is the negative
// control. It proves the test above measures something real: going from zero
// commands to one flips the {{- if or .InitCommands .InitScripts }} presence
// gate and adds the km-init.sh download block to executable bootstrap.
func TestInitCommandsGate_EmptyToNonEmptyDoesChangeUserdata(t *testing.T) {
	none := baseProfile()
	none.Spec.Execution.InitCommands = nil

	one := baseProfile()
	one.Spec.Execution.InitCommands = []string{"echo one"}

	a, err := generateUserData(none, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render none: %v", err)
	}
	b, err := generateUserData(one, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render one: %v", err)
	}
	if a == b {
		t.Fatal("expected the InitCommands presence gate to change userdata; it did not")
	}
}
