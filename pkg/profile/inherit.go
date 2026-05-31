package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

const maxInheritanceDepth = 3

// Resolve loads a profile by name and resolves its extends chain.
// It searches built-in profiles first, then the provided searchPaths directories.
// Cycle detection and max depth (3) are enforced.
// NOTE: Resolve does NOT call Validate() internally. Callers are responsible
// for calling Resolve() and Validate() separately.
func Resolve(name string, searchPaths []string) (*SandboxProfile, error) {
	return resolve(name, searchPaths, make(map[string]bool), 0)
}

func resolve(name string, searchPaths []string, visited map[string]bool, depth int) (*SandboxProfile, error) {
	if depth > maxInheritanceDepth {
		return nil, fmt.Errorf("inheritance depth exceeded: max %d levels allowed", maxInheritanceDepth)
	}

	if visited[name] {
		return nil, fmt.Errorf("circular inheritance detected: profile %q already in chain", name)
	}
	visited[name] = true

	profile, err := load(name, searchPaths)
	if err != nil {
		return nil, err
	}

	if profile.Extends == "" {
		return profile, nil
	}

	parent, err := resolve(profile.Extends, searchPaths, visited, depth+1)
	if err != nil {
		return nil, fmt.Errorf("resolving parent %q of %q: %w", profile.Extends, name, err)
	}

	merged := merge(parent, profile)
	return merged, nil
}

func load(name string, searchPaths []string) (*SandboxProfile, error) {
	// Try built-in profiles first
	if IsBuiltin(name) {
		return LoadBuiltin(name)
	}

	// Search in provided paths
	for _, dir := range searchPaths {
		path := filepath.Join(dir, name+".yaml")
		data, err := os.ReadFile(path)
		if err == nil {
			p, parseErr := Parse(data)
			if parseErr != nil {
				return nil, fmt.Errorf("parsing profile %q from %s: %w", name, path, parseErr)
			}
			return p, nil
		}
	}

	return nil, fmt.Errorf("profile %q not found in built-in profiles or search paths %v", name, searchPaths)
}

// merge combines parent and child profiles. Child values override parent values.
// For allowlist arrays (AllowedDNSSuffixes, AllowedHosts, etc.), if child specifies
// them at all, child's array replaces parent's entirely.
// metadata.labels are the ONE exception: they are merged additively.
func merge(parent, child *SandboxProfile) *SandboxProfile {
	result := &SandboxProfile{
		APIVersion: child.APIVersion,
		Kind:       child.Kind,
		Metadata:   child.Metadata,
		Spec:       child.Spec,
	}

	// Merge metadata.labels additively (the one exception)
	if parent.Metadata.Labels != nil || child.Metadata.Labels != nil {
		merged := make(map[string]string)
		for k, v := range parent.Metadata.Labels {
			merged[k] = v
		}
		for k, v := range child.Metadata.Labels {
			merged[k] = v
		}
		result.Metadata.Labels = merged
	}

	// For each spec section: if child section is zero-value, use parent's
	mergeSpecSection(&result.Spec.Lifecycle, &parent.Spec.Lifecycle, &child.Spec.Lifecycle)
	mergeSpecSection(&result.Spec.Runtime, &parent.Spec.Runtime, &child.Spec.Runtime)
	mergeSpecSection(&result.Spec.Execution, &parent.Spec.Execution, &child.Spec.Execution)
	mergeSpecSection(&result.Spec.SourceAccess, &parent.Spec.SourceAccess, &child.Spec.SourceAccess)
	mergeSpecSection(&result.Spec.Network, &parent.Spec.Network, &child.Spec.Network)
	mergeSpecSection(&result.Spec.IAM, &parent.Spec.IAM, &child.Spec.IAM)
	mergeSpecSection(&result.Spec.Sidecars, &parent.Spec.Sidecars, &child.Spec.Sidecars)
	mergeSpecSection(&result.Spec.Observability, &parent.Spec.Observability, &child.Spec.Observability)
	// Phase 92 (Wave 1): the dead top-level Spec.Agent merge was removed. Wave 4
	// adds a typed merger for the new pointer-typed Spec.Agent.

	// Phase 92 (Wave 2): the Notification block is the first pointer-typed Spec
	// section to get a field-level typed merger. result.Spec was bulk-copied from
	// child above (so result.Spec.Notification currently == child.Spec.Notification);
	// overwrite it with the deep-merged value so a child that sets one notification
	// field still inherits the parent's other settings (the pointer-merge bug fix).
	result.Spec.Notification = mergeNotificationSpec(parent.Spec.Notification, child.Spec.Notification)

	// Clear extends — resolved profile has no parent
	result.Extends = ""

	return result
}

