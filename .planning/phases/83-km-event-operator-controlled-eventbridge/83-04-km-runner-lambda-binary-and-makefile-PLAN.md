---
phase: 83-km-event-operator-controlled-eventbridge
plan: 04
type: execute
wave: 2
depends_on: [01, 02]
files_modified:
  - cmd/km-runner/main.go
  - cmd/km-runner/handler.go
  - Makefile
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "cmd/km-runner/main.go contains a Lambda handler that accepts events.CloudWatchEvent, unmarshals event.Detail into RunnerEvent{Command string}, splits Command into argv, execs the baked-in km binary, captures stdout+stderr into CloudWatch"
    - "Wave 0 TestKMRunner_ValidCommand and TestKMRunner_MissingCommand move from SKIP to PASS"
    - "Makefile has a `build-km-runner` target (or `build-lambdas` includes km-runner) that produces build/km-runner.zip containing both `bootstrap` (Lambda entrypoint) and `km` (operator CLI) — both compiled for linux/arm64 with -ldflags '$(LDFLAGS)' (version embed)"
    - "build/km-runner.zip exists after `make build-km-runner` runs; `unzip -l build/km-runner.zip` lists both `bootstrap` and `km` files"
    - "Handler validates that event.Detail contains a `command` field; missing field returns a structured error logged before return (Lambda runtime turns this into a failed invocation, which surfaces in CloudWatch metrics)"
    - "Handler has a 5-minute execution budget (matches Lambda timeout 900s minus exec startup overhead); commands that exceed are terminated with logged context"
  artifacts:
    - path: "cmd/km-runner/main.go"
      provides: "Lambda entrypoint: lambda.Start(KMRunner{}.Handle)"
      contains: "lambda.Start"
    - path: "cmd/km-runner/handler.go"
      provides: "KMRunner type with Handle(ctx, events.CloudWatchEvent) error method; testable via injected exec.CommandContext seam"
      contains: "type KMRunner struct"
    - path: "Makefile"
      provides: "build-km-runner target producing build/km-runner.zip"
      contains: "build-km-runner"
  key_links:
    - from: "cmd/km-runner/handler.go"
      to: "the baked-in km binary at /var/task/km inside Lambda"
      via: "exec.CommandContext(ctx, \"/var/task/km\", argv...)"
      pattern: "/var/task/km|os.Executable"
    - from: "Makefile build-km-runner target"
      to: "infra/modules/operator-event-bus/v1.0.0/ (consumed by Plan 83-05's km init wiring)"
      via: "build/km-runner.zip is what Plan 83-03's lambda_zip_path variable points at"
      pattern: "km-runner.zip"
---

<objective>
Build the km-runner Lambda — a tiny Go program that reads a `command` string from an EventBridge event,
exec()s the km binary baked into the same Lambda zip, and surfaces stdout+stderr to CloudWatch.

Purpose: Make Plan 83-03's operator-event-bus module deployable. The module references `var.lambda_zip_path`;
this plan produces that zip. Once both are in place, `km init` (Plan 83-06) can apply the bus module and
Plan 83-05's `km event add` has a real target ARN to wire rules to.

Output:
- cmd/km-runner/ Go package with main.go (lambda.Start) and handler.go (testable Handle method)
- Makefile target that compiles km + km-runner for linux/arm64 and zips them together
- Wave 0 km-runner tests now PASS (assert valid input parses, missing command errors)
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-CONTEXT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-RESEARCH.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-VALIDATION.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-01-SUMMARY.md
@Makefile
@cmd/create-handler/main.go
@cmd/km-slack-bridge/main.go

<interfaces>
Per Wave 0 (83-01) the km-runner tests target:

```go
// cmd/km-runner/handler.go (this plan creates):

type RunnerEvent struct {
    Command string `json:"command"`
}

type KMRunner struct {
    KMBinaryPath string                                                // default "/var/task/km"
    ExecCommand  func(ctx context.Context, name string, args ...string) *exec.Cmd  // test seam, defaults to exec.CommandContext
    Now          func() time.Time                                     // test seam
}

func (h *KMRunner) Handle(ctx context.Context, ebEvent events.CloudWatchEvent) error
```

