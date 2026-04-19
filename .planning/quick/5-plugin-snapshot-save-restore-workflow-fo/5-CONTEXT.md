# Quick Task 5: Plugin snapshot save/restore workflow — Context

**Gathered:** 2026-04-19
**Status:** Ready for planning

<domain>
## Task Boundary

Enable operators to snapshot Claude Code plugins from a sandbox and restore them into future sandboxes via the existing rsync mechanism. The workflow is: boot a learn sandbox, install plugins interactively, save the snapshot, then reference it in future profiles.

</domain>

<decisions>
## Implementation Decisions

### Snapshot scope
- Use existing `rsyncPaths` mechanism — no new flags or subcommands
- Operator sets `rsyncPaths: [".claude/plugins"]` in their profile to scope snapshots to just plugins
- Zero code changes for the save/restore pipeline itself

### Path rewriting on restore
- Add a post-restore step in the bootstrap userdata that rewrites absolute paths in `installed_plugins.json`
- Needed because plugins installed on a sandbox at `/home/sandbox/.claude/plugins/cache/...` may be restored to a sandbox with a different home directory
- Use sed to replace any `/home/*/` or `/Users/*/` prefix with the actual `$SHELL_HOME/`

### Claude's Discretion
- The path rewrite should be safe/idempotent — only touch `installed_plugins.json` if it exists
- Keep the rewrite in the existing bootstrap rsync section (after tar extract, before initCommands)

</decisions>

<specifics>
## Specific Ideas

- The rewrite targets `installPath` and `installLocation` fields in `installed_plugins.json` and `known_marketplaces.json`
- Pattern: replace any absolute path prefix up to `.claude/` with `$SHELL_HOME/.claude/`
- This should work for both Mac→sandbox and sandbox→sandbox transfers

</specifics>
