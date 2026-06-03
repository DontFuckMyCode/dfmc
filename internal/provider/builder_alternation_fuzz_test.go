package provider

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// FuzzAnthropicMessagesAlternate pins the alternation invariant that
// buildAnthropicMessages' coalesce exists to guarantee: the on-wire
// `messages` array must never contain two consecutive same-role entries.
// Anthropic's /messages API rejects that shape with a 400 that never names
// the violation. The agent loop produces it organically (N parallel tool
// results flushed as N user messages; a /continue note after a user tail),
// so any history shape must survive the builder.
func FuzzAnthropicMessagesAlternate(f *testing.F) {
	for _, s := range []string{"\x00", "\x03\x03", "\x02\x03\x03", "\x00\x00\x01", "\x02\x03\x02\x03\x03"} {
		f.Add([]byte(s), false)
	}
	f.Fuzz(func(t *testing.T, shape []byte, withContext bool) {
		req := CompletionRequest{Messages: messagesFromShape(shape)}
		if withContext {
			req.Context = []types.ContextChunk{{Path: "a.go", Content: "ctx"}}
		}

		out := buildAnthropicMessages(req)
		for i := 1; i < len(out); i++ {
			if out[i].Role == out[i-1].Role {
				t.Fatalf("consecutive %q messages at %d-%d (shape=%v ctx=%v); Anthropic rejects non-alternating turns",
					out[i].Role, i-1, i, shape, withContext)
			}
		}
	})
}

// FuzzGoogleContentsAlternate is the same invariant for the Gemini builder
// after the #64 coalesce fix: streamGenerateContent rejects non-alternating
// `contents`, and parallel functionResponses must fold into one user content.
func FuzzGoogleContentsAlternate(f *testing.F) {
	for _, s := range []string{"\x00", "\x03\x03", "\x02\x03\x03", "\x02\x03\x02\x03\x03", "\x01\x01\x00"} {
		f.Add([]byte(s), false)
	}
	f.Fuzz(func(t *testing.T, shape []byte, withContext bool) {
		req := CompletionRequest{Messages: messagesFromShape(shape)}
		if withContext {
			req.Context = []types.ContextChunk{{Path: "a.go", Content: "ctx"}}
		}

		out := buildGoogleContents(req)
		for i := 1; i < len(out); i++ {
			if out[i].Role == out[i-1].Role {
				t.Fatalf("consecutive %q contents at %d-%d (shape=%v ctx=%v); Gemini rejects non-alternating turns",
					out[i].Role, i-1, i, shape, withContext)
			}
		}
		// Every emitted content must carry a recognised role.
		for i, c := range out {
			if c.Role != "user" && c.Role != "model" {
				t.Fatalf("content[%d] has unexpected role %q", i, c.Role)
			}
		}
	})
}
