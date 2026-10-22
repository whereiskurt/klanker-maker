package bridge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// reactTestAdapter wires a SlackPosterAdapter at an httptest server that records
// every (path, JSON body) it receives and answers with the given body per path.
func reactTestAdapter(t *testing.T, answers map[string]string) (*bridge.SlackPosterAdapter, *[]string, *[]map[string]any) {
	t.Helper()
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, m)
		if a, ok := answers[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, a)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	tok := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
		},
	}, Path: "/km/slack/bot-token"}
	return &bridge.SlackPosterAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: tok}, &paths, &bodies
}

// The adapter satisfies the optional ReactionPoster surface the handler asserts.
func TestSlackPosterAdapter_ImplementsReactionPoster(t *testing.T) {
	var _ bridge.ReactionPoster = (*bridge.SlackPosterAdapter)(nil)
	var _ bridge.BroadcastPoster = (*bridge.SlackPosterAdapter)(nil)
}

func TestSlackPosterAdapter_AddReaction_WireFormat(t *testing.T) {
	ad, paths, bodies := reactTestAdapter(t, map[string]string{"/reactions.add": `{"ok":true}`})
	if err := ad.AddReaction(context.Background(), "C01", "1714280400.000100", "white_check_mark"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if strings.Join(*paths, ",") != "/reactions.add" {
		t.Fatalf("paths = %v", *paths)
	}
	b := (*bodies)[0]
	if b["channel"] != "C01" || b["timestamp"] != "1714280400.000100" || b["name"] != "white_check_mark" {
		t.Errorf("reactions.add body = %v; want channel/timestamp/name (Slack's field names, not ts/emoji)", b)
	}
}

func TestSlackPosterAdapter_RemoveReaction_WireFormat(t *testing.T) {
	ad, paths, bodies := reactTestAdapter(t, map[string]string{"/reactions.remove": `{"ok":true}`})
	if err := ad.RemoveReaction(context.Background(), "C01", "1.1", "hourglass_flowing_sand"); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	if strings.Join(*paths, ",") != "/reactions.remove" {
		t.Fatalf("paths = %v", *paths)
	}
	b := (*bodies)[0]
	if b["channel"] != "C01" || b["timestamp"] != "1.1" || b["name"] != "hourglass_flowing_sand" {
		t.Errorf("reactions.remove body = %v", b)
	}
}

// The adapter is raw: it returns the Slack code verbatim in the error so the
// handler can decide what is idempotent. The code must be the LAST colon-separated
// token (slackResponse extracts it that way).
func TestSlackPosterAdapter_Reactions_ErrorCarriesCode(t *testing.T) {
	ad, _, _ := reactTestAdapter(t, map[string]string{
		"/reactions.add":    `{"ok":false,"error":"already_reacted"}`,
		"/reactions.remove": `{"ok":false,"error":"no_reaction"}`,
	})
	err := ad.AddReaction(context.Background(), "C01", "1.1", "x")
	if err == nil || !strings.HasSuffix(err.Error(), ": already_reacted") {
		t.Errorf("AddReaction err = %v; want suffix ': already_reacted'", err)
	}
	err = ad.RemoveReaction(context.Background(), "C01", "1.1", "x")
	if err == nil || !strings.HasSuffix(err.Error(), ": no_reaction") {
		t.Errorf("RemoveReaction err = %v; want suffix ': no_reaction'", err)
	}
}

// reply_broadcast travels on chat.postMessage, text and blocks alike, and only
// when a thread_ts is present (Slack rejects it on a top-level post).
func TestSlackPosterAdapter_PostMessageBroadcast_WireFormat(t *testing.T) {
	ad, _, bodies := reactTestAdapter(t, map[string]string{"/chat.postMessage": `{"ok":true,"ts":"9.9"}`})

	ts, err := ad.PostMessageBroadcast(context.Background(), "C01", "", "hello", "", "1.1")
	if err != nil || ts != "9.9" {
		t.Fatalf("text broadcast: ts=%q err=%v", ts, err)
	}
	b := (*bodies)[0]
	if b["reply_broadcast"] != true || b["thread_ts"] != "1.1" || b["text"] != "hello" {
		t.Errorf("text payload = %v; want reply_broadcast:true + thread_ts + text", b)
	}
	if _, has := b["blocks"]; has {
		t.Errorf("text payload must not carry blocks: %v", b)
	}

	_, err = ad.PostMessageBroadcast(context.Background(), "C01", "", "fallback", `[{"type":"divider"}]`, "1.1")
	if err != nil {
		t.Fatalf("blocks broadcast: %v", err)
	}
	b = (*bodies)[1]
	if b["reply_broadcast"] != true || b["thread_ts"] != "1.1" || b["blocks"] == nil || b["text"] != "fallback" {
		t.Errorf("blocks payload = %v; want reply_broadcast:true + thread_ts + blocks + text fallback", b)
	}

	// No thread: the flag is dropped rather than sent (Slack would reject it).
	_, err = ad.PostMessageBroadcast(context.Background(), "C01", "", "top", "", "")
	if err != nil {
		t.Fatalf("top-level: %v", err)
	}
	if _, has := (*bodies)[2]["reply_broadcast"]; has {
		t.Errorf("top-level payload must not carry reply_broadcast: %v", (*bodies)[2])
	}
}

// Existing PostMessage/PostMessageBlocks never send reply_broadcast; the shared
// payload builder must not have leaked the field onto them.
func TestSlackPosterAdapter_PostMessage_NoBroadcastByDefault(t *testing.T) {
	ad, _, bodies := reactTestAdapter(t, map[string]string{"/chat.postMessage": `{"ok":true,"ts":"9.9"}`})
	if _, err := ad.PostMessage(context.Background(), "C01", "s", "b", "1.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.PostMessageBlocks(context.Background(), "C01", "s", "b", `[]`, "1.1"); err != nil {
		t.Fatal(err)
	}
	for i, b := range *bodies {
		if _, has := b["reply_broadcast"]; has {
			t.Errorf("call %d carries reply_broadcast: %v", i, b)
		}
	}
}
