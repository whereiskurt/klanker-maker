package sshkey

import "errors"

// GenerateAndWrite generates an ed25519 keypair and writes:
//   - privPath: OpenSSH private key PEM (-----BEGIN OPENSSH PRIVATE KEY-----), mode 0600
//   - pubPath:  single-line authorized_keys format with comment, mode 0644
//
// Returns the pubkey line WITHOUT a trailing newline (for embedding in userdata).
// Overwrites existing files. Creates parent directories (mode 0700) as needed.
//
// Phase 73 Wave 1 Plan 73-01 implements; this is a Wave 0 compile-only stub.
func GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error) {
	return "", errors.New("sshkey: not implemented (Wave 1 Plan 73-01)")
}
