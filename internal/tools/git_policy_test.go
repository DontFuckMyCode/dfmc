package tools

import "testing"

func TestBlockedGitArgRejectsDangerousFlags(t *testing.T) {
	for _, arg := range []string{"--force", "-f", "--hard", "--exec=sh", "--receive-pack=/tmp/x", "--upload-pack=/tmp/x", "--no-checkout"} {
		if !blockedGitArg(arg, nil) {
			t.Fatalf("expected %q to be blocked", arg)
		}
	}
}

func TestBlockedGitArgAllowsExplicitOverride(t *testing.T) {
	allowed := map[string]struct{}{"--force": {}}
	if blockedGitArg("--force", allowed) {
		t.Fatal("expected explicit allowlist to permit --force")
	}
}

func TestBlockedGitArgAllowsExplicitOverrideCaseInsensitive(t *testing.T) {
	allowed := map[string]struct{}{" --FORCE ": {}}
	if blockedGitArg("--force", normalizeBlockedGitAllowlist(allowed)) {
		t.Fatal("expected normalized explicit allowlist to permit --force regardless of casing")
	}
}
