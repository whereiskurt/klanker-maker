// Command km-profile-migrate mechanically upgrades a SandboxProfile YAML from
// the v1alpha1 schema to the v1alpha2 schema introduced in Phase 92.
//
// Phase 92 was a HARD breaking change. This one-shot tool lets operators with
// profiles in OTHER AWS accounts (still written against v1alpha1) upgrade them
// without re-authoring by hand. It operates on the generic yaml.v3 node tree so
// it does not depend on the (now-deleted) v1alpha1 Go types; comments on
// untouched nodes are preserved.
//
// Usage:
//
//	go run ./cmd/km-profile-migrate <in.yaml> [-o out.yaml]
//	go run ./cmd/km-profile-migrate < in.yaml > out.yaml
//	cat in.yaml | go run ./cmd/km-profile-migrate -o out.yaml
//
// Transformations performed (v1alpha1 -> v1alpha2):
//
//	1. apiVersion <domain>/v1alpha1 -> <domain>/v1alpha2.
//	2. spec.identity -> spec.iam; the dead spec.identity.sessionPolicy field is dropped.
//	3. The dead v1alpha1 spec.agent block (maxConcurrentTasks/taskTimeout/allowedTools)
//	   is removed. (The NEW spec.agent is built from cli.* in step 5/6.)
//	4. The inlined configFiles["/home/sandbox/.claude/settings.json"] JSON blob is
//	   parsed and lifted into typed spec.agent.claude.* (trustedDirectories,
//	   tools.autoApprove, tools.deny, permissions passthrough); the entry is removed.
//	5. spec.cli.notify* / *slack* / vscodeEnabled / agent / claudeArgs / codexArgs
//	   are rehomed into spec.notification.*, spec.runtime.vscode.enabled, and
//	   spec.agent.*. cli.noBedrock is preserved; an emptied cli block is dropped.
//
// The tool is idempotent: a v1alpha2 input is passed through unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	out := flag.String("o", "-", "output file (- for stdout)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: km-profile-migrate [<in.yaml>] [-o out.yaml]\n")
		fmt.Fprintf(os.Stderr, "  reads a v1alpha1 SandboxProfile and writes the equivalent v1alpha2 profile.\n")
		flag.PrintDefaults()
	}
	// Hoist flags ahead of a positional path so both `migrate in.yaml -o out`
	// and `migrate -o out in.yaml` work (Go's flag package stops at the first
	// non-flag arg otherwise).
	flag.CommandLine.Parse(reorderArgs(os.Args[1:]))

	var raw []byte
	var err error
	switch {
	case flag.NArg() == 0 || flag.Arg(0) == "-":
		raw, err = io.ReadAll(os.Stdin)
	case flag.NArg() == 1:
		raw, err = os.ReadFile(flag.Arg(0))
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-profile-migrate: read input: %v\n", err)
		os.Exit(1)
	}

	migrated, changed, err := Migrate(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-profile-migrate: %v\n", err)
		os.Exit(1)
	}
	if !changed {
		fmt.Fprintln(os.Stderr, "km-profile-migrate: input already v1alpha2 — passing through unchanged")
	}

	if *out == "-" {
		os.Stdout.Write(migrated)
		return
	}
	if err := os.WriteFile(*out, migrated, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "km-profile-migrate: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "km-profile-migrate: wrote %s\n", *out)
}

const settingsJSONKey = "/home/sandbox/.claude/settings.json"

// reorderArgs moves recognized flags (and their values) ahead of positional
// arguments so the standard flag parser sees them. Only the single-valued -o
// flag is supported.
func reorderArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" && i+1 < len(args):
			flags = append(flags, a, args[i+1])
			i++
		case strings.HasPrefix(a, "-o="):
			flags = append(flags, a)
		case strings.HasPrefix(a, "-") && a != "-":
			// unknown flag (e.g. -h/--help) — let flag package handle it
			flags = append(flags, a)
		default:
			positional = append(positional, a)
		}
	}
	return append(flags, positional...)
}

