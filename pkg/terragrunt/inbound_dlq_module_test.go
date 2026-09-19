package terragrunt_test

// Every shared inbound DLQ that pkg/aws names must actually be created by the
// pinned sqs-inbound-dlq module. The names are a CONTRACT, not a reference:
// internal/app/cmd/create_<kind>_inbound.go derives the DLQ ARN from
// <Kind>InboundDLQName(prefix) via pkg/aws.DLQArn and never reads a module
// output — and SQS validates a RedrivePolicy's deadLetterTargetArn at
// CreateQueue time, not at redrive time. So a DLQ that nothing provisions is
// not a missing safety net; it is a hard 400 on the very first per-sandbox
// queue, AFTER the EC2 apply has already launched a billable instance. That is
// exactly how the H1 DLQ went missing from Phase 103 until 2026-09-19 (the
// v1.1.0 module comment even recorded the gap and scoped it out). This guard
// derives the kinds from the helpers so a fifth bridge is covered the day its
// helper is added.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func inboundDLQKinds() map[string]string {
	helpers := map[string]func(string) string{
		"slack":   awspkg.SlackInboundDLQName,
		"github":  awspkg.GitHubInboundDLQName,
		"h1":      awspkg.H1InboundDLQName,
		"webhook": awspkg.WebhookInboundDLQName,
	}
	re := regexp.MustCompile(`^P-(.+)-inbound-dlq\.fifo$`)
	out := map[string]string{}
	for label, fn := range helpers {
		m := re.FindStringSubmatch(fn("P"))
		if m == nil {
			panic("inbound DLQ helper " + label + " no longer matches {prefix}-<kind>-inbound-dlq.fifo: " + fn("P"))
		}
		out[label] = m[1]
	}
	return out
}

func TestSqsInboundDLQModule_EveryDLQNameHelperHasAQueueResource(t *testing.T) {
	root := findRepoRoot(t)
	livePath := filepath.Join(root, "infra", "live", "use1", "sqs-inbound-dlq", "terragrunt.hcl")
	liveRaw, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("read %s: %v", livePath, err)
	}
	live := string(liveRaw)

	// Resolve the pinned module from the live unit so a bump is tracked automatically.
	pin := regexp.MustCompile(`infra/modules/sqs-inbound-dlq/(v[0-9]+\.[0-9]+\.[0-9]+)`).FindStringSubmatch(live)
	if pin == nil {
		t.Fatalf("no sqs-inbound-dlq module pin found in %s", livePath)
	}
	mainPath := filepath.Join(root, "infra", "modules", "sqs-inbound-dlq", pin[1], "main.tf")
	mainRaw, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read pinned module %s: %v", mainPath, err)
	}
	tf := string(mainRaw)

	for label, kind := range inboundDLQKinds() {
		// (a) the pinned module creates a queue named from var.<kind>_dlq_name
		resource := regexp.MustCompile(`resource\s+"aws_sqs_queue"\s+"[a-z0-9_]+"\s*\{[^}]*name\s*=\s*var\.` + kind + `_dlq_name`)
		if !resource.MatchString(tf) {
			t.Errorf("%s: pinned module %s has no aws_sqs_queue whose name = var.%s_dlq_name — "+
				"%sInboundDLQName(prefix) names a queue nothing creates, and the first per-sandbox "+
				"%s-inbound queue's RedrivePolicy will 400 at CreateQueue (after the EC2 apply)",
				label, pin[1], kind, strings.ToUpper(label[:1])+label[1:], kind)
		}
		// (b) the live unit passes the name in the exact shape the Go helper produces
		input := regexp.MustCompile(kind + `_dlq_name\s*=\s*"\$\{local\.site_vars\.locals\.site\.label\}-` + kind + `-inbound-dlq\.fifo"`)
		if !input.MatchString(live) {
			t.Errorf("%s: live unit does not set %s_dlq_name = \"${local.site_vars.locals.site.label}-%s-inbound-dlq.fifo\" "+
				"(the Go side derives the ARN from that exact name and never reads the module output)",
				label, kind, kind)
		}
	}
}