// mergeNotificationSpec performs field-level nil-aware merge of parent and child
// NotificationSpec values. Child non-nil fields override parent; nil fields inherit.
// This replaces the broken pointer-replace path in mergeSpecSection for
// Spec.Notification.
//
// Bug being fixed: pre-Phase-92, a child Profile setting any single notification
// field caused the entire parent.Spec.CLI pointer to be replaced wholesale by
// child.Spec.CLI, losing all other parent notify settings. The Notification block
// is the first pointer-typed Spec section to get a typed merger; mergeAgentSpec
// (Wave 4) is the second.
func mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationSpec{
		Events: mergeNotificationEventsSpec(parent.Events, child.Events),
		Email:  mergeNotificationEmailSpec(parent.Email, child.Email),
		Slack:  mergeNotificationSlackSpec(parent.Slack, child.Slack),
	}
}

func mergeNotificationEventsSpec(parent, child *NotificationEventsSpec) *NotificationEventsSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationEventsSpec{
		OnPermission:    pickBoolPtr(parent.OnPermission, child.OnPermission),
		OnIdle:          pickBoolPtr(parent.OnIdle, child.OnIdle),
		CooldownSeconds: pickIntPtr(parent.CooldownSeconds, child.CooldownSeconds),
	}
}

func mergeNotificationEmailSpec(parent, child *NotificationEmailSpec) *NotificationEmailSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationEmailSpec{
		Enabled: pickBoolPtr(parent.Enabled, child.Enabled),
		Address: pickString(parent.Address, child.Address),
	}
}

func mergeNotificationSlackSpec(parent, child *NotificationSlackSpec) *NotificationSlackSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationSlackSpec{
		Enabled:          pickBoolPtr(parent.Enabled, child.Enabled),
		PerSandbox:       pickBoolPtr(parent.PerSandbox, child.PerSandbox),
		ChannelOverride:  pickString(parent.ChannelOverride, child.ChannelOverride),
		ArchiveOnDestroy: pickBoolPtr(parent.ArchiveOnDestroy, child.ArchiveOnDestroy),
		Inbound:          mergeNotificationSlackInboundSpec(parent.Inbound, child.Inbound),
		Transcript:       mergeNotificationSlackTranscriptSpec(parent.Transcript, child.Transcript),
		Invites:          mergeNotificationSlackInvitesSpec(parent.Invites, child.Invites),
	}
}

func mergeNotificationSlackInboundSpec(parent, child *NotificationSlackInboundSpec) *NotificationSlackInboundSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationSlackInboundSpec{
		Enabled:     pickBoolPtr(parent.Enabled, child.Enabled),
		MentionOnly: pickBoolPtr(parent.MentionOnly, child.MentionOnly),
		ReactAlways: pickBoolPtr(parent.ReactAlways, child.ReactAlways),
	}
}

func mergeNotificationSlackTranscriptSpec(parent, child *NotificationSlackTranscriptSpec) *NotificationSlackTranscriptSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return &NotificationSlackTranscriptSpec{
		Enabled: pickBoolPtr(parent.Enabled, child.Enabled),
	}
}

func mergeNotificationSlackInvitesSpec(parent, child *NotificationSlackInvitesSpec) *NotificationSlackInvitesSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	// Emails: child non-empty replaces parent (do not concat — match the "child
	// wins" semantics of every other field; an operator who wants to extend
	// re-lists the parent's emails).
	out := &NotificationSlackInvitesSpec{
		Emails:     parent.Emails,
		UseConnect: pickBoolPtr(parent.UseConnect, child.UseConnect),
	}
	if len(child.Emails) > 0 {
		out.Emails = child.Emails
	}
	return out
}

// pickBoolPtr returns child when non-nil, else parent (field-level nil-aware merge).
func pickBoolPtr(parent, child *bool) *bool {
	if child != nil {
		return child
	}
	return parent
}

// pickIntPtr returns child when non-nil, else parent.
func pickIntPtr(parent, child *int) *int {
	if child != nil {
		return child
	}
	return parent
}

// pickString returns child when non-empty, else parent.
func pickString(parent, child string) string {
	if child != "" {
		return child
	}
	return parent
}

// mergeSpecSection uses reflection to check if the child section is zero-value.
// If it is, use parent's value instead.
func mergeSpecSection(result, parent, child interface{}) {
	childVal := reflect.ValueOf(child).Elem()
	parentVal := reflect.ValueOf(parent).Elem()
	resultVal := reflect.ValueOf(result).Elem()

	if childVal.IsZero() {
		resultVal.Set(parentVal)
	} else {
		resultVal.Set(childVal)
	}
}
