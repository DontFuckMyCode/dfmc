package engine

// engine_prompt_notices.go — cacheable + non-cacheable system-prompt
// notices appended by buildSystemPrompt, plus the small bundle→blocks
// converter that produces the (text, []SystemBlock) pair providers
// consume. Sibling of engine_prompt.go which keeps the
// PromptRecommendation surface, promptRuntime resolver, and the
// buildSystemPrompt assembler that calls into these notices.
//
// Notice authors:
//   - conversationPruneSystemNotice  [id:]/[next:]/[cleanup:]/[done:]
//                                    autonomy + pruning protocol.
//   - toolReasoningSystemNotice      teaches `_reason` virtual field
//                                    when the surface is enabled.
//   - hostOSSystemNotice             runtime.GOOS-aware reminder that
//                                    run_command has no shell.
//   - memoryDegradedSystemNotice     non-cacheable warning when the
//                                    SQLite memory store failed to load.
//
// appendSystemNoticeText avoids the leading-blank-line footgun when
// the existing prompt is empty; bundleToSystemBlocks splits a
// PromptBundle into the paired (flat text, []SystemBlock) form
// providers expect, returning nil blocks when nothing's cacheable so
// non-cache-aware providers keep the flat-string fast path.

import (
	"runtime"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// conversationPruneSystemNotice is the cacheable system-prompt block
// that teaches the model the [id:], [next:], [cleanup:], and [done:]
// markers. The IDs are pre-injected on every history turn
// ([id:u-3f29a1] …); the tail markers are required on the FINAL answer
// of every turn (not intermediate tool-step responses). The engine
// parses all of them, strips them from the persisted answer, honours
// cleanup IDs by dropping those messages, and uses [done:false] (or
// missing [done:]) as the trigger to self-resume with the first
// [next:] action as the next user prompt.
func conversationPruneSystemNotice() string {
	return "[Conversation pruning + autonomy protocol — REQUIRED on every final answer]\n" +
		"  Every prior turn arrives with a `[id:<role>-<hex>]` prefix (e.g. `[id:u-3f29a1] my question…`).\n" +
		"  Use those IDs in the THREE REQUIRED tail markers below. They MUST appear at the very end of\n" +
		"  the text answer (after any prose, code blocks, or summaries). All markers are stripped before\n" +
		"  the answer is shown to the user — they are metadata, not output.\n" +
		"\n" +
		"  [next: …]\n" +
		"     Two-to-three short next-action suggestions, one per line. If the task is NOT yet complete,\n" +
		"     the FIRST line is what the engine will auto-feed back as the next user prompt — phrase it\n" +
		"     as a concrete instruction (\"verify the fix by running go test ./...\"), not a question.\n" +
		"     Examples:\n" +
		"       [next:\n" +
		"        - run go test ./internal/engine to confirm the fix\n" +
		"        - extract the duplicated parser into a helper\n" +
		"        - add a regression test for the empty-input case]\n" +
		"\n" +
		"  [cleanup: id1, id2, …]\n" +
		"     Comma-separated list of prior message IDs that are no longer load-bearing for ongoing work.\n" +
		"     Pick aggressively — superseded debugging exchanges, resolved questions, off-thread asides,\n" +
		"     stale tool-output recaps. Do NOT include the just-arrived user turn or your own current\n" +
		"     answer. Empty cleanup is acceptable but the marker must still appear: `[cleanup: ]`.\n" +
		"\n" +
		"  [done: true|false]\n" +
		"     `true` ONLY when the user's overall goal is fully met and no further work is pending.\n" +
		"     `false` (or simply omit the marker) when there is more to do — the engine will then\n" +
		"     self-resume with the first [next:] action as the next user prompt, capped by\n" +
		"     max_auto_continue_iterations so a runaway loop can't burn the budget. The user explicitly\n" +
		"     asked for this autonomy: keep going until the task is done; do not stop and wait between\n" +
		"     obvious follow-up steps you already know need to happen.\n" +
		"\n" +
		"  Skipping any marker leaves the rolling history unmanaged or strands the autonomy loop — the\n" +
		"  user explicitly asked for this contract, so the absence is a contract failure.]"
}

// toolReasoningSystemNotice is the cacheable system-prompt block that tells
// the model to fill the `_reason` virtual field on every tool call. The schema
// keeps it optional for provider compatibility, but this runtime instruction
// treats it as required UI metadata.
func toolReasoningSystemNotice() string {
	return "[Tool self-narration REQUIRED: every tool call must include the virtual `_reason` string in args; every model-initiated tool call must provide it. " +
		"Treat missing `_reason` as an invalid tool-call shape. Use one concise sentence (<=140 chars) explaining why this tool is needed now and what signal you expect; " +
		"the TUI shows it in the debug tool timeline, and batch calls need a reason both for the batch and for each inner call when possible. Example: " +
		`{"name":"read_file","args":{"path":"server.go","_reason":"checking how the SSE handler closes the stream before editing it"}}.` +
		" `_reason` is stripped before dispatch and never reaches the tool implementation.]"
}

// hostOSSystemNotice returns the runtime.GOOS-aware reminder injected
// into every system prompt. Tells the model what host it's on, which
// path separators are native, and — most importantly — that
// run_command does not invoke a shell so chain operators and
// redirects belong nowhere.
func hostOSSystemNotice() string {
	switch runtime.GOOS {
	case "windows":
		return "[Host: Windows. run_command executes binaries directly (no cmd.exe / no PowerShell): " +
			"`&&`, `||`, `;`, `|`, `>`, and `cd ...` chains are NOT interpreted. " +
			"Pass {command, args, dir} separately, and set dir to an absolute path. " +
			"Prefer forward slashes for cross-platform tools (`go`, `git`, `npm`, `python`); use escaped backslashes only when explicitly invoking Windows-native built-ins via cmd.exe/PowerShell.]"
	case "darwin":
		return "[Host: macOS (darwin). run_command executes binaries directly — no shell — so " +
			"`&&`, `||`, `;`, `|`, `>`, redirects do NOT work. Use {command, args, dir} and " +
			"sequence dependent steps as separate tool_calls.]"
	default:
		return "[Host: " + runtime.GOOS + " (Unix-like). run_command executes binaries directly — no shell — so " +
			"`&&`, `||`, `;`, `|`, `>`, redirects do NOT work. Use {command, args, dir} and " +
			"sequence dependent steps as separate tool_calls.]"
	}
}

// appendSystemNoticeText is a tiny join helper that avoids leading
// blank lines when the existing prompt is empty (rare but possible
// when buildSystemPromptBundle returns an empty bundle).
func appendSystemNoticeText(existing, notice string) string {
	if strings.TrimSpace(notice) == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return notice
	}
	return existing + "\n\n" + notice
}

