package hygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// dangerousConcat matches a "km-" fragment that, when concatenated with a
// variable (BinaryExpr +) or used as an fmt.Sprintf format with args, builds an
// AWS resource name / ARN / SSM path / KMS alias. These MUST derive the prefix
// from resourcePrefix() / cfg.GetResourcePrefix() instead.
var dangerousConcat = regexp.MustCompile(`^(km-[a-z]|km_[a-z]|/km/|alias/km-|/aws/lambda/km-)`)

// dangerousBare matches high-signal path/alias literals that are almost always
// AWS resource identifiers even as a standalone literal (no concatenation) —
// SSM parameter paths, KMS aliases, Lambda log groups. Plain "km-foo" bare
// literals are deliberately NOT flagged here (too many benign config defaults
// and log strings); only the path/alias forms are.
var dangerousBare = regexp.MustCompile(`^(/km/|alias/km-|/aws/lambda/km-)`)

// scanDirs are the resource-management trees audited. sidecars run inside the
// sandbox network namespace and pkg/ebpf names kernel objects, so a literal
// "km-" there cannot collide across installs — but they are cheap to scan and
// the regexes above won't match their benign bare "km-budgets" env fallbacks.
var scanDirs = []string{"cmd", "internal", "pkg", "sidecars"}

type violation struct {
	rel  string
	line int
	lit  string
}

func (v violation) key() string { return v.rel + ":" + v.lit }

func (v violation) String() string {
	return fmt.Sprintf("%s:%d: hardcoded %q in a name-building context — derive the prefix from resourcePrefix()/cfg.GetResourcePrefix()", v.rel, v.line, v.lit)
}

// TestGoSourceNamesUseResourcePrefix is the regression gate. It walks the
// resource-management Go trees, finds km- literals used to construct names, and
// fails on any not present in the allow-list. New violations fail CI; legitimate
// literals (brand-prefix aliases, local-only identifiers, config defaults, and
// explicitly-deferred substrate sites) are enumerated in testdata/allowlist.txt.
func TestGoSourceNamesUseResourcePrefix(t *testing.T) {
	t.Parallel()

	root := findRepoRoot(t)
	allow := loadAllowlist(t, filepath.Join(root, "pkg", "hygiene", "testdata", "allowlist.txt"))

	var all []violation
	for _, d := range scanDirs {
		all = append(all, scanTree(t, root, filepath.Join(root, d))...)
	}

	var unexpected []violation
	seenAllowed := map[string]bool{}
	for _, v := range all {
		if allow[v.key()] {
			seenAllowed[v.key()] = true
			continue
		}
		unexpected = append(unexpected, v)
	}

	if len(unexpected) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d hardcoded km- name-construction site(s) not in the allow-list:\n", len(unexpected))
		for _, v := range unexpected {
			fmt.Fprintf(&b, "  %s\n", v)
		}
		b.WriteString("\nFix by deriving the prefix (resourcePrefix() / cfg.GetResourcePrefix()), or — if genuinely\n")
		b.WriteString("benign (local-only identifier, brand alias, config default) — add 'relpath:literal' to\n")
		b.WriteString("pkg/hygiene/testdata/allowlist.txt with a comment explaining why.\n")
		t.Fatal(b.String())
	}

	// Keep the allow-list honest: an entry that no longer matches anything is
	// stale and should be removed (e.g. after a deferred site is fixed).
	for key := range allow {
		if !seenAllowed[key] {
			t.Errorf("stale allow-list entry %q matches nothing — remove it from testdata/allowlist.txt", key)
		}
	}
}

// TestGoAuditCatchesBadFixture is the negative confidence test: a synthetic file
// with each bug shape must be flagged. Guards against the detector silently
// passing everything.
func TestGoAuditCatchesBadFixture(t *testing.T) {
	t.Parallel()

	const bad = `package x
import "fmt"
func f(id, prefix string) {
	_ = "km-budget-" + id                                    // concat
	_ = fmt.Sprintf("km-github-token-refresher-%s", id)      // sprintf
	_ = "alias/km-github-token-" + id                        // alias concat
	const p = "/km/config/github/private-key"                // bare ssm path
}
`
	got, err := scanSrc("bad.go", []byte(bad))
	if err != nil {
		t.Fatalf("scanSrc: %v", err)
	}
	if len(got) < 4 {
		t.Fatalf("expected >=4 violations for the bad fixture, got %d: %v", len(got), got)
	}
}

