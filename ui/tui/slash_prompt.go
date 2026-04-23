// slash_prompt.go — the /prompt slash-command family. Exposes the
// merged prompt-template catalog (embedded defaults + ~/.dfmc/prompts
// + .dfmc/prompts) directly in chat so users can discover / audit
// templates without leaving the TUI. Runs off the local library with
// no provider call — safe on the offline / placeholder router.
//
//   - promptSlash: the dispatcher (list | show | recommend | render).
//   - formatPromptSlashList: filtered + paginated one-line-per-template
//     renderer with axis fingerprint (task/role/lang/profile).
//   - formatPromptSlashShow: full body + metadata for a single
//     template.
//
// filteredPrompts + nonEmpty + truncateCommandBlock are shared helpers
// and live in prompts.go / mention_helpers.go.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

// promptSlash exposes the merged prompt-template catalog (embedded
// defaults + ~/.dfmc/prompts + .dfmc/prompts) in chat. Supports:
//
//	/prompt                      → first page of templates (alias: /prompt list)
//	/prompt list [query]         → all templates, optionally filtered by substring
//	/prompt show <id>            → full body of a single template
//	/prompt recommend [question] → engine recommendation for a task
//	/prompt render <id>          → render the template with sample vars
//
// Runs purely off the local catalog — no provider call — so it's safe
// on the offline/placeholder router.
func (m Model) promptSlash(args []string) string {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}
	lib := promptlib.New()
	if m.eng != nil {
		// LoadOverrides is a no-op when the project root is blank; safe
		// to call in degraded-startup.
		_ = lib.LoadOverrides(m.eng.ProjectRoot)
	}
	templates := lib.List()

	switch sub {
	case "", "list", "ls":
		query := strings.TrimSpace(strings.Join(rest, " "))
		return formatPromptSlashList(templates, query)
	case "show", "cat", "body":
		if len(rest) == 0 {
			return "Usage: /prompt show <id> — pass a template id from /prompt list."
		}
		target := strings.TrimSpace(rest[0])
		for _, t := range templates {
			if strings.EqualFold(strings.TrimSpace(t.ID), target) {
				return formatPromptSlashShow(t)
			}
		}
		return fmt.Sprintf("No prompt template with id %q. Run /prompt list to see available ids.", target)
	case "recommend", "suggest":
		if m.eng == nil {
			return "Engine unavailable."
		}
		question := strings.TrimSpace(strings.Join(rest, " "))
		if question == "" {
			question = "Summarise the project"
		}
		rec := m.eng.PromptRecommendation(question)
		return fmt.Sprintf("Prompt recommendation for %q:\n  profile:    %s\n  role:       %s\n  task:       %s\n  budget:     %d tokens\n  tool_style: %s",
			question,
			nonEmpty(rec.Profile, "-"),
			nonEmpty(rec.Role, "-"),
			nonEmpty(rec.Task, "-"),
			rec.PromptBudgetTokens,
			nonEmpty(rec.ToolStyle, "-"),
		)
	case "render":
		if len(rest) == 0 {
			return "Usage: /prompt render <id> [query...] — renders the template with the sample query."
		}
		target := strings.TrimSpace(rest[0])
		var picked *promptlib.Template
		for i := range templates {
			if strings.EqualFold(strings.TrimSpace(templates[i].ID), target) {
				picked = &templates[i]
				break
			}
		}
		if picked == nil {
			return fmt.Sprintf("No prompt template with id %q.", target)
		}
		query := strings.TrimSpace(strings.Join(rest[1:], " "))
		if query == "" {
			query = "Example query for template preview."
		}
		req := promptlib.RenderRequest{
			Task:     picked.Task,
			Role:     picked.Role,
			Language: picked.Language,
			Profile:  picked.Profile,
			Vars:     map[string]string{"query": query, "task": picked.Task},
		}
		body := lib.Render(req)
		if strings.TrimSpace(body) == "" {
			body = "(empty render — template has no body after overlay composition)"
		}
		return fmt.Sprintf("Render of %s:\n%s", picked.ID, truncateCommandBlock(body, 4000))
	default:
		return "prompt: unknown subcommand. Try: list [query] | show <id> | recommend [question] | render <id> [query]"
	}
}

func formatPromptSlashList(templates []promptlib.Template, query string) string {
	filtered := filteredPrompts(templates, query)
	if len(filtered) == 0 {
		if query == "" {
			return "No prompt templates registered."
		}
		return fmt.Sprintf("No prompt templates match %q.", query)
	}
	var b strings.Builder
	if query == "" {
		fmt.Fprintf(&b, "Prompt templates (%d):\n", len(filtered))
	} else {
		fmt.Fprintf(&b, "Prompt templates matching %q (%d):\n", query, len(filtered))
	}
	for i, t := range filtered {
		if i >= 30 {
			fmt.Fprintf(&b, "  +%d more — narrow with /prompt list <query>\n", len(filtered)-i)
			break
		}
		axes := []string{}
		if t.Task != "" {
			axes = append(axes, "task="+t.Task)
		}
		if t.Role != "" {
			axes = append(axes, "role="+t.Role)
		}
		if t.Language != "" {
			axes = append(axes, "lang="+t.Language)
		}
		if t.Profile != "" {
			axes = append(axes, "profile="+t.Profile)
		}
		suffix := ""
		if len(axes) > 0 {
			suffix = "  (" + strings.Join(axes, " ") + ")"
		}
		fmt.Fprintf(&b, "  %s%s\n", t.ID, suffix)
	}
	b.WriteString("Show one with: /prompt show <id>")
	return strings.TrimRight(b.String(), "\n")
}

func formatPromptSlashShow(t promptlib.Template) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ %s", t.ID)
	if t.Type != "" {
		fmt.Fprintf(&b, "  [%s]", t.Type)
	}
	b.WriteString("\n")
	if t.Description != "" {
		fmt.Fprintf(&b, "  description: %s\n", t.Description)
	}
	if t.Task != "" {
		fmt.Fprintf(&b, "  task:        %s\n", t.Task)
	}
	if t.Role != "" {
		fmt.Fprintf(&b, "  role:        %s\n", t.Role)
	}
	if t.Language != "" {
		fmt.Fprintf(&b, "  language:    %s\n", t.Language)
	}
	if t.Profile != "" {
		fmt.Fprintf(&b, "  profile:     %s\n", t.Profile)
	}
	if t.Compose != "" {
		fmt.Fprintf(&b, "  compose:     %s\n", t.Compose)
	}
	if t.Priority != 0 {
		fmt.Fprintf(&b, "  priority:    %d\n", t.Priority)
	}
	b.WriteString("  body:\n")
	b.WriteString(truncateCommandBlock(t.Body, 4000))
	return strings.TrimRight(b.String(), "\n")
}
