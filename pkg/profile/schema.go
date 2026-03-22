package profile

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/sandbox_profile.schema.json
var sandboxProfileSchemaJSON []byte

// defaultSchemaDomain is the fallback domain when no domain is configured.
const defaultSchemaDomain = "klankermaker.ai"

// schemaID is the default schema $id URI using the default domain.
const schemaID = "https://" + defaultSchemaDomain + "/schemas/sandbox-profile/v1alpha1"

var (
	compiledSchema     *jsonschema.Schema
	compiledSchemaOnce sync.Once
	compiledSchemaErr  error
)

// Schema returns the compiled JSON Schema for SandboxProfile.
// The schema is compiled once and cached for subsequent calls.
// It panics if the embedded schema fails to compile (this is a build-time error).
func Schema() *jsonschema.Schema {
	compiledSchemaOnce.Do(func() {
		compiledSchema, compiledSchemaErr = compileSchema()
	})
	if compiledSchemaErr != nil {
		panic(fmt.Sprintf("failed to compile embedded SandboxProfile schema: %v", compiledSchemaErr))
	}
	return compiledSchema
}

func compileSchema() (*jsonschema.Schema, error) {
	return compileSchemaForDomain(defaultSchemaDomain)
}

// SchemaForDomain returns a compiled JSON Schema for SandboxProfile with the given domain
// substituted into the schema $id. Unlike Schema(), this is not cached — it is intended
// for use when the configured domain differs from the default.
// When domain is empty, the default domain ("klankermaker.ai") is used.
func SchemaForDomain(domain string) (*jsonschema.Schema, error) {
	if domain == "" {
		domain = defaultSchemaDomain
	}
	return compileSchemaForDomain(domain)
}

// compileSchemaForDomain compiles the embedded JSON Schema with the given domain substituted
// for the __SCHEMA_DOMAIN__ placeholder in the $id and apiVersion pattern.
func compileSchemaForDomain(domain string) (*jsonschema.Schema, error) {
	// Replace the __SCHEMA_DOMAIN__ placeholder with the configured domain.
	schemaJSON := bytes.ReplaceAll(sandboxProfileSchemaJSON,
		[]byte("__SCHEMA_DOMAIN__"),
		[]byte(domain),
	)

	id := "https://" + domain + "/schemas/sandbox-profile/v1alpha1"

	// Sanity check: placeholder should be fully replaced.
	if strings.Contains(string(schemaJSON), "__SCHEMA_DOMAIN__") {
		return nil, fmt.Errorf("schema placeholder __SCHEMA_DOMAIN__ not fully replaced")
	}

	c := jsonschema.NewCompiler()

	// Parse the embedded JSON schema document.
	// AddResource requires the doc to be a parsed JSON value (any), not raw []byte.
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("parsing embedded schema JSON: %w", err)
	}

	if err := c.AddResource(id, schemaDoc); err != nil {
		return nil, fmt.Errorf("loading schema resource: %w", err)
	}

	schema, err := c.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compiling schema: %w", err)
	}

	return schema, nil
}
