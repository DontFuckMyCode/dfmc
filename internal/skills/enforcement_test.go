package skills

import (
	"reflect"
	"testing"
)

func TestEffectiveAllowedTools_NoActive(t *testing.T) {
	union, enforced := EffectiveAllowedTools(nil)
	if enforced {
		t.Errorf("expected gate OFF for empty active set")
	}
	if union != nil {
		t.Errorf("expected nil union, got %v", union)
	}
}

func TestEffectiveAllowedTools_SingleSkillRestricts(t *testing.T) {
	active := []Skill{{Name: "audit", Allowed: []string{"read_file", "grep_codebase"}}}
	union, enforced := EffectiveAllowedTools(active)
	if !enforced {
		t.Fatal("expected gate ON when the single active skill declares allowed_tools")
	}
	want := []string{"grep_codebase", "read_file"} // sorted lowercase
	if !reflect.DeepEqual(union, want) {
		t.Errorf("want %v, got %v", want, union)
	}
}

func TestEffectiveAllowedTools_UnionAcrossSkills(t *testing.T) {
	active := []Skill{
		{Name: "a", Allowed: []string{"Read", "Grep"}},
		{Name: "b", Allowed: []string{"Edit", "Grep"}}, // Grep duplicate must dedupe
	}
	union, enforced := EffectiveAllowedTools(active)
	if !enforced {
		t.Fatal("expected gate ON")
	}
	want := []string{"edit", "grep", "read"}
	if !reflect.DeepEqual(union, want) {
		t.Errorf("want %v, got %v", want, union)
	}
}

func TestEffectiveAllowedTools_AnyEmptyOptsOut(t *testing.T) {
	// One skill with declared list, one without — gate should be OFF
	// per rule 2 (any unrestricted active skill defeats the gate).
	active := []Skill{
		{Name: "audit", Allowed: []string{"read_file"}},
		{Name: "review", Allowed: nil}, // no Allowed declared
	}
	union, enforced := EffectiveAllowedTools(active)
	if enforced {
		t.Error("expected gate OFF when any active skill omits allowed_tools")
	}
	if union != nil {
		t.Errorf("expected nil union, got %v", union)
	}
}

func TestEffectiveAllowedTools_AllWhitespace(t *testing.T) {
	// Skill declares allowed_tools but every entry is whitespace —
	// treat as no restriction (otherwise we'd deny every dispatch).
	active := []Skill{{Name: "x", Allowed: []string{"  ", ""}}}
	_, enforced := EffectiveAllowedTools(active)
	if enforced {
		t.Error("expected gate OFF when all entries are blank")
	}
}

