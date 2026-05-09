package skills

// scaffold.go — `dfmc skill new <name>` template generator. Writes
// a starter SKILL.md (agentskills.io flavoured frontmatter + 6-step
// playbook body) so authors don't have to copy/paste from docs to
// start a new skill.
//
// Two flavours:
//
//   - SkillTemplateSimple: prompt-overlay only (name, description,
//     allowed-tools, body). Mirrors the smallest skill that actually
//     does something.
//   - SkillTemplateTriggered: adds a `triggers:` block with a
//     placeholder pattern so the author sees the auto-activation
//     wiring. This is the form most users want — it's the agentskills.io
//     killer feature, and a starter without it teaches the wrong shape.
//
// The generated body is intentionally a placeholder — clear comments
// in the playbook tell the author "replace this with your skill's
// actual instructions". Shipping a real-looking body invites users
// to delete-only-the-header changes and end up with a skill that
// claims to do X but contains the AUDIT playbook.

import (
	"fmt"
	"strings"
)

// SkillTemplate selects which scaffold flavour Render emits.
type SkillTemplate string

const (
	SkillTemplateSimple    SkillTemplate = "simple"
	SkillTemplateTriggered SkillTemplate = "triggered"
)

// RenderSkillTemplate produces the starter SKILL.md text for `name`.
// The output includes frontmatter and a markdown body — it's a
// drop-in file ready to be written to .dfmc/skills/<name>/SKILL.md.
//
// `name` is normalised to the agentskills.io-permitted character set
// (letters, digits, dot, dash, underscore, slash) by trimming
// whitespace; callers should pre-validate the name shape via
// ValidateSkillBytes if they want to fail loudly on bad input.
func RenderSkillTemplate(name string, kind SkillTemplate) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "my-skill"
	}
	header := fmt.Sprintf(`---
name: %s
description: One-line summary of what this skill does and when it should fire.
allowed-tools: read_file grep_codebase find_symbol
preferred-tools: read_file
version: 0.1.0
metadata:
  author: your-name
  tags: [project, custom]
`, name)
	if kind == SkillTemplateTriggered {
		header += `triggers:
  - pattern: "TODO replace with a regex that matches user queries this skill should handle"
    weight: 0.85
  - "another|alternative|trigger:0.7"
`
	}
	header += "---\n"

	body := fmt.Sprintf(`# %s

You are running the %s skill. Replace this body with the actual instructions
the model should follow when this skill activates.

Playbook:
1. Restate what the user asked for in your own words.
2. Read the smallest slice of code needed to answer.
3. Identify the one or two checks specific to this skill's domain.
4. Apply them — cite file:line for every claim.
5. Stop early when the answer is already clear.
6. Surface what you did NOT check, so the user knows the gap.

Notes for the author of this skill:
- Replace the playbook above with steps tailored to YOUR skill's domain.
- The 'triggers' field controls auto-activation — tighten the regex once
  you know which queries should fire it (test with: dfmc skill validate <path>).
- 'allowed-tools' is currently a textual hint shown to the model. Treat it
  as an honor-system list, not a hard restriction.
- Run 'dfmc skill validate <this file>' before installing to catch
  format mistakes early.
`, strings.ToTitle(name), strings.ToUpper(name))

	return header + body
}
