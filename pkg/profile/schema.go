package profile

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/sandbox_profile.schema.json
var sandboxProfileSchemaJSON []byte

const schemaID = "https://klankermaker.ai/schemas/sandbox-profile/v1alpha1"

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
	c := jsonschema.NewCompiler()

	// Parse the embedded JSON schema document.
	// AddResource requires the doc to be a parsed JSON value (any), not raw []byte.
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(sandboxProfileSchemaJSON))
	if err != nil {
		return nil, fmt.Errorf("parsing embedded schema JSON: %w", err)
	}

	if err := c.AddResource(schemaID, schemaDoc); err != nil {
		return nil, fmt.Errorf("loading schema resource: %w", err)
	}

	schema, err := c.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("compiling schema: %w", err)
	}

	return schema, nil
}
