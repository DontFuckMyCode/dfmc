package skills

// catalog_render.go — string-rendering and query-marker helpers for
// the skills catalog: DecorateQuery + StripMarkers (the [[skill:X]]
// marker injectors/strippers used by ResolveForQuery and prompt
// composition), RenderSystemText (assembles the per-skill system
// block injected into the request system prompt), Skill.System
// Instruction (the user-supplied body extractor), explicitNames (the
// regex-based marker scanner), and skillForTask (the task→skill name
// mapper used by ResolveForQuery's auto-pick fallback).
//
// Sibling of catalog.go which keeps the public Skill+Selection types,
// the Discover/Lookup/SkillForName/ResolveForQuery surface, and the
// regex-state shared with this file.

import (
	"fmt"
	"strings"
)

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