// memoryDegradedSystemNotice formats the user-invisible system-prompt
// hint that warns the model recall is offline. Lives next to its only
// caller so future tweaks (wording, label) stay obvious. The reason
// is included verbatim so the model can decide whether the failure is
// recoverable (e.g. "database locked" → suggest /doctor) or terminal
// (corrupt store → fall back to project-only context).
func memoryDegradedSystemNotice(reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "store unavailable"
	}
	return "[Memory store is offline — do not rely on historical recall. " +
		"Memory.Search/Recall will return empty results regardless of what was " +
		"learned in prior sessions. Reason: " + r + "]"
}

// bundleToSystemBlocks converts a PromptBundle into the paired (flat text,
// SystemBlocks) form consumed by providers. When the bundle has no cacheable
// sections the blocks slice is nil so non-cache-aware providers keep the
// flat-string fast path.
func bundleToSystemBlocks(bundle *promptlib.PromptBundle) (string, []provider.SystemBlock) {
	if bundle == nil {
		return "", nil
	}
	text := bundle.Text()
	if !bundle.HasCacheable() {
		return text, nil
	}
	blocks := make([]provider.SystemBlock, 0, len(bundle.Sections))
	for _, s := range bundle.Sections {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		blocks = append(blocks, provider.SystemBlock{
			Label:     s.Label,
			Text:      s.Text,
			Cacheable: s.Cacheable,
		})
	}
	return text, blocks
}
