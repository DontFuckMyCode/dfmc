// cli_config.go — `dfmc config <subcommand>` dispatcher. Each
// subcommand body lives in its own sibling so this file stays a thin
// switch:
//
//   cli_config_show.go    — list, get
//   cli_config_set.go     — set
//   cli_config_sync.go    — sync-models
//   cli_config_edit.go    — edit
//   cli_config_helpers.go — config-file IO + dotted-path tree walkers
//
// formatConfigSubcommandError + suggestConfigSub stay here because
// they're only ever consumed by the dispatcher's default branch.

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConfig(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"list"}
	}

	switch args[0] {
	case "list":
		return runConfigList(eng, args[1:], jsonMode)
	case "get":
		return runConfigGet(eng, args[1:], jsonMode)
	case "set":
		return runConfigSet(eng, args[1:], jsonMode)
	case "sync-models":
		return runConfigSyncModels(ctx, eng, args[1:], jsonMode)
	case "edit":
		return runConfigEdit(eng, args[1:])
	default:
		fmt.Fprintln(os.Stderr, formatConfigSubcommandError(args[0]))
		return 2
	}
}

// formatConfigSubcommandError renders the help-on-typo block shown when
// `dfmc config <unknown>` is run. Lists each sub with a one-line summary
// and adds a "did you mean" suggestion when the typo is close to a real
// sub name. Without these the user only sees a bare `usage:` line that
// names the verbs but never says what they do.
func formatConfigSubcommandError(typo string) string {
	subs := []struct {
		name    string
		summary string
	}{
		{"list", "Print the merged config (sensitive values redacted; --raw to show)."},
		{"get", "Read one dotted path. `dfmc config get providers.profiles.anthropic.model`."},
		{"set", "Write one dotted path. `dfmc config set [--global] <path> <value>`."},
		{"sync-models", "Refresh providers.profiles.* from https://models.dev/api.json (preserves API keys)."},
		{"edit", "Open the active config file in $EDITOR (or notepad/vi). `--global` switches to ~/.dfmc/."},
	}
	var b strings.Builder
	if t := strings.TrimSpace(typo); t != "" {
		fmt.Fprintf(&b, "unknown subcommand %q.", t)
		if hint := suggestConfigSub(t, subs); hint != "" {
			fmt.Fprintf(&b, " Did you mean %q?", hint)
		}
		b.WriteByte('\n')
	}
	b.WriteString("usage: dfmc config <subcommand> [flags]\n")
	for _, s := range subs {
		fmt.Fprintf(&b, "  %-13s %s\n", s.name, s.summary)
	}
	b.WriteString("\nRun `dfmc help config` for the full reference.")
	return b.String()
}

func suggestConfigSub(typo string, subs []struct{ name, summary string }) string {
	typo = strings.ToLower(strings.TrimSpace(typo))
	for _, s := range subs {
		if strings.HasPrefix(s.name, typo) && s.name != typo {
			return s.name
		}
	}
	for _, s := range subs {
		if editDistanceAtMost(typo, s.name, 1) && s.name != typo {
			return s.name
		}
	}
	return ""
}
