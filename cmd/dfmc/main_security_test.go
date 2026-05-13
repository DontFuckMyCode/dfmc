package main

import "testing"

func TestUnsafeHooksOverrideEnabled(t *testing.T) {
	t.Setenv("DFMC_UNSAFE_HOOKS", "")
	if unsafeHooksOverrideEnabled() {
		t.Fatal("empty override must be disabled")
	}
	t.Setenv("DFMC_UNSAFE_HOOKS", "1")
	if !unsafeHooksOverrideEnabled() {
		t.Fatal("DFMC_UNSAFE_HOOKS=1 must enable override")
	}
	t.Setenv("DFMC_UNSAFE_HOOKS", "true")
	if !unsafeHooksOverrideEnabled() {
		t.Fatal("DFMC_UNSAFE_HOOKS=true must enable override")
	}
}
