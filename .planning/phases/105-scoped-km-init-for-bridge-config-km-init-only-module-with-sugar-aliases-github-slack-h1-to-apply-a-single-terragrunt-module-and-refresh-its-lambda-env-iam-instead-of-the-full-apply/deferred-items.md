
## Plan 04 Deferred

### Pre-existing: TestRunInitPlan_ModuleOrder expects 17 modules but regionalModules() returns 22

**Found during:** Plan 04 full suite run
**Scope:** Pre-existing test constant drift — `allModuleNames` in init_plan_test.go line 133 lists 17 modules; `regionalModules()` now returns 22 (5 modules added since the test was written). NOT introduced by Plan 04 changes.
**Fix:** Update `allModuleNames` slice to match current `regionalModules()` output. One-line fix in init_plan_test.go.
**Files affected:** `internal/app/cmd/init_plan_test.go`
