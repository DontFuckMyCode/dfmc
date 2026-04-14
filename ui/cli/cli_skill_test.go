package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSkillsIncludesProjectCustom(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	projectRoot := t.TempDir()
	skillDir := filepath.Join(projectRoot, ".dfmc", "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "" +
		"name: custom-review\n" +
		"description: custom project skill\n" +
		"prompt: |\n" +
		"  Review this carefully:\n" +
		"  {input}\n"
	if err := os.WriteFile(filepath.Join(skillDir, "custom-review.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := discoverSkills(projectRoot)
	var found skillInfo
	ok := false
	for _, item := range items {
		if strings.EqualFold(item.Name, "custom-review") {
			found = item
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("custom skill not discovered: %#v", items)
	}
	if found.Source != "project" {
		t.Fatalf("unexpected source: %s", found.Source)
	}
	if found.Builtin {
		t.Fatalf("custom skill should not be builtin")
	}

	prompt := buildSkillPrompt(found, "check auth module")
	if !strings.Contains(prompt, "check auth module") {
		t.Fatalf("input placeholder not applied: %s", prompt)
	}
}
