package tools

import "testing"

func TestIsDestructive_KnownDestructiveTools(t *testing.T) {
	for _, name := range []string{
		"write_file",
		"edit_file",
		"apply_patch",
		"run_command",
		"delegate_task",
	} {
		if !IsDestructive(name) {
			t.Errorf("IsDestructive(%q) = false, want true", name)
		}
	}
}

func TestIsDestructive_ReadOnlyTools(t *testing.T) {
	for _, name := range []string{
		"read_file",
		"grep_codebase",
		"glob",
		"find_symbol",
		"ast_query",
		"codemap",
	} {
		if IsDestructive(name) {
			t.Errorf("IsDestructive(%q) = true, want false", name)
		}
	}
}

func TestIsDestructive_CaseInsensitive(t *testing.T) {
	if !IsDestructive("WRITE_FILE") {
		t.Error("IsDestructive should be case-insensitive")
	}
	if !IsDestructive("Run_Command") {
		t.Error("IsDestructive should be case-insensitive")
	}
}

func TestIsDestructive_TrimsWhitespace(t *testing.T) {
	if !IsDestructive("  write_file  ") {
		t.Error("IsDestructive should trim whitespace")
	}
}

func TestIsDestructive_EmptyAndUnknown(t *testing.T) {
	if IsDestructive("") {
		t.Error("empty string should not be destructive")
	}
	if IsDestructive("nonexistent_tool") {
		t.Error("unknown tool should not be destructive")
	}
}
