// Package cmd — stale operator-side VS Code Remote-SSH state detection
// for `km doctor`.
//
// km vscode start writes two operator-local artifacts per sandbox:
//
//   - A "Host km-{sandbox-id}" block inside the managed marker section of
//     ~/.ssh/config (added by UpsertHost in sshconfig.go).
//   - A keypair at ~/.km/keys/{sandbox-id} + ~/.km/keys/{sandbox-id}.pub.
//
// km destroy normally cleans them up via cleanupVSCodeState (destroy.go),
// but only on the local destroy path. The default --remote destroy
// publishes an EventBridge event that the management Lambda handles
// cloud-side, and the Lambda has no access to the operator's filesystem.
// TTL- and budget-driven destroys hit the same cloud-side path. Cross-
// machine usage (km vscode start on laptop A, km destroy on laptop B)
// also leaves laptop A's state behind.
//
// checkStaleSSHConfig surfaces both kinds of orphan and, with the
// --delete-ssh opt-in, removes them. SSH config entries are local-only
// (no AWS-side cost or race), so the explicit-opt-in is mainly for
// consistency with --delete-ebs/--delete-sqs/--delete-s3/--delete-lambdas.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checkStaleSSHConfig flags ~/.ssh/config managed-block entries and
// ~/.km/keys/* keypairs whose sandbox-id has no matching DynamoDB record.
// Deletion is gated on dryRun==false AND deleteSSH==true.
//
// configPath is the absolute path to ~/.ssh/config; keysDir is the
// absolute path to ~/.km/keys/. Tests inject t.TempDir() paths.
func checkStaleSSHConfig(
	ctx context.Context,
	configPath string,
	keysDir string,
	lister SandboxLister,
	dryRun bool,
	deleteSSH bool,
) CheckResult {
	name := "Stale VS Code SSH state"

	// Parse managed-block aliases from ssh config.
	aliasSet, parseErr := readManagedAliases(configPath)
	if parseErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("read ssh config %s: %v", configPath, parseErr)}
	}

	// Enumerate per-sandbox key files.
	keySet, keyErr := readKeyFileSandboxIDs(keysDir)
	if keyErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("scan keys dir %s: %v", keysDir, keyErr)}
	}

	// Union of every sandbox-id referenced by either source.
	claimed := make(map[string]bool, len(aliasSet)+len(keySet))
	for sid := range aliasSet {
		claimed[sid] = true
	}
	for sid := range keySet {
		claimed[sid] = true
	}
	if len(claimed) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no operator-side VS Code state to inspect"}
	}

	// Live sandbox set from DDB. lister is allowed to fail soft — an empty
	// set on error means everything looks stale, which is the right WARN
	// in a credentials-broken state (we don't accidentally delete in
	// dry-run anyway, and the destructive path explicitly uses the empty
	// set as "delete all").
	activeSandboxes := make(map[string]bool)
	if lister != nil {
		records, err := lister.ListSandboxes(ctx, false)
		if err == nil {
			for _, r := range records {
				activeSandboxes[r.SandboxID] = true
			}
		}
	}

	var stale []string
	for sid := range claimed {
		if !activeSandboxes[sid] {
			stale = append(stale, sid)
		}
	}
	if len(stale) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d operator-side sandbox(es) on disk, all active", len(claimed)),
		}
	}
	sort.Strings(stale)

	// Build a per-sandbox plan describing what would be (or was) removed.
	// Used both in dry-run output and as the action list when deleting.
	type plan struct {
		sandboxID string
		hasAlias  bool
		hasKey    bool
		hasKeyPub bool
	}
	plans := make([]plan, 0, len(stale))
	for _, sid := range stale {
		p := plan{sandboxID: sid}
		if aliasSet[sid] {
			p.hasAlias = true
		}
		if _, err := os.Stat(filepath.Join(keysDir, sid)); err == nil {
			p.hasKey = true
		}
		if _, err := os.Stat(filepath.Join(keysDir, sid+".pub")); err == nil {
			p.hasKeyPub = true
		}
		plans = append(plans, p)
	}

	// Report-only path: dryRun OR opt-in missing.
	if dryRun || !deleteSSH {
		var sb strings.Builder
		fmt.Fprintf(&sb, "found %d stale operator-side sandbox(es):", len(plans))
		for _, p := range plans {
			fmt.Fprintf(&sb, "\n  %s%s", p.sandboxID, planArtifacts(p.hasAlias, p.hasKey, p.hasKeyPub))
		}
		remediation := "Re-run with --dry-run=false --delete-ssh to remove the orphan ssh-config entries and ~/.km/keys/ files"
		if !dryRun && !deleteSSH {
			remediation = "Add --delete-ssh to also remove the orphan ssh-config entries and ~/.km/keys/ files"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     sb.String(),
			Remediation: remediation,
		}
	}

	// Destructive path. Per-sandbox failures don't abort the loop.
	cleaned, failed := 0, 0
	failures := make(map[string]error)
	for _, p := range plans {
		var firstErr error
		if p.hasAlias {
			if err := RemoveHost(configPath, "km-"+p.sandboxID); err != nil {
				firstErr = err
			}
		}
		if p.hasKey {
			if err := os.Remove(filepath.Join(keysDir, p.sandboxID)); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
		}
		if p.hasKeyPub {
			if err := os.Remove(filepath.Join(keysDir, p.sandboxID+".pub")); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			failed++
			failures[p.sandboxID] = firstErr
			continue
		}
		cleaned++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "found %d stale operator-side sandbox(es); %d cleaned, %d failed:", len(plans), cleaned, failed)
	for _, p := range plans {
		marker := " [removed]"
		if e, present := failures[p.sandboxID]; present {
			marker = fmt.Sprintf(" [remove failed: %v]", e)
		}
		fmt.Fprintf(&sb, "\n  %s%s%s", p.sandboxID, planArtifacts(p.hasAlias, p.hasKey, p.hasKeyPub), marker)
	}
	remediation := ""
	if failed > 0 {
		remediation = "Re-run after resolving the listed failures (typically permissions on ~/.ssh/config or ~/.km/keys)."
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: remediation,
	}
}

