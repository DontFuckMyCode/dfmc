package tui

// describe.go — transcript-level operations: export to markdown and
// compact-into-summary. The "what does the system look like right now"
// describe helpers moved out to describe_workflow.go (/stats, /workflow,
// /todos, /subagents, /queue) and describe_health.go (/doctor, /health,
// /hooks, /approve, /intent). Keeping the transcript transforms in this
// file so the "touches m.chat.transcript" surface is small and obvious.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m Model) exportTranscript(target string) (string, error) {
	if len(m.chat.transcript) == 0 {
		return "", fmt.Errorf("transcript is empty")
	}
	projectRoot := strings.TrimSpace(m.projectRoot())
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		projectRoot = cwd
	}
	if target == "" {
		stamp := time.Now().Format("20060102-150405")
		target = filepath.Join(".dfmc", "exports", "transcript-"+stamp+".md")
	}
	// Resolve against project root when relative.
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectRoot, target)
	}
	// Make sure the parent directory exists. MkdirAll is a no-op when it
	// already does, so safe to call unconditionally.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "# DFMC transcript — %s\n\n", time.Now().Format(time.RFC3339))
	if provider := strings.TrimSpace(m.status.Provider); provider != "" {
		fmt.Fprintf(&buf, "_provider:_ `%s`", provider)
		if model := strings.TrimSpace(m.status.Model); model != "" {
			fmt.Fprintf(&buf, " · _model:_ `%s`", model)
		}
		buf.WriteString("\n\n")
	}
	for _, line := range m.chat.transcript {
		role := strings.ToLower(strings.TrimSpace(string(line.Role)))
		content := strings.TrimRight(line.Content, "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		switch role {
		case "user":
			fmt.Fprintf(&buf, "## user\n\n%s\n\n", content)
		case "assistant":
			fmt.Fprintf(&buf, "## assistant\n\n%s\n\n", content)
		case "tool":
			fmt.Fprintf(&buf, "### [tool] %s\n\n%s\n\n", strings.Join(line.ToolNames, ", "), content)
		case "system":
			fmt.Fprintf(&buf, "### [system]\n\n%s\n\n", content)
		default:
			fmt.Fprintf(&buf, "### [%s]\n\n%s\n\n", role, content)
		}
	}

	if err := os.WriteFile(target, []byte(buf.String()), 0o644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	return target, nil
}

// compactTranscript collapses all transcript entries older than the last
// `keep` into a single system-role summary line so a long session stays
// scannable. Purely a view-layer operation — the engine's own memory and
// conversation store are untouched.
//
// Returns the new transcript, the number of lines that were collapsed,
// and ok=true iff there was actually something to compact. We compact
// only when there are older lines AND they include at least one user or
// assistant turn — summarising a tail of system/tool chatter gains
// nothing and just inflates the notice.
func compactTranscript(lines []chatLine, keep int) ([]chatLine, int, bool) {
	if keep <= 0 {
		keep = 1
	}
	if len(lines) <= keep {
		return lines, 0, false
	}
	head := lines[:len(lines)-keep]
	tail := lines[len(lines)-keep:]

	// Count by role so the summary carries a useful one-glance fingerprint
	// ("5 user turns, 5 assistant replies, 12 tool events, 2 system notes").
	users, assistants, tools, systems, other := 0, 0, 0, 0, 0
	for _, ln := range head {
		switch strings.ToLower(strings.TrimSpace(string(ln.Role))) {
		case "user":
			users++
		case "assistant":
			assistants++
		case "tool":
			tools++
		case "system":
			systems++
		default:
			other++
		}
	}
	if users == 0 && assistants == 0 && tools == 0 {
		// Only a run of system lines to collapse — not worth a summary.
		return lines, 0, false
	}
	fingerprint := make([]string, 0, 5)
	if users > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d user", users))
	}
	if assistants > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d assistant", assistants))
	}
	if tools > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d tool", tools))
	}
	if systems > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d system", systems))
	}
	if other > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d other", other))
	}
	summary := newChatLine(chatRoleSystem,
		fmt.Sprintf("▸ Transcript compacted — %s collapsed. Full history kept in Conversations panel.",
			strings.Join(fingerprint, ", ")))

	out := make([]chatLine, 0, 1+keep)
	out = append(out, summary)
	out = append(out, tail...)
	return out, len(head), true
}
