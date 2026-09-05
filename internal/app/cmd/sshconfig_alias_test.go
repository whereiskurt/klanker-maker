// Tests for the alias+id Host name pair written into ~/.ssh/config.
//
// ssh(5) allows several patterns on one Host line and matches the first block
// whose list contains the requested name, so km writes `Host km-<alias>
// km-<sandbox-id>`: the alias is what an operator types, the id keeps every
// pre-existing reference (saved VS Code workspaces, scripts, docs) resolving.
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func TestSandboxHostNames(t *testing.T) {
	cases := []struct {
		name    string
		alias   string
		sandbox string
		want    []string
	}{
		{"alias present — friendly first, id last", "herdrbox", "herdr-cacc8426",
			[]string{"km-herdrbox", "km-herdr-cacc8426"}},
		{"no alias — id alone", "", "herdr-cacc8426",
			[]string{"km-herdr-cacc8426"}},
		{"blank alias — id alone", "   ", "herdr-cacc8426",
			[]string{"km-herdr-cacc8426"}},
		// A duplicated pattern on one Host line is legal but pointless, and it
		// would make the "last name is the id" rule ambiguous.
		{"alias equals id — not duplicated", "herdr-cacc8426", "herdr-cacc8426",
			[]string{"km-herdr-cacc8426"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sandboxHostNames(tc.alias, tc.sandbox)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			// The id MUST be last — km doctor's stale sweep reads the last
			// name as the sandbox id, and that sweep deletes keypairs.
			if got[len(got)-1] != "km-"+tc.sandbox {
				t.Errorf("last name must be the id form, got %q", got[len(got)-1])
			}
		})
	}
}

func TestUpsertHost_WritesBothNamesOnOneHostLine(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	names := sandboxHostNames("herdrbox", "herdr-cacc8426")
	if err := UpsertHost(cfgPath, names, HostOptions{HostName: "localhost", Port: 2224, User: "sandbox", IdentityFile: "/k"}); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), "Host km-herdrbox km-herdr-cacc8426\n") {
		t.Fatalf("expected both names on one Host line, got:\n%s", b)
	}
}

// Reusing an alias for a freshly created sandbox must REPLACE the old block.
// Appending a second block claiming km-herdrbox would leave ssh taking the
// first match — the destroyed sandbox — with no error anywhere.
func TestUpsertHost_AliasReuseReplacesRatherThanDuplicates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	opts := HostOptions{HostName: "localhost", Port: 2224, User: "sandbox", IdentityFile: "/k"}

	if err := UpsertHost(cfgPath, sandboxHostNames("herdrbox", "herdr-OLD"), opts); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := UpsertHost(cfgPath, sandboxHostNames("herdrbox", "herdr-NEW"), opts); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got := string(mustRead(t, cfgPath))
	if n := strings.Count(got, "km-herdrbox"); n != 1 {
		t.Fatalf("alias must appear exactly once, appeared %d times:\n%s", n, got)
	}
	if strings.Contains(got, "herdr-OLD") {
		t.Fatalf("stale sandbox id survived the alias reuse:\n%s", got)
	}
	if !strings.Contains(got, "km-herdr-NEW") {
		t.Fatalf("new sandbox id missing:\n%s", got)
	}
}

// destroy.go and km doctor both remove by the km-<sandbox-id> form. That must
// drop the whole block, including its alias name.
func TestRemoveHost_ByIDDropsTheAliasNameToo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	opts := HostOptions{HostName: "localhost", Port: 2224, User: "sandbox", IdentityFile: "/k"}
	if err := UpsertHost(cfgPath, sandboxHostNames("herdrbox", "herdr-cacc8426"), opts); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := RemoveHost(cfgPath, "km-herdr-cacc8426"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	got := string(mustRead(t, cfgPath))
	if strings.Contains(got, "km-herdrbox") {
		t.Fatalf("alias name survived removal by id:\n%s", got)
	}
}

// THE GUARD THAT MATTERS.
//
// readManagedAliases feeds km doctor's stale sweep, which compares its output
// against live sandbox IDs and, under --delete-ssh, deletes the ssh entry AND
// the keypair. If it read the alias name instead of the id, every live
// alias-named box would look stale and get deleted.
func TestReadManagedAliases_ReadsTheIDNotTheAlias(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	opts := HostOptions{HostName: "localhost", Port: 2224, User: "sandbox", IdentityFile: "/k"}
	if err := UpsertHost(cfgPath, sandboxHostNames("herdrbox", "herdr-cacc8426"), opts); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := readManagedAliases(cfgPath)
	if err != nil {
		t.Fatalf("readManagedAliases: %v", err)
	}
	if !got["herdr-cacc8426"] {
		t.Errorf("sandbox id not claimed: %v", got)
	}
	if got["herdrbox"] {
		t.Errorf("alias was mistaken for a sandbox id — doctor would report a live box stale: %v", got)
	}
	if len(got) != 1 {
		t.Errorf("expected exactly the sandbox id, got %v", got)
	}
}

// End-to-end: a live sandbox reached through its alias-named Host block must
// not be reported stale.
func TestCheckStaleSSHConfig_AliasNamedBlockOfLiveSandboxIsNotStale(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	opts := HostOptions{HostName: "localhost", Port: 2224, User: "sandbox", IdentityFile: "/k"}
	if err := UpsertHost(cfgPath, sandboxHostNames("herdrbox", "herdr-cacc8426"), opts); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	keysDir := makeKeyFiles(t, dir, []string{"herdr-cacc8426"})
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "herdr-cacc8426", Alias: "herdrbox"},
	}}
	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, lister, true, false)
	if r.Status != CheckOK {
		t.Fatalf("live alias-named block reported stale: %s: %s", r.Status, r.Message)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