// TestGoAuditPassesGoodFixture verifies prefix-derived constructions and benign
// plain "km-foo" bare literals do NOT trip the detector.
func TestGoAuditPassesGoodFixture(t *testing.T) {
	t.Parallel()

	const good = `package x
import "fmt"
func resourcePrefix() string { return "km" }
func f(id string) {
	_ = resourcePrefix() + "-budget-" + id                   // prefix-derived
	_ = fmt.Sprintf("%s-github-token-%s", resourcePrefix(), id)
	_ = "km-sandboxes"                                       // bare plain km- (config default) — not flagged
	_ = getEnv("KM_BUDGET_TABLE", "km-budgets")              // bare env fallback — not flagged
}
func getEnv(k, d string) string { return d }
`
	got, err := scanSrc("good.go", []byte(good))
	if err != nil {
		t.Fatalf("scanSrc: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations for the good fixture, got %d: %v", len(got), got)
	}
}

// scanTree parses every non-test .go file under dir and returns violations.
func scanTree(t *testing.T, root, dir string) []violation {
	t.Helper()
	var out []violation
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		vs, serr := scanSrc(filepath.ToSlash(rel), src)
		if serr != nil {
			return fmt.Errorf("parse %s: %w", rel, serr)
		}
		out = append(out, vs...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// scanSrc parses Go source and reports km- name-construction violations. The
// allow-list is applied by the caller; scanSrc itself is allow-list-agnostic so
// the fixtures can exercise the raw detector.
func scanSrc(rel string, src []byte) ([]violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{} // dedupe (line:lit) — BinaryExpr leaves also visit as BasicLit
	var out []violation
	add := func(line int, lit string) {
		// "km-config.yaml" is the config FILENAME — benign everywhere, never an
		// AWS resource name. Skip at the detector level so error/log messages
		// referencing it don't need per-site allow-list entries.
		if strings.HasPrefix(lit, "km-config") {
			return
		}
		k := fmt.Sprintf("%d:%s", line, lit)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, violation{rel: rel, line: line, lit: lit})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				for _, s := range stringLeaves(x) {
					if dangerousConcat.MatchString(s) {
						add(fset.Position(x.Pos()).Line, s)
					}
				}
			}
		case *ast.CallExpr:
			// fmt.Sprintf("km-...%s", arg) — name building, not a log message.
			if isSprintf(x) && len(x.Args) >= 2 {
				if s, ok := stringValue(x.Args[0]); ok && dangerousConcat.MatchString(s) {
					add(fset.Position(x.Pos()).Line, s)
				}
			}
		case *ast.BasicLit:
			// Standalone path/alias literal used directly as an SDK argument.
			if x.Kind == token.STRING {
				if s, err := strconv.Unquote(x.Value); err == nil && dangerousBare.MatchString(s) {
					add(fset.Position(x.Pos()).Line, s)
				}
			}
		}
		return true
	})
	return out, nil
}

// stringLeaves collects the string-literal values from a (possibly nested) +
// expression tree.
func stringLeaves(n ast.Expr) []string {
	switch x := n.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return append(stringLeaves(x.X), stringLeaves(x.Y)...)
		}
	case *ast.BasicLit:
		if s, ok := stringValue(x); ok {
			return []string{s}
		}
	}
	return nil
}

func stringValue(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func isSprintf(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "fmt"
}

// findRepoRoot walks up from the package dir until it finds go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found walking up)")
		}
		dir = parent
	}
}

// loadAllowlist reads testdata/allowlist.txt: one "relpath:literal" per line,
// '#' comments and blank lines ignored.
func loadAllowlist(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("read allowlist %s: %v", path, err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		allow[line] = true
	}
	return allow
}
