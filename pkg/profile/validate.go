package profile

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	jschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationError represents a single validation failure with a JSON path and message.
// The Error() method produces the "path: message" format required by the CI and logging pipeline.
type ValidationError struct {
	// Path is the JSON path to the invalid field (e.g. "spec.runtime.substrate").
	Path string
	// Message describes what is wrong with the value at Path.
	Message string
}

// Error implements the error interface. Returns "path: message" format.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// Validate runs both schema validation and semantic validation against raw YAML bytes.
// Schema validation checks structural correctness against the JSON Schema.
// Semantic validation checks logical consistency (e.g. TTL > idleTimeout).
//
// Returns a slice of ValidationError. An empty slice means the profile is fully valid.
func Validate(raw []byte) []ValidationError {
	var errs []ValidationError
	errs = append(errs, ValidateSchema(raw)...)

	// Only run semantic checks if schema validation passed
	// (semantic checks assume well-formed data)
	if len(errs) == 0 {
		p, err := Parse(raw)
		if err != nil {
			errs = append(errs, ValidationError{
				Path:    "",
				Message: fmt.Sprintf("failed to parse profile for semantic validation: %v", err),
			})
			return errs
		}
		errs = append(errs, ValidateSemantic(p)...)
	} else {
		// Still attempt semantic validation if parse succeeds — structural errors
		// and semantic errors can coexist.
		p, err := Parse(raw)
		if err == nil {
			errs = append(errs, ValidateSemantic(p)...)
		}
	}

	return errs
}

// ValidateSchema validates raw YAML bytes against the embedded JSON Schema Draft 2020-12.
// It converts YAML to JSON internally before validation.
//
// Returns a slice of ValidationError describing any schema violations.
// Field paths use JSON path format (e.g. "spec.runtime.substrate").
func ValidateSchema(raw []byte) []ValidationError {
	// Step 1: YAML -> Go interface{} via goccy/go-yaml
	var doc interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []ValidationError{{
			Path:    "",
			Message: fmt.Sprintf("YAML parse error: %v", err),
		}}
	}

	// Step 2: Go interface{} -> JSON bytes -> any (for jsonschema)
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return []ValidationError{{
			Path:    "",
			Message: fmt.Sprintf("failed to convert YAML to JSON: %v", err),
		}}
	}

	var jsonDoc interface{}
	if err := json.Unmarshal(jsonBytes, &jsonDoc); err != nil {
		return []ValidationError{{
			Path:    "",
			Message: fmt.Sprintf("failed to re-parse JSON: %v", err),
		}}
	}

	// Step 3: Validate against compiled schema
	schema := Schema()
	validationErr := schema.Validate(jsonDoc)
	if validationErr == nil {
		return nil
	}

	// Step 4: Convert schema ValidationError tree to our ValidationError slice
	schErr, ok := validationErr.(*jschema.ValidationError)
	if !ok {
		return []ValidationError{{
			Path:    "",
			Message: validationErr.Error(),
		}}
	}

	return flattenSchemaErrors(schErr)
}

// flattenSchemaErrors recursively walks the ValidationError tree produced by
// santhosh-tekuri/jsonschema/v6 and returns a flat slice of ValidationError
// values with JSON-path-formatted paths.
func flattenSchemaErrors(e *jschema.ValidationError) []ValidationError {
	var errs []ValidationError

	if len(e.Causes) == 0 {
		// Leaf error — extract path and message from BasicOutput
		output := e.BasicOutput()
		errs = append(errs, extractOutputErrors(output)...)
	} else {
		for _, cause := range e.Causes {
			errs = append(errs, flattenSchemaErrors(cause)...)
		}
	}

	// Deduplicate identical errors
	return deduplicateErrors(errs)
}

