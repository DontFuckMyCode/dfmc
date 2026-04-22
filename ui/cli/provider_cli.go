// Top-level provider/model shortcuts so users don't have to remember the
// `config set providers.primary.name ...` dotted path. These are the
// session-only counterparts of the chat-repl `/provider` and `/model`
// commands — they bind the engine for the rest of this process but do
// not persist. Use `dfmc config set ...` for persistence across runs.

package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// runProviderCLI is the top-level `dfmc provider [name]` handler.
//
//	dfmc provider            → print current provider/model
//	dfmc provider anthropic  → set provider for this process
//
// Persistence is intentionally opt-in via `dfmc config set`. A transient
// provider set here only lasts for the current invocation, which is the
// behaviour the chat-repl `/provider` has always had.
func runProviderCLI(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		st := eng.Status()
		if jsonMode {
			_ = printJSON(map[string]any{
				"provider":   st.Provider,
				"model":      st.Model,
				"configured": st.ProviderProfile.Configured,
			})
			return 0
		}
		fmt.Printf("provider: %s\n", blankFallback(st.Provider, "-"))
		fmt.Printf("model:    %s\n", blankFallback(st.Model, "-"))
		if !st.ProviderProfile.Configured && !strings.EqualFold(st.Provider, "offline") {
			fmt.Fprintln(os.Stderr, "warning: provider profile is not fully configured (missing api_key or base_url?)")
		}
		return 0
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		fmt.Fprintln(os.Stderr, "provider name must not be empty")
		return 2
	}
	available := listProviderNames(eng)
	if len(available) > 0 && !containsFold(available, name) {
		fmt.Fprintf(os.Stderr, "unknown provider %q. Known: %s\n", name, strings.Join(available, ", "))
		fmt.Fprintln(os.Stderr, "Persist with: dfmc config set providers.primary.name <name>")
		return 1
	}
	eng.SetProviderModel(name, "")
	st := eng.Status()
	if jsonMode {
		_ = printJSON(map[string]any{
			"provider": st.Provider,
			"model":    st.Model,
			"scope":    "session",
		})
		return 0
	}
	fmt.Printf("provider set to %s (model=%s) — session only\n", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"))
	fmt.Println("Persist with: dfmc config set providers.primary.name " + st.Provider)
	return 0
}

// runModelCLI is the top-level `dfmc model [name]` handler — same shape
// as runProviderCLI but for the model axis.
func runModelCLI(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		st := eng.Status()
		if jsonMode {
			_ = printJSON(map[string]any{
				"provider": st.Provider,
				"model":    st.Model,
			})
			return 0
		}
		fmt.Printf("provider: %s\n", blankFallback(st.Provider, "-"))
		fmt.Printf("model:    %s\n", blankFallback(st.Model, "-"))
		return 0
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		fmt.Fprintln(os.Stderr, "model name must not be empty")
		return 2
	}
	st := eng.Status()
	eng.SetProviderModel(st.Provider, name)
	st = eng.Status()
	if jsonMode {
		_ = printJSON(map[string]any{
			"provider": st.Provider,
			"model":    st.Model,
			"scope":    "session",
		})
		return 0
	}
	fmt.Printf("model set to %s (provider=%s) — session only\n", blankFallback(st.Model, "-"), blankFallback(st.Provider, "-"))
	fmt.Println("Persist with: dfmc config set providers.primary.model " + st.Model)
	return 0
}

// runProvidersList is the top-level `dfmc providers` handler. Lists
// every loaded provider so users can see what `dfmc provider <name>`
// will accept.
func runProvidersList(eng *engine.Engine, jsonMode bool) int {
	names := listProviderNames(eng)
	if jsonMode {
		mustPrintJSON(map[string]any{"providers": names})
		return 0
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "No providers loaded. Check .dfmc/config.yaml providers.profiles.*")
		return 1
	}
	st := eng.Status()
	current := strings.TrimSpace(st.Provider)
	for _, name := range names {
		marker := "  "
		if strings.EqualFold(name, current) {
			marker = "* "
		}
		fmt.Printf("%s%s\n", marker, name)
	}
	return 0
}

func listProviderNames(eng *engine.Engine) []string {
	if eng == nil || eng.Providers == nil {
		return nil
	}
	names := eng.Providers.List()
	sort.Strings(names)
	return names
}

func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}