Test expectations (from Wave 0 cmd/km-runner/main_test.go):

```go
// TestKMRunner_ValidCommand:
//   - Inject KMRunner{KMBinaryPath: "/bin/echo", ExecCommand: exec.CommandContext}
//   - Build events.CloudWatchEvent with Detail = json.RawMessage(`{"command":"hello world"}`)
//   - Call Handle(ctx, ev) → nil error
//   - (Optional) Capture stdout via a piped exec to confirm "hello world" was logged

// TestKMRunner_MissingCommand:
//   - Build events.CloudWatchEvent with Detail = json.RawMessage(`{}`)
//   - Call Handle(ctx, ev) → error containing "command"
```

Reference existing Lambdas:
- cmd/create-handler/main.go — pattern for lambda.Start(handler.Handle), zerolog setup, ctx handling
- cmd/km-slack-bridge/main.go — pattern for cleaner handler injection (no S3 toolchain download, just bake binaries)

Makefile reference (find `build-lambdas` target — likely at line ~150 per 83-RESEARCH.md):

```makefile
# Existing pattern (paraphrased) for building a Lambda:
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/{name}/
cd build && zip -j {name}.zip bootstrap && rm bootstrap
```

km-runner adds a step: ALSO build the km binary into build/km and zip both. The Lambda /var/task/ layout will be `bootstrap` + `km` side by side. AWS Lambda treats `bootstrap` as the entrypoint (provided.al2023 runtime).
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Implement cmd/km-runner/ handler + main, make Wave 0 km-runner tests PASS</name>
  <files>cmd/km-runner/main.go, cmd/km-runner/handler.go</files>
  <behavior>
    - cmd/km-runner/main.go has the standard Lambda entrypoint:
      ```go
      package main
      import (
        "github.com/aws/aws-lambda-go/lambda"
        // ...
      )
      func main() {
        runner := &KMRunner{
          KMBinaryPath: "/var/task/km",
          ExecCommand:  exec.CommandContext,
          Now:          time.Now,
        }
        lambda.Start(runner.Handle)
      }
      ```
    - cmd/km-runner/handler.go defines KMRunner type + Handle method:
      - Unmarshal event.Detail into RunnerEvent
      - If Command is empty → return fmt.Errorf("km-runner: event.detail.command is required (got %q)", string(event.Detail))
      - Split Command using shellwords-style split — use `mvdan.cc/sh/v3/shell` if already in go.mod, ELSE use `strings.Fields(Command)` for v1 (simple whitespace split; supports the common case `doctor --all-regions`). Document the strings.Fields choice in a // FIXME comment for later upgrade.
      - exec.CommandContext using h.ExecCommand, args = strings.Fields(Command)
      - Capture stdout + stderr by setting cmd.Stdout = os.Stdout, cmd.Stderr = os.Stderr (Lambda runtime captures both → CloudWatch)
      - Run cmd, return wrapped error on non-zero exit including the exit code
    - Wave 0 tests in cmd/km-runner/main_test.go: remove t.Skip lines, write the test bodies per the <interfaces> contract.
    - For TestKMRunner_ValidCommand, use `/bin/echo` as the KMBinaryPath so the test runs anywhere without baking km — assert nil error and (optionally) cmd.Run() worked. To avoid stdout pollution in test output, capture via cmd.Stdout = &bytes.Buffer{} via a small refactor: KMRunner gains optional Stdout/Stderr io.Writer fields (default os.Stdout / os.Stderr); tests inject buffers.
    - For TestKMRunner_MissingCommand, send `{}` (no command field) → assert err != nil and strings.Contains(err.Error(), "command").

    AVOID:
    - Implementing shell-quote parsing in v1 — strings.Fields is sufficient for `doctor --all-regions`. Operators wanting quoted args should use --hcl escape hatch (per CONTEXT.md).
    - Re-downloading km from S3 at cold start — km is baked into the zip at /var/task/km (Task 2's Makefile does this)
    - Hardcoding stdout/stderr — use Writer fields for testability
    - Using zerolog inside Handle — Lambda captures stdout/stderr natively; double-logging is overkill for v1. A single zerolog message at the top of main() is fine ("km-runner starting; binary=%s", h.KMBinaryPath)

    REFERENCES:
    - cmd/create-handler/main.go lines 135-140 — Handle pattern with events.CloudWatchEvent
    - cmd/km-slack-bridge/main.go — pattern for keeping the Lambda lean
    - Wave 0 cmd/km-runner/main_test.go (created in Plan 83-01) — enumerates exact assertions
  </behavior>
  <action>
    1. Read cmd/km-runner/main_test.go (Wave 0 scaffold) to confirm the test interface expectations.
    2. Read cmd/create-handler/main.go for the lambda.Start pattern + zerolog setup.
    3. Check if mvdan.cc/sh is in go.mod: `grep "mvdan.cc/sh" go.mod`. If absent, use strings.Fields (simpler, no new dep).
    4. Write cmd/km-runner/handler.go containing:
       - RunnerEvent struct with Command string and json:"command" tag
       - KMRunner struct with KMBinaryPath, ExecCommand seam, Now seam, Stdout io.Writer, Stderr io.Writer fields
       - Constructor `NewKMRunner()` returning &KMRunner{...} with sane defaults
       - Handle(ctx, event) method per <behavior>
    5. Write cmd/km-runner/main.go with the lambda.Start invocation.
    6. Remove the doc.go stub from Wave 0 if it was created (since main.go now provides the package). If main.go's package is "main" (which it is for Lambda binaries), check that test file matches.
    7. Replace t.Skip in cmd/km-runner/main_test.go's two tests with real implementations using bytes.Buffer to capture stdout, /bin/echo as the test binary path.
    8. Run `go build ./cmd/km-runner/...`. Must succeed.
    9. Run `go test ./cmd/km-runner/... -v -count=1`. Both tests must PASS.
    10. Run `go vet ./cmd/km-runner/...`. Must exit 0.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go build ./cmd/km-runner/... && go test ./cmd/km-runner/... -v -count=1 2>&1 | grep -E "(PASS|FAIL|TestKMRunner_ValidCommand|TestKMRunner_MissingCommand)" && go vet ./cmd/km-runner/...</automated>
  </verify>
  <done>
    cmd/km-runner/ contains main.go + handler.go. `go build ./cmd/km-runner/...` succeeds. TestKMRunner_ValidCommand and TestKMRunner_MissingCommand both PASS (not SKIP, not FAIL). `go vet ./cmd/km-runner/...` clean. The handler properly handles empty Command and exec failures.
  </done>
</task>

<task type="auto">
  <name>Task 2: Add `build-km-runner` Makefile target producing build/km-runner.zip with both bootstrap + km</name>
  <files>Makefile</files>
  <behavior>
    - Locate the existing `build-lambdas` target in Makefile (per 83-RESEARCH.md, around line 150).
    - Add a new target `build-km-runner` that:
      - Compiles `./cmd/km-runner/` for linux/arm64 with -ldflags '$(LDFLAGS)' into build/km-runner-bootstrap (named bootstrap inside the zip, hence the rename step)
      - Compiles `./cmd/km/` for linux/arm64 with -ldflags '$(LDFLAGS)' into build/km-runner-km (named km inside the zip)
      - Zips both into build/km-runner.zip with the names `bootstrap` and `km` at the zip root
      - Cleans up the intermediate files (rm -f build/km-runner-bootstrap build/km-runner-km after zipping)
    - Add `build-km-runner` to the dependency chain of `build-lambdas` (so `make build` continues to produce all Lambda zips in one shot).
    - Document the target with a comment block above it: `# build-km-runner — produces build/km-runner.zip containing the km-runner Lambda entrypoint AND the km binary used to exec operator commands (Phase 83)`

    Example Makefile snippet:
    ```makefile
    # build-km-runner — produces build/km-runner.zip containing both bootstrap (Lambda entrypoint)
    # and km (operator CLI baked in). The Lambda execs /var/task/km on each invocation. (Phase 83)
    .PHONY: build-km-runner
    build-km-runner:
    	mkdir -p build
    	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km-runner-bootstrap ./cmd/km-runner/
    	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km-runner-km ./cmd/km/
    	cd build && cp km-runner-bootstrap bootstrap && cp km-runner-km km && zip -j km-runner.zip bootstrap km && rm -f bootstrap km km-runner-bootstrap km-runner-km
    ```

    AVOID:
    - Stripping symbols beyond what other Lambdas do — preserve the -ldflags '$(LDFLAGS)' for the version embed
    - Forgetting to mkdir -p build (CI may run from a clean tree)
    - Putting the zip target in the default `build` target — it belongs in `build-lambdas` (operator runs `make build-lambdas` separately from `make build` per project convention; check the existing target for the right chain)

    REFERENCES:
    - Makefile existing `build-lambdas` target — get the exact LDFLAGS variable name + the zip pattern (zip -j name.zip bootstrap)
    - 83-RESEARCH.md "Lambda build entry in Makefile" snippet (lines 570-580 of RESEARCH.md)
  </behavior>
  <action>
    1. Read Makefile to locate the existing build-lambdas target and the LDFLAGS variable.
    2. Identify where to insert the new target — alongside other lambda-build targets if they exist as separate phony rules, OR inline within build-lambdas if all Lambdas share one target.
    3. Add the build-km-runner target per <behavior>. Wire it into the build-lambdas chain (either add as a dependency: `build-lambdas: build-create-handler build-ttl-handler ... build-km-runner` or call its commands inline if that's the project convention).
    4. Run `make build-km-runner` from the repo root. Must produce build/km-runner.zip with no errors.
    5. Run `unzip -l build/km-runner.zip` and confirm both `bootstrap` AND `km` appear in the listing (no path prefix, just file names at zip root — that's what the -j flag ensures).
    6. Run `make build` (the default target) — must continue to succeed and now produce km-runner.zip alongside other zips (if build-km-runner was wired into the default chain).

    AVOID:
    - Editing the LDFLAGS variable — use $(LDFLAGS) verbatim
    - Renaming the bootstrap inside the zip — AWS Lambda's provided.al2023 runtime expects `bootstrap` as the entrypoint name; cannot rename
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && make build-km-runner 2>&1 | tail -20 && test -f build/km-runner.zip && unzip -l build/km-runner.zip | grep -E "(bootstrap|km)" | wc -l</automated>
  </verify>
  <done>
    `make build-km-runner` exits 0. build/km-runner.zip exists. `unzip -l build/km-runner.zip` lists both `bootstrap` and `km` at the zip root (count ≥ 2). The zip is consumable by Plan 83-03's lambda_zip_path variable in the operator-event-bus module.
  </done>
</task>

</tasks>

<verification>
- `go build ./cmd/km-runner/...` exits 0
- `go test ./cmd/km-runner/... -v -count=1` exits 0; both Wave 0 tests PASS
- `make build-km-runner` exits 0 and produces build/km-runner.zip
- `unzip -l build/km-runner.zip` shows `bootstrap` + `km` at root
- No regression in other Makefile targets — `make build` continues to succeed
</verification>

<success_criteria>
- km-runner Lambda binary builds locally and as a Lambda zip
- Wave 0 km-runner tests have been promoted from SKIP to PASS
- Plan 83-03's lambda_zip_path variable has a concrete file to consume
- Plan 83-05's `km event add --target-km` will route to this Lambda's ARN once the bus module is applied (Plan 83-06)
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-04-SUMMARY.md` documenting:
- The exact KMRunner struct fields and their default values (for Plan 83-05 / future debugging)
- The Makefile chain (which targets call build-km-runner, in what order)
- Whether strings.Fields was sufficient for v1 or if mvdan.cc/sh was wired in (decision documented for future plans)
- The exact size of build/km-runner.zip (sanity check; should be ~25-40 MB for arm64 binaries with full km surface)
</output>
</content>
