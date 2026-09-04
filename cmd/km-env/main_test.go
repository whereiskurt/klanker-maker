package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func TestParseArgs_Exec(t *testing.T) {
	verb, req, cmd, err := parseArgs([]string{"exec", "--as", "claude", "--only", "A,B", "--", "claude", "-p", "hi"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if verb != "exec" || req.As != "claude" {
		t.Errorf("verb=%q as=%q", verb, req.As)
	}
	if !reflect.DeepEqual(req.Only, []string{"A", "B"}) {
		t.Errorf("only = %v", req.Only)
	}
	if !reflect.DeepEqual(cmd, []string{"claude", "-p", "hi"}) {
		t.Errorf("cmd = %v", cmd)
	}
}

func TestParseArgs_ExecRequiresCommand(t *testing.T) {
	if _, _, _, err := parseArgs([]string{"exec", "--as", "claude"}); err == nil {
		t.Fatal("exec with no -- command was accepted")
	}
}

// The single most important test in this file. km-env must never grow a verb
// that puts the bundle back into a shell — that is the entire thing being
// removed. Same disposition as km-netpolicy having no un-deny verb.
// TestNoShellExportVerbExists is a lexical grep guard, not a semantic one: it
// scans every non-test .go file in this package for a small set of literal
// quoted verb tokens. It catches a banned verb added to main.go OR to a new
// sibling file in this package (the gap a prior review found: the original
// version only ever read main.go). It does NOT and cannot catch a verb
// assembled from a constant, a string concatenation, or matched via a
// prefix/regex instead of a literal switch case — that would need real
// static analysis. Treat a pass as "no obvious banned verb", not a proof.
func TestNoShellExportVerbExists(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{`"export"`, `"env"`, `"eval"`, `"dump"`, `"shell"`, `"source"`}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, b := range banned {
			if strings.Contains(string(src), b) {
				t.Errorf("%s appears to define a %s verb: a form that emits secrets to a "+
					"shell defeats the whole phase. Remove it.", name, b)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files found in cmd/km-env — guard is checking nothing")
	}
}

func TestBuildEnv_OverlaysAndPreserves(t *testing.T) {
	got := buildEnv([]string{"PATH=/bin", "API_KEY=stale"}, map[string][]byte{"API_KEY": []byte("fresh")})
	want := map[string]bool{"PATH=/bin": true, "API_KEY=fresh": true}
	if len(got) != 2 {
		t.Fatalf("buildEnv() = %v, want 2 entries", got)
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected entry %q", e)
		}
	}
}

func TestUnseal_RoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req secrets.UnsealRequest
		_ = json.NewDecoder(c).Decode(&req)
		_ = json.NewEncoder(c).Encode(secrets.UnsealResponse{
			Keys:   []string{"A"},
			Values: map[string][]byte{"A": []byte("v")},
		})
	}()

	resp, err := unseal(sock, secrets.UnsealRequest{As: "claude"})
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if string(resp.Values["A"]) != "v" {
		t.Errorf("Values = %v", resp.Values)
	}
}

func TestUnseal_AbsentSocketFailsClosed(t *testing.T) {
	// A broker-less box must produce a diagnosable error, never a silent
	// exec with no secrets that dies later on a confusing 401.
	if _, err := unseal(filepath.Join(t.TempDir(), "absent.sock"), secrets.UnsealRequest{}); err == nil {
		t.Fatal("unseal succeeded with no broker listening")
	}
}

func TestUnseal_BrokerErrorSurfaces(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, _ := net.Listen("unix", sock)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req secrets.UnsealRequest
		_ = json.NewDecoder(c).Decode(&req)
		_ = json.NewEncoder(c).Encode(secrets.UnsealResponse{Error: "unknown consumer"})
	}()

	_, err := unseal(sock, secrets.UnsealRequest{As: "nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown consumer") {
		t.Fatalf("err = %v, want the broker's message", err)
	}
}