// Migrate transforms raw v1alpha1 SandboxProfile YAML into v1alpha2 YAML.
// It returns the migrated bytes, whether anything changed (false when input was
// already v1alpha2), and an error if the input is not a recognizable profile.
func Migrate(raw []byte) ([]byte, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, false, fmt.Errorf("parse YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false, fmt.Errorf("input is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("input root is not a mapping (not a SandboxProfile?)")
	}

	apiVersion := mapGet(root, "apiVersion")
	kind := mapGet(root, "kind")
	if apiVersion == nil || kind == nil || scalar(kind) != "SandboxProfile" {
		return nil, false, fmt.Errorf("not a recognizable SandboxProfile (missing apiVersion/kind: SandboxProfile)")
	}

	ver := scalar(apiVersion)
	switch {
	case strings.HasSuffix(ver, "/v1alpha2"):
		// Already migrated — pass through byte-for-byte.
		return raw, false, nil
	case strings.HasSuffix(ver, "/v1alpha1"):
		apiVersion.Value = strings.TrimSuffix(ver, "v1alpha1") + "v1alpha2"
	default:
		return nil, false, fmt.Errorf("unsupported apiVersion %q (expected suffix /v1alpha1 or /v1alpha2)", ver)
	}

	spec := mapGet(root, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("missing spec mapping")
	}

	// 2. spec.identity -> spec.iam (drop sessionPolicy).
	if id := mapGet(spec, "identity"); id != nil && id.Kind == yaml.MappingNode {
		mapDelete(id, "sessionPolicy")
		renameKey(spec, "identity", "iam")
	}

	// 3. Remove the dead v1alpha1 spec.agent block. It always carries
	//    maxConcurrentTasks/taskTimeout/allowedTools and NO new-schema keys.
	if oldAgent := mapGet(spec, "agent"); oldAgent != nil && isDeadAgentBlock(oldAgent) {
		mapDelete(spec, "agent")
	}

	cli := mapGet(spec, "cli")

	// 4. Lift the inlined Claude settings.json out of execution.configFiles.
	claude := buildClaudeFromSettings(spec)

	// 5 + 6. Build the new spec.agent and spec.notification, plus runtime.vscode,
	//        from the cli.* fields.
	agent := buildAgent(cli, claude)
	notification := buildNotification(cli)
	vscodeEnabled := extractScalarPtr(cli, "vscodeEnabled")

	// Insert spec.runtime.vscode.enabled.
	if vscodeEnabled != nil {
		runtime := mapGet(spec, "runtime")
		if runtime == nil || runtime.Kind != yaml.MappingNode {
			runtime = newMapping()
			mapSet(spec, "runtime", runtime)
		}
		vscode := newMapping()
		mapSet(vscode, "enabled", scalarBool(*vscodeEnabled))
		mapSet(runtime, "vscode", vscode)
	}

	// Strip the consumed cli.* keys; keep only noBedrock.
	if cli != nil {
		for _, k := range consumedCLIKeys {
			mapDelete(cli, k)
		}
		if len(cli.Content) == 0 {
			mapDelete(spec, "cli")
		}
	}

	// Insert spec.agent and spec.notification (after cli/iam if present, else append).
	if agent != nil {
		mapSet(spec, "agent", agent)
	}
	if notification != nil {
		mapSet(spec, "notification", notification)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, fmt.Errorf("re-encode YAML: %w", err)
	}
	enc.Close()
	return buf.Bytes(), true, nil
}

