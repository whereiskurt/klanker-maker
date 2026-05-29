package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
	// IsWarning marks non-blocking findings. Phase 63 introduces this for
	// semantic rules that flag no-op combinations (e.g. perSandbox without
	// slackEnabled). km validate prints warnings to stderr with a WARN: prefix
	// but does not flip the anyFailed flag for them.
	IsWarning bool
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
	// Step 1: YAML -> Go any via goccy/go-yaml
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []ValidationError{{
			Path:    "",
			Message: fmt.Sprintf("YAML parse error: %v", err),
		}}
	}

	// Step 2: Go any -> JSON bytes -> any (for jsonschema)
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return []ValidationError{{
			Path:    "",
			Message: fmt.Sprintf("failed to convert YAML to JSON: %v", err),
		}}
	}

	var jsonDoc any
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

	// Rule 1: TTL must not be shorter than idleTimeout.
	// TTL="" or "0" means no auto-destroy (--ttl 0 sentinel); skip TTL >= idle check.
	if p.Spec.Lifecycle.TTL != "" && p.Spec.Lifecycle.TTL != "0" && p.Spec.Lifecycle.IdleTimeout != "" {
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

	// Rule 2: substrate must be ec2, ecs, or docker (belt-and-suspenders — schema also checks this)
	substrate := p.Spec.Runtime.Substrate
	if substrate != "" && substrate != "ec2" && substrate != "ecs" && substrate != "docker" {
		errs = append(errs, ValidationError{
			Path:    "spec.runtime.substrate",
			Message: fmt.Sprintf("substrate %q is not supported; must be one of: ec2, ecs, docker", substrate),
		})
	}

	// Rule 3: spot is valid on both substrates
	// EC2: spot instance request; ECS: FARGATE_SPOT capacity provider

	// Rule 4: enforcement must be proxy, ebpf, or both (belt-and-suspenders — schema enum also checks this)
	enforcement := p.Spec.Network.Enforcement
	if enforcement != "" && enforcement != "proxy" && enforcement != "ebpf" && enforcement != "both" {
		errs = append(errs, ValidationError{
			Path:    "spec.network.enforcement",
			Message: fmt.Sprintf("enforcement %q is not supported; must be one of: proxy, ebpf, both", enforcement),
		})
	}

	// Rule: hibernation requires rootVolumeSize > instance RAM.
	// AWS dumps RAM to the root EBS volume on suspend; if the volume is too
	// small RunInstances rejects the launch with InvalidParameterCombination,
	// long after km validate / km create has already accepted the profile.
	// Caught here so the failure surfaces at validation time.
	//
	// Skips silently when:
	//   - hibernation is off,
	//   - instance type isn't in the RAM table (fail open — see instance_ram.go),
	//   - rootVolumeSize is 0 (AMI default; we can't know the default at
	//     semantic-validation time without an AWS API call).
	if p.Spec.Runtime.Hibernation && p.Spec.Runtime.RootVolumeSize > 0 {
		if ramGiB, ok := instanceRAMGiB(p.Spec.Runtime.InstanceType); ok {
			if p.Spec.Runtime.RootVolumeSize <= ramGiB {
				errs = append(errs, ValidationError{
					Path: "spec.runtime.rootVolumeSize",
					Message: fmt.Sprintf(
						"rootVolumeSize=%d GiB is too small for hibernation on %s (%d GiB RAM); AWS dumps RAM to the root EBS volume on suspend. Increase rootVolumeSize to at least %d GiB (RAM + headroom for OS/workspace)",
						p.Spec.Runtime.RootVolumeSize, p.Spec.Runtime.InstanceType, ramGiB, ramGiB+8),
				})
			}
		}
	}

	// Rule 5: eBPF enforcement is EC2-only in Phase 40 — warn when requested on non-EC2 substrates
	if enforcement == "ebpf" || enforcement == "both" {
		switch substrate {
		case "ecs":
			errs = append(errs, ValidationError{
				Path:    "spec.network.enforcement",
				Message: "eBPF enforcement is EC2-only; ECS substrate uses proxy enforcement regardless",
			})
		case "docker":
			errs = append(errs, ValidationError{
				Path:    "spec.network.enforcement",
				Message: "eBPF enforcement is EC2-only in Phase 40; Docker substrate uses proxy enforcement regardless",
			})
		}
	}

	// Phase 63 — Slack notification validation (SLCK-01).
	if p.Spec.CLI != nil {
		cli := p.Spec.CLI
		perSandbox := cli.NotifySlackPerSandbox
		override := cli.NotifySlackChannelOverride
		slackOn := cli.NotifySlackEnabled != nil && *cli.NotifySlackEnabled
		emailOn := cli.NotifyEmailEnabled == nil || *cli.NotifyEmailEnabled // nil = backward-compat true

		// Rule S1 (error): perSandbox AND override → mutually exclusive.
		if perSandbox && override != "" {
			errs = append(errs, ValidationError{
				Path:    "spec.cli",
				Message: "notifySlackPerSandbox: true and notifySlackChannelOverride are mutually exclusive — choose one",
			})
		}

		// Rule S2 (warning): perSandbox without slackEnabled → no-op.
		if perSandbox && cli.NotifySlackEnabled != nil && !*cli.NotifySlackEnabled {
			errs = append(errs, ValidationError{
				Path:      "spec.cli.notifySlackPerSandbox",
				Message:   "notifySlackPerSandbox: true has no effect when notifySlackEnabled is false",
				IsWarning: true,
			})
		}

		// Rule S3 (warning): slackArchiveOnDestroy set without perSandbox → no-op.
		if cli.SlackArchiveOnDestroy != nil && !perSandbox {
			errs = append(errs, ValidationError{
				Path:      "spec.cli.slackArchiveOnDestroy",
				Message:   "slackArchiveOnDestroy is only meaningful when notifySlackPerSandbox: true",
				IsWarning: true,
			})
		}

		// Rule S4 (error): channel ID regex. Belt-and-suspenders with the JSON schema pattern.
		if override != "" {
			ok, _ := regexp.MatchString(`^C[A-Z0-9]+$`, override)
			if !ok {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackChannelOverride",
					Message: fmt.Sprintf("invalid Slack channel ID %q — must match ^C[A-Z0-9]+$", override),
				})
			}
		}

		// Rule S5 (warning): both channels off → no notification path.
		if !slackOn && !emailOn {
			errs = append(errs, ValidationError{
				Path:      "spec.cli",
				Message:   "notifyEmailEnabled: false and notifySlackEnabled: false — no notification channel will deliver",
				IsWarning: true,
			})
		}

		// Phase 67 — Slack inbound validation rules.
		inboundOn := cli.NotifySlackInboundEnabled
		if inboundOn {
			// Rule SI1 (error): inbound requires outbound Slack enabled.
			if !slackOn {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackInboundEnabled",
					Message: "notifySlackInboundEnabled: true requires notifySlackEnabled: true",
				})
			}
			// Rule SI2 (error): inbound requires per-sandbox channel (1:1 routing).
			if !perSandbox {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackInboundEnabled",
					Message: "notifySlackInboundEnabled: true requires notifySlackPerSandbox: true",
				})
			}
			// Rule SI3 (error): inbound incompatible with channel override (ambiguous routing).
			if override != "" {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackInboundEnabled",
					Message: "notifySlackInboundEnabled: true is incompatible with notifySlackChannelOverride (channel→sandbox routing requires 1:1 mapping in v1)",
				})
			}
		}

		// Phase 68 — Slack transcript streaming validation rules.
		// Mirror Phase 67 inbound: same prerequisites (outbound Slack + per-sandbox)
		// and same incompatibility (channel override) — both flags require
		// audience-containment guarantees.
		transcriptOn := cli.NotifySlackTranscriptEnabled
		if transcriptOn {
			// Rule ST1 (error): transcript requires outbound Slack enabled.
			if !slackOn {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackTranscriptEnabled",
					Message: "notifySlackTranscriptEnabled requires notifySlackEnabled",
				})
			}
			// Rule ST2 (error): transcript requires per-sandbox channel.
			if !perSandbox {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackTranscriptEnabled",
					Message: "notifySlackTranscriptEnabled requires notifySlackPerSandbox",
				})
			}
			// Rule ST3 (error): transcript incompatible with channel override
			// (transcript audience must be operator-controlled — the per-sandbox
			// channel guarantees a known invitee list).
			if override != "" {
				errs = append(errs, ValidationError{
					Path:    "spec.cli.notifySlackTranscriptEnabled",
					Message: "notifySlackTranscriptEnabled is incompatible with notifySlackChannelOverride (transcript audience must be operator-controlled)",
				})
			}
		}

		// Phase 72 — Slack invite-email validation rules.

		// Rule SE1 (error): invite-emails requires outbound Slack enabled.
		// Empty list is a no-op (does not require notifySlackEnabled).
		if len(cli.NotifySlackInviteEmails) > 0 && !slackOn {
			errs = append(errs, ValidationError{
				Path:    "spec.cli.notifySlackInviteEmails",
				Message: "notifySlackInviteEmails requires notifySlackEnabled: true",
			})
		}
		// Rule SE2 (error): each entry must be a syntactically valid email.
		for i, e := range cli.NotifySlackInviteEmails {
			if !emailLooksValid(e) {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("spec.cli.notifySlackInviteEmails[%d]", i),
					Message: fmt.Sprintf("invalid email %q", e),
				})
			}
		}
	}

	// Phase 87 SNAP-02: Layer 1 semantic validation for additionalSnapshots.
	errs = append(errs, validateAdditionalSnapshots(p)...)

	// Phase 89 SOPS-02: spec.secrets.sopsFile must end with .enc.yaml (offline check).
	// File existence and sops: block presence are layered on by callers
	// (km validate / km create) via ValidateSopsBundleFile — ValidateSemantic
	// itself does NOT touch the filesystem.
	if p.Spec.Secrets != nil && p.Spec.Secrets.SopsFile != "" {
		if !strings.HasSuffix(p.Spec.Secrets.SopsFile, ".enc.yaml") {
			errs = append(errs, ValidationError{
				Path:    "spec.secrets.sopsFile",
				Message: fmt.Sprintf("sopsFile %q must end with .enc.yaml (SOPS-encrypted bundles must follow the .enc.yaml naming convention so they can be .gitignore'd)", p.Spec.Secrets.SopsFile),
			})
		}
	}

	return errs
}

