// cli_agents.go — `dfmc agents` subcommand. Mirrors the TUI /agents
// surface and the /api/v1/agents web endpoint, all reading from the
// engine's Agents() catalog so the four layers stay in sync.

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/tui"
)

// runAgents handles `dfmc agents [list|show NAME]`. JSON mode prints the
// raw catalog so scripts can pipe it through jq; text mode reuses the TUI
// formatter so the layout matches /agents inside chat.
func runAgents(_ context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	cat := eng.Agents()

	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	switch sub {
	case "", "list", "ls":
		if jsonMode {
			mustPrintJSON(cat)
			return 0
		}
		fmt.Println(tui.FormatAgentsList(cat.Roles, cat.Profiles))
		return 0
	case "show", "describe", "cat":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: dfmc agents show <role-or-profile>")
			return 2
		}
		target := strings.TrimSpace(rest[0])
		for _, r := range cat.Roles {
			if strings.EqualFold(r.Role, target) {
				if jsonMode {
					mustPrintJSON(map[string]any{"role": r})
					return 0
				}
				fmt.Println(tui.FormatAgentRoleShow(r))
				return 0
			}
		}
		for _, p := range cat.Profiles {
			if strings.EqualFold(p.Name, target) {
				if jsonMode {
					mustPrintJSON(map[string]any{"profile": p})
					return 0
				}
				fmt.Println(tui.FormatAgentProfileShow(p))
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "no role or profile named %q (try `dfmc agents list`)\n", target)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "agents: unknown subcommand %q. Try: list | show <name>\n", sub)
		return 2
	}
}
