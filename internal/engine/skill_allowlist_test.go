package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/skills"
)

func TestSkillAllowlist_NoActiveSet_AllowsAll(t *testing.T) {
	ctx := context.Background()
	if reason := checkSkillAllowlist(ctx, "run_command", nil); reason != "" {
		t.Errorf("expected no denial when no allowlist attached, got %q", reason)
	}
}

func TestSkillAllowlist_DeniesUnlisted(t *testing.T) {
	ctx := withSkillAllowlist(context.Background(), []string{"read_file"}, true)
	reason := checkSkillAllowlist(ctx, "run_command", nil)
	if reason == "" {
		t.Fatal("expected denial for run_command outside allowlist")
	}
	if !strings.Contains(reason, "skill allowed_tools") {
		t.Errorf("denial reason should mention skill allowed_tools; got %q", reason)
	}
}

func TestSkillAllowlist_AllowsListed(t *testing.T) {
	ctx := withSkillAllowlist(context.Background(), []string{"read_file", "grep_codebase"}, true)
	if reason := checkSkillAllowlist(ctx, "READ_FILE", nil); reason != "" {
		t.Errorf("case-insensitive match must allow READ_FILE; got %q", reason)
	}
}

func TestSkillAllowlist_MetaToolsAlwaysPermitted(t *testing.T) {
	ctx := withSkillAllowlist(context.Background(), []string{"read_file"}, true)
	for _, meta := range []string{"tool_search", "tool_help"} {
		if reason := checkSkillAllowlist(ctx, meta, nil); reason != "" {
			t.Errorf("meta tool %q must always pass; got %q", meta, reason)
		}
	}
}

func TestSkillAllowlist_MetaCallChecksInnerName(t *testing.T) {
	// tool_call wrapping a forbidden inner tool must be refused at
	// the outer site — same semantic as checkSubagentAllowlist.
	ctx := withSkillAllowlist(context.Background(), []string{"read_file"}, true)
	reason := checkSkillAllowlist(ctx, "tool_call", []string{"run_command"})
	if reason == "" {
		t.Fatal("expected denial when tool_call wraps a forbidden inner")
	}
}

func TestSkillAllowlist_DisabledIsNoOp(t *testing.T) {
	// enforced=false should not attach any context value.
	ctx := withSkillAllowlist(context.Background(), []string{"read_file"}, false)
	if reason := checkSkillAllowlist(ctx, "run_command", nil); reason != "" {
		t.Errorf("gate should be off when enforced=false; got denial %q", reason)
	}
}

func TestWithActiveSkillsAllowlist_RestrictedSet(t *testing.T) {
	active := []skills.Skill{{Name: "x", Allowed: []string{"read_file"}}}
	ctx := WithActiveSkillsAllowlist(context.Background(), active)
	if reason := checkSkillAllowlist(ctx, "run_command", nil); reason == "" {
		t.Error("expected denial under restricted skill set")
	}
}

func TestWithActiveSkillsAllowlist_AnyEmptyDisablesGate(t *testing.T) {
	// Mixed: one declared, one omitted → gate stays off (any
	// unrestricted skill defeats enforcement).
	active := []skills.Skill{
		{Name: "a", Allowed: []string{"read_file"}},
		{Name: "b", Allowed: nil},
	}
	ctx := WithActiveSkillsAllowlist(context.Background(), active)
	if reason := checkSkillAllowlist(ctx, "run_command", nil); reason != "" {
		t.Errorf("gate should be off when any active skill omits allowed_tools; got %q", reason)
	}
}