// extractOutputErrors converts an OutputUnit tree to ValidationError slice.
func extractOutputErrors(unit *jschema.OutputUnit) []ValidationError {
	var errs []ValidationError

	if !unit.Valid && unit.Error != nil {
		path := jsonPointerToPath(unit.InstanceLocation)
		msg := unit.Error.String()
		if msg == "" {
			msg = fmt.Sprintf("validation failed at %s", unit.KeywordLocation)
		}
		errs = append(errs, ValidationError{
			Path:    path,
			Message: msg,
		})
	}

	for i := range unit.Errors {
		errs = append(errs, extractOutputErrors(&unit.Errors[i])...)
	}

	return errs
}

// jsonPointerToPath converts a JSON Pointer (e.g. "/spec/runtime/substrate")
// to a dot-notation path (e.g. "spec.runtime.substrate").
// Array indices are preserved: "/spec/network/egress/allowedDNSSuffixes/0" -> "spec.network.egress.allowedDNSSuffixes[0]"
func jsonPointerToPath(ptr string) string {
	if ptr == "" || ptr == "/" {
		return ""
	}

	// Strip leading slash
	ptr = strings.TrimPrefix(ptr, "/")

	parts := strings.Split(ptr, "/")
	var result []string
	for i, part := range parts {
		if part == "" {
			continue
		}
		// Check if it's a numeric index
		isIndex := true
		for _, c := range part {
			if c < '0' || c > '9' {
				isIndex = false
				break
			}
		}
		if isIndex && i > 0 {
			// Append as array index notation to previous part
			if len(result) > 0 {
				result[len(result)-1] = result[len(result)-1] + "[" + part + "]"
				continue
			}
		}
		result = append(result, part)
	}

	return strings.Join(result, ".")
}

// deduplicateErrors removes duplicate ValidationError entries.
func deduplicateErrors(errs []ValidationError) []ValidationError {
	seen := make(map[string]struct{})
	var result []ValidationError
	for _, e := range errs {
		key := e.Path + "|" + e.Message
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, e)
		}
	}
	return result
}

// ValidateSemantic checks logical consistency constraints on a parsed SandboxProfile.
// These are constraints that cannot be expressed in JSON Schema alone.
//
// Returns a slice of ValidationError. An empty slice means no semantic violations.
func ValidateSemantic(p *SandboxProfile) []ValidationError {
	var errs []ValidationError

	// Rule 1: TTL must not be shorter than idleTimeout
	if p.Spec.Lifecycle.TTL != "" && p.Spec.Lifecycle.IdleTimeout != "" {
		ttl, ttlErr := parseDuration(p.Spec.Lifecycle.TTL)
		idle, idleErr := parseDuration(p.Spec.Lifecycle.IdleTimeout)
		if ttlErr == nil && idleErr == nil {
			if ttl < idle {
				errs = append(errs, ValidationError{
					Path:    "spec.lifecycle.ttl",
					Message: fmt.Sprintf("ttl (%s) must not be shorter than idleTimeout (%s)", p.Spec.Lifecycle.TTL, p.Spec.Lifecycle.IdleTimeout),
				})
			}
		}
	}

	// Rule 2: substrate must be ec2 or ecs (belt-and-suspenders — schema also checks this)
	substrate := p.Spec.Runtime.Substrate
	if substrate != "" && substrate != "ec2" && substrate != "ecs" {
		errs = append(errs, ValidationError{
			Path:    "spec.runtime.substrate",
			Message: fmt.Sprintf("substrate %q is not supported; must be one of: ec2, ecs", substrate),
		})
	}

	// Rule 3: spot instances are only supported on EC2
	if p.Spec.Runtime.Spot && p.Spec.Runtime.Substrate == "ecs" {
		errs = append(errs, ValidationError{
			Path:    "spec.runtime.spot",
			Message: "spot instances are not supported on ECS substrate; set spot: false or change substrate to ec2",
		})
	}

	return errs
}

// parseDuration parses a duration string supporting s, m, h, d suffixes.
// Standard Go time.ParseDuration handles s, m, h. We extend it to support d (days).
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		dayStr := strings.TrimSuffix(s, "d")
		days, err := time.ParseDuration(dayStr + "h")
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return days * 24, nil
	}
	return time.ParseDuration(s)
}
