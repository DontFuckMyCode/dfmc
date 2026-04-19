package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description"`
	Path        string   `json:"path,omitempty" yaml:"-"`
	Source      string   `json:"source" yaml:"-"`
	Builtin     bool     `json:"builtin" yaml:"-"`
	Prompt      string   `json:"-" yaml:"prompt"`
	System      string   `json:"-" yaml:"system_prompt"`
	Task        string   `json:"task,omitempty" yaml:"task"`
	Role        string   `json:"role,omitempty" yaml:"role"`
	Profile     string   `json:"profile,omitempty" yaml:"profile"`
	Preferred   []string `json:"preferred_tools,omitempty" yaml:"preferred_tools"`
	Allowed     []string `json:"allowed_tools,omitempty" yaml:"allowed_tools"`
}

type Selection struct {
	Query    string
	Skills   []Skill
	Explicit bool
}

var (
	skillMarkerRE     = regexp.MustCompile(`\[\[skill:([A-Za-z0-9._/-]+)\]\]`)
	trailingRequestRE = regexp.MustCompile(`(?is)\n*(user request|request)\s*:\s*$`)
)

func Discover(projectRoot string) []Skill {
	out := make([]Skill, 0, 16)
	seen := map[string]struct{}{}

	for _, item := range builtinCatalog() {
		key := strings.ToLower(strings.TrimSpace(item.Name))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}

	roots := []struct {
		path   string
		source string
	}{}
	if strings.TrimSpace(projectRoot) != "" {
		roots = append(roots, struct {
			path   string
			source string
		}{
			path:   filepath.Join(projectRoot, ".dfmc", "skills"),
			source: "project",
		})
	}
	roots = append(roots, struct {
		path   string
		source string
	}{
		path:   filepath.Join(config.UserConfigDir(), "skills"),
		source: "global",
	})

	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, path := range files {
			item := readSkillFile(path, root.source)
			key := strings.ToLower(strings.TrimSpace(item.Name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func Lookup(projectRoot, name string) (Skill, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Skill{}, false
	}
	for _, item := range Discover(projectRoot) {
		if strings.EqualFold(item.Name, name) {
			return item, true
		}
	}
	return Skill{}, false
}

func ResolveForQuery(projectRoot, query, detectedTask string) Selection {
	all := Discover(projectRoot)
	byName := make(map[string]Skill, len(all))
	for _, item := range all {
		byName[strings.ToLower(strings.TrimSpace(item.Name))] = item
	}

	add := func(dst *[]Skill, seen map[string]struct{}, name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		item, ok := byName[key]
		if !ok {
			return
		}
		seen[key] = struct{}{}
		*dst = append(*dst, item)
	}

	selected := make([]Skill, 0, 2)
	selectedSeen := map[string]struct{}{}
	explicit := explicitNames(query)
	for _, name := range explicit {
		add(&selected, selectedSeen, name)
	}
	if len(selected) == 0 {
		if mapped := skillForTask(detectedTask); mapped != "" {
			add(&selected, selectedSeen, mapped)
		}
	}

	return Selection{
		Query:    StripMarkers(query),
		Skills:   selected,
		Explicit: len(explicit) > 0,
	}
}

func (s Selection) Primary() (Skill, bool) {
	if len(s.Skills) == 0 {
		return Skill{}, false
	}
	return s.Skills[0], true
}

func DecorateQuery(name, input string) string {
	name = strings.TrimSpace(name)
	input = strings.TrimSpace(input)
	if name == "" {
		return input
	}
	if input == "" {
		return "[[skill:" + name + "]]"
	}
	return fmt.Sprintf("[[skill:%s]]\n%s", name, input)
}

func StripMarkers(query string) string {
	clean := skillMarkerRE.ReplaceAllString(query, "")
	clean = strings.TrimSpace(clean)
	for strings.Contains(clean, "\n\n\n") {
		clean = strings.ReplaceAll(clean, "\n\n\n", "\n\n")
	}
	return clean
}

func RenderSystemText(skill Skill) string {
	var b strings.Builder
	b.WriteString("Activated skill: ")
	b.WriteString(skill.Name)
	if desc := strings.TrimSpace(skill.Description); desc != "" {
		b.WriteString(" — ")
		b.WriteString(desc)
	}
	if task := strings.TrimSpace(skill.Task); task != "" || strings.TrimSpace(skill.Role) != "" || strings.TrimSpace(skill.Profile) != "" {
		b.WriteString("\nRuntime hints:")
		if task != "" {
			b.WriteString("\n- task hint: ")
			b.WriteString(task)
		}
		if role := strings.TrimSpace(skill.Role); role != "" {
			b.WriteString("\n- role hint: ")
			b.WriteString(role)
		}
		if profile := strings.TrimSpace(skill.Profile); profile != "" {
			b.WriteString("\n- profile hint: ")
			b.WriteString(profile)
		}
	}
	if len(skill.Preferred) > 0 {
		b.WriteString("\nPreferred tools:")
		for _, name := range skill.Preferred {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(name)
		}
	}
	if len(skill.Allowed) > 0 {
		b.WriteString("\nScope guard:")
		b.WriteString("\n- Prefer staying within these tools unless a stronger justification exists: ")
		b.WriteString(strings.Join(skill.Allowed, ", "))
	}
	if body := strings.TrimSpace(skill.SystemInstruction()); body != "" {
		b.WriteString("\nSkill contract:\n")
		b.WriteString(body)
	}
	return strings.TrimSpace(b.String())
}

func (s Skill) SystemInstruction() string {
	if body := strings.TrimSpace(s.System); body != "" {
		return body
	}
	body := strings.TrimSpace(strings.ReplaceAll(s.Prompt, "{input}", ""))
	body = trailingRequestRE.ReplaceAllString(body, "")
	return strings.TrimSpace(body)
}

func explicitNames(query string) []string {
	matches := skillMarkerRE.FindAllStringSubmatch(query, -1)
	out := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := strings.TrimSpace(m[1])
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func skillForTask(task string) string {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "review":
		return "review"
	case "refactor":
		return "refactor"
	case "debug":
		return "debug"
	case "test":
		return "test"
	case "doc":
		return "doc"
	case "security":
		return "audit"
	case "planning":
		return "onboard"
	default:
		return ""
	}
}

func readSkillFile(path, source string) Skill {
	item := Skill{
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:    path,
		Source:  source,
		Builtin: false,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return item
	}
	if err := yaml.Unmarshal(data, &item); err == nil && strings.TrimSpace(item.Name) != "" {
		item.Source = source
		item.Path = path
		return item
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return item
	}
	if v, ok := raw["name"]; ok {
		if name := strings.TrimSpace(fmt.Sprint(v)); name != "" {
			item.Name = name
		}
	}
	if v, ok := raw["description"]; ok {
		item.Description = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["prompt"]; ok {
		item.Prompt = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.Prompt == "" {
		if v, ok := raw["template"]; ok {
			item.Prompt = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if v, ok := raw["system_prompt"]; ok {
		item.System = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.System == "" {
		if v, ok := raw["system"]; ok {
			item.System = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	if v, ok := raw["task"]; ok {
		item.Task = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["role"]; ok {
		item.Role = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["profile"]; ok {
		item.Profile = strings.TrimSpace(fmt.Sprint(v))
	}
	item.Preferred = parseStringList(raw["preferred_tools"])
	item.Allowed = parseStringList(raw["allowed_tools"])
	return item
}

func parseStringList(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case []string:
		return cleanStringList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStringList(out)
	case string:
		return cleanStringList(strings.Split(v, ","))
	default:
		return cleanStringList([]string{fmt.Sprint(v)})
	}
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func builtinCatalog() []Skill {
	return []Skill{
		{
			Name:        "review",
			Description: "Code review: correctness, risk, missing tests, security smells",
			Source:      "builtin",
			Builtin:     true,
			Task:        "review",
			Role:        "code_reviewer",
			Preferred:   []string{"git_diff", "read_file", "grep_codebase", "find_symbol", "git_blame"},
			System: `You are running the REVIEW skill. Review the changed code for correctness, risk, and test coverage — not style nits.

Playbook:
1. Scope. Identify exactly what changed using git and file/context tools before judging it.
2. Correctness. Trace the happy path and at least one failure path for every non-trivial change.
3. Behavioral risk. Flag silent changes to APIs, side effects, persistence, concurrency, or performance.
4. Tests. Name the exact missing test when a gap matters.
5. Security/resource. Check for real security and reliability regressions, not hypothetical lint.
6. Report. Structure findings as Must-fix / Should-fix / Nits / Tests to add with file:line evidence.

Do not restate what the code already says. Do not pad the review when the change is clean.`,
		},
		{
			Name:        "explain",
			Description: "Explain code: trace the flow, name the invariants, call out surprises",
			Source:      "builtin",
			Builtin:     true,
			Preferred:   []string{"find_symbol", "read_file", "grep_codebase", "codemap"},
			System: `You are running the EXPLAIN skill. Produce a working mental model of the target, not a paraphrase of the source.

Playbook:
1. Locate the true entry point or slice of code being asked about.
2. Trace one representative flow end-to-end with concrete file:line evidence.
3. Name invariants and who enforces them.
4. Call out non-obvious constraints, ordering, or design surprises.
5. Use a tiny plaintext diagram when multiple files or paths matter.
6. Lead with the answer, then the evidence.

Do not narrate line-by-line. Do not guess when more files need to be read.`,
		},
		{
			Name:        "refactor",
			Description: "Plan and execute a safe refactor: scope, invariants, step list, verification",
			Source:      "builtin",
			Builtin:     true,
			Task:        "refactor",
			Preferred:   []string{"grep_codebase", "find_symbol", "edit_file", "apply_patch", "run_command"},
			System: `You are running the REFACTOR skill. Ship a concrete, reversible refactor — not a design essay.

Playbook:
1. State what is in scope and out of scope.
2. List observable behaviors that must not change.
3. Break work into the smallest safe sequence.
4. Make minimal edits that improve structure without widening the change.
5. Verify changed behavior with targeted tests or builds.
6. Summarize what moved, what stayed, and what you verified.

Stop and ask if the request implies a hidden behavior change.`,
		},
		{
			Name:        "debug",
			Description: "Reproduce, bisect, and fix a bug — with a regression test",
			Source:      "builtin",
			Builtin:     true,
			Task:        "debug",
			Preferred:   []string{"run_command", "grep_codebase", "read_file", "git_blame", "edit_file"},
			System: `You are running the DEBUG skill. Root-cause the problem; do not paper over it.

Playbook:
1. Reproduce the issue with a concrete command or test when possible.
2. Narrow the fault to a specific file, function, config, or commit.
3. Explain the failure mechanism clearly before patching.
4. Fix the root cause with the smallest durable change.
5. Add or update a regression test when practical.
6. Verify the reported path and the nearest affected package.

If you cannot reproduce, say so clearly instead of guessing.`,
		},
		{
			Name:        "test",
			Description: "Generate or improve tests: discover framework, find gaps, implement, run",
			Source:      "builtin",
			Builtin:     true,
			Task:        "test",
			Preferred:   []string{"read_file", "grep_codebase", "edit_file", "write_file", "run_command"},
			System: `You are running the TEST skill. Ship tests that actually execute, not pseudocode.

Playbook:
1. Mirror the repo's existing test style and framework.
2. Map important branches, edge cases, and regressions.
3. Add the smallest high-value tests first.
4. Keep tests deterministic and isolated.
5. Run them and report real output.
6. Name the residual risk you intentionally left untested.

Do not add ornate mocking layers the repository does not already use.`,
		},
		{
			Name:        "doc",
			Description: "Write documentation that teaches the code, not the signature",
			Source:      "builtin",
			Builtin:     true,
			Task:        "doc",
			Preferred:   []string{"read_file", "find_symbol", "grep_codebase"},
			System: `You are running the DOC skill. Write documentation a future engineer can act on — not a pretty-printed function signature.

Playbook:
1. Read the code before documenting it.
2. Choose the documentation shape that matches the repo.
3. Explain purpose, usage constraints, and sharp edges.
4. Prefer one concrete example over abstract prose.
5. Link to existing code/tests instead of duplicating them.
6. Keep documentation implementation-aligned and concise.

Do not document trivially obvious code.`,
		},
		{
			Name:        "generate",
			Description: "Generate new code that obeys the project's conventions and tests it",
			Source:      "builtin",
			Builtin:     true,
			Preferred:   []string{"read_file", "grep_codebase", "edit_file", "write_file", "run_command"},
			System: `You are running the GENERATE skill. Ship working, tested code — not scaffolding.

Playbook:
1. Restate the requested behavior precisely.
2. Read nearby sibling code and mirror its conventions.
3. Place the code in the existing architectural boundary that fits best.
4. Write the smallest complete version that works.
5. Add at least one meaningful test.
6. Wire registration/export/import changes in the same patch.
7. Verify with build and targeted tests.

Do not introduce speculative abstractions or dead options.`,
		},
		{
			Name:        "audit",
			Description: "Security audit: triaged findings with file:line, severity, and fix direction",
			Source:      "builtin",
			Builtin:     true,
			Task:        "security",
			Role:        "security_auditor",
			Preferred:   []string{"grep_codebase", "read_file", "find_symbol", "git_blame"},
			System: `You are running the AUDIT skill. Produce a triaged security report — exploitable findings first, theoretical concerns last.

Playbook:
1. Define the trust boundary being audited.
2. Check likely sinks and taint flow for the language and subsystem.
3. Confirm each hit before calling it a finding.
4. Triage by exploitability and impact.
5. Give one concrete remediation direction per finding.
6. Separate confirmed findings from things reviewed and cleared.

Do not invent findings to pad the report.`,
		},
		{
			Name:        "onboard",
			Description: "Codebase walkthrough: hot paths, surprises, where to start changing",
			Source:      "builtin",
			Builtin:     true,
			Task:        "planning",
			Role:        "planner",
			Preferred:   []string{"codemap", "read_file", "find_symbol", "list_dir"},
			System: `You are running the ONBOARD skill. Give a new contributor the shortest path to being productive — not a table of contents.

Playbook:
1. State what the project actually does.
2. Identify the execution hub and entry point.
3. Trace one representative flow end-to-end.
4. Summarize the top modules and what each owns.
5. Call out non-obvious constraints and surprises.
6. End with three small, concrete first-commit ideas.

Do not list every file or merely restate the directory tree.`,
		},
	}
}
