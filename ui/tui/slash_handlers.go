package tui

// slash_handlers.go — concrete implementations for the expanded slash-command
// surface (F1c / F1d / F1e). Each helper is self-contained: it takes the
// command's raw args, does the work (either composing a prompt to feed the
// chat pipeline or calling an engine method directly), and returns either a
// formatted string to append to the transcript or a (Model, tea.Cmd, bool)
// triple matching the switch's signature.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/commands"
)


// codemapSummary renders a one-paragraph snapshot of the codemap graph.
func (m Model) codemapSummary() string {
	if m.eng == nil || m.eng.CodeMap == nil || m.eng.CodeMap.Graph() == nil {
		return "Codemap not built yet. Run /analyze or restart with -v."
	}
	g := m.eng.CodeMap.Graph()
	nodes := g.Nodes()
	edges := g.Edges()
	return fmt.Sprintf("Codemap: %d nodes, %d edges. Use `dfmc map --format svg --out map.svg` for a visual.",
		len(nodes), len(edges))
}

// versionSummary composes a short runtime readout for /version.
func (m Model) versionSummary() string {
	st := m.eng.Status()
	return fmt.Sprintf("DFMC (Go %s, %s/%s)\nProvider: %s / %s\nAST backend: %s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH,
		blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"),
		blankFallback(st.ASTBackend, "unknown"))
}

// magicDocSlash handles /magicdoc show (read file) and /magicdoc update
// (delegates to CLI for now — implementation lives in ui/cli).
func (m Model) magicDocSlash(args []string) string {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	root := ""
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.Status().ProjectRoot)
	}
	if root == "" {
		return "Project root unknown — run /reload after opening a project."
	}
	path := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	switch sub {
	case "", "show", "cat":
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "MAGIC_DOC.md not found. Generate it with: dfmc magicdoc update (or dfmc magicdoc)"
			}
			return "magicdoc read failed: " + err.Error()
		}
		return "MAGIC_DOC (" + filepath.ToSlash(path) + "):\n" + truncateCommandBlock(string(data), 4000)
	case "update", "sync", "generate":
		return "Run from CLI for now: `dfmc magicdoc update`. TUI in-place update is planned."
	default:
		return "magicdoc: unknown subcommand. Try: show | update"
	}
}


// suggestSlashCommand picks the closest canonical slash command for an
// unknown token. Prefix match first, then containment — returns "/name" form
// or empty string when nothing is reasonably close.
func suggestSlashCommand(token string) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return ""
	}
	reg := commands.DefaultRegistry()
	// Canonical names + aliases from the registry.
	candidates := make([]string, 0, 32)
	for _, cmd := range reg.ForSurface(commands.SurfaceTUI) {
		candidates = append(candidates, cmd.Name)
		candidates = append(candidates, cmd.Aliases...)
	}
	// TUI-only slash utilities.
	candidates = append(candidates,
		"help", "status", "reload", "context", "tools", "tool", "ls", "read",
		"grep", "run", "diff", "patch", "undo", "apply", "providers", "provider",
		"models", "model",
	)
	// Dedup + lowercase.
	seen := map[string]struct{}{}
	norm := candidates[:0]
	for _, c := range candidates {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		norm = append(norm, c)
	}
	// Prefix match wins.
	for _, c := range norm {
		if strings.HasPrefix(c, token) {
			return "/" + c
		}
	}
	for _, c := range norm {
		if strings.Contains(c, token) {
			return "/" + c
		}
	}
	return ""
}


