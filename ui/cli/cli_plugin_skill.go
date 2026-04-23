// Plugin subcommand dispatcher: runPlugin routes `dfmc plugin ...`
// verbs to the right helpers and runPluginRPC speaks the binary
// plugin protocol. The install / discover / manifest / download
// surface lives in cli_plugin_install.go — everything that talks
// to the plugins directory on disk ended up there once this file
// grew too big. Skill subcommands are in the lighter cli_skill.go.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/pluginexec"
)

type pluginInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Version   string `json:"version,omitempty"`
	Type      string `json:"type,omitempty"`
	Entry     string `json:"entry,omitempty"`
	Manifest  string `json:"manifest,omitempty"`
}

type pluginManifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Type        string `yaml:"type"`
	Entry       string `yaml:"entry"`
	Description string `yaml:"description"`
}

func runPlugin(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items := discoverPlugins(eng.Config.PluginDir(), eng.Config.Plugins.Enabled)
		if jsonMode {
			_ = printJSON(map[string]any{
				"directory": eng.Config.PluginDir(),
				"plugins":   items,
			})
			return 0
		}
		if len(items) == 0 {
			fmt.Printf("No plugins found in %s\n", eng.Config.PluginDir())
			return 0
		}
		for _, p := range items {
			state := "disabled"
			if p.Enabled {
				state = "enabled"
			}
			installed := "missing"
			if p.Installed {
				installed = "installed"
			}
			meta := ""
			if p.Version != "" {
				meta = " v" + p.Version
			}
			if p.Type != "" {
				meta += " (" + p.Type + ")"
			}
			fmt.Printf("- %s%s [%s, %s]\n", p.Name, meta, state, installed)
		}
		return 0

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		items := discoverPlugins(eng.Config.PluginDir(), eng.Config.Plugins.Enabled)
		for _, p := range items {
			if strings.EqualFold(p.Name, name) {
				if jsonMode {
					mustPrintJSON(p)
				} else {
					fmt.Printf("Name:      %s\n", p.Name)
					fmt.Printf("Installed: %t\n", p.Installed)
					fmt.Printf("Enabled:   %t\n", p.Enabled)
					if strings.TrimSpace(p.Version) != "" {
						fmt.Printf("Version:   %s\n", p.Version)
					}
					if strings.TrimSpace(p.Type) != "" {
						fmt.Printf("Type:      %s\n", p.Type)
					}
					if strings.TrimSpace(p.Entry) != "" {
						fmt.Printf("Entry:     %s\n", p.Entry)
					}
					if strings.TrimSpace(p.Manifest) != "" {
						fmt.Printf("Manifest:  %s\n", p.Manifest)
					}
					if strings.TrimSpace(p.Path) != "" {
						fmt.Printf("Path:      %s\n", p.Path)
					}
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "plugin not found: %s\n", name)
		return 1

	case "enable", "disable":
		fs := flag.NewFlagSet("plugin "+args[0], flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintf(os.Stderr, "usage: dfmc plugin %s [--global] <name>\n", args[0])
			return 2
		}
		name := strings.TrimSpace(fs.Args()[0])
		enabled := args[0] == "enable"
		if err := updatePluginEnabled(ctx, eng, name, enabled, *global); err != nil {
			fmt.Fprintf(os.Stderr, "plugin %s failed: %v\n", args[0], err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"plugin":  name,
				"enabled": enabled,
			})
		} else {
			fmt.Printf("Plugin %s: %s\n", name, map[bool]string{true: "enabled", false: "disabled"}[enabled])
		}
		return 0

	case "install":
		fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		nameOverride := fs.String("name", "", "plugin name override")
		enable := fs.Bool("enable", true, "enable after install")
		global := fs.Bool("global", false, "write enable state to ~/.dfmc/config.yaml")
		force := fs.Bool("force", false, "overwrite existing plugin target")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin install [--name X] [--enable] [--global] [--force] <source_path_or_url>")
			return 2
		}
		sourcePath := strings.TrimSpace(fs.Args()[0])
		installed, err := installPluginFile(eng.Config.PluginDir(), sourcePath, strings.TrimSpace(*nameOverride), *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plugin install failed: %v\n", err)
			return 1
		}
		if *enable {
			if err := updatePluginEnabled(ctx, eng, installed.Name, true, *global); err != nil {
				fmt.Fprintf(os.Stderr, "plugin installed but enable failed: %v\n", err)
				return 1
			}
			installed.Enabled = true
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status": "ok",
				"plugin": installed,
			})
		} else {
			fmt.Printf("Installed plugin %s at %s\n", installed.Name, installed.Path)
			if installed.Enabled {
				fmt.Println("Plugin enabled")
			}
		}
		return 0

	case "remove":
		fs := flag.NewFlagSet("plugin remove", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		global := fs.Bool("global", false, "write disable state to ~/.dfmc/config.yaml")
		purge := fs.Bool("purge", true, "remove installed files")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if len(fs.Args()) < 1 {
			fmt.Fprintln(os.Stderr, "usage: dfmc plugin remove [--global] [--purge=true] <name>")
			return 2
		}
		name := strings.TrimSpace(fs.Args()[0])
		if err := updatePluginEnabled(ctx, eng, name, false, *global); err != nil {
			fmt.Fprintf(os.Stderr, "plugin disable failed: %v\n", err)
			return 1
		}
		removedPath := ""
		if *purge {
			path, err := removeInstalledPlugin(eng.Config.PluginDir(), name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "plugin remove failed: %v\n", err)
				return 1
			}
			removedPath = path
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"plugin":  name,
				"removed": removedPath,
			})
		} else {
			fmt.Printf("Plugin %s disabled\n", name)
			if removedPath != "" {
				fmt.Printf("Removed %s\n", removedPath)
			}
		}
		return 0

	case "run", "call":
		return runPluginRPC(ctx, eng, args[1:], jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc plugin [list|info <name>|enable <name>|disable <name>|install <path>|remove <name>|run <name> <method> [params-json]]")
		return 2
	}
}

