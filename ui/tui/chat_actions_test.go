package tui

import "testing"

func TestIsMutationTool_IncludesApplyPatch(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "apply_patch"} {
		if !isMutationTool(name) {
			t.Fatalf("%s must be treated as a mutation tool", name)
		}
	}
	if isMutationTool("read_file") {
		t.Fatal("read_file must not be treated as a mutation tool")
	}
}