// consumedCLIKeys are the v1alpha1 spec.cli.* fields that are rehomed elsewhere
// in v1alpha2 and must be deleted from the cli block. cli.noBedrock survives.
var consumedCLIKeys = []string{
	"agent", "claudeArgs", "codexArgs",
	"notifyOnPermission", "notifyOnIdle", "notifyCooldownSeconds",
	"notificationEmailAddress", "notifyEmailEnabled",
	"notifySlackEnabled", "notifySlackPerSandbox", "notifySlackChannelOverride",
	"slackArchiveOnDestroy",
	"notifySlackInboundEnabled", "notifySlackInboundMentionOnly", "notifySlackInboundReactAlways",
	"notifySlackTranscriptEnabled",
	"notifySlackInviteEmails", "useSlackConnect",
	"vscodeEnabled",
}

// isDeadAgentBlock reports whether the given spec.agent node is the dead
// v1alpha1 block (only maxConcurrentTasks/taskTimeout/allowedTools keys) rather
// than an already-migrated v1alpha2 block (default/claude/codex).
func isDeadAgentBlock(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "default", "claude", "codex":
			return false // looks like a v1alpha2 block — leave it alone
		}
	}
	return true
}

// buildClaudeFromSettings parses the inlined Claude settings.json from
// execution.configFiles, removes that entry, and returns a populated
// agent.claude mapping node (or nil if there was no settings.json). It maps:
//
//	trustedDirectories          -> agent.claude.trustedDirectories
//	autoApprove / permissions.allow -> agent.claude.tools.autoApprove
//	disallowedTools / permissions.deny -> agent.claude.tools.deny
//	any other top-level keys    -> agent.claude.permissions (passthrough)
func buildClaudeFromSettings(spec *yaml.Node) *yaml.Node {
	exec := mapGet(spec, "execution")
	if exec == nil {
		return nil
	}
	configFiles := mapGet(exec, "configFiles")
	if configFiles == nil || configFiles.Kind != yaml.MappingNode {
		return nil
	}
	val := mapGet(configFiles, settingsJSONKey)
	if val == nil {
		return nil
	}

	var settings map[string]any
	if err := json.Unmarshal([]byte(scalar(val)), &settings); err != nil {
		// Not parseable — leave the configFiles entry in place; the mixed-mode
		// validator will flag it. Return nil so we don't synthesize a broken block.
		fmt.Fprintf(os.Stderr, "km-profile-migrate: WARNING: could not parse inlined %s as JSON (%v); leaving it inline for hand-review\n", settingsJSONKey, err)
		return nil
	}

	// Remove the settings.json entry; drop configFiles / execution if now empty.
	mapDelete(configFiles, settingsJSONKey)
	if len(configFiles.Content) == 0 {
		mapDelete(exec, "configFiles")
	}
	if len(exec.Content) == 0 {
		mapDelete(spec, "execution")
	}

	var trusted []string
	var allow []string
	var deny []string
	passthrough := map[string]any{}

	for k, v := range settings {
		switch k {
		case "trustedDirectories":
			trusted = toStringSlice(v)
		case "autoApprove":
			allow = append(allow, toStringSlice(v)...)
		case "disallowedTools":
			deny = append(deny, toStringSlice(v)...)
		case "permissions":
			// permissions.allow / permissions.deny fold into the typed tool sets;
			// anything else under permissions stays as passthrough.
			if pm, ok := v.(map[string]any); ok {
				rest := map[string]any{}
				for pk, pv := range pm {
					switch pk {
					case "allow":
						allow = append(allow, toStringSlice(pv)...)
					case "deny":
						deny = append(deny, toStringSlice(pv)...)
					default:
						rest[pk] = pv
					}
				}
				if len(rest) > 0 {
					passthrough["permissions"] = rest
				}
			} else {
				passthrough[k] = v
			}
		default:
			passthrough[k] = v
		}
	}

	claude := newMapping()
	if len(trusted) > 0 {
		mapSet(claude, "trustedDirectories", scalarStringSlice(trusted))
	}
	allow = dedupe(allow)
	deny = dedupe(deny)
	if len(allow) > 0 || len(deny) > 0 {
		tools := newMapping()
		if len(allow) > 0 {
			mapSet(tools, "autoApprove", scalarStringSlice(allow))
		}
		if len(deny) > 0 {
			mapSet(tools, "deny", scalarStringSlice(deny))
		}
		mapSet(claude, "tools", tools)
	}
	if len(passthrough) > 0 {
		node, err := anyToNode(passthrough)
		if err == nil {
			mapSet(claude, "permissions", node)
		}
	}
	if len(claude.Content) == 0 {
		return nil
	}
	return claude
}

