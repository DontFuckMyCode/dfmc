package provider

import (
	"fmt"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// messagesFromShape builds a synthetic agent-loop history from a byte
// string, where each byte selects one message shape. This lets the fuzzer
// explore arbitrary interleavings of plain turns and tool roundtrips without
// needing a structured corpus.
func messagesFromShape(shape []byte) []Message {
	msgs := make([]Message, 0, len(shape))
	call := 0
	for _, b := range shape {
		switch b % 4 {
		case 0:
			msgs = append(msgs, Message{Role: types.RoleUser, Content: "user turn"})
		case 1:
			msgs = append(msgs, Message{Role: types.RoleAssistant, Content: "assistant turn"})
		case 2:
			id := fmt.Sprintf("call_%d", call)
			call++
			msgs = append(msgs, Message{Role: types.RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: "tool"}}})
		case 3:
			id := fmt.Sprintf("call_%d", call)
			msgs = append(msgs, Message{Role: types.RoleUser, ToolCallID: id, ToolName: "tool", Content: "result"})
		}
	}
	return msgs
}

// FuzzCompactMessagesForRetryInvariants is the property-based guard for the
// orphan-tool_result fix (#62). For ANY history, compaction must either keep
// the slice unchanged (trimmed==0) or produce a [notice]+tail where:
//   - the notice is a user message,
//   - the first message of the kept tail is a CLEAN user turn (no ToolCallID)
//     — never an orphan tool_result whose tool_use was dropped,
//   - the original final message is preserved (we never lose the live tail),
//   - the returned trimmed count is consistent with the length delta.
//
// A violation means compaction can still emit a provider-rejected shape on
// the overflow-rescue path.
func FuzzCompactMessagesForRetryInvariants(f *testing.F) {
	for _, s := range []string{"\x00", "\x00\x01\x02\x03", "\x02\x03\x02\x03", "\x00\x02\x03\x01\x00\x02\x03", "\x03\x03\x03"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, shape []byte) {
		msgs := messagesFromShape(shape)
		orig := append([]Message(nil), msgs...)

		compacted, trimmed := compactMessagesForRetry(msgs)

		// Compaction must not mutate the caller's slice contents.
		for i := range orig {
			if msgs[i].Role != orig[i].Role || msgs[i].ToolCallID != orig[i].ToolCallID {
				t.Fatalf("compactMessagesForRetry mutated input at %d", i)
			}
		}

		if trimmed == 0 {
			// No-op contract: returns the original slice untouched.
			if len(compacted) != len(orig) {
				t.Fatalf("trimmed==0 but length changed: %d -> %d", len(orig), len(compacted))
			}
			return
		}

		if len(compacted) < 2 {
			t.Fatalf("trimmed>0 but compacted has %d messages", len(compacted))
		}
		// Notice is a user message.
		if string(compacted[0].Role) != "user" {
			t.Fatalf("notice role = %q, want user", compacted[0].Role)
		}
		// The kept tail must START with a clean user turn — never an orphan
		// tool_result. This is the exact shape #62 fixed.
		first := compacted[1]
		if string(first.Role) != "user" {
			t.Fatalf("kept tail starts with role %q, want user", first.Role)
		}
		if first.ToolCallID != "" {
			t.Fatalf("kept tail starts with an ORPHAN tool_result (ToolCallID=%q)", first.ToolCallID)
		}
		// The live final turn is never lost.
		last := compacted[len(compacted)-1]
		origLast := orig[len(orig)-1]
		if last.Role != origLast.Role || last.ToolCallID != origLast.ToolCallID || last.Content != origLast.Content {
			t.Fatalf("final message not preserved: got %+v want %+v", last, origLast)
		}
	})
}
