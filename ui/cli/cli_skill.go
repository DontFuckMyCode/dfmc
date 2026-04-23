// cli_skill.go — `dfmc skill` command surface and skill discovery.
// Skills are short, prompt-shaped role overlays (review, explain,
// test, doc, ...) that the CLI exposes both as their own subcommand
// (`dfmc skill run <name> [input]`) and as top-level shortcuts
// (`dfmc review ./pkg/foo`, wired via runSkillShortcut). Discovery
// layers on top of internal/skills.Discover — builtin skills from
// cli_skills_data.go first, then anything the user drops into
// `.dfmc/skills/`. Plugin-side surface (installers, manifests,
// binary RPC) lives in cli_plugin_skill.go alongside its installers,
// since they share plugin-directory resolution; skills are the
// lighter sibling and stand on their own here.

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/skills"
)

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Source      string `json:"source"`
	Builtin     bool   `json:"builtin"`
	Prompt      string `json:"-"`
}

func runSkill(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items := discoverSkills(eng.Status().ProjectRoot)
		if jsonMode {
			mustPrintJSON(map[string]any{"skills": items})
			return 0
		}
		for _, s := range items {
			label := s.Source
			if s.Builtin {
				label = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, label)
		}
		return 0

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		items := discoverSkills(eng.Status().ProjectRoot)
		for _, s := range items {
			if strings.EqualFold(s.Name, name) {
				if jsonMode {
					mustPrintJSON(s)
				} else {
					fmt.Printf("Name:        %s\n", s.Name)
					fmt.Printf("Source:      %s\n", s.Source)
					fmt.Printf("Builtin:     %t\n", s.Builtin)
					if s.Description != "" {
						fmt.Printf("Description: %s\n", s.Description)
					}
					if s.Path != "" {
						fmt.Printf("Path:        %s\n", s.Path)
					}
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
		return 1

	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill run <name> [input]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		input := strings.TrimSpace(strings.Join(args[2:], " "))
		return runNamedSkill(ctx, eng, name, input, jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc skill [list|info <name>|run <name> [input]]")
		return 2
	}
}

func runSkillShortcut(ctx context.Context, eng *engine.Engine, name string, args []string, jsonMode bool) int {
	input := strings.TrimSpace(strings.Join(args, " "))
	if input == "" {
		input = "Analyze the current project."
	}
	return runNamedSkill(ctx, eng, name, input, jsonMode)
}

func runNamedSkill(ctx context.Context, eng *engine.Engine, name, input string, jsonMode bool) int {
	item, ok := skills.Lookup(eng.Status().ProjectRoot, name)
	if !ok {
		fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
		return 1
	}
	prompt := skills.DecorateQuery(item.Name, input)
	answer, err := eng.Ask(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill run failed: %v\n", err)
		return 1
	}
	if jsonMode {
		_ = printJSON(map[string]any{
			"skill":  item.Name,
			"source": item.Source,
			"input":  input,
			"answer": answer,
		})
		return 0
	}
	fmt.Println(answer)
	return 0
}

func buildSkillPrompt(skill skillInfo, input string) string {
	p := strings.TrimSpace(skill.Prompt)
	if p == "" {
		p = input
	} else if strings.Contains(p, "{input}") {
		p = strings.ReplaceAll(p, "{input}", input)
	} else if strings.TrimSpace(input) != "" {
		p = p + "\n\nUser request:\n" + input
	}
	return p
}

func discoverSkills(projectRoot string) []skillInfo {
	raw := skills.Discover(projectRoot)
	out := make([]skillInfo, 0, len(raw))
	for _, item := range raw {
		out = append(out, skillInfo{
			Name:        item.Name,
			Description: item.Description,
			Path:        item.Path,
			Source:      item.Source,
			Builtin:     item.Builtin,
			Prompt:      item.SystemInstruction(),
		})
	}
	return out
}
