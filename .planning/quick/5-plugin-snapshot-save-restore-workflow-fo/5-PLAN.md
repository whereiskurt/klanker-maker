---
phase: quick-5
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - pkg/compiler/userdata.go
  - profiles/learn.yaml
autonomous: true
must_haves:
  truths:
    - "Restored plugin snapshots have correct paths regardless of source sandbox home directory"
    - "Learn-mode profile scopes rsync to .claude/plugins for plugin-only snapshots"
  artifacts:
    - path: "pkg/compiler/userdata.go"
      provides: "Post-restore path rewrite for plugin JSON files"
      contains: "installed_plugins.json"
    - path: "profiles/learn.yaml"
      provides: "rsyncPaths includes .claude/plugins"
      contains: ".claude/plugins"
  key_links:
    - from: "pkg/compiler/userdata.go"
      to: "bootstrap rsync restore"
      via: "sed rewrite after tar extract"
      pattern: "sed.*installed_plugins"
---

<objective>
Add post-restore path rewriting to the rsync bootstrap so Claude plugin snapshots work across sandboxes with different home directories. Also update learn.yaml rsyncPaths.

Purpose: Plugin JSON files contain hardcoded absolute paths from the source sandbox. When restored to a sandbox with a different home dir, Claude cannot find the plugins. The sed rewrite fixes this.
Output: Updated userdata.go bootstrap template and learn.yaml profile.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@pkg/compiler/userdata.go (lines 1553-1565 — rsync restore section)
@profiles/learn.yaml
</context>

<tasks>

<task type="auto">
  <name>Task 1: Add post-restore path rewrite to bootstrap userdata</name>
  <files>pkg/compiler/userdata.go</files>
  <action>
In pkg/compiler/userdata.go, in the rsync restore block (around line 1553-1565), after the `chown -R` line and before the echo success line, add a path rewrite step inside the `&& { }` block:

```bash
  # Rewrite absolute paths in Claude plugin manifests to match this sandbox's home
  for _pf in "$SHELL_HOME/.claude/plugins/installed_plugins.json" "$SHELL_HOME/.claude/plugins/known_marketplaces.json"; do
    [ -f "$_pf" ] && sed -i 's|"installPath":"\(/[^"]*\)/\.claude/|"installPath":"'"$SHELL_HOME"'/.claude/|g; s|"installLocation":"\(/[^"]*\)/\.claude/|"installLocation":"'"$SHELL_HOME"'/.claude/|g' "$_pf" && echo "[km-bootstrap] Rewrote plugin paths in $_pf"
  done
```

Key details:
- The sed pattern matches any absolute path prefix up to `/.claude/` and replaces with `$SHELL_HOME/.claude/`
- Handles both `installPath` (installed_plugins.json) and `installLocation` (known_marketplaces.json)
- Only runs if the file exists (idempotent/safe)
- Must be inside the existing `&& { }` block so it only runs on successful tar extract
- Note: the JSON files use no spaces around colons (compact JSON), so match `"installPath":"` not `"installPath": "`
- Actually check the JSON format — if it uses spaces, adjust the sed accordingly. The sed should handle both with and without spaces: use `"installPath"\s*:\s*"` — but since this is bash sed, just do two patterns or use a flexible match like `"installPath" *: *"`

Simpler approach — just match the value portion regardless of key formatting:

```bash
  for _pf in "$SHELL_HOME/.claude/plugins/installed_plugins.json" "$SHELL_HOME/.claude/plugins/known_marketplaces.json"; do
    [ -f "$_pf" ] && sed -i -E 's|(/[^"]*)/\.claude/|'"$SHELL_HOME"'/.claude/|g' "$_pf" && echo "[km-bootstrap] Rewrote plugin paths in $_pf"
  done
```

This is cleaner — it replaces ANY absolute path ending in `/.claude/` with `$SHELL_HOME/.claude/`, which handles both fields and any JSON formatting. Use this approach.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go build ./pkg/compiler/</automated>
  </verify>
  <done>The rsync restore block in userdata.go includes a post-extract sed step that rewrites absolute paths in installed_plugins.json and known_marketplaces.json to use the target sandbox's $SHELL_HOME. The Go template compiles without errors.</done>
</task>

<task type="auto">
  <name>Task 2: Add .claude/plugins to learn.yaml rsyncPaths</name>
  <files>profiles/learn.yaml</files>
  <action>
In profiles/learn.yaml, the `rsyncPaths` list already includes `.claude` (which covers `.claude/plugins`). Check if a more specific `.claude/plugins` entry is still desired for documentation clarity.

Looking at the current rsyncPaths:
```yaml
    rsyncPaths:
      - ".gitconfig"
      - ".config/goose"
      - ".claude"
      - ".claude.json"
      - ".codex"
```

Since `.claude` already covers `.claude/plugins`, no change is needed to learn.yaml for rsync to work. The existing config already snapshots the full `.claude` directory including plugins.

However, if the user wants a SEPARATE plugin-only profile or a note in learn.yaml, add a YAML comment above the rsyncPaths block:

```yaml
    # For plugin-only snapshots, use rsyncPaths: [".claude/plugins"] in your profile
    rsyncPaths:
```

This is a documentation-only change. Do NOT remove `.claude` from the list — it's needed for the full learn workflow.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && km validate profiles/learn.yaml 2>&1 | head -5</automated>
  </verify>
  <done>learn.yaml has a comment noting the plugin-only rsyncPaths pattern, and continues to validate successfully.</done>
</task>

</tasks>

<verification>
- `go build ./pkg/compiler/` compiles without errors
- `km validate profiles/learn.yaml` passes
- Visual inspection: the sed rewrite appears in the rsync restore block after tar extract and chown
</verification>

<success_criteria>
- Bootstrap userdata template rewrites plugin absolute paths on restore
- The rewrite is idempotent and only touches files that exist
- learn.yaml validates and documents the plugin-only rsyncPaths pattern
</success_criteria>

<output>
After completion, create `.planning/quick/5-plugin-snapshot-save-restore-workflow-fo/5-SUMMARY.md`
</output>
