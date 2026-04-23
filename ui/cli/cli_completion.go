// Shell completion scripts and man page generation: `dfmc completion`
// (bash/zsh/fish/powershell) and `dfmc man` (man/markdown). Extracted
// from cli_admin.go. The command catalog (commandNames / commandDocs)
// is the shared source of truth and lives here with its consumers.

package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func runCompletion(args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	shell := fs.String("shell", "", "bash|zsh|fish|powershell")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*shell) == "" && len(fs.Args()) > 0 {
		*shell = fs.Args()[0]
	}
	sh := strings.ToLower(strings.TrimSpace(*shell))
	if sh == "" {
		fmt.Fprintln(os.Stderr, "usage: dfmc completion [--shell bash|zsh|fish|powershell]")
		return 2
	}

	commands := commandNames()
	if jsonMode {
		_ = printJSON(map[string]any{
			"shell":    sh,
			"commands": commands,
		})
		return 0
	}

	switch sh {
	case "bash":
		fmt.Print(completionBash(commands))
	case "zsh":
		fmt.Print(completionZsh(commands))
	case "fish":
		fmt.Print(completionFish(commands))
	case "powershell", "pwsh":
		fmt.Print(completionPowerShell(commands))
	default:
		fmt.Fprintf(os.Stderr, "unsupported shell: %s\n", sh)
		return 2
	}
	return 0
}

type commandDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func commandDocs() []commandDoc {
	return []commandDoc{
		{Name: "status", Description: "Runtime status snapshot"},
		{Name: "init", Description: "Initialize DFMC in project"},
		{Name: "chat", Description: "Interactive chat session"},
		{Name: "tui", Description: "Terminal workbench (chat/status/patch)"},
		{Name: "ask", Description: "One-shot question"},
		{Name: "analyze", Description: "Analyze codebase"},
		{Name: "scan", Description: "Quick security scan"},
		{Name: "map", Description: "Generate/display codemap"},
		{Name: "tool", Description: "Tool engine (list/run)"},
		{Name: "conversation", Description: "Conversation management (list/search/load/save/undo/branch)"},
		{Name: "memory", Description: "Memory management"},
		{Name: "config", Description: "Configuration management"},
		{Name: "context", Description: "Context budget and recent files"},
		{Name: "prompt", Description: "Prompt library management"},
		{Name: "magicdoc", Description: "Build/show concise project magic doc"},
		{Name: "plugin", Description: "Plugin management"},
		{Name: "skill", Description: "Skill management"},
		{Name: "serve", Description: "Start Web API server"},
		{Name: "remote", Description: "Remote control server"},
		{Name: "doctor", Description: "Environment and config health checks"},
		{Name: "completion", Description: "Generate shell completion scripts"},
		{Name: "man", Description: "Generate command manual page"},
		{Name: "review", Description: "Code review shortcut"},
		{Name: "explain", Description: "Explain code shortcut"},
		{Name: "refactor", Description: "Refactor code shortcut"},
		{Name: "test", Description: "Test generation shortcut"},
		{Name: "doc", Description: "Documentation shortcut"},
		{Name: "version", Description: "Version info"},
		{Name: "help", Description: "Show help"},
	}
}

func runMan(args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("man", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "man", "man|markdown")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	docs := commandDocs()
	if jsonMode {
		_ = printJSON(map[string]any{
			"format":   strings.ToLower(strings.TrimSpace(*format)),
			"commands": docs,
		})
		return 0
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "markdown", "md":
		fmt.Print(renderManMarkdown(docs))
	case "man", "roff":
		fmt.Print(renderManRoff(docs))
	default:
		fmt.Fprintf(os.Stderr, "unsupported man format: %s\n", *format)
		return 2
	}
	return 0
}

func renderManMarkdown(docs []commandDoc) string {
	var b strings.Builder
	b.WriteString("# dfmc(1)\n\n")
	b.WriteString("Don't Fuck My Code command line interface.\n\n")
	b.WriteString("## Usage\n\n")
	b.WriteString("`dfmc [global flags] <command> [args]`\n\n")
	b.WriteString("## Commands\n\n")
	for _, d := range docs {
		fmt.Fprintf(&b, "- `%s`: %s\n", d.Name, d.Description)
	}
	b.WriteString("\n## Global Flags\n\n")
	b.WriteString("- `--provider`: LLM provider override\n")
	b.WriteString("- `--model`: model override\n")
	b.WriteString("- `--profile`: config profile\n")
	b.WriteString("- `--verbose`: verbose output\n")
	b.WriteString("- `--json`: JSON output mode\n")
	b.WriteString("- `--no-color`: disable colors\n")
	b.WriteString("- `--project`: project root path\n")
	return b.String()
}

func renderManRoff(docs []commandDoc) string {
	var b strings.Builder
	b.WriteString(".TH DFMC 1 \"DFMC\" \"dfmc\"\n")
	b.WriteString(".SH NAME\n")
	b.WriteString("dfmc \\- Don't Fuck My Code CLI\n")
	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(".B dfmc\n")
	b.WriteString("[global flags] <command> [args]\n")
	b.WriteString(".SH COMMANDS\n")
	for _, d := range docs {
		fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", d.Name, d.Description)
	}
	b.WriteString(".SH GLOBAL FLAGS\n")
	b.WriteString(".TP\n.B --provider\nLLM provider override\n")
	b.WriteString(".TP\n.B --model\nModel override\n")
	b.WriteString(".TP\n.B --profile\nConfig profile\n")
	b.WriteString(".TP\n.B --verbose\nVerbose output\n")
	b.WriteString(".TP\n.B --json\nJSON output mode\n")
	b.WriteString(".TP\n.B --no-color\nDisable colors\n")
	b.WriteString(".TP\n.B --project\nProject root path\n")
	return b.String()
}

func commandNames() []string {
	return []string{
		"status",
		"init",
		"chat",
		"tui",
		"ask",
		"analyze",
		"scan",
		"map",
		"tool",
		"conversation",
		"memory",
		"config",
		"context",
		"prompt",
		"magicdoc",
		"plugin",
		"skill",
		"serve",
		"remote",
		"doctor",
		"completion",
		"man",
		"review",
		"explain",
		"refactor",
		"test",
		"doc",
		"version",
		"help",
	}
}

func completionBash(commands []string) string {
	cmds := strings.Join(commands, " ")
	return fmt.Sprintf(`# bash completion for dfmc
_dfmc_completion() {
  local cur
  cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=( $(compgen -W "%s" -- "$cur") )
  return 0
}
complete -F _dfmc_completion dfmc
`, cmds)
}

func completionZsh(commands []string) string {
	cmds := strings.Join(commands, " ")
	return fmt.Sprintf(`#compdef dfmc
_dfmc_completion() {
  local -a commands
  commands=(%s)
  _describe 'command' commands
}
compdef _dfmc_completion dfmc
`, cmds)
}

func completionFish(commands []string) string {
	var b strings.Builder
	b.WriteString("# fish completion for dfmc\n")
	b.WriteString("complete -c dfmc -f\n")
	for _, cmd := range commands {
		fmt.Fprintf(&b, "complete -c dfmc -n '__fish_use_subcommand' -a %s\n", cmd)
	}
	return b.String()
}

func completionPowerShell(commands []string) string {
	cmds := strings.Join(commands, "', '")
	return fmt.Sprintf(`# PowerShell completion for dfmc
Register-ArgumentCompleter -Native -CommandName dfmc -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $commands = @('%s')
  $commands | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`, cmds)
}
