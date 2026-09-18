package terragrunt_test

// Every per-sandbox inbound FIFO queue that `km create` provisions needs a
// lifecycle grant on the create-handler role (composed from
// infra/modules/km-operator-policy), because a remote `km create` runs INSIDE
// that Lambda and provisionXInboundQueue failure is fatal. Phases 103 (h1) and
// 127 (webhook) shipped queue-name helpers with no matching grant — invisible
// until a live remote create 403'd on sqs:CreateQueue — so this guard derives
// the queue kinds from pkg/aws's helpers and demands a grant for each.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// inboundQueueKinds derives the "<kind>" token of every {prefix}-<kind>-inbound-<id>.fifo
// helper so a fifth bridge is covered the day its helper is added here.
func inboundQueueKinds() map[string]string {
	helpers := map[string]func(string, string) string{
		"slack":   awspkg.SlackInboundQueueName,
		"github":  awspkg.GitHubInboundQueueName,
		"h1":      awspkg.H1InboundQueueName,
		"webhook": awspkg.WebhookInboundQueueName,
	}
	re := regexp.MustCompile(`^P-(.+)-inbound-S\.fifo$`)
	out := map[string]string{}
	for label, fn := range helpers {
		m := re.FindStringSubmatch(fn("P", "S"))
		if m == nil {
			panic("inbound queue helper " + label + " no longer matches {prefix}-<kind>-inbound-<id>.fifo: " + fn("P", "S"))
		}
		out[label] = m[1]
	}
	return out
}

// drainedByCreateHandler names the kinds whose bridge cold-creates by carrying a
// serialized envelope that cmd/create-handler drains into the queue after
// provisioning (drainInboundEnvelope) — those grants also need sqs:SendMessage.
// slack has no cold-create path; webhook cold-creates via the Prompt field
// (km create --prompt), not a queue.
var drainedByCreateHandler = map[string]bool{"github": true, "h1": true}

func TestCreateHandlerRole_EveryInboundQueueKindHasALifecycleGrant(t *testing.T) {
	root := findRepoRoot(t)
	tfPath := filepath.Join(root, "infra", "modules", "km-operator-policy", "v1.0.0", "main.tf")
	raw, err := os.ReadFile(tfPath)
	if err != nil {
		t.Fatalf("read %s: %v", tfPath, err)
	}
	tf := string(raw)

	for label, kind := range inboundQueueKinds() {
		arn := "${var.resource_prefix}-" + kind + "-inbound-*.fifo"
		idx := strings.Index(tf, arn)
		if idx < 0 {
			t.Errorf("%s: no create-handler SQS grant scoped to %q in %s — a remote `km create` of a profile with %s inbound enabled 403s on sqs:CreateQueue", label, arn, tfPath, label)
			continue
		}
		// The Statement that carries this ARN must grant queue creation. Look
		// back from the ARN to the nearest `Action = [` and check that block.
		stmt := tf[:idx]
		actIdx := strings.LastIndex(stmt, "Action = [")
		if actIdx < 0 {
			t.Errorf("%s: could not find the Action block for %q", label, arn)
			continue
		}
		actions := stmt[actIdx:]
		need := []string{`"sqs:CreateQueue"`, `"sqs:GetQueueUrl"`}
		if drainedByCreateHandler[label] {
			need = append(need, `"sqs:SendMessage"`)
		}
		for _, n := range need {
			if !strings.Contains(actions, n) {
				t.Errorf("%s: grant for %q lacks %s (km create needs CreateQueue/GetQueueUrl; kinds the create-handler drains on cold-create also need SendMessage)", label, arn, n)
			}
		}
	}
}
