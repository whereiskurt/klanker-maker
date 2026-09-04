// Package secrets carries the wire contract and grant arithmetic shared by
// km-secretsd (the root broker) and km-env (the sandbox-side client).
//
// It is deliberately pure: no AWS, no syscalls, no I/O. That keeps it testable
// on a dev machine, which the Linux-only broker internals are not.
package secrets

// On-box paths. These are the contract between the compiler-rendered userdata
// and the two binaries; changing one without the others silently breaks boot.
const (
	// SocketPath is the broker's unix socket, 0660 root:sandbox.
	SocketPath = "/run/km/secrets.sock"

	// CiphertextPath is the SOPS bundle at rest, 0400 root. Only ciphertext
	// is ever stored; there is no plaintext file anywhere on the box.
	CiphertextPath = "/etc/sandbox-secrets.enc.yaml"

	// ShimDir holds the generated per-consumer shims and is prepended to PATH.
	ShimDir = "/opt/km/shims"

	// AuditPipePath is the km-audit-log sidecar's input FIFO (mode 0666).
	AuditPipePath = "/run/km/audit-pipe"
)

// DefaultConsumers is the consumer set when a profile declares no grants.
// These get shims and, absent grants, the whole bundle.
var DefaultConsumers = []string{"claude", "codex"}

// Grants maps a consumer name to the keys it may receive. A key is BOTH the
// binary name intercepted on PATH and the identity presented to the broker.
type Grants map[string][]string

// Op names the RPC. The zero value means OpUnseal, so the request km-env has
// always written keeps meaning exactly what it meant.
const (
	OpUnseal      = ""
	OpCredentials = "credentials"
)

// UnsealRequest is the client's ask. As is the claimed consumer identity;
// Only narrows further and can never widen.
type UnsealRequest struct {
	Op   string   `json:"op,omitempty"`
	As   string   `json:"as,omitempty"`
	Only []string `json:"only,omitempty"`
}

// Credentials is the credential_process contract (schema Version 1), returned by
// OpCredentials and printed verbatim by km-creds.
//
// The field names and their casing ARE the contract: km-creds marshals this
// struct straight to stdout, so there is exactly one shape and it cannot drift
// from what the AWS SDKs parse. A single wrong key and every SDK on the box
// silently falls back down the credential chain — and behind the fence there is
// nothing to fall back to.
type Credentials struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"` // RFC3339
}

// UnsealResponse carries the values, or an Error string when the request was
// refused. Values are []byte so the broker can zero them; see bundle.go.
type UnsealResponse struct {
	Keys   []string          `json:"keys,omitempty"`
	Values map[string][]byte `json:"values,omitempty"`
	Error  string            `json:"error,omitempty"`

	// Credentials answers OpCredentials, and is nil for every other RPC.
	Credentials *Credentials `json:"credentials,omitempty"`
}
