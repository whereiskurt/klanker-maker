package main

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// ttlHandlerModuleMainTF is the live Terraform module backing this Lambda.
const ttlHandlerModuleMainTF = "../../infra/modules/ttl-handler/v1.0.0/main.tf"

// Every DynamoDB table the ttl-handler is TOLD about (handed to it as a
// KM_*_TABLE environment variable) must also be granted to its role somewhere
// in the same module. Env without IAM is the drift shape that has now bitten
// this Lambda three separate times, and it is invisible at runtime because
// every one of those call sites treats its own failure as non-fatal:
//
//   - km-budgets:   RecordPauseStart's error is a log.Warn "(non-fatal)", so
//                   idle-stops silently stopped excluding paused wall-clock
//                   from compute spend.
//   - km-budgets:   handleBudgetAdd — scheduled `km at ... budget-add`.
//   - km-schedules: handleCreate's PutSchedule error was discarded outright,
//                   so a deferred create fired correctly but never appeared in
//                   `km at list`.
//
// This test makes the pairing mechanical rather than remembered. It is
// deliberately name-agnostic: it compares the SET of var.*_table_name
// references in the environment block against the SET used in IAM resource
// ARNs, so a table added later is covered without editing this test.
func TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant(t *testing.T) {
	raw, err := os.ReadFile(ttlHandlerModuleMainTF)
	if err != nil {
		t.Fatalf("read %s: %v", ttlHandlerModuleMainTF, err)
	}
	tf := string(raw)

	// Table vars handed to the Lambda as environment: `KM_..._TABLE... = var.x_table_name`.
	envRe := regexp.MustCompile(`KM_[A-Z_]*TABLE[A-Z_]*\s*=\s*var\.([a-z_]+_table_name)`)
	// Table vars named inside a DynamoDB IAM resource ARN: `:table/${var.x_table_name}`.
	iamRe := regexp.MustCompile(`:table/\$\{var\.([a-z_]+_table_name)\}`)

	envTables := map[string]bool{}
	for _, m := range envRe.FindAllStringSubmatch(tf, -1) {
		envTables[m[1]] = true
	}
	iamTables := map[string]bool{}
	for _, m := range iamRe.FindAllStringSubmatch(tf, -1) {
		iamTables[m[1]] = true
	}

	if len(envTables) == 0 {
		t.Fatalf("found no KM_*TABLE env wiring in %s — the regex has drifted from the module", ttlHandlerModuleMainTF)
	}

	var missing []string
	for name := range envTables {
		if !iamTables[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("ttl-handler is given these DynamoDB tables as env but has no IAM grant for them: %v\n"+
			"Add an aws_iam_role_policy in %s whose Resource is \"...:table/${var.<name>}\". "+
			"Without it the handler 403s at runtime, and the call sites log non-fatally or discard "+
			"the error entirely, so nothing surfaces.", missing, ttlHandlerModuleMainTF)
	}
}
