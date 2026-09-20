package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
)

// Shared sandbox access credentials — the per-laptop half.
//
// ~/.km/keys/<id> (ed25519 private key) and ~/.km/desktop/<id> ("user:pass")
// used to exist only on the laptop that ran km create. They are now a CACHE
// of the SSM parameters under /{prefix}/access/<id>/ (pkg/aws/access.go):
// every km vscode|desktop|herdr|tunnel start reconciles the local file to SSM
// first, so any operator of the install can hop into any sandbox, and a rekey
// by one analyst reaches the others on their next start. SSM is the truth;
// "start" never invalidates a credential — only "rekey" does.
//
// One shared credential per sandbox, no per-analyst identity — decided with
// the operator: anyone who can run `start` already holds operator creds and
// therefore `km shell --root`, so the key is a convenience, not a boundary.
//
// Design: docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md

// sharedCredStore is the SSM-backed store. A nil *sharedCredStore means "no
// store available" and every function here degrades to the pre-existing
// local-file-only behaviour — that is what the test binary uses.
type sharedCredStore struct {
	ssm    kmaws.IdentitySSMAPI
	prefix string
	kmsKey string
}

// NewSharedCredStoreFunc builds the store. Seam: TestMain replaces it with a
// func returning (nil, nil). A non-nil error is reported by `start` callers
// as a WARN and treated as "no store" — the credential is a convenience, and
// an SSO or network hiccup must never stop a laptop that worked yesterday.
var NewSharedCredStoreFunc = func(ctx context.Context, cfg *config.Config) (*sharedCredStore, error) {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, err
	}
	// Same fallback create.go uses for the safe-phrase parameter: the resolved
	// platform key ARN when km init exported it, else the alias (SSM accepts
	// an alias name as KeyId).
	kmsKey := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
	if kmsKey == "" {
		kmsKey = cfg.GetPlatformKMSAlias()
	}
	return &sharedCredStore{ssm: ssm.NewFromConfig(awsCfg), prefix: cfg.GetResourcePrefix(), kmsKey: kmsKey}, nil
}

// credKind describes one of the two credentials: where it lives in SSM, where
// it lives locally, how to compare two copies, what to do after a pull, and
// the operator-facing words.
type credKind struct {
	name      string                                       // "key" / "desktop credential" — used in messages
	param     func(prefix, sandboxID string) string        // SSM path
	localRel  func(sandboxID string) string                // path under $HOME
	rekeyVerb string                                       // "km vscode rekey" / "km desktop rekey"
	legacyErr func(sandboxID, localPath string) error      // pre-existing "not found" error, kept verbatim for a nil store
	normalize func(b []byte) string                        // equality basis
	afterPull func(localPath string, content []byte) error // side files (the .pub)
}

var sshKeyKind = credKind{
	name:      "key",
	param:     kmaws.SSHKeyPath,
	localRel:  func(id string) string { return filepath.Join(".km", "keys", id) },
	rekeyVerb: "km vscode rekey",
	legacyErr: func(id, p string) error {
		return fmt.Errorf("private key for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/keys/%s* files over", id, p, id)
	},
	normalize: func(b []byte) string { return strings.TrimRight(string(b), "\n") },
	afterPull: func(localPath string, content []byte) error {
		// pubkeyFingerprint and km doctor's stale-keypair sweep read the .pub;
		// the comment matches what km create wrote so fingerprints line up.
		id := filepath.Base(localPath)
		line, err := sshkey.PublicKeyLine(content, "km-"+id)
		if err != nil {
			return fmt.Errorf("derive public key: %w", err)
		}
		return os.WriteFile(localPath+".pub", []byte(line+"\n"), 0o644)
	},
}

var desktopCredKind = credKind{
	name:      "desktop credential",
	param:     kmaws.DesktopCredPath,
	localRel:  func(id string) string { return filepath.Join(".km", "desktop", id) },
	rekeyVerb: "km desktop rekey",
	legacyErr: func(id, p string) error {
		return fmt.Errorf("desktop credential for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/desktop/%s file over", id, p, id)
	},
	normalize: func(b []byte) string { return strings.TrimSpace(string(b)) },
	afterPull: func(string, []byte) error { return nil },
}