func runPluginRPC(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("plugin run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeoutSec := fs.Int("timeout", 30, "per-call timeout in seconds")
	paramsFile := fs.String("params-file", "", "read params JSON from this file (use - for stdin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dfmc plugin run [--timeout SEC] [--params-file PATH] <name> <method> [params-json]")
		return 2
	}
	name := strings.TrimSpace(rest[0])
	method := strings.TrimSpace(rest[1])
	if name == "" || method == "" {
		fmt.Fprintln(os.Stderr, "plugin name and method are required")
		return 2
	}

	var rawParams json.RawMessage
	switch {
	case *paramsFile == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			return 1
		}
		rawParams = bytes.TrimSpace(data)
	case *paramsFile != "":
		data, err := os.ReadFile(*paramsFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read params file: %v\n", err)
			return 1
		}
		rawParams = bytes.TrimSpace(data)
	case len(rest) >= 3:
		rawParams = []byte(strings.Join(rest[2:], " "))
	}
	if len(rawParams) > 0 {
		if !json.Valid(rawParams) {
			fmt.Fprintln(os.Stderr, "params must be valid JSON")
			return 2
		}
	}

	info, ok := findInstalledPlugin(eng.Config.PluginDir(), name)
	if !ok {
		fmt.Fprintf(os.Stderr, "plugin not found: %s\n", name)
		return 1
	}
	entry, err := resolvePluginEntry(info)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin %s: %v\n", name, err)
		return 1
	}

	timeout := time.Duration(*timeoutSec) * time.Second
	spec := pluginexec.Spec{
		Name:  info.Name,
		Entry: entry,
		Type:  info.Type,
	}
	client, err := pluginexec.Spawn(ctx, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn plugin %s: %v\n", info.Name, err)
		return 1
	}
	defer func() {
		_ = client.Close(context.Background())
	}()

	var params any
	if len(rawParams) > 0 {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			fmt.Fprintf(os.Stderr, "decode params: %v\n", err)
			return 1
		}
	}

	raw, err := client.Call(ctx, method, params, timeout)
	if err != nil {
		if stderr := strings.TrimSpace(client.Stderr()); stderr != "" {
			fmt.Fprintln(os.Stderr, stderr)
		}
		fmt.Fprintf(os.Stderr, "plugin %s call %s failed: %v\n", info.Name, method, err)
		return 1
	}

	if jsonMode {
		if len(raw) == 0 {
			raw = []byte("null")
		}
		_, _ = os.Stdout.Write(raw)
		_, _ = os.Stdout.Write([]byte("\n"))
		return 0
	}
	pretty := &bytes.Buffer{}
	if err := json.Indent(pretty, raw, "", "  "); err != nil {
		_, _ = os.Stdout.Write(raw)
		_, _ = os.Stdout.Write([]byte("\n"))
		return 0
	}
	fmt.Println(pretty.String())
	return 0
}

// findInstalledPlugin looks up a plugin by name in the plugin dir,
// ignoring whether it's currently enabled. The returned pluginInfo has
// Path and (if a manifest exists) Type/Entry populated.
func findInstalledPlugin(pluginDir, name string) (pluginInfo, bool) {
	for _, p := range discoverPlugins(pluginDir, nil) {
		if strings.EqualFold(p.Name, name) && p.Installed {
			return p, true
		}
	}
	return pluginInfo{}, false
}

// resolvePluginEntry returns the absolute path to the plugin's executable
// entry. For directory-shaped plugins, the manifest's `entry` field is
// taken relative to the plugin dir. For single-file plugins the path
// itself is the entry.
func resolvePluginEntry(p pluginInfo) (string, error) {
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return "", fmt.Errorf("plugin %s has no installed path (not yet installed?)", p.Name)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return path, nil
	}
	entry := strings.TrimSpace(p.Entry)
	if entry == "" {
		return "", fmt.Errorf("plugin %s: manifest missing `entry` field", p.Name)
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(path, entry)
	}
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("plugin %s entry %q: %w", p.Name, entry, err)
	}
	return entry, nil
}

