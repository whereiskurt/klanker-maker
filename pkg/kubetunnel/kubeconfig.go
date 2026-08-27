// Package kubetunnel resolves an operator's kubeconfig and brokers Kubernetes
// exec-credentials over a reverse tunnel into a sandbox (Phase 130, `km tunnel`).
//
// Everything here runs on the OPERATOR'S workstation, never on a sandbox. The
// kubeconfig is read at execution time rather than at `km create` time, because
// the machine holding it — the one with the VPN route to the cluster — need not
// be the machine that created the sandbox.
//
// The kubeconfig structs below are a deliberately minimal hand-rolled subset.
// k8s.io/client-go is an enormous dependency to take on for four structs, and
// nothing here needs its cluster-connection machinery: km only ever reads a
// server address, an SNI name, and an exec stanza it passes through untouched.
package kubetunnel

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExecEnv is one entry of a kubeconfig user's exec.env list.
type ExecEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ExecConfig is a kubeconfig user's exec stanza — the credential plugin kubectl
// would run locally. km carries it verbatim and hands it to the broker, which
// runs it unchanged. km never interprets, expands, or validates its contents:
// the plugin (kubelogin and friends) owns the OIDC flow, the token cache, and
// the browser hop, and second-guessing any of that here would just be a second
// place to be wrong about software this repo cannot exercise in CI.
type ExecConfig struct {
	APIVersion      string    `yaml:"apiVersion"`
	Command         string    `yaml:"command"`
	Args            []string  `yaml:"args,omitempty"`
	Env             []ExecEnv `yaml:"env,omitempty"`
	InteractiveMode string    `yaml:"interactiveMode,omitempty"`
}

type clusterSpec struct {
	Server        string `yaml:"server"`
	TLSServerName string `yaml:"tls-server-name,omitempty"`
	// CertificateAuthorityData is the cluster CA, already base64-encoded.
	CertificateAuthorityData string `yaml:"certificate-authority-data,omitempty"`
	// CertificateAuthority is a PATH to the CA on the OPERATOR'S filesystem.
	// It cannot be carried across as a path — see Resolve, which inlines it.
	CertificateAuthority string `yaml:"certificate-authority,omitempty"`
	// InsecureSkipTLSVerify is mirrored, never invented. km will not silently
	// stop verifying a certificate on the operator's behalf.
	InsecureSkipTLSVerify bool `yaml:"insecure-skip-tls-verify,omitempty"`
}

type namedCluster struct {
	Name    string      `yaml:"name"`
	Cluster clusterSpec `yaml:"cluster"`
}

type contextSpec struct {
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

type namedContext struct {
	Name    string      `yaml:"name"`
	Context contextSpec `yaml:"context"`
}

type userSpec struct {
	Exec *ExecConfig `yaml:"exec,omitempty"`
}

type namedUser struct {
	Name string   `yaml:"name"`
	User userSpec `yaml:"user"`
}

// Kubeconfig is the merged view of one or more kubeconfig files.
type Kubeconfig struct {
	Clusters []namedCluster `yaml:"clusters"`
	Contexts []namedContext `yaml:"contexts"`
	Users    []namedUser    `yaml:"users"`
}

// Target is everything `km tunnel` needs about the operator's cluster:
// where to dial it from the laptop, what name its certificate carries, and how
// to mint a credential for it.
type Target struct {
	// Context is the kubeconfig context name this was resolved from.
	Context string
	// ServerHost and ServerPort are the REAL cluster address, dialled on the
	// operator's side of the SSH connection (over their VPN). They become the
	// target half of the `-R <bind>:<host>:<port>` forward.
	ServerHost string
	ServerPort int
	// TLSServerName is the name the sandbox's kubectl must present as SNI and
	// verify the certificate against. It is never empty — see Resolve.
	TLSServerName string
	// CAData is the cluster CA in base64, as a kubeconfig carries it. Empty
	// means the box falls back to its system trust store, which works only for
	// a publicly-trusted certificate.
	//
	// This is the ONE piece of cluster material that must reach the box, and it
	// is safe to send: a CA certificate is public, verification-only, and mints
	// nothing. It does not weaken the phase's rule that no credential — no VPN
	// profile, no SSO refresh token, no AWS key — ever lands on a sandbox.
	CAData string
	// InsecureSkipTLSVerify mirrors the operator's own setting. km never sets
	// this on its own initiative.
	InsecureSkipTLSVerify bool
	// Exec is the operator's credential plugin, carried verbatim.
	Exec *ExecConfig
}

// KubeconfigPaths returns the kubeconfig files to read, in precedence order.
//
// An explicit override always wins. Otherwise $KUBECONFIG is honoured, split on
// the platform path-list separator exactly as kubectl does, with empty segments
// dropped. Falling back to ~/.kube/config last matches kubectl, so an operator
// whose kubectl works has a km tunnel that reads the same files.
func KubeconfigPaths(override string) []string {
	if override != "" {
		return []string{override}
	}

	if env := os.Getenv("KUBECONFIG"); env != "" {
		var paths []string
		for _, p := range strings.Split(env, string(os.PathListSeparator)) {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// No home directory is exotic enough that a relative path is a more
		// useful error later than a panic here.
		return []string{filepath.Join(".kube", "config")}
	}
	return []string{filepath.Join(home, ".kube", "config")}
}

// Load reads and merges the given kubeconfig files.
//
// Merge precedence is kubectl's: iterating in order, a name that is already
// present is NOT overwritten, so the FIRST file wins. The opposite would
// silently prefer whatever file happened to be listed last, which is how an
// operator ends up tunnelling to a stale cluster address and blaming the tunnel.
func Load(paths []string) (*Kubeconfig, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no kubeconfig path given")
	}

	merged := &Kubeconfig{}
	seenClusters := map[string]bool{}
	seenContexts := map[string]bool{}
	seenUsers := map[string]bool{}

	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path, by design
		if err != nil {
			return nil, fmt.Errorf("read kubeconfig %s: %w", path, err)
		}

		var kc Kubeconfig
		if err := yaml.Unmarshal(raw, &kc); err != nil {
			return nil, fmt.Errorf("parse kubeconfig %s: %w", path, err)
		}

		for _, c := range kc.Clusters {
			if !seenClusters[c.Name] {
				seenClusters[c.Name] = true
				merged.Clusters = append(merged.Clusters, c)
			}
		}
		for _, c := range kc.Contexts {
			if !seenContexts[c.Name] {
				seenContexts[c.Name] = true
				merged.Contexts = append(merged.Contexts, c)
			}
		}
		for _, u := range kc.Users {
			if !seenUsers[u.Name] {
				seenUsers[u.Name] = true
				merged.Users = append(merged.Users, u)
			}
		}
	}

	return merged, nil
}