// localCredPath is $HOME-relative resolution for one kind.
func localCredPath(kind credKind, sandboxID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, kind.localRel(sandboxID)), nil
}

// writeLocalCredential writes content atomically (dir 0700, file 0600, .new +
// rename) and runs the kind's afterPull.
func writeLocalCredential(kind credKind, localPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(localPath), err)
	}
	tmp := localPath + ".new"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, localPath); err != nil {
		return fmt.Errorf("commit %s: %w", localPath, err)
	}
	return kind.afterPull(localPath, content)
}

// syncSharedCredential reconciles the local file with SSM and returns the
// local path to use. The table (spec §4.1):
//
//	SSM      local    action
//	present  absent   pull        "✓ Pulled shared <kind> from SSM"
//	present  differs  overwrite   "✓ Local <kind> refreshed from SSM (rekeyed elsewhere)"
//	present  same     nothing
//	absent   present  publish     "✓ Published existing <kind> to SSM"   (publish failure = warn)
//	absent   absent   error naming <rekeyVerb> <id>
//	error    present  warn, use local (fail open)
//	error    absent   error
//
// A nil store is the legacy path: local file or the pre-existing error.
func syncSharedCredential(ctx context.Context, store *sharedCredStore, kind credKind, sandboxID string, w io.Writer) (string, error) {
	localPath, err := localCredPath(kind, sandboxID)
	if err != nil {
		return "", err
	}
	localBytes, readErr := os.ReadFile(localPath)
	localPresent := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("read local %s: %w", kind.name, readErr)
	}

	if store == nil {
		if !localPresent {
			return "", kind.legacyErr(sandboxID, localPath)
		}
		return localPath, nil
	}

	path := kind.param(store.prefix, sandboxID)
	remote, found, getErr := kmaws.GetAccessParam(ctx, store.ssm, path)
	switch {
	case getErr != nil && localPresent:
		fmt.Fprintf(w, "  [warn] could not read shared %s from SSM (%v); using local copy\n", kind.name, getErr)
		return localPath, nil
	case getErr != nil:
		return "", fmt.Errorf("read shared %s for %s from SSM: %w", kind.name, sandboxID, getErr)
	case found && !localPresent:
		if err := writeLocalCredential(kind, localPath, []byte(remote)); err != nil {
			return "", err
		}
		fmt.Fprintf(w, "✓ Pulled shared %s from SSM\n", kind.name)
	case found && kind.normalize(localBytes) != kind.normalize([]byte(remote)):
		if err := writeLocalCredential(kind, localPath, []byte(remote)); err != nil {
			return "", err
		}
		fmt.Fprintf(w, "✓ Local %s refreshed from SSM (rekeyed elsewhere)\n", kind.name)
	case found:
		// identical — silent
	case localPresent:
		// Backfill: this laptop holds a credential SSM has never seen (a
		// sandbox created before shared credentials shipped). Publish so the
		// next analyst can get in. Failure is a warning — the local copy works.
		if err := kmaws.PutAccessParam(ctx, store.ssm, path, string(localBytes), store.kmsKey); err != nil {
			fmt.Fprintf(w, "  [warn] could not publish %s to SSM (%v); other analysts cannot connect until this succeeds\n", kind.name, err)
		} else {
			fmt.Fprintf(w, "✓ Published existing %s to SSM\n", kind.name)
		}
	default:
		return "", fmt.Errorf("no shared %s for %s in SSM and none at %s — run: %s %s", kind.name, sandboxID, localPath, kind.rekeyVerb, sandboxID)
	}
	return localPath, nil
}

// publishSharedCredential writes content to SSM verbatim for one kind. Used by
// rekey (after box + local are committed) and by create (after generation). A
// nil store (test binary) is a no-op; a store-construction error is returned
// so the caller can decide (create warns, rekey errors).
func publishSharedCredential(ctx context.Context, cfg *config.Config, kind credKind, sandboxID string, content []byte) error {
	store, err := NewSharedCredStoreFunc(ctx, cfg)
	if err != nil {
		return fmt.Errorf("shared credential store: %w", err)
	}
	if store == nil {
		return nil
	}
	return kmaws.PutAccessParam(ctx, store.ssm, kind.param(store.prefix, sandboxID), string(content), store.kmsKey)
}