// validateAdditionalSnapshots enforces Layer 1 (offline, no AWS calls) rules for the
// additionalSnapshots field per SNAP-02. Path convention: spec.runtime.additionalSnapshots[i].<field>.
func validateAdditionalSnapshots(p *SandboxProfile) []ValidationError {
	if len(p.Spec.Runtime.AdditionalSnapshots) == 0 {
		return nil
	}

	var errs []ValidationError

	// EC2-only substrate check (also in service_hcl.go for compile-time defense-in-depth).
	substrate := p.Spec.Runtime.Substrate
	if !strings.HasPrefix(substrate, "ec2") {
		errs = append(errs, ValidationError{
			Path:    "spec.runtime.additionalSnapshots",
			Message: fmt.Sprintf("additionalSnapshots is not supported for %s substrate", substrate),
		})
		// Continue — report all issues at once so the operator can fix them in one pass.
	}

	snapIDRe := regexp.MustCompile(`^snap-[0-9a-f]{8,17}$`)

	// Reserved mount points (top-level exact match only — /opt/foo is fine, /opt is not).
	reserved := map[string]bool{
		"/": true, "/shared": true, "/workspace": true,
		"/proc": true, "/sys": true, "/dev": true,
		"/etc": true, "/usr": true, "/var": true,
		"/root": true, "/home": true, "/boot": true,
		"/tmp": true, "/run": true, "/opt": true,
	}

	seenMountPoints := map[string]int{} // mountPoint → first-seen index
	seenDevices := map[string]int{}     // explicit device → first-seen index

	// Pre-load additionalVolume mountPoint for collision check.
	var addlVolMP string
	if p.Spec.Runtime.AdditionalVolume != nil {
		addlVolMP = p.Spec.Runtime.AdditionalVolume.MountPoint
	}

	for i, snap := range p.Spec.Runtime.AdditionalSnapshots {
		pathBase := fmt.Sprintf("spec.runtime.additionalSnapshots[%d]", i)

		// snapshotId regex: ^snap-[0-9a-f]{8,17}$
		if !snapIDRe.MatchString(snap.SnapshotID) {
			errs = append(errs, ValidationError{
				Path:    pathBase + ".snapshotId",
				Message: fmt.Sprintf("snapshotId %q does not match ^snap-[0-9a-f]{8,17}$", snap.SnapshotID),
			})
		}

		// mountPoint must be an absolute path.
		if !strings.HasPrefix(snap.MountPoint, "/") {
			errs = append(errs, ValidationError{
				Path:    pathBase + ".mountPoint",
				Message: fmt.Sprintf("mountPoint %q must be an absolute path", snap.MountPoint),
			})
		} else if reserved[snap.MountPoint] {
			// Reserved path (exact match only).
			errs = append(errs, ValidationError{
				Path:    pathBase + ".mountPoint",
				Message: fmt.Sprintf("mountPoint %q is reserved", snap.MountPoint),
			})
		}

		// Collision with additionalVolume.mountPoint.
		if addlVolMP != "" && snap.MountPoint == addlVolMP {
			errs = append(errs, ValidationError{
				Path:    pathBase + ".mountPoint",
				Message: fmt.Sprintf("mountPoint %q collides with spec.runtime.additionalVolume.mountPoint", snap.MountPoint),
			})
		}

		// Collision across additionalSnapshots entries.
		if snap.MountPoint != "" {
			if prev, ok := seenMountPoints[snap.MountPoint]; ok {
				errs = append(errs, ValidationError{
					Path:    pathBase + ".mountPoint",
					Message: fmt.Sprintf("mountPoint %q duplicates spec.runtime.additionalSnapshots[%d].mountPoint", snap.MountPoint, prev),
				})
			} else {
				seenMountPoints[snap.MountPoint] = i
			}
		}

		// Explicit device uniqueness (optional field).
		if snap.Device != "" {
			if prev, ok := seenDevices[snap.Device]; ok {
				errs = append(errs, ValidationError{
					Path:    pathBase + ".device",
					Message: fmt.Sprintf("device %q duplicates spec.runtime.additionalSnapshots[%d].device", snap.Device, prev),
				})
			} else {
				seenDevices[snap.Device] = i
			}
		}

		// Size positivity: Layer 1 only checks < 0; Layer 2 enforces size >= snapshot.VolumeSize.
		// Size == 0 is valid and means "inherit snapshot's native size".
		if snap.Size < 0 {
			errs = append(errs, ValidationError{
				Path:    pathBase + ".size",
				Message: fmt.Sprintf("size %d must be >= 1 when specified (0 means inherit snapshot size)", snap.Size),
			})
		}
	}

	return errs
}

