package main

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// recordingSSM captures every DeleteParameter name CleanupSandboxIdentity issues.
type recordingSSM struct{ deleted []string }

func (r *recordingSSM) PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	return &ssm.PutParameterOutput{}, nil
}

func (r *recordingSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{}, nil
}

func (r *recordingSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	r.deleted = append(r.deleted, *in.Name)
	return &ssm.DeleteParameterOutput{}, nil
}

type noopIdentityDDB struct{}

func (noopIdentityDDB) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (noopIdentityDDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (noopIdentityDDB) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// Every SSM parameter CleanupSandboxIdentity deletes must have a matching
// ssm:DeleteParameter ARN in the ttl-handler module — the grant there is per
// EXACT NAME, so a parameter added to cleanup without an ARN 403s silently on
// every TTL expiry (the handler logs cleanup errors non-fatally) and leaves an
// orphan. Name-agnostic: it runs the real cleanup against a recorder and maps
// each path to the ARN shape the module uses, so the next parameter added to
// cleanup is covered without editing this test.
func TestTTLHandlerModule_EveryCleanupSSMParamHasADeleteGrant(t *testing.T) {
	raw, err := os.ReadFile(ttlHandlerModuleMainTF)
	if err != nil {
		t.Fatalf("read %s: %v", ttlHandlerModuleMainTF, err)
	}
	tf := string(raw)

	const prefix, sbx = "PFX", "SBX"
	rec := &recordingSSM{}
	if err := awspkg.CleanupSandboxIdentity(context.Background(), rec, noopIdentityDDB{}, "t", prefix, sbx); err != nil {
		t.Fatal(err)
	}
	if len(rec.deleted) == 0 {
		t.Fatal("CleanupSandboxIdentity deleted nothing — the recorder has drifted from the function")
	}

	arnRe := regexp.MustCompile(`parameter/\$\{var\.resource_prefix\}/([^"]+)"`)
	granted := map[string]bool{}
	for _, m := range arnRe.FindAllStringSubmatch(tf, -1) {
		granted[m[1]] = true
	}
	if len(granted) == 0 {
		t.Fatalf("found no parameter/${var.resource_prefix}/... ARNs in %s — the regex has drifted from the module", ttlHandlerModuleMainTF)
	}

	var missing []string
	for _, p := range rec.deleted {
		// "/PFX/sandbox/SBX/signing-key" -> "sandbox/*/signing-key"
		rel := strings.TrimPrefix(p, "/"+prefix+"/")
		want := strings.Replace(rel, sbx, "*", 1)
		if !granted[want] {
			missing = append(missing, want)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("CleanupSandboxIdentity deletes these parameters but %s grants no ssm:DeleteParameter on them: %v\n"+
			"Add \"arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/<path>\" to the IdentitySSMDelete statement.",
			ttlHandlerModuleMainTF, missing)
	}
}
