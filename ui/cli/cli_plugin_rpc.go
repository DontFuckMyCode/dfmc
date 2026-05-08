// cli_plugin_rpc.go — `dfmc plugin run` JSON-RPC dispatcher and the
// two install-dir lookups it relies on (findInstalledPlugin,
// resolvePluginEntry). Sibling of cli_plugin_skill.go which keeps the
// runPlugin verb router (list/install/enable/disable/manifest/show).
//
// runPluginRPC spawns the plugin process via pluginexec.Spawn, marshals
// the user-supplied params (positional, --params-file, or --params-file
// - for stdin), invokes the requested method with a per-call timeout,
// and prints the response either raw (--json) or pretty-indented. On
// failure it surfaces the captured plugin stderr alongside the exec
// error so the operator sees both the protocol-level error and any
// extra context the plugin's own logging produced.

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