// readManagedAliases parses configPath, extracts aliases inside the
// managed-block markers, and returns the set of sandbox IDs referenced
// by km-{sandbox-id} aliases. Non-km-* aliases inside the block are
// ignored (operator-edited content) — the caller never touches them.
// Missing file is treated as "empty set, no error".
func readManagedAliases(configPath string) (map[string]bool, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	_, inside, _ := managedSections(content)
	if inside == nil {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool)
	for _, b := range parseHostBlocks(inside) {
		if sid, ok := strings.CutPrefix(b.alias, "km-"); ok && sid != "" {
			out[sid] = true
		}
	}
	return out, nil
}

// readKeyFileSandboxIDs returns the set of sandbox IDs implied by
// filenames in keysDir. A pair (sb-x, sb-x.pub) yields a single entry
// "sb-x". Missing keysDir is treated as "empty set, no error" — operators
// who never used km vscode start won't have the dir.
func readKeyFileSandboxIDs(keysDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if sid, ok := strings.CutSuffix(name, ".pub"); ok {
			out[sid] = true
			continue
		}
		out[name] = true
	}
	return out, nil
}

// planArtifacts renders a "(ssh+key+pub)" / "(ssh)" / etc. suffix for the
// per-sandbox row in the check's output.
func planArtifacts(hasAlias, hasKey, hasKeyPub bool) string {
	var parts []string
	if hasAlias {
		parts = append(parts, "ssh-config")
	}
	if hasKey {
		parts = append(parts, "key")
	}
	if hasKeyPub {
		parts = append(parts, "pub")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "+") + ")"
}
