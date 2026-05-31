// Package hygiene holds static-analysis regression tests that keep the Go
// source tree multi-install-safe.
//
// It is the Go-source analogue of Phase 84.4's pkg/terragrunt module-hygiene
// test (which audits infra/modules/*.tf). Where that test guarantees Terraform
// resource names are templated off var.resource_prefix, the test here guarantees
// Go code does not construct AWS resource names from a hardcoded "km-" literal —
// the bug class that leaks/cross-talks resources when two operators run
// different resource_prefix values (e.g. km and km2) in the same AWS account.
//
// The package intentionally contains no runtime code; see goprefix_test.go.
package hygiene
