package compiler

import (
	"regexp"
	"strings"
	"testing"
)

// A {prefix}-scoped IAM grant cannot cover a literal km-* table on a non-km
// install; the Allow-star used to hide that. Resource names derived from
// KM_RESOURCE_PREFIX must fail loudly when it is unset rather than default to km.
func TestUserdata_NoLiteralKmResourceNameFallback(t *testing.T) {
	re := regexp.MustCompile(`\$\{KM_RESOURCE_PREFIX:-km\}`)
	for i, line := range strings.Split(userDataTemplate, "\n") {
		if re.MatchString(line) {
			t.Errorf("template line %d defaults the resource prefix to km: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// Every unit or script that derives a name from KM_RESOURCE_PREFIX must receive it.
func TestUserdata_PollersReceiveResourcePrefix(t *testing.T) {
	for _, needle := range []string{
		`GITHUB_THREADS_TABLE="${KM_GITHUB_THREADS_TABLE:-${KM_RESOURCE_PREFIX:?`,
		`H1_THREADS_TABLE="${KM_H1_THREADS_TABLE:-${KM_RESOURCE_PREFIX:?`,
		`IDENTITIES_TABLE="${KM_IDENTITIES_TABLE:-${KM_RESOURCE_PREFIX:?`,
	} {
		if !strings.Contains(userDataTemplate, needle) {
			t.Errorf("template missing the fail-loud form: %s", needle)
		}
	}
	for _, unit := range []string{"SLACKINBOUNDUNIT", "GITHUBINBOUNDUNIT", "H1INBOUNDUNIT"} {
		i := strings.Index(userDataTemplate, "<< '"+unit+"'")
		if i < 0 {
			t.Fatalf("unit %s not found", unit)
		}
		end := strings.Index(userDataTemplate[i+10:], "\n"+unit)
		if !strings.Contains(userDataTemplate[i:i+10+end], "Environment=KM_RESOURCE_PREFIX={{ .ResourcePrefix }}") {
			t.Errorf("unit %s lacks Environment=KM_RESOURCE_PREFIX", unit)
		}
	}
}