// buildAgent assembles the new spec.agent block from cli.agent / cli.claudeArgs /
// cli.codexArgs and the claude node lifted from settings.json. Returns nil if
// there is nothing to emit.
func buildAgent(cli *yaml.Node, claude *yaml.Node) *yaml.Node {
	var defaultAgent string
	var claudeArgs, codexArgs []string
	if cli != nil {
		if d := mapGet(cli, "agent"); d != nil {
			defaultAgent = scalar(d)
		}
		claudeArgs = sequenceStrings(mapGet(cli, "claudeArgs"))
		codexArgs = sequenceStrings(mapGet(cli, "codexArgs"))
	}

	if len(claudeArgs) > 0 {
		if claude == nil {
			claude = newMapping()
		}
		mapSet(claude, "args", scalarStringSlice(claudeArgs))
	}

	var codex *yaml.Node
	if len(codexArgs) > 0 {
		codex = newMapping()
		mapSet(codex, "args", scalarStringSlice(codexArgs))
	}

	if defaultAgent == "" && claude == nil && codex == nil {
		return nil
	}

	agent := newMapping()
	if defaultAgent != "" {
		mapSet(agent, "default", scalarStr(defaultAgent))
	}
	if claude != nil {
		mapSet(agent, "claude", claude)
	}
	if codex != nil {
		mapSet(agent, "codex", codex)
	}
	return agent
}

// buildNotification assembles spec.notification from the cli.notify* / *slack*
// fields. Returns nil if there is nothing to emit.
func buildNotification(cli *yaml.Node) *yaml.Node {
	if cli == nil {
		return nil
	}

	events := newMapping()
	if v := extractScalarPtr(cli, "notifyOnPermission"); v != nil {
		mapSet(events, "onPermission", scalarBool(*v))
	}
	if v := extractScalarPtr(cli, "notifyOnIdle"); v != nil {
		mapSet(events, "onIdle", scalarBool(*v))
	}
	if v := mapGet(cli, "notifyCooldownSeconds"); v != nil {
		mapSet(events, "cooldownSeconds", scalarRaw(v))
	}

	email := newMapping()
	if v := extractScalarPtr(cli, "notifyEmailEnabled"); v != nil {
		mapSet(email, "enabled", scalarBool(*v))
	}
	if v := mapGet(cli, "notificationEmailAddress"); v != nil {
		mapSet(email, "address", scalarStr(scalar(v)))
	}

	slack := buildSlack(cli)

	notification := newMapping()
	if len(events.Content) > 0 {
		mapSet(notification, "events", events)
	}
	if len(email.Content) > 0 {
		mapSet(notification, "email", email)
	}
	if slack != nil {
		mapSet(notification, "slack", slack)
	}
	if len(notification.Content) == 0 {
		return nil
	}
	return notification
}

