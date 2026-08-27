package kubetunnel

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubeconfigPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name     string
		override string
		env      string
		envSet   bool
		want     []string
	}{
		{
			name:     "override wins over KUBECONFIG",
			override: "/tmp/explicit",
			env:      "/a" + string(os.PathListSeparator) + "/b",
			envSet:   true,
			want:     []string{"/tmp/explicit"},
		},
		{
			name:   "KUBECONFIG splits on the path list separator",
			env:    "/a" + string(os.PathListSeparator) + "/b",
			envSet: true,
			want:   []string{"/a", "/b"},
		},
		{
			name:   "KUBECONFIG drops empty segments",
			env:    "/a" + string(os.PathListSeparator) + string(os.PathListSeparator) + "/b",
			envSet: true,
			want:   []string{"/a", "/b"},
		},
		{
			name:   "unset KUBECONFIG falls back to ~/.kube/config",
			envSet: false,
			want:   []string{filepath.Join(home, ".kube", "config")},
		},
		{
			name:   "empty KUBECONFIG falls back to ~/.kube/config",
			env:    "",
			envSet: true,
			want:   []string{filepath.Join(home, ".kube", "config")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv("KUBECONFIG", tc.env)
			} else {
				os.Unsetenv("KUBECONFIG")
			}
			got := KubeconfigPaths(tc.override)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("path[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLoad_MergeFirstFileWins(t *testing.T) {
	k, err := Load([]string{"testdata/simple.yaml", "testdata/second-file.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// simple.yaml defines cluster k8s1 first; second-file.yaml redefines it.
	// kubectl's documented precedence is first-file-wins.
	tgt, err := k.Resolve("k8s1")
	if err != nil {
		t.Fatalf("Resolve(k8s1): %v", err)
	}
	if tgt.ServerHost != "k8s1.corp" {
		t.Errorf("ServerHost = %q, want k8s1.corp (first file must win)", tgt.ServerHost)
	}
	if tgt.ServerPort != 6443 {
		t.Errorf("ServerPort = %d, want 6443 (first file must win)", tgt.ServerPort)
	}

	// The second file's unique context must still be reachable.
	if _, err := k.Resolve("k8s2"); err != nil {
		t.Errorf("Resolve(k8s2) from second file: %v", err)
	}
}

func TestLoad_Errors(t *testing.T) {
	t.Run("missing file names the path", func(t *testing.T) {
		_, err := Load([]string{"testdata/does-not-exist.yaml"})
		if err == nil {
			t.Fatal("expected an error for a missing kubeconfig")
		}
		if !strings.Contains(err.Error(), "does-not-exist.yaml") {
			t.Errorf("error should name the path, got: %v", err)
		}
	})

	t.Run("malformed YAML names the file", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "broken.yaml")
		if err := os.WriteFile(bad, []byte("clusters: [oh no\n  : :"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := Load([]string{bad})
		if err == nil {
			t.Fatal("expected an error for malformed YAML")
		}
		if !strings.Contains(err.Error(), "broken.yaml") {
			t.Errorf("error should name the file, got: %v", err)
		}
	})

	t.Run("no readable files at all", func(t *testing.T) {
		if _, err := Load(nil); err == nil {
			t.Fatal("expected an error when given no paths")
		}
	})
}

func TestResolve_Simple(t *testing.T) {
	k, err := Load([]string{"testdata/simple.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tgt, err := k.Resolve("k8s1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if tgt.Context != "k8s1" {
		t.Errorf("Context = %q, want k8s1", tgt.Context)
	}
	if tgt.ServerHost != "k8s1.corp" {
		t.Errorf("ServerHost = %q, want k8s1.corp", tgt.ServerHost)
	}
	if tgt.ServerPort != 6443 {
		t.Errorf("ServerPort = %d, want 6443", tgt.ServerPort)
	}
	if tgt.TLSServerName != "k8s1.corp" {
		t.Errorf("TLSServerName = %q, want k8s1.corp", tgt.TLSServerName)
	}
	if tgt.Exec == nil {
		t.Fatal("Exec is nil, want the fixture's exec stanza")
	}
	if tgt.Exec.Command != "kubectl" {
		t.Errorf("Exec.Command = %q, want kubectl", tgt.Exec.Command)
	}
}

func TestResolve_SNIFallbackAndPortDefault(t *testing.T) {
	k, err := Load([]string{"testdata/no-tls-server-name.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	t.Run("absent tls-server-name falls back to the server hostname", func(t *testing.T) {
		tgt, err := k.Resolve("nosni")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tgt.TLSServerName != "k8s1.corp" {
			t.Errorf("TLSServerName = %q, want the server hostname k8s1.corp", tgt.TLSServerName)
		}
		if tgt.TLSServerName == "" {
			t.Error("TLSServerName must never be empty — the box kubeconfig needs an SNI name")
		}
	})

	t.Run("absent port defaults to 443", func(t *testing.T) {
		tgt, err := k.Resolve("noport")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tgt.ServerPort != 443 {
			t.Errorf("ServerPort = %d, want 443", tgt.ServerPort)
		}
		if tgt.ServerHost != "k8s1.corp" {
			t.Errorf("ServerHost = %q, want k8s1.corp", tgt.ServerHost)
		}
	})
}

func TestResolve_Errors(t *testing.T) {
	k, err := Load([]string{"testdata/no-tls-server-name.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name        string
		context     string
		wantSubstrs []string
	}{
		{
			name:        "unknown context lists what is available",
			context:     "nope",
			wantSubstrs: []string{"nope", "nosni"},
		},
		{
			name:        "context naming a missing cluster names the cluster",
			context:     "missing-cluster",
			wantSubstrs: []string{"ghost", "cluster"},
		},
		{
			name:        "context naming a missing user names the user",
			context:     "missing-user",
			wantSubstrs: []string{"ghost", "user"},
		},
		{
			name:        "user without an exec stanza is rejected",
			context:     "no-exec",
			wantSubstrs: []string{"exec"},
		},
		{
			name:        "non-https server is rejected",
			context:     "plainhttp",
			wantSubstrs: []string{"https"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := k.Resolve(tc.context)
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded, want an error", tc.context)
			}
			for _, want := range tc.wantSubstrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err.Error(), want)
				}
			}
		})
	}
}

func TestResolve_ExecCarriedVerbatim(t *testing.T) {
	k, err := Load([]string{"testdata/exec-with-env-args.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tgt, err := k.Resolve("k8s1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if tgt.TLSServerName != "api.k8s1.corp" {
		t.Errorf("TLSServerName = %q, want the explicit api.k8s1.corp", tgt.TLSServerName)
	}
	e := tgt.Exec
	if e == nil {
		t.Fatal("Exec is nil")
	}
	if e.Command != "kubelogin" {
		t.Errorf("Command = %q, want kubelogin", e.Command)
	}
	if e.APIVersion != "client.authentication.k8s.io/v1" {
		t.Errorf("APIVersion = %q", e.APIVersion)
	}
	if e.InteractiveMode != "IfAvailable" {
		t.Errorf("InteractiveMode = %q, want IfAvailable", e.InteractiveMode)
	}

	wantArgs := []string{"get-token", "--oidc-issuer-url=https://oidc.example.com", "--oidc-client-id=kubernetes"}
	if len(e.Args) != len(wantArgs) {
		t.Fatalf("Args = %v, want %v", e.Args, wantArgs)
	}
	for i := range wantArgs {
		if e.Args[i] != wantArgs[i] {
			t.Errorf("Args[%d] = %q, want %q", i, e.Args[i], wantArgs[i])
		}
	}

	if len(e.Env) != 2 {
		t.Fatalf("Env = %v, want 2 entries", e.Env)
	}
	if e.Env[0].Name != "KUBECACHEDIR" || e.Env[0].Value != "/tmp/kubecache" {
		t.Errorf("Env[0] = %+v", e.Env[0])
	}
	// An explicitly-empty value must survive: it is how an operator unsets a
	// proxy for the plugin, and silently dropping it would change behaviour.
	if e.Env[1].Name != "HTTPS_PROXY" || e.Env[1].Value != "" {
		t.Errorf("Env[1] = %+v, want HTTPS_PROXY with an empty value", e.Env[1])
	}
}

// loadCAFixture materialises testdata/ca.yaml with CA_FILE_PATH rewritten to a
// real temp file, so the file-CA case exercises a path that actually exists on
// the machine running the test.
func loadCAFixture(t *testing.T, caPEM string) *Kubeconfig {
	t.Helper()

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caPath, []byte(caPEM), 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}

	raw, err := os.ReadFile("testdata/ca.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "CA_FILE_PATH", caPath)), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	kc, err := Load([]string{path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return kc
}

// TestResolve_CarriesClusterCA pins the fix for the live-UAT failure
// "x509: certificate signed by unknown authority".
//
// The box dials 127.0.0.1 but verifies the REAL cluster certificate via
// tls-server-name, so it needs the cluster's CA. Almost every real cluster is
// fronted by an internal or EKS-managed CA absent from the sandbox's system
// trust store, so without carrying it the handshake can only fail. A CA
// certificate is public material, not a credential — carrying it does not
// weaken the phase's "no secret reaches the box" property.
func TestResolve_CarriesClusterCA(t *testing.T) {
	const caPEM = "-----BEGIN CERTIFICATE-----\nFILE-CA\n-----END CERTIFICATE-----\n"
	kc := loadCAFixture(t, caPEM)

	t.Run("inline data is carried through", func(t *testing.T) {
		tgt, err := kc.Resolve("inline-ca")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tgt.CAData != "SU5MSU5FLUNB" {
			t.Errorf("CAData = %q, want the kubeconfig's certificate-authority-data verbatim", tgt.CAData)
		}
		if tgt.InsecureSkipTLSVerify {
			t.Error("InsecureSkipTLSVerify must never be set from a config that supplies a CA")
		}
	})

	t.Run("a CA file is read and inlined", func(t *testing.T) {
		tgt, err := kc.Resolve("file-ca")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// The path is on the OPERATOR'S filesystem. Passing it to the box
		// verbatim would dangle, so it must arrive as base64 data.
		want := base64.StdEncoding.EncodeToString([]byte(caPEM))
		if tgt.CAData != want {
			t.Errorf("CAData = %q, want the file's contents base64-encoded", tgt.CAData)
		}
	})

	t.Run("no CA stays empty, not invented", func(t *testing.T) {
		tgt, err := kc.Resolve("no-ca")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if tgt.CAData != "" {
			t.Errorf("CAData = %q, want empty so the box uses its system trust store", tgt.CAData)
		}
	})

	t.Run("insecure is mirrored, never invented", func(t *testing.T) {
		tgt, err := kc.Resolve("insecure")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !tgt.InsecureSkipTLSVerify {
			t.Error("InsecureSkipTLSVerify should mirror the operator's own insecure-skip-tls-verify")
		}
	})
}

// A CA file km cannot read must fail loudly on the operator's own machine,
// naming the path. Falling through to an empty CA would defer the failure to an
// opaque x509 error inside the sandbox.
func TestResolve_UnreadableCAFileFails(t *testing.T) {
	raw, err := os.ReadFile("testdata/ca.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist.crt")
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "CA_FILE_PATH", missing)), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	kc, err := Load([]string{path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := kc.Resolve("file-ca"); err == nil {
		t.Error("expected an error naming the unreadable CA path")
	} else if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q should name the path %q", err, missing)
	}
}
