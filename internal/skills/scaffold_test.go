package skills

import (
	"strings"
	"testing"
)

func TestRenderSkillTemplate_SimpleHasNoTriggers(t *testing.T) {
	got := RenderSkillTemplate("my-skill", SkillTemplateSimple)
	if !strings.Contains(got, "name: my-skill") {
		t.Errorf("expected name in template, got: %s", got)
	}
	if strings.Contains(got, "triggers:") {
		t.Errorf("simple template should not contain triggers block")
	}
	if !strings.Contains(got, "version: 0.1.0") {
		t.Errorf("expected version in template")
	}
}

func TestRenderSkillTemplate_TriggeredHasTriggers(t *testing.T) {
	got := RenderSkillTemplate("audit-internal", SkillTemplateTriggered)
	if !strings.Contains(got, "triggers:") {
		t.Errorf("triggered template missing triggers block: %s", got)
	}
	if !strings.Contains(got, "weight: 0.85") {
		t.Errorf("triggered template missing weight: %s", got)
	}
}

func TestRenderSkillTemplate_FallbackName(t *testing.T) {
	got := RenderSkillTemplate("", SkillTemplateSimple)
	if !strings.Contains(got, "name: my-skill") {
		t.Errorf("expected fallback name, got: %s", got)
	}
}

// Output should validate cleanly via ValidateSkillBytes (the loop
// has to close: we promise users that the scaffolded file is a
// well-formed skill, not just plausible-looking text).
func TestRenderSkillTemplate_PassesValidation(t *testing.T) {
	for _, kind := range []SkillTemplate{SkillTemplateSimple, SkillTemplateTriggered} {
		t.Run(string(kind), func(t *testing.T) {
			body := RenderSkillTemplate("test-skill", kind)
			diags := ValidateSkillBytes([]byte(body), "scaffold.md")
			for _, d := range diags {
				if d.Severity == SeverityError {
					t.Errorf("scaffold output has validation error: %+v\n--- body ---\n%s", d, body)
				}
			}
		})
	}
}