// parseDuration parses a duration string supporting s, m, h, d suffixes.
// Standard Go time.ParseDuration handles s, m, h. We extend it to support d (days).
func parseDuration(s string) (time.Duration, error) {
	if dayStr, ok := strings.CutSuffix(s, "d"); ok {
		days, err := time.ParseDuration(dayStr + "h")
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return days * 24, nil
	}
	return time.ParseDuration(s)
}

// ValidateSopsBundleFile performs offline checks on a SOPS-encrypted YAML bundle:
//   - the file exists and is readable
//   - the file parses as valid YAML
//   - the file contains a top-level "sops:" metadata block
//
// This function does NOT decrypt the bundle — no AWS / KMS access is performed.
// It is called by km validate and km create to layer file-existence and sops: block
// checks on top of the struct-only check in ValidateSemantic.
//
// Phase 89 SOPS-02.
func ValidateSopsBundleFile(bundlePath string) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("read sops bundle %s: %w", bundlePath, err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse sops bundle %s as YAML: %w", bundlePath, err)
	}
	if _, ok := parsed["sops"]; !ok {
		return fmt.Errorf("sops bundle %s missing top-level 'sops:' metadata block (is the file encrypted with sops?)", bundlePath)
	}
	return nil
}

// emailLooksValid is a permissive RFC-5322-ish check used by the
// notifySlackInviteEmails validator (Rule SE2). It does not verify
// deliverability — the Slack API call is the authoritative gate.
var emailRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func emailLooksValid(s string) bool {
	return emailRegex.MatchString(s)
}
