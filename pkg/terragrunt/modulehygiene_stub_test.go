// Package terragrunt_test — Phase 84.4 module hygiene stub.
// This file imports github.com/hashicorp/hcl/v2/hclsyntax to anchor the
// hcl/v2 direct dependency in go.mod so Plan 01's full HCL AST audit test
// can compile without running `go get` again. The stub test itself is a no-op;
// Plan 01 (84.4-01-PLAN.md) replaces this file with the real audit test.
package terragrunt_test

import (
	// hclsyntax is the HCL v2 AST library used by Plan 01's static-analysis
	// audit test (modulehygiene_test.go). Imported here to keep hcl/v2 as a
	// direct dependency before Plan 01 lands its real test file.
	_ "github.com/hashicorp/hcl/v2/hclsyntax"
)
