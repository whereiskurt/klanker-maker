package profile

// SchemaJSON returns the raw bytes of the embedded JSON schema for SandboxProfile.
// This accessor exposes the otherwise unexported sandboxProfileSchemaJSON for use
// by other packages (e.g., the ConfigUI schema endpoint).
func SchemaJSON() []byte {
	return sandboxProfileSchemaJSON
}
