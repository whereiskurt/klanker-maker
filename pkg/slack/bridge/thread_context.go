package bridge

import (
	"encoding/json"
	"sort"
)

const (
	MaxContextMessages  = 100
	MaxContextTextBytes = 32 << 10
	MaxContextFiles     = 10
	MaxContextFileBytes = 50 << 20
	MaxQueueBodyBytes   = 240 << 10 // margin under SQS's 256 KiB
)

// ThreadContext is the human conversation since the bot's last turn, attached
// to the queue envelope for a mentioned thread reply (strict mention-only mode).
type ThreadContext struct {
	Messages        []ContextMessage `json:"messages,omitempty"`
	TruncatedOlder  int              `json:"truncated_older,omitempty"`
	FilesOmitted    []string         `json:"files_omitted,omitempty"`
	FileErrors      []FileError      `json:"file_errors,omitempty"`
	Error           string           `json:"error,omitempty"`
}

// ContextMessage is one prior human message in the thread.
type ContextMessage struct {
	User        string       `json:"user"`
	TS          string       `json:"ts"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// contextFileRef pairs a file to download with the index of its ContextMessage.
type contextFileRef struct {
	MsgIndex int
	File     SlackFile
}

// BuildThreadContext is pure: no I/O. It excludes the trigger and anything at
// or after it, and bots (the bot itself included), then keeps the newest
// messages under the message and text caps in chronological order, and applies
// a newest-first file budget. Every human reply is carried: membership is the
// permission (2026-10-24); the allowlist decides who may START a turn, not what
// the bot may read.
func BuildThreadContext(replies []SlackReplyMessage, triggerTS, botUserID string) (ThreadContext, []contextFileRef) {
	var out ThreadContext
	var eligible []SlackReplyMessage
	for _, r := range replies {
		if r.TS == "" || r.TS >= triggerTS { // fixed-width ts => lexical == numeric
			continue
		}
		if r.BotID != "" || r.Subtype == "bot_message" || r.User == "" || (botUserID != "" && r.User == botUserID) {
			continue
		}
		eligible = append(eligible, r)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].TS > eligible[j].TS }) // newest first
	var taken []SlackReplyMessage
	textBytes := 0
	for _, r := range eligible {
		if len(taken) >= MaxContextMessages || textBytes+len(r.Text) > MaxContextTextBytes {
			break
		}
		taken = append(taken, r)
		textBytes += len(r.Text)
	}
	out.TruncatedOlder = len(eligible) - len(taken)

	// Files: newest-first budget over the taken messages.
	var refs []contextFileRef
	fileCount, fileBytes := 0, int64(0)
	for i, r := range taken { // taken is newest-first here
		for _, f := range r.Files {
			if fileCount >= MaxContextFiles || fileBytes+f.Size > MaxContextFileBytes {
				out.FilesOmitted = append(out.FilesOmitted, f.Name)
				continue
			}
			refs = append(refs, contextFileRef{MsgIndex: len(taken) - 1 - i, File: f})
			fileCount++
			fileBytes += f.Size
		}
	}
	// Chronological output.
	for i := len(taken) - 1; i >= 0; i-- {
		r := taken[i]
		out.Messages = append(out.Messages, ContextMessage{User: r.User, TS: r.TS, Text: r.Text})
	}
	// FilesOmitted was collected newest-first; present oldest-first to match messages.
	for i, j := 0, len(out.FilesOmitted)-1; i < j; i, j = i+1, j-1 {
		out.FilesOmitted[i], out.FilesOmitted[j] = out.FilesOmitted[j], out.FilesOmitted[i]
	}
	return out, refs
}

// TrimContextToFit drops context messages oldest-first until the serialized
// body fits in maxBytes. The trigger text is never touched. Returns the number
// of messages dropped (also added to TruncatedOlder).
func TrimContextToFit(body *InboundQueueBody, maxBytes int) int {
	if body.ThreadContext == nil {
		return 0
	}
	dropped := 0
	for {
		b, _ := json.Marshal(body)
		if len(b) <= maxBytes || len(body.ThreadContext.Messages) == 0 {
			break
		}
		body.ThreadContext.Messages = body.ThreadContext.Messages[1:]
		body.ThreadContext.TruncatedOlder++
		dropped++
	}
	return dropped
}
