// Administrative CLI subcommands: version, status, init, completion,
// man pages, and doctor. Extracted from cli.go so the dispatcher stays
// focused. The config subcommands moved out to cli_config.go, but these
// commands still share the formatting helpers for engine status,
// provider profiles, and AST/codemap metrics.
//
// Companion siblings (extracted to keep the entry-point file lean):
//
//   - cli_admin_status.go  runStatus aggregator + operator-visible
//                          status renderer
//   - cli_admin_summary.go approval-gate + hooks-dispatcher
//                          summarisers (collect + render pairs)
//   - cli_admin_format.go  provider profile / models.dev cache /
//                          AST language / AST metrics / codemap
//                          metrics one-line formatters
//
// Doctor lives in cli_doctor.go (and its own siblings); the config
// subcommands moved out to cli_config.go.

package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runVersion(eng *engine.Engine, version string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := fs.Bool("json", false, "output as json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || *jsonFlag

	st := eng.Status()
	loadedProviders := []string{}
	if eng.Providers != nil {
		loadedProviders = eng.Providers.List()
		sort.Strings(loadedProviders)
	}
	payload := map[string]any{
		"name":             "dfmc",
		"version":          version,
		"provider":         st.Provider,
		"model":            st.Model,
		"project_root":     st.ProjectRoot,
		"state":            st.State,
		"go_version":       runtimeVersion(),
		"loaded_providers": loadedProviders,
		"binary_size":      executableSize(),
	}
	if jsonMode {
		mustPrintJSON(payload)
		return 0
	}
	fmt.Printf("dfmc %s\n", version)
	fmt.Printf("provider: %s\n", st.Provider)
	fmt.Printf("model: %s\n", st.Model)
	fmt.Printf("project: %s\n", st.ProjectRoot)
	fmt.Printf("providers: %s\n", strings.Join(loadedProviders, ", "))
	if sz := executableSize(); sz > 0 {
		fmt.Printf("binary size: %d bytes\n", sz)
	}
	return 0
}

// runStatus lives in cli_admin_status.go.

func runInit(jsonMode bool, projectOverride string) int {
	root := projectOverride
	if strings.TrimSpace(root) == "" {
		root = config.FindProjectRoot("")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot resolve cwd: %v\n", err)
			return 1
		}
		root = cwd
	}

	dfmcDir := filepath.Join(root, ".dfmc")
	cfgPath := filepath.Join(dfmcDir, "config.yaml")

	if err := os.MkdirAll(dfmcDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		return 1
	}

	cfg := config.DefaultConfig()
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write default config: %v\n", err)
		return 1
	}

	// Prepare local knowledge placeholders. Mode 0o600 because the
	// files live next to the project config (which holds API keys
	// after /key set) — world-readable would be a regression even
	// though the placeholders themselves are empty today. Surface
	// any write failure rather than silently claiming "Initialized
	// DFMC project at ..." with broken scaffolding.
	for _, name := range []string{"knowledge.json", "conventions.json"} {
		fpath := filepath.Join(dfmcDir, name)
		if err := os.WriteFile(fpath, []byte("{}\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write %s: %v\n", name, err)
			return 1
		}
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status":       "ok",
			"project_root": root,
			"config_path":  cfgPath,
			"next_steps":   initNextSteps(),
		})
		return 0
	}

	fmt.Printf("Initialized DFMC project at %s\n", root)
	fmt.Printf("Created %s\n", cfgPath)
	fmt.Println("        " + filepath.Join(dfmcDir, "knowledge.json") + "  (project facts, populated by `dfmc analyze`)")
	fmt.Println("        " + filepath.Join(dfmcDir, "conventions.json") + "  (style/conventions, populated by `dfmc analyze`)")
	fmt.Println()
	fmt.Println("Next steps:")
	for _, step := range initNextSteps() {
		fmt.Println("  · " + step)
	}
	fmt.Println()
	fmt.Println("Tip: `dfmc doctor` reports any missing API keys or config issues; `dfmc agents` lists the sub-agent roles + provider profiles available to you.")
	return 0
}

// initNextSteps is the short, ordered checklist printed after `dfmc init`
// (and surfaced in --json mode for scripted onboarding flows). Lives as a
// helper so CLI text output and the JSON payload stay aligned.
func initNextSteps() []string {
	return []string{
		"Set a provider API key (e.g. ANTHROPIC_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY) in your shell or a project-root .env — DFMC auto-loads .env at startup.",
		"Run `dfmc config sync-models` to refresh provider profiles from models.dev (preserves any API keys you already set).",
		"Try `dfmc ask \"summarise this project\"` for a one-shot answer, or `dfmc tui` for the interactive workbench.",
		"For autonomous multi-step work, `dfmc drive \"<task>\"` plans + executes a DAG of TODOs end-to-end.",
	}
}