func buildSlack(cli *yaml.Node) *yaml.Node {
	slack := newMapping()
	if v := extractScalarPtr(cli, "notifySlackEnabled"); v != nil {
		mapSet(slack, "enabled", scalarBool(*v))
	}
	if v := extractScalarPtr(cli, "notifySlackPerSandbox"); v != nil {
		mapSet(slack, "perSandbox", scalarBool(*v))
	}
	if v := mapGet(cli, "notifySlackChannelOverride"); v != nil {
		mapSet(slack, "channelOverride", scalarStr(scalar(v)))
	}
	if v := extractScalarPtr(cli, "slackArchiveOnDestroy"); v != nil {
		mapSet(slack, "archiveOnDestroy", scalarBool(*v))
	}

	// inbound sub-block
	inbound := newMapping()
	if v := extractScalarPtr(cli, "notifySlackInboundEnabled"); v != nil {
		mapSet(inbound, "enabled", scalarBool(*v))
	}
	if v := extractScalarPtr(cli, "notifySlackInboundMentionOnly"); v != nil {
		mapSet(inbound, "mentionOnly", scalarBool(*v))
	}
	if v := extractScalarPtr(cli, "notifySlackInboundReactAlways"); v != nil {
		mapSet(inbound, "reactAlways", scalarBool(*v))
	}
	if len(inbound.Content) > 0 {
		mapSet(slack, "inbound", inbound)
	}

	// transcript sub-block
	if v := extractScalarPtr(cli, "notifySlackTranscriptEnabled"); v != nil {
		transcript := newMapping()
		mapSet(transcript, "enabled", scalarBool(*v))
		mapSet(slack, "transcript", transcript)
	}

	// invites sub-block
	invites := newMapping()
	if v := mapGet(cli, "notifySlackInviteEmails"); v != nil && v.Kind == yaml.SequenceNode {
		mapSet(invites, "emails", scalarRaw(v))
	}
	if v := extractScalarPtr(cli, "useSlackConnect"); v != nil {
		mapSet(invites, "useConnect", scalarBool(*v))
	}
	if len(invites.Content) > 0 {
		mapSet(slack, "invites", invites)
	}

	if len(slack.Content) == 0 {
		return nil
	}
	return slack
}

// ---- yaml.Node helpers ----

func newMapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

func scalarStr(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func scalarBool(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

// scalarRaw clones an existing node (used to carry sequences/scalars verbatim).
func scalarRaw(n *yaml.Node) *yaml.Node { return n }

func scalarStringSlice(ss []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, s := range ss {
		seq.Content = append(seq.Content, scalarStr(s))
	}
	return seq
}

// mapGet returns the value node for key in a mapping node, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet sets key=val, replacing an existing entry or appending a new one.
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalarStr(key), val)
}

// mapDelete removes key (and its value) from a mapping node.
func mapDelete(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// renameKey renames a mapping key in place, preserving position and value node.
func renameKey(m *yaml.Node, oldKey, newKey string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == oldKey {
			m.Content[i].Value = newKey
			m.Content[i].Tag = "!!str"
			return
		}
	}
}

func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

// extractScalarPtr reads a boolean-valued scalar key into a *bool (nil if absent).
func extractScalarPtr(m *yaml.Node, key string) *bool {
	n := mapGet(m, key)
	if n == nil || n.Kind != yaml.ScalarNode {
		return nil
	}
	b := n.Value == "true"
	return &b
}

// sequenceStrings reads a sequence node of scalars into []string.
func sequenceStrings(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// anyToNode marshals an arbitrary Go value into a yaml.Node directly, sorting
// map keys for deterministic output.
func anyToNode(v any) (*yaml.Node, error) {
	return valueToNode(v), nil
}

// valueToNode recursively builds a yaml.Node from a decoded JSON/Go value,
// sorting map keys so passthrough output is deterministic.
func valueToNode(v any) *yaml.Node {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		n := newMapping()
		for _, k := range keys {
			n.Content = append(n.Content, scalarStr(k), valueToNode(x[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range x {
			n.Content = append(n.Content, valueToNode(e))
		}
		return n
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	default:
		// Encode scalars (string/bool/float/int) via yaml round-trip to get the
		// correct tag and rendering.
		b, err := yaml.Marshal(v)
		if err == nil {
			var doc yaml.Node
			if yaml.Unmarshal(b, &doc) == nil && doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
				return doc.Content[0]
			}
		}
		return scalarStr(fmt.Sprintf("%v", v))
	}
}
