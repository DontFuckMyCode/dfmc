package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: count diagnostics at a given severity.
func countSeverity(diags []Diagnostic, sev Severity) int {
	n := 0
	for _, d := range diags {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

// hasFieldDiag returns true when any diagnostic targets `field`.
func hasFieldDiag(diags []Diagnostic, field string) bool {
	for _, d := range diags {
		if d.Field == field {
			return true
		}
	}
	return false
}

func TestValidateSkillBytes_OK(t *testing.T) {
	data := []byte(`---
name: ok-skill
description: Valid skill
allowed-tools: read_file
triggers:
  - "foo|bar"
---
# OK Skill

Body here.`)
	diags := ValidateSkillBytes(data, "ok.md")
	if countSeverity(diags, SeverityError) != 0 {
		t.Fatalf("expected no errors, got %+v", diags)
	}
}

func TestValidateSkillBytes_MissingName(t *testing.T) {
	data := []byte(`---
description: nameless
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if !hasFieldDiag(diags, "name") {
		t.Fatalf("expected 'name' error, got %+v", diags)
	}
	if countSeverity(diags, SeverityError) == 0 {
		t.Fatal("missing name should be an error")
	}
}

func TestValidateSkillBytes_MissingClosingFrontmatter(t *testing.T) {
	data := []byte(`---
name: x
description: y
no closing separator`)
	diags := ValidateSkillBytes(data, "x.md")
	if countSeverity(diags, SeverityError) == 0 {
		t.Fatalf("expected error for missing closing ---, got %+v", diags)
	}
}

func TestValidateSkillBytes_EmptyBody(t *testing.T) {
	data := []byte(`---
name: x
description: y
---

`)
	diags := ValidateSkillBytes(data, "x.md")
	if !hasFieldDiag(diags, "system_prompt") {
		t.Fatalf("expected system_prompt error for empty body, got %+v", diags)
	}
}

func TestValidateSkillBytes_InvalidTriggerRegex(t *testing.T) {
	data := []byte(`---
name: x
description: y
triggers:
  - "[unterminated"
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	found := false
	for _, d := range diags {
		if strings.HasPrefix(d.Field, "triggers[") && d.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error for invalid trigger regex, got %+v", diags)
	}
}

func TestValidateSkillBytes_TriggerObjectMissingPattern(t *testing.T) {
	data := []byte(`---
name: x
description: y
triggers:
  - weight: 0.9
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if countSeverity(diags, SeverityError) == 0 {
		t.Fatalf("expected error for trigger missing pattern, got %+v", diags)
	}
}

func TestValidateSkillBytes_RequiresMissingSkill(t *testing.T) {
	data := []byte(`---
name: x
description: y
requires:
  - reason: orphan
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if countSeverity(diags, SeverityError) == 0 {
		t.Fatalf("expected error for requires without skill name, got %+v", diags)
	}
}

func TestValidateSkillBytes_UnknownFieldWarning(t *testing.T) {
	data := []byte(`---
name: x
description: y
sometyypo: 42
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if countSeverity(diags, SeverityWarning) == 0 {
		t.Fatalf("expected warning for unknown field, got %+v", diags)
	}
}

func TestValidateSkillBytes_MissingDescriptionWarning(t *testing.T) {
	data := []byte(`---
name: x
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if !hasFieldDiag(diags, "description") {
		t.Fatalf("expected description warning, got %+v", diags)
	}
	// description missing is a warning, not an error
	if countSeverity(diags, SeverityError) > 0 {
		// only error allowed here would be unrelated; description alone shouldn't error
		for _, d := range diags {
			if d.Field == "description" && d.Severity == SeverityError {
				t.Fatalf("description missing should be warning, got error: %+v", d)
			}
		}
	}
}

func TestValidateSkillBytes_NativeYAMLNoBody(t *testing.T) {
	data := []byte(`name: x
description: y
`)
	diags := ValidateSkillBytes(data, "x.yaml")
	if !hasFieldDiag(diags, "system_prompt") {
		t.Fatalf("expected system_prompt warning for native YAML without body, got %+v", diags)
	}
}

func TestValidateSkillFile_FileNotFound(t *testing.T) {
	_, err := ValidateSkillFile("/path/that/does/not/exist.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateSkillFile_RealFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "good.md")
	body := `---
name: good
description: A valid skill
---
# Good
content`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	diags, err := ValidateSkillFile(path)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if countSeverity(diags, SeverityError) != 0 {
		t.Fatalf("expected no errors, got %+v", diags)
	}
}

func TestValidateSkillBytes_BadName(t *testing.T) {
	data := []byte(`---
name: "has spaces and !"
description: y
---
# Body
content`)
	diags := ValidateSkillBytes(data, "x.md")
	if !hasFieldDiag(diags, "name") {
		t.Fatalf("expected name error for invalid chars, got %+v", diags)
	}
}
