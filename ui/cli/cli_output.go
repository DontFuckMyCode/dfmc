// CLI help rendering and JSON output helpers. Extracted from cli.go so
// the dispatcher stays focused. These functions drive the textual
// surface of the CLI (top-level help, per-command help, JSON encoder)
// and share the commands.Registry backing the help catalog.

package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dontfuckmycode/dfmc/internal/commands"
)

func printHelp() {
	fmt.Println(renderCLIHelp(""))
}

// printCommandHelp renders a single-command detail view, or falls back to the
// full catalog when the name is unknown.
func printCommandHelp(name string) {
	reg := commands.DefaultRegistry()
	if detail := reg.RenderCommandHelp(name); detail != "" {
		fmt.Println(detail)
		return
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", name)
	fmt.Println(renderCLIHelp(""))
}

func renderCLIHelp(extraHeader string) string {
	reg := commands.DefaultRegistry()
	header := "Usage: dfmc [global flags] <command> [args]"
	if extraHeader != "" {
		header = extraHeader + "\n\n" + header
	}
	body := reg.RenderHelp(commands.SurfaceCLI, header)
	globalFlags := `

Global flags:
  --provider  LLM provider override
  --model     Model override
  --profile   Config profile
  --verbose   Verbose output
  --json      JSON output mode
  --no-color  Disable colors
  --project   Project root path

Run "dfmc help <command>" for details on a specific command.`
	return body + globalFlags
}

// printJSON encodes v as indented JSON to stdout. Intended for non-interactive
// use (scripts, CI pipes). When encoding fails (e.g. stdout is a closed pipe),
// prints a brief error to stderr and returns false so the caller can exit non-zero.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// mustPrintJSON calls printJSON and reports whether encoding succeeded.
// On failure it prints a one-line error to stderr so broken pipes surface
// as non-zero exit codes rather than silent empty output.
func mustPrintJSON(v any) bool {
	if err := printJSON(v); err != nil {
		fmt.Fprintf(os.Stderr, "json output error: %v\n", err)
		return false
	}
	return true
}
