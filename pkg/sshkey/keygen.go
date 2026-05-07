package sshkey

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	gossh "golang.org/x/crypto/ssh"
)

// GenerateAndWrite generates an ed25519 keypair and writes:
//   - privPath: OpenSSH private key PEM (-----BEGIN OPENSSH PRIVATE KEY-----), mode 0600
//   - pubPath:  single-line authorized_keys entry "ssh-ed25519 <base64> <comment>\n", mode 0644
//
// Returns the pubkey line WITHOUT trailing newline for embedding into userdata templates.
// Overwrites existing files. Creates the parent directory of privPath with mode 0700 if absent.
//
// The comment is included in both the OpenSSH PEM (via MarshalPrivateKey) and the public-key
// line (manually constructed because gossh.MarshalAuthorizedKey drops the comment field).
func GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error) {
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return "", fmt.Errorf("sshkey: create key directory: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		return "", fmt.Errorf("sshkey: generate ed25519 key: %w", err)
	}

	privPEM, err := gossh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", fmt.Errorf("sshkey: marshal private key: %w", err)
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(privPEM), 0o600); err != nil {
		return "", fmt.Errorf("sshkey: write private key: %w", err)
	}

	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("sshkey: new public key: %w", err)
	}
	pubLine := fmt.Sprintf("ssh-ed25519 %s %s",
		base64.StdEncoding.EncodeToString(sshPub.Marshal()),
		comment)
	if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("sshkey: write public key: %w", err)
	}

	return pubLine, nil
}
