package cli

import "testing"

func TestCompletionGenerators(t *testing.T) {
	cmds := commandNames()
	if len(cmds) == 0 {
		t.Fatal("expected non-empty command list")
	}

	bash := completionBash(cmds)
	if bash == "" || !containsAll(bash, []string{"complete -F", "dfmc", "analyze", "tui"}) {
		t.Fatalf("unexpected bash completion script: %s", bash)
	}

	zsh := completionZsh(cmds)
	if zsh == "" || !containsAll(zsh, []string{"compdef", "dfmc", "doctor", "tui"}) {
		t.Fatalf("unexpected zsh completion script: %s", zsh)
	}

	fish := completionFish(cmds)
	if fish == "" || !containsAll(fish, []string{"complete -c dfmc", "remote", "tui"}) {
		t.Fatalf("unexpected fish completion script: %s", fish)
	}

	pwsh := completionPowerShell(cmds)
	if pwsh == "" || !containsAll(pwsh, []string{"Register-ArgumentCompleter", "version", "tui"}) {
		t.Fatalf("unexpected powershell completion script: %s", pwsh)
	}
}

func containsAll(s string, needles []string) bool {
	for _, n := range needles {
		if !contains(s, n) {
			return false
		}
	}
	return true
}

func contains(s, needle string) bool {
	return len(needle) == 0 || (len(s) >= len(needle) && indexOf(s, needle) >= 0)
}

func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
