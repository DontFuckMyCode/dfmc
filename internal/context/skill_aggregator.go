package context

// skill_aggregator.go — turns the resolved skill set from
// skills.ResolveForQuery into prompt-bundle sections and the
// summarized inventory text that goes into the system prompt.
//
// summarizeActiveSkills produces the comma-separated active list
// shown in the runtime card and embedded in {active_skills}.
// summarizeSkillInventory produces the bullet-list of all
// discovered skills (with "(active)" markers on the resolved set).
// appendSkillSections prepends one cacheable section per active
// skill so trim-to-budget still keeps stable prefix sections
// addressable. appendSkillInventorySection inserts the inventory
// summary right after the system section so trim-to-budget treats
// it as part of the stable prefix.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/skills"
)

func appendSkillInventorySection(bundle *promptlib.PromptBundle, active, inventory string) *promptlib.PromptBundle {
	if bundle == nil {
		return bundle
	}
	inventory = strings.TrimSpace(inventory)
	if inventory == "" || inventory == "(none)" {
		return bundle
	}
	active = strings.TrimSpace(active)
	if active == "" {
		active = "(none active)"
	}
	text := "Skills inventory:\nActive: " + active + "\n" + inventory
	section := promptlib.PromptSection{
		Label:     "skills-inventory",
		Text:      text,
		Cacheable: true,
	}
	sections := make([]promptlib.PromptSection, 0, len(bundle.Sections)+1)
	inserted := false
	for _, existing := range bundle.Sections {
		if !inserted && existing.Label == "system" {
			sections = append(sections, existing, section)
			inserted = true
			continue
		}
		sections = append(sections, existing)
	}
	if !inserted {
		sections = append([]promptlib.PromptSection{section}, sections...)
	}
	bundle.Sections = sections
	return bundle
}

func appendSkillSections(bundle *promptlib.PromptBundle, active []skills.Skill) *promptlib.PromptBundle {
	if bundle == nil || len(active) == 0 {
		return bundle
	}
	extras := make([]promptlib.PromptSection, 0, len(active))
	for _, skill := range active {
		text := strings.TrimSpace(skills.RenderSystemText(skill))
		if text == "" {
			continue
		}
		extras = append(extras, promptlib.PromptSection{
			Label:     "skill." + strings.ToLower(strings.TrimSpace(skill.Name)),
			Text:      text,
			Cacheable: true,
		})
	}
	if len(extras) == 0 {
		return bundle
	}

	sections := make([]promptlib.PromptSection, 0, len(bundle.Sections)+len(extras))
	// Prepend skill extras before all existing sections. The stable section
	// (system prompt, tool policies) is typically 4000+ chars while skill playbooks
	// are ~700 chars. trimBundleToBudget scans top-to-bottom and each cacheable
	// section takes its proportional share — inserting AFTER the stable section
	// would let stable consume the budget before skill sections are even reached.
	// Prepending puts skill text first so it gets priority when trimming kicks in.
	sections = append(sections, extras...)
	sections = append(sections, bundle.Sections...)
	bundle.Sections = sections
	return bundle
}

func summarizeActiveSkills(active []skills.Skill) string {
	if len(active) == 0 {
		return "(none active)"
	}
	names := make([]string, 0, len(active))
	for _, skill := range active {
		if name := strings.TrimSpace(skill.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func summarizeSkillInventory(projectRoot string, active []skills.Skill, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	activeSet := make(map[string]struct{}, len(active))
	for _, skill := range active {
		if name := strings.ToLower(strings.TrimSpace(skill.Name)); name != "" {
			activeSet[name] = struct{}{}
		}
	}
	items := skills.Discover(projectRoot)
	lines := make([]string, 0, min(limit, len(items)))
	for _, skill := range items {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(skill.Description)
		if desc == "" {
			desc = strings.TrimSpace(skill.Task)
		}
		if desc == "" {
			desc = "project-specific guidance"
		}
		marker := ""
		if _, ok := activeSet[strings.ToLower(name)]; ok {
			marker = " (active)"
		}
		lines = append(lines, "- "+name+marker+" - "+desc)
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return "(none)"
	}
	return strings.Join(lines, "\n")
}
