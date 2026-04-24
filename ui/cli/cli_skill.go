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
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"gopkg.in/yaml.v3"
)

type skillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path,omitempty"`
	Source      string   `json:"source"`
	Builtin     bool     `json:"builtin"`
	Prompt      string   `json:"-"`
	Preferred   []string `json:"preferred,omitempty"`
	Allowed     []string `json:"allowed,omitempty"`
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
	case "export":
		return runSkillExport(args[1:])
	case "install":
		return runSkillInstall(args[1:])
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
			Preferred:   item.Preferred,
			Allowed:     item.Allowed,
		})
	}
	return out
}

// runSkillExport writes a skill's YAML definition to stdout.
// Usage: dfmc skill export <name>
func runSkillExport(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc skill export <name>")
		return 2
	}
	name := strings.TrimSpace(args[0])
	item, ok := skills.Lookup("", name)
	if !ok {
		fmt.Fprintf(os.Stderr, "skill %q not found\n", name)
		return 1
	}
	if item.Builtin {
		fmt.Fprintf(os.Stderr, "warning: %q is a builtin — export shows source fields only\n", name)
	}
	rec := skillExportRecord{
		Name:           item.Name,
		Description:    item.Description,
		Task:           item.Task,
		Role:           item.Role,
		Profile:        item.Profile,
		PreferredTools: item.Preferred,
		AllowedTools:   item.Allowed,
	}
	if body := strings.TrimSpace(item.System); body != "" {
		rec.SystemPrompt = body
	} else if body := strings.TrimSpace(item.Prompt); body != "" {
		rec.Prompt = body
	}
	data, err := yaml.Marshal(rec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaml marshal: %v\n", err)
		return 1
	}
	fmt.Print(string(data))
	return 0
}

type skillExportRecord struct {
	Name           string   `yaml:"name"`
	Description    string   `yaml:"description,omitempty"`
	Task           string   `yaml:"task,omitempty"`
	Role           string   `yaml:"role,omitempty"`
	Profile        string   `yaml:"profile,omitempty"`
	PreferredTools []string `yaml:"preferred_tools,omitempty"`
	AllowedTools   []string `yaml:"allowed_tools,omitempty"`
	Prompt         string   `yaml:"prompt,omitempty"`
	SystemPrompt   string   `yaml:"system_prompt,omitempty"`
}

// runSkillInstall copies a skill YAML file to .dfmc/skills/.
// Usage: dfmc skill install <path>
func runSkillInstall(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc skill install <path>")
		return 2
	}
	src := strings.TrimSpace(args[0])
	if src == "" {
		fmt.Fprintln(os.Stderr, "path required")
		return 2
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", src, err)
		return 1
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s as YAML: %v\n", src, err)
		return 1
	}
	nameRaw, ok := raw["name"]
	if !ok {
		fmt.Fprintln(os.Stderr, "YAML must have a top-level 'name' field")
		return 1
	}
	name := strings.TrimSpace(fmt.Sprint(nameRaw))
	if name == "" {
		fmt.Fprintln(os.Stderr, "'name' field cannot be empty")
		return 1
	}
	// Determine install target (prefer project over global)
	projectRoot := os.Getenv("DFMC_PROJECT_ROOT")
	targetDir := filepath.Join(config.UserConfigDir(), "skills")
	if projectRoot != "" {
		targetDir = filepath.Join(projectRoot, ".dfmc", "skills")
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create skills dir %s: %v\n", targetDir, err)
		return 1
	}
	dst := filepath.Join(targetDir, name+".yaml")
	if _, err := os.Stat(dst); err == nil {
		fmt.Fprintf(os.Stderr, "skill %q already exists at %s\n", name, dst)
		return 1
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", dst, err)
		return 1
	}
	fmt.Printf("installed: %s\n", dst)
	return 0
}
