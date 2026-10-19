package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeThreadRowScanner answers Select COUNT scans from per-table page lists.
// Each page is a count; a non-final page carries a LastEvaluatedKey.
type fakeThreadRowScanner struct {
	pages  map[string][]int32
	errs   map[string]error
	calls  map[string]int
	inputs []*dynamodb.ScanInput
}

func (f *fakeThreadRowScanner) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.inputs = append(f.inputs, in)
	table := aws.ToString(in.TableName)
	if err := f.errs[table]; err != nil {
		return nil, err
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	i := f.calls[table]
	f.calls[table]++
	pages := f.pages[table]
	if i >= len(pages) {
		return &dynamodb.ScanOutput{}, nil
	}
	out := &dynamodb.ScanOutput{Count: pages[i]}
	if i < len(pages)-1 {
		out.LastEvaluatedKey = map[string]dynamodbtypes.AttributeValue{
			"k": &dynamodbtypes.AttributeValueMemberS{Value: table},
		}
	}
	return out, nil
}

func TestCheckThreadRowsWithoutOwner_AllZeroIsOK(t *testing.T) {
	f := &fakeThreadRowScanner{pages: map[string][]int32{}}
	r := checkThreadRowsWithoutOwner(context.Background(), f, "km")
	if r.Status != CheckOK {
		t.Fatalf("status = %s (%s); want OK", r.Status, r.Message)
	}
	tables := map[string]bool{}
	for _, in := range f.inputs {
		tables[aws.ToString(in.TableName)] = true
		if aws.ToString(in.FilterExpression) != "attribute_not_exists(sandbox_id)" {
			t.Errorf("FilterExpression = %q; want attribute_not_exists(sandbox_id)", aws.ToString(in.FilterExpression))
		}
		if in.Select != dynamodbtypes.SelectCount {
			t.Errorf("Select = %q; want COUNT", in.Select)
		}
	}
	for _, want := range []string{"km-slack-threads", "km-github-threads", "km-h1-threads"} {
		if !tables[want] {
			t.Errorf("table %s was not scanned", want)
		}
	}
}

func TestCheckThreadRowsWithoutOwner_CountsWarnNamingEachTable(t *testing.T) {
	f := &fakeThreadRowScanner{pages: map[string][]int32{
		"km2-github-threads": {3},
		"km2-h1-threads":     {1},
	}}
	r := checkThreadRowsWithoutOwner(context.Background(), f, "km2")
	if r.Status != CheckWarn {
		t.Fatalf("status = %s; want WARN", r.Status)
	}
	for _, want := range []string{"km2-github-threads: 3", "km2-h1-threads: 1"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message %q missing %q", r.Message, want)
		}
	}
	if strings.Contains(r.Message, "km2-slack-threads") {
		t.Errorf("message %q names a table with zero ownerless rows", r.Message)
	}
	if !strings.Contains(r.Remediation, "self-heal") {
		t.Errorf("remediation %q should say the rows self-heal", r.Remediation)
	}
}

func TestCheckThreadRowsWithoutOwner_FollowsPagination(t *testing.T) {
	f := &fakeThreadRowScanner{pages: map[string][]int32{
		"km-slack-threads": {2, 0, 5},
	}}
	r := checkThreadRowsWithoutOwner(context.Background(), f, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %s; want WARN", r.Status)
	}
	if !strings.Contains(r.Message, "km-slack-threads: 7") {
		t.Errorf("message %q; want the summed count km-slack-threads: 7 across 3 pages", r.Message)
	}
	if f.calls["km-slack-threads"] != 3 {
		t.Errorf("scanned km-slack-threads %d times; want 3 (one per page)", f.calls["km-slack-threads"])
	}
	// The second and third scans must resume from the previous page's key.
	var slackInputs []*dynamodb.ScanInput
	for _, in := range f.inputs {
		if aws.ToString(in.TableName) == "km-slack-threads" {
			slackInputs = append(slackInputs, in)
		}
	}
	if slackInputs[0].ExclusiveStartKey != nil {
		t.Error("first scan carries an ExclusiveStartKey; want nil")
	}
	for i := 1; i < len(slackInputs); i++ {
		if slackInputs[i].ExclusiveStartKey == nil {
			t.Errorf("scan %d has no ExclusiveStartKey; pagination not followed", i)
		}
	}
}

func TestCheckThreadRowsWithoutOwner_MissingTableSkippedSilently(t *testing.T) {
	f := &fakeThreadRowScanner{
		pages: map[string][]int32{"km-slack-threads": {2}},
		errs: map[string]error{
			"km-h1-threads": &dynamodbtypes.ResourceNotFoundException{Message: aws.String("Requested resource not found")},
		},
	}
	r := checkThreadRowsWithoutOwner(context.Background(), f, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %s; want WARN from the slack table", r.Status)
	}
	if strings.Contains(r.Message, "km-h1-threads") {
		t.Errorf("message %q names the missing table; want it skipped silently", r.Message)
	}
}

func TestCheckThreadRowsWithoutOwner_ScanErrorWarns(t *testing.T) {
	f := &fakeThreadRowScanner{errs: map[string]error{"km-github-threads": errors.New("AccessDeniedException")}}
	r := checkThreadRowsWithoutOwner(context.Background(), f, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %s; want WARN on a scan error", r.Status)
	}
	if !strings.Contains(r.Message, "km-github-threads") {
		t.Errorf("message %q should name the table that could not be scanned", r.Message)
	}
}

func TestCheckThreadRowsWithoutOwner_NilScannerSkipped(t *testing.T) {
	r := checkThreadRowsWithoutOwner(context.Background(), nil, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("status = %s; want SKIPPED with no scanner", r.Status)
	}
}
