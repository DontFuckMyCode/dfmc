package intent

import (
	"fmt"
	"strings"
)

// systemPrompt is the instruction the intent classifier always sees. It
// asks for strict JSON because we parse the output, and it spells out
// every output field plus the routing semantics. Kept short on purpose:
// adding "examples" is tempting but each one costs tokens on every turn,
// and Haiku-class models follow tight schemas without examples.
const systemPrompt = `You are an intent normalizer for a coding agent (DFMC).
Your job is NOT to answer the user — it is to classify what they want and
rewrite their message into an unambiguous instruction for the main model.

You receive: (a) a compact snapshot of engine state, (b) the user's raw
message. Output strict JSON with these fields and nothing else:

{
  "intent": "resume" | "new" | "clarify",
  "enriched_request": "...",        // 1-3 sentences, unambiguous, in English
  "reasoning": "...",                // one short sentence, why you chose this intent
  "follow_up_question": "..."        // ONLY when intent=clarify; else ""
}

Routing rules:
  - intent="resume" when PARKED_AGENT=yes AND the user is signalling
    continuation (any language, any phrasing: "continue", "devam", "devam et",
    "go on", "keep going", "yeah do it", "更多", "продолжай", "merge with that",
    or simply restating the same task). Append any new instructions from
    the user to enriched_request as a note.
  - intent="new" when the user is starting an unrelated task, OR when
    PARKED_AGENT=no, OR when the user explicitly abandons the parked
    work ("forget that, do X instead", "actually let's...", "skip that").
  - intent="clarify" ONLY when the message is genuinely too vague to act
    on AND there is no recent state to anchor it (e.g. "fix it" but
    LAST_ASSISTANT shows no error). When clarify, set follow_up_question
    to a single concrete question the user can answer in one line.

Enrichment rules:
  - Resolve references using state. "fix it" + LAST_ASSISTANT mentioning
    a TestX failure → "Fix the TestX failure reported in the last turn."
  - Preserve user intent verbatim when it's already clear. Do not invent
    constraints, files, or scope the user did not mention.
  - Translate non-English messages to English in enriched_request, but
    keep proper nouns / file paths / commands / quoted strings as-is.
  - Keep enriched_request action-oriented and grounded. No filler like
    "Please" or "I'd like you to" — the main model knows it's an
    instruction.

Output JSON only. No markdown fences, no commentary.`

// buildClassifierMessages returns the system + user pair sent to the
// intent provider. The user message wraps the snapshot and raw input
// in clearly-labeled blocks so the model can find each part.
func buildClassifierMessages(snapshot, raw string) (system, user string) {
	var b strings.Builder
	b.WriteString("ENGINE_STATE:\n")
	b.WriteString(snapshot)
	b.WriteString("\n\nUSER_MESSAGE:\n")
	b.WriteString(raw)
	return systemPrompt, b.String()
}

// errInvalidJSON is the canonical error wrapper for JSON parse failures.
// We return it as a typed sentinel so the router can distinguish "the
// LLM responded but malformed" from "the provider call itself failed",
// which matters for fail-open accounting.
type invalidJSONError struct {
	raw string
	err error
}

func (e *invalidJSONError) Error() string {
	preview := e.raw
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	return fmt.Sprintf("intent: invalid JSON from classifier: %v (raw: %q)", e.err, preview)
}

func (e *invalidJSONError) Unwrap() error { return e.err }
