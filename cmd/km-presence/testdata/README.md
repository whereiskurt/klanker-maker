# Herdr API fixtures for km-presence signal 8

Captured live from herdr **0.8.2** on sandbox `learn-e311c3c9`, 2026-09-04, driving a
headless `herdr server` over SSM. No interactive client was ever attached — which is
exactly the detached state signal 8 exists to detect.

| file | state |
|---|---|
| `herdr_process_info_idle.json` | pane sitting at a bare `/bin/sh` |
| `herdr_process_info_busy.json` | same pane running `sleep 900` |
| `herdr_pane_list.json`         | `pane list` output (identical in both states) |
| `herdr_agent_list_empty.json`  | `agent list` with no recognised agent |

## §6.4 decision: **option 3-ish — one `process-info` call per pane**

Determined by measurement, not documentation:

1. **`pane list` cannot work.** Its output is byte-identical busy vs idle — it carries
   `agent_status`, `cwd`, `foreground_cwd`, `pane_id`, `terminal_id` and no process data
   at all. Verified by capturing both states.
2. **`agent list` cannot work alone.** It returns `agents: []` for a plain `sleep`, since
   Herdr only lists *recognised agents*. Signal 8 must be agent-agnostic, which is the
   whole reason it exists.
3. **`pane process-info --pane <id>` is the only source**, and it is a separate call per
   pane. So signal 8 costs 1 (`pane list`) + N (`process-info`) subprocesses per 60s tick.

## The discriminator — and the trap

**busy ⟺ `foreground_process_group_id != shell_pid`.**

`foreground_processes` is **NON-EMPTY EVEN WHEN IDLE** — it contains the pane's own shell
(`{"name":"sh","argv":["/bin/sh"],"pid":<shell_pid>}`). A `len(foreground_processes) > 0`
check is therefore **ALWAYS TRUE**, which would make signal 8 permanently positive and
silently disable idle teardown fleet-wide — the exact `pgrep vscode-server` failure the
design set out to avoid. The plan originally specified that check. It was caught only by
capturing a real idle pane.

Note also the CLI surface differs from the published docs:
- there is **no `--json` flag**; these commands emit JSON by default
- responses are **wrapped**: `{"id":..., "result":{...}, "type":...}`, not bare arrays
- `process-info` takes **`--pane <ID>`**, not a positional argument


## Verified invocation syntax

The fixtures above record response *bodies*. These are the exact commands that produced them,
run **as root** on the live sandbox — which is how km-presence runs. Both forms were executed and
their output captured; neither is inferred from documentation.

```sh
SOCK=/home/sandbox/.config/herdr/herdr.sock

# enumerate panes  -> herdr_pane_list.json
runuser -u sandbox -- bash -lc "HERDR_SOCKET_PATH=\"$SOCK\" herdr pane list"

# per-pane process state -> herdr_process_info_{busy,idle}.json
runuser -u sandbox -- bash -lc "HERDR_SOCKET_PATH=\"$SOCK\" herdr pane process-info --pane w1:p1"
```

Notes, each established by running the wrong form first:

- **The session is selected by the `HERDR_SOCKET_PATH` environment variable, not a `--socket`
  flag.** `commandRunner` has no env support, so the assignment goes inside the `bash -lc` string.
- **There is no `--json` flag.** `herdr pane list --json` fails with `unknown option: --json`;
  JSON is the default output.
- **`process-info` takes `--pane <ID>`.** A positional (`herdr pane process-info w1:p1`) fails with
  `unknown option: w1:p1`.
- **`bash -lc` is mandatory, not stylistic.** `base/userinit.yaml` installs herdr to
  `/home/sandbox/.local/bin/herdr`, and root's PATH does not include it. Measured:
  `runuser -u sandbox -- herdr pane list` → `runuser: failed to execute herdr: No such file or
  directory`. A non-login shell therefore makes signal 8 fail idle **permanently** — and the
  negative-case test passes against a permanently-false signal, so no test would catch it.
- **Pane ids look like `w1:p1`** and come from `pane_id` in the `pane list` response (not `id`).
- herdr also creates `herdr-client.sock` beside `herdr.sock`; only `herdr.sock` speaks this API.
