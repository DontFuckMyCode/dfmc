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

func TestIsToolAllowedBySkills_GateOff(t *testing.T) {
	if !IsToolAllowedBySkills("anything", nil, false) {
		t.Error("when enforced=false, every tool must be allowed")
	}
}

func TestIsToolAllowedBySkills_DenyOutsideList(t *testing.T) {
	if IsToolAllowedBySkills("run_command", []string{"read_file"}, true) {
		t.Error("run_command must be denied when only read_file is allowed")
	}
}

func TestIsToolAllowedBySkills_AllowInList(t *testing.T) {
	if !IsToolAllowedBySkills("READ_FILE", []string{"read_file"}, true) {
		t.Error("case-insensitive match must allow READ_FILE")
	}
}

func TestIsToolAllowedBySkills_MetaToolsAlwaysPermitted(t *testing.T) {
	// The meta dispatcher re-enters this gate with the inner backend
	// tool name. Blocking meta wrappers here would short-circuit
	// legitimate dispatches.
	for _, meta := range []string{"tool_call", "tool_batch_call", "tool_search", "tool_help"} {
		if !IsToolAllowedBySkills(meta, []string{"read_file"}, true) {
			t.Errorf("meta tool %q must always pass at the outer gate", meta)
		}
	}
}
