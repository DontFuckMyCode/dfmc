package tools

import (
	"testing"
)

func TestDetectShellMetacharacterVectors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple_command", "go build ./...", ""},
		{"and_chain", "go build && rm -rf /", "&&"},
		{"or_chain", "go build || echo pwned", "||"},
		{"pipe", "cat /etc/passwd | bash", "|"},
		{"redirect", "go run . > /tmp/out", ">"},
		{"backtick_subst", "echo `cat /flag`", "`"},
		{"dollar_subst", "echo $(cat /flag)", "$("},
		{"semicolon", "ls; rm -rf /", ";"},
		// Note: detectShellMetacharacter does not detect \n — it checks operators and metacharacters only.
		{"cd_and", "cd /repo && go build", "&&"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectShellMetacharacter(tc.input)
			if got != tc.want {
				t.Errorf("detectShellMetacharacter(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestBlockedShellInterpreters(t *testing.T) {
	blocked := []string{"bash", "sh", "zsh", "powershell", "cmd", "pwsh"}
	for _, bin := range blocked {
		if !isBlockedShellInterpreter(bin) {
			t.Errorf("isBlockedShellInterpreter(%q) = false, want true", bin)
		}
	}
	allowed := []string{"go", "npm", "pytest", "cargo"}
	for _, bin := range allowed {
		if isBlockedShellInterpreter(bin) {
			t.Errorf("isBlockedShellInterpreter(%q) = true, want false", bin)
		}
	}
}

func TestDetectShellSubstitutionArg(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"clean_args", []string{"build", "./..."}, ""},
		{"backtick_in_arg", []string{"-e", "system(`whoami`)"}, "`"},
		{"dollar_in_arg", []string{"-e", "os.system($(id))"}, "$("},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := detectShellSubstitutionArg(tc.args)
			if got != tc.want {
				t.Errorf("detectShellSubstitutionArg(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
