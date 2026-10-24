package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func reply(user, ts, text string) SlackReplyMessage {
	return SlackReplyMessage{User: user, TS: ts, Text: text}
}

func TestBuildThreadContext_FiltersAndCounts(t *testing.T) {
	replies := []SlackReplyMessage{
		reply("U0ROOT", "100.000001", "root question"),
		reply("U0RON", "100.000002", "the build fails"),
		{User: "UBOT", TS: "100.000003", Text: "bot said", BotID: "B1"},
		{User: "UBOT", TS: "100.000004", Text: "bot again"},
		reply("U0EVIL", "100.000005", "secret"),
		reply("U0KAR", "100.000006", "same here"),
		reply("U0KURT", "100.000007", "<@UBOT> what now?"), // trigger
		reply("U0LATE", "100.000008", "after trigger"),
	}
	ctx, files := BuildThreadContext(replies, "100.000007", "UBOT")
	if len(files) != 0 {
		t.Fatalf("no files expected, got %d", len(files))
	}
	var users []string
	for _, m := range ctx.Messages {
		users = append(users, m.User)
	}
	// Every human reply, the formerly filtered U0EVIL included; bots, the trigger and later messages excluded.
	if strings.Join(users, ",") != "U0ROOT,U0RON,U0EVIL,U0KAR" {
		t.Fatalf("messages %v", users)
	}
	if ctx.TruncatedOlder != 0 {
		t.Fatalf("truncated=%d", ctx.TruncatedOlder)
	}
	for _, m := range ctx.Messages {
		if strings.Contains(m.Text, "bot") || strings.Contains(m.Text, "what now") {
			t.Fatalf("leaked %q", m.Text)
		}
	}
}

func TestBuildThreadContext_CapsKeepNewestChronological(t *testing.T) {
	var replies []SlackReplyMessage
	for i := 1; i <= 150; i++ {
		replies = append(replies, reply("U1", fmt.Sprintf("100.%06d", i), fmt.Sprintf("m%d", i)))
	}
	ctx, _ := BuildThreadContext(replies, "100.000151", "UBOT")
	if len(ctx.Messages) != MaxContextMessages || ctx.TruncatedOlder != 50 {
		t.Fatalf("len=%d truncated=%d", len(ctx.Messages), ctx.TruncatedOlder)
	}
	if ctx.Messages[0].Text != "m51" || ctx.Messages[99].Text != "m150" {
		t.Fatalf("want m51..m150 chronological, got %s..%s", ctx.Messages[0].Text, ctx.Messages[99].Text)
	}

	big := strings.Repeat("x", 20<<10)
	replies = []SlackReplyMessage{reply("U1", "100.000001", big), reply("U1", "100.000002", big), reply("U1", "100.000003", "small")}
	ctx, _ = BuildThreadContext(replies, "100.000004", "UBOT")
	if len(ctx.Messages) != 2 || ctx.Messages[1].Text != "small" || ctx.TruncatedOlder != 1 {
		t.Fatalf("32KiB cap: len=%d truncated=%d", len(ctx.Messages), ctx.TruncatedOlder)
	}
}

func TestBuildThreadContext_FileBudget(t *testing.T) {
	var replies []SlackReplyMessage
	for i := 1; i <= 12; i++ {
		replies = append(replies, SlackReplyMessage{User: "U1", TS: fmt.Sprintf("100.%06d", i), Text: "f",
			Files: []SlackFile{{ID: fmt.Sprintf("F%d", i), Name: fmt.Sprintf("f%d.png", i), Size: 1 << 20}}})
	}
	ctx, files := BuildThreadContext(replies, "100.000099", "UBOT")
	if len(files) != MaxContextFiles {
		t.Fatalf("want %d files, got %d", MaxContextFiles, len(files))
	}
	if len(ctx.FilesOmitted) != 2 || ctx.FilesOmitted[0] != "f1.png" || ctx.FilesOmitted[1] != "f2.png" {
		t.Fatalf("omitted %v (newest-first keeps f12..f3, omitted presented oldest-first)", ctx.FilesOmitted)
	}
	// Byte budget: one 49 MiB + one 2 MiB => the older one is omitted.
	replies = []SlackReplyMessage{
		{User: "U1", TS: "100.000001", Text: "a", Files: []SlackFile{{ID: "F1", Name: "old.bin", Size: 2 << 20}}},
		{User: "U1", TS: "100.000002", Text: "b", Files: []SlackFile{{ID: "F2", Name: "new.bin", Size: 49 << 20}}},
	}
	ctx, files = BuildThreadContext(replies, "100.000003", "UBOT")
	if len(files) != 1 || files[0].File.Name != "new.bin" || len(ctx.FilesOmitted) != 1 || ctx.FilesOmitted[0] != "old.bin" {
		t.Fatalf("byte budget: files=%+v omitted=%v", files, ctx.FilesOmitted)
	}
	if files[0].MsgIndex != 1 {
		t.Fatalf("file must point at its message index, got %d", files[0].MsgIndex)
	}
}

func TestTrimContextToFit_DropsOldestKeepsTrigger(t *testing.T) {
	body := InboundQueueBody{Channel: "C1", ThreadTS: "1.0", Text: "trigger", User: "U1", EventTS: "9.0",
		ThreadContext: &ThreadContext{}}
	for i := 0; i < 50; i++ {
		body.ThreadContext.Messages = append(body.ThreadContext.Messages,
			ContextMessage{User: "U1", TS: fmt.Sprintf("1.%06d", i), Text: strings.Repeat("y", 10<<10)})
	}
	dropped := TrimContextToFit(&body, MaxQueueBodyBytes)
	b, _ := json.Marshal(body)
	if len(b) > MaxQueueBodyBytes {
		t.Fatalf("still %d bytes", len(b))
	}
	if dropped == 0 || body.ThreadContext.TruncatedOlder != dropped || body.Text != "trigger" {
		t.Fatalf("dropped=%d truncated=%d text=%q", dropped, body.ThreadContext.TruncatedOlder, body.Text)
	}
	if body.ThreadContext.Messages[0].TS != fmt.Sprintf("1.%06d", dropped) {
		t.Fatal("must drop oldest first")
	}
}