// ContextNames lists every context in the merged config, for error messages.
func (k *Kubeconfig) ContextNames() []string {
	names := make([]string, 0, len(k.Contexts))
	for _, c := range k.Contexts {
		names = append(names, c.Name)
	}
	return names
}

// Resolve turns a context name into a Target.
//
// Note that current-context is deliberately ignored: `km tunnel` requires an
// explicit --context. Silently tunnelling whatever the operator last used is a
// bad failure mode when the whole point is that the sandbox cannot reach the
// cluster on its own — a wrong guess produces a confusing timeout rather than
// an obvious error.
//
// Every failure names the key that was missing, because these errors are read
// on a machine with a VPN and no debugger.
func (k *Kubeconfig) Resolve(contextName string) (*Target, error) {
	var ctx *contextSpec
	for i := range k.Contexts {
		if k.Contexts[i].Name == contextName {
			ctx = &k.Contexts[i].Context
			break
		}
	}
	if ctx == nil {
		return nil, fmt.Errorf("context %q not found in kubeconfig; available contexts: %s",
			contextName, strings.Join(k.ContextNames(), ", "))
	}

	var cl *clusterSpec
	for i := range k.Clusters {
		if k.Clusters[i].Name == ctx.Cluster {
			cl = &k.Clusters[i].Cluster
			break
		}
	}
	if cl == nil {
		return nil, fmt.Errorf("context %q names cluster %q, which is not defined in the kubeconfig",
			contextName, ctx.Cluster)
	}

	var usr *userSpec
	for i := range k.Users {
		if k.Users[i].Name == ctx.User {
			usr = &k.Users[i].User
			break
		}
	}
	if usr == nil {
		return nil, fmt.Errorf("context %q names user %q, which is not defined in the kubeconfig",
			contextName, ctx.User)
	}
	if usr.Exec == nil {
		return nil, fmt.Errorf("user %q has no exec stanza; km tunnel requires an exec-based (OIDC) user, "+
			"because the credential is minted on your workstation and proxied to the sandbox", ctx.User)
	}

	u, err := url.Parse(cl.Server)
	if err != nil {
		return nil, fmt.Errorf("cluster %q has an unparseable server URL %q: %w", ctx.Cluster, cl.Server, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("cluster %q server %q is not https; km tunnel forwards TLS to the cluster untouched "+
			"and cannot proxy a plaintext API server", ctx.Cluster, cl.Server)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("cluster %q server %q has no host", ctx.Cluster, cl.Server)
	}

	// The port must always be explicit downstream: the -R spec is
	// host:port and the box kubeconfig needs a concrete loopback port, so an
	// empty port here would render "host:" and fail obscurely at ssh time.
	port := 443
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("cluster %q server %q has a non-numeric port: %w", ctx.Cluster, cl.Server, err)
		}
	}

	// SNI falls back to the server hostname. This is what lets the sandbox's
	// kubeconfig point at 127.0.0.1 while still verifying the real
	// certificate — and is why km needs no /etc/hosts entry and no root on the
	// box. It must never be empty.
	sni := cl.TLSServerName
	if sni == "" {
		sni = host
	}

	// The box verifies the REAL certificate (that is what tls-server-name is
	// for), so it needs the cluster's CA. Almost every cluster worth tunnelling
	// to is fronted by an internal or EKS-managed CA that a stock sandbox
	// trust store has never heard of, so omitting this can only ever produce
	// "x509: certificate signed by unknown authority" on the box.
	//
	// A CA given as a FILE PATH must be read here, on the machine where that
	// path exists, and inlined as data. Copying the path across would dangle.
	caData := cl.CertificateAuthorityData
	if caData == "" && cl.CertificateAuthority != "" {
		pem, err := os.ReadFile(cl.CertificateAuthority) //nolint:gosec // operator's own kubeconfig, by design
		if err != nil {
			return nil, fmt.Errorf("cluster %q names certificate-authority %q, which could not be read: %w",
				ctx.Cluster, cl.CertificateAuthority, err)
		}
		caData = base64.StdEncoding.EncodeToString(pem)
	}

	return &Target{
		Context:               contextName,
		ServerHost:            host,
		ServerPort:            port,
		TLSServerName:         sni,
		CAData:                caData,
		InsecureSkipTLSVerify: cl.InsecureSkipTLSVerify,
		Exec:                  usr.Exec,
	}, nil
}
