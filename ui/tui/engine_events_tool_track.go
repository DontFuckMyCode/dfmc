package tui

// engine_events_tool_track.go — turn-level mutation + validation
// ledger that handleToolEvent feeds. Tracks which files an
// edit_file/write_file/apply_patch touched in the current turn (live
// "unvalidated" badge + per-turn changed-file roster for the summary
// card) and clears the live ledger when run_command fires a known
// validation invocation. Sibling to engine_events_tool.go which keeps
// handleToolEvent itself.

import "strings"

// trackMutationOrValidation is called once per successful tool event.
// Mutation tools (edit_file/write_file/apply_patch) fan their
// changed_files into both the live "still unvalidated" list and the
// per-turn changed-file roster; validation tools (the run_command set
// in isValidationCommand) clear the live list and bump the
// validation-passes counter. Other tools no-op.
//
// Thread-safe? Caller is the bubbletea reducer, which is single-
// threaded by construction, so no extra synchronisation required.
//
// Filter: only fires when the event payload says success=true. The
// caller checks success before invoking us, so we can trust
// success=true so we don't have to re-validate here.
//
// `payload` carries `changed_files` (engine populates for edit_file/
// write_file/apply_patch via nativeToolEventMetadata) and `command`
// (populated for run_command). Tools that touch neither are silently
// ignored — the ledger only changes on signal events.
func (m Model) trackMutationOrValidation(toolName string, payload map[string]any, step int) Model {
	switch toolName {
	case "edit_file", "write_file", "apply_patch":
		paths := payloadStringSlice(payload, "changed_files")
		if len(paths) == 0 {
			return m
		}
		if len(m.agentLoop.unvalidatedEdits) == 0 {
			m.agentLoop.unvalidatedSinceStep = step
		}
		// turnEditedFiles is the same de-dup'd accumulator as
		// unvalidatedEdits but survives validation — it answers "what
		// did this turn touch overall" for the final summary card,
		// while unvalidatedEdits tracks "what's still unverified
		// RIGHT NOW" for the live badge.
		seenLive := make(map[string]struct{}, len(m.agentLoop.unvalidatedEdits))
		for _, p := range m.agentLoop.unvalidatedEdits {
			seenLive[p] = struct{}{}
		}
		seenTurn := make(map[string]struct{}, len(m.agentLoop.turnEditedFiles))
		for _, p := range m.agentLoop.turnEditedFiles {
			seenTurn[p] = struct{}{}
		}
		for _, p := range paths {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			if _, dup := seenLive[p]; !dup {
				m.agentLoop.unvalidatedEdits = append(m.agentLoop.unvalidatedEdits, p)
				seenLive[p] = struct{}{}
			}
			if _, dup := seenTurn[p]; !dup {
				m.agentLoop.turnEditedFiles = append(m.agentLoop.turnEditedFiles, p)
				seenTurn[p] = struct{}{}
			}
		}
	case "run_command":
		cmd := strings.TrimSpace(payloadString(payload, "command", ""))
		if cmd == "" {
			return m
		}
		if isValidationCommand(cmd) {
			m.agentLoop.unvalidatedEdits = nil
			m.agentLoop.unvalidatedSinceStep = 0
			m.agentLoop.turnValidationPasses++
		}
	}
	return m
}

// isValidationCommand recognises shell commands that constitute a
// validation pass — running one of these clears the unvalidated-edits
// ledger because the model has at least attempted to verify its work.
// We match on the leading token so flag variants ("go test ./...",
// "go test -race -count=1 ./internal/engine/...") all count.
//
// The list mirrors coach.answerMentionsValidation in spirit but
// matches command syntax instead of free-form prose. Keep them in
// rough sync — both surfaces ask the same question ("did the agent
// actually validate this?") just at different layers.
func isValidationCommand(cmd string) bool {
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	if cmd == "" {
		return false
	}
	// Validation prefixes — first one or two tokens. Order matters: more
	// specific multi-word prefixes (e.g. "go test") come before the
	// single-word match so "go run" doesn't accidentally count.
	multi := []string{
		"go test", "go vet", "go build",
		"npm test", "pnpm test", "yarn test",
		"npm run test", "pnpm run test", "yarn run test",
		"cargo test", "cargo check", "cargo build", "cargo clippy",
	}
	for _, prefix := range multi {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	single := []string{"pytest", "tsc", "eslint", "biome", "mypy", "ruff", "make"}
	for _, prefix := range single {
		if cmd == prefix || strings.HasPrefix(cmd, prefix+" ") {
			return true
		}
	}
	return false
}
