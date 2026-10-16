package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func irbotItem(id string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":              &dynamodbtypes.AttributeValueMemberS{Value: id},
		"alias":                   &dynamodbtypes.AttributeValueMemberS{Value: "ir-bot"},
		"slack_inbound_queue_url": &dynamodbtypes.AttributeValueMemberS{Value: "https://sqs/" + id + ".fifo"},
		"status":                  &dynamodbtypes.AttributeValueMemberS{Value: "stopped"},
		"action_frozen":           &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
	}
}

func TestDDBSandboxByID_ParsesSameFieldsAsByChannel(t *testing.T) {
	m := &mockDDBQueryGetPut{getItem: func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
		if v := in.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "sb-1" {
			t.Errorf("key %q", v)
		}
		return &dynamodb.GetItemOutput{Item: irbotItem("sb-1")}, nil
	}}
	info, err := (&bridge.DDBSandboxByID{Client: m, TableName: "km-sandboxes"}).FetchBySandboxID(context.Background(), "sb-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.SandboxID != "sb-1" || info.QueueURL != "https://sqs/sb-1.fifo" || !info.Paused || !info.ActionFrozen {
		t.Fatalf("info %+v", info)
	}
}

func TestDDBAliasResolver_FoundAbsentAmbiguous(t *testing.T) {
	items := []map[string]dynamodbtypes.AttributeValue{}
	m := &mockDDBQueryGetPut{
		query: func(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			if awssdk.ToString(in.IndexName) != "alias-index" {
				t.Errorf("index %q", awssdk.ToString(in.IndexName))
			}
			return &dynamodb.QueryOutput{Items: items}, nil
		},
		getItem: func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: irbotItem("sb-1")}, nil
		},
	}
	r := &bridge.DDBAliasResolver{Client: m, TableName: "km-sandboxes"}

	_, found, err := r.ResolveAlias(context.Background(), "ir-bot")
	if err != nil || found {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
	items = []map[string]dynamodbtypes.AttributeValue{{"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-1"}}}
	info, found, err := r.ResolveAlias(context.Background(), "ir-bot")
	if err != nil || !found || info.QueueURL != "https://sqs/sb-1.fifo" {
		t.Fatalf("found: %+v %v %v", info, found, err)
	}
	items = append(items, items[0])
	if _, _, err := r.ResolveAlias(context.Background(), "ir-bot"); err == nil {
		t.Fatal("ambiguous alias must error")
	}
}

type mockEventBridge struct{ entries []string }

func (m *mockEventBridge) PutEvents(_ context.Context, in *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	for _, e := range in.Entries {
		m.entries = append(m.entries, awssdk.ToString(e.Detail))
	}
	return &eventbridge.PutEventsOutput{}, nil
}

func TestEventBridgeAdapter_PutSandboxCreate(t *testing.T) {
	eb := &mockEventBridge{}
	a := &bridge.EventBridgeAdapter{Client: eb, ArtifactBucket: "km-artifacts"}
	id, err := a.PutSandboxCreate(context.Background(), "ir-bot", "profiles/IR-Bot.yaml", `{"channel":"C1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "sl-") {
		t.Fatalf("id %q", id)
	}
	var d kmaws.SandboxCreateDetail
	json.Unmarshal([]byte(eb.entries[0]), &d)
	if d.SandboxID != id || d.Alias != "ir-bot" || d.ArtifactPrefix != "slack-profiles/ir-bot" || d.SlackEnvelope != `{"channel":"C1"}` || d.CreatedBy != "slack" {
		t.Fatalf("detail %+v", d)
	}
}

type staticTokens struct{}

func (staticTokens) Fetch(_ context.Context) (string, error) { return "xoxb-test", nil }

func TestSlackSenderEmailAdapter(t *testing.T) {
	var reply string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	a := &bridge.SlackSenderEmailAdapter{Tokens: staticTokens{}, HTTPClient: srv.Client(), BaseURL: srv.URL}

	reply = `{"ok":true,"user":{"id":"U1","profile":{"email":"A@B.C"}}}`
	if e, err := a.SenderEmail(context.Background(), "U1"); err != nil || e != "a@b.c" {
		t.Fatalf("%q %v", e, err)
	}
	for _, bad := range []string{
		`{"ok":true,"user":{"id":"U1","deleted":true,"profile":{"email":"a@b.c"}}}`,
		`{"ok":true,"user":{"id":"U1","is_bot":true,"profile":{"email":"a@b.c"}}}`,
		`{"ok":true,"user":{"id":"U1","profile":{}}}`,
	} {
		reply = bad
		if _, err := a.SenderEmail(context.Background(), "U1"); err == nil {
			t.Fatalf("want ErrSenderIneligible for %s", bad)
		}
	}
}
