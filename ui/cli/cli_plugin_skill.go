// Plugin and skill subcommands plus their installers, discovery,
// manifest validation, archive extraction, and skill-prompt builders.
// Extracted from cli.go so the dispatcher stays focused. These commands
// share plugin-directory resolution, manifest parsing, and builtin
// skill definitions so they travel together.

package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/pluginexec"
	"gopkg.in/yaml.v3"
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

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Source      string `json:"source"`
	Builtin     bool   `json:"builtin"`
	Prompt      string `json:"-"`
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
					_ = printJSON(p)
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

func runSkill(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		items := discoverSkills(eng.Status().ProjectRoot)
		if jsonMode {
			_ = printJSON(map[string]any{"skills": items})
			return 0
		}
		for _, s := range items {
			label := s.Source
			if s.Builtin {
				label = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, label)
		}
		return 0

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		items := discoverSkills(eng.Status().ProjectRoot)
		for _, s := range items {
			if strings.EqualFold(s.Name, name) {
				if jsonMode {
					_ = printJSON(s)
				} else {
					fmt.Printf("Name:        %s\n", s.Name)
					fmt.Printf("Source:      %s\n", s.Source)
					fmt.Printf("Builtin:     %t\n", s.Builtin)
					if s.Description != "" {
						fmt.Printf("Description: %s\n", s.Description)
					}
					if s.Path != "" {
						fmt.Printf("Path:        %s\n", s.Path)
					}
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
		return 1

	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc skill run <name> [input]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		input := strings.TrimSpace(strings.Join(args[2:], " "))
		return runNamedSkill(ctx, eng, name, input, jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc skill [list|info <name>|run <name> [input]]")
		return 2
	}
}

func runSkillShortcut(ctx context.Context, eng *engine.Engine, name string, args []string, jsonMode bool) int {
	input := strings.TrimSpace(strings.Join(args, " "))
	if input == "" {
		input = "Analyze the current project."
	}
	return runNamedSkill(ctx, eng, name, input, jsonMode)
}

func runNamedSkill(ctx context.Context, eng *engine.Engine, name, input string, jsonMode bool) int {
	items := discoverSkills(eng.Status().ProjectRoot)
	for _, s := range items {
		if !strings.EqualFold(s.Name, name) {
			continue
		}
		prompt := buildSkillPrompt(s, input)
		answer, err := eng.Ask(ctx, prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill run failed: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"skill":  s.Name,
				"source": s.Source,
				"input":  input,
				"answer": answer,
			})
			return 0
		}
		fmt.Println(answer)
		return 0
	}
	fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
	return 1
}

func buildSkillPrompt(skill skillInfo, input string) string {
	p := strings.TrimSpace(skill.Prompt)
	if p == "" {
		p = input
	} else if strings.Contains(p, "{input}") {
		p = strings.ReplaceAll(p, "{input}", input)
	} else if strings.TrimSpace(input) != "" {
		p = p + "\n\nUser request:\n" + input
	}
	return p
}

func discoverPlugins(pluginDir string, enabled []string) []pluginInfo {
	seen := map[string]pluginInfo{}
	entries, err := os.ReadDir(pluginDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			base := strings.TrimSuffix(name, filepath.Ext(name))
			path := filepath.Join(pluginDir, name)
			if e.IsDir() {
				base = name
			} else {
				ext := strings.ToLower(filepath.Ext(name))
				if !pluginFileExtSupported(ext) {
					continue
				}
			}
			info := pluginInfo{
				Name:      base,
				Path:      path,
				Installed: true,
				Enabled:   containsCI(enabled, base),
			}
			if mf, mfPath, ok := readPluginManifest(path); ok {
				if strings.TrimSpace(mf.Name) != "" {
					info.Name = strings.TrimSpace(mf.Name)
				}
				info.Version = strings.TrimSpace(mf.Version)
				info.Type = strings.TrimSpace(mf.Type)
				info.Entry = strings.TrimSpace(mf.Entry)
				info.Manifest = mfPath
				info.Enabled = info.Enabled || containsCI(enabled, info.Name)
			}
			key := strings.ToLower(strings.TrimSpace(info.Name))
			if key == "" {
				continue
			}
			seen[key] = info
		}
	}

	for _, name := range enabled {
		n := strings.TrimSpace(name)
		key := strings.ToLower(n)
		if n == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = pluginInfo{
			Name:      n,
			Installed: false,
			Enabled:   true,
		}
	}

	out := make([]pluginInfo, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func installPluginFile(pluginDir, sourcePath, nameOverride string, force bool) (pluginInfo, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return pluginInfo{}, fmt.Errorf("source path is required")
	}

	resolvedSource, cleanup, err := resolvePluginSource(sourcePath)
	if err != nil {
		return pluginInfo{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	srcAbs, err := filepath.Abs(resolvedSource)
	if err != nil {
		return pluginInfo{}, err
	}
	srcAbs, archiveCleanup, err := expandPluginSourceIfArchive(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if archiveCleanup != nil {
		defer archiveCleanup()
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return pluginInfo{}, err
	}
	if !srcInfo.IsDir() {
		if !pluginSourceFileExtSupported(strings.ToLower(filepath.Ext(srcAbs))) {
			return pluginInfo{}, fmt.Errorf("unsupported plugin file extension: %s", filepath.Ext(srcAbs))
		}
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return pluginInfo{}, err
	}
	pluginDirAbs, err := filepath.Abs(pluginDir)
	if err != nil {
		return pluginInfo{}, err
	}

	targetName := strings.TrimSpace(nameOverride)
	if targetName == "" {
		if srcInfo.IsDir() {
			if mf, _, ok := readPluginManifest(srcAbs); ok && strings.TrimSpace(mf.Name) != "" {
				targetName = strings.TrimSpace(mf.Name)
			}
		}
		if srcInfo.IsDir() {
			if targetName == "" {
				targetName = filepath.Base(srcAbs)
			}
		} else {
			targetName = strings.TrimSuffix(filepath.Base(srcAbs), filepath.Ext(srcAbs))
		}
	}
	targetName = sanitizePluginName(targetName)
	if targetName == "" {
		return pluginInfo{}, fmt.Errorf("invalid plugin name")
	}

	targetPath := filepath.Join(pluginDirAbs, targetName)
	if !srcInfo.IsDir() {
		ext := filepath.Ext(srcAbs)
		if ext != "" {
			targetPath = targetPath + ext
		}
	}
	targetPath, err = resolvePathWithinBase(pluginDirAbs, targetPath)
	if err != nil {
		return pluginInfo{}, err
	}

	if _, err := os.Stat(targetPath); err == nil {
		if !force {
			return pluginInfo{}, fmt.Errorf("target already exists: %s (use --force)", targetPath)
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	if srcInfo.IsDir() {
		if err := copyDir(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
		if err := validatePluginManifestEntry(targetPath); err != nil {
			_ = removePathSafe(pluginDirAbs, targetPath)
			return pluginInfo{}, err
		}
	} else {
		if err := copyFile(srcAbs, targetPath); err != nil {
			return pluginInfo{}, err
		}
	}

	info := pluginInfo{
		Name:      targetName,
		Path:      targetPath,
		Installed: true,
		Enabled:   false,
	}
	if srcInfo.IsDir() {
		if mf, mfPath, ok := readPluginManifest(targetPath); ok {
			info.Version = strings.TrimSpace(mf.Version)
			info.Type = strings.TrimSpace(mf.Type)
			info.Entry = strings.TrimSpace(mf.Entry)
			info.Manifest = mfPath
			if strings.TrimSpace(mf.Name) != "" && strings.TrimSpace(nameOverride) == "" {
				info.Name = strings.TrimSpace(mf.Name)
			}
		}
	}
	return info, nil
}

func removeInstalledPlugin(pluginDir, name string) (string, error) {
	items := discoverPlugins(pluginDir, nil)
	for _, item := range items {
		if !item.Installed || strings.TrimSpace(item.Path) == "" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
		if !strings.EqualFold(item.Name, name) && !strings.EqualFold(base, name) {
			continue
		}
		pluginDirAbs, err := filepath.Abs(pluginDir)
		if err != nil {
			return "", err
		}
		targetPath, err := resolvePathWithinBase(pluginDirAbs, item.Path)
		if err != nil {
			return "", err
		}
		if err := removePathSafe(pluginDirAbs, targetPath); err != nil {
			return "", err
		}
		return targetPath, nil
	}
	return "", nil
}

func resolvePluginSource(source string) (resolved string, cleanup func(), err error) {
	if isHTTPPluginSource(source) {
		path, err := downloadPluginSource(source)
		if err != nil {
			return "", nil, err
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	return source, nil, nil
}

func isHTTPPluginSource(source string) bool {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	if u == nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return strings.TrimSpace(u.Host) != ""
	default:
		return false
	}
}

func downloadPluginSource(src string) (string, error) {
	resp, err := http.Get(src) //nolint:gosec // plugin install intentionally fetches user-provided URL.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	ext := ".plugin"
	if u, err := url.Parse(src); err == nil {
		if e := strings.TrimSpace(filepath.Ext(u.Path)); e != "" {
			ext = e
		}
	}
	tmp, err := os.CreateTemp("", "dfmc-plugin-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func readPluginManifest(path string) (pluginManifest, string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return pluginManifest{}, "", false
	}
	if !info.IsDir() {
		return pluginManifest{}, "", false
	}

	candidates := []string{
		filepath.Join(path, "plugin.yaml"),
		filepath.Join(path, "plugin.yml"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var mf pluginManifest
		if err := yaml.Unmarshal(data, &mf); err != nil {
			continue
		}
		if strings.TrimSpace(mf.Name) == "" &&
			strings.TrimSpace(mf.Version) == "" &&
			strings.TrimSpace(mf.Type) == "" &&
			strings.TrimSpace(mf.Entry) == "" {
			continue
		}
		return mf, candidate, true
	}
	return pluginManifest{}, "", false
}

func sanitizePluginName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, ":", "_")
	return name
}

func pluginFileExtSupported(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".so", ".dll", ".dylib", ".wasm", ".js", ".mjs", ".py", ".sh":
		return true
	default:
		return false
	}
}

func pluginSourceFileExtSupported(ext string) bool {
	if pluginFileExtSupported(ext) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(ext), ".zip")
}

func expandPluginSourceIfArchive(path string) (string, func(), error) {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return path, nil, nil
	}
	tmpDir, err := os.MkdirTemp("", "dfmc-plugin-zip-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	if err := extractZipArchive(path, tmpDir); err != nil {
		cleanup()
		return "", nil, err
	}
	root := archiveRootDir(tmpDir)
	return root, cleanup, nil
}

func archiveRootDir(tmpDir string) string {
	entries, err := os.ReadDir(tmpDir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return tmpDir
	}
	return filepath.Join(tmpDir, entries[0].Name())
}

func extractZipArchive(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) == 0 {
		return fmt.Errorf("zip archive is empty")
	}
	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if cleanName == "." || cleanName == "" {
			continue
		}
		if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
			return fmt.Errorf("zip archive contains unsafe path: %s", f.Name)
		}
		targetPath, err := resolvePathWithinBase(dstDir, filepath.Join(dstDir, cleanName))
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip archive contains symlink entry: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if err := writeFileFromReader(targetPath, rc); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
	}
	return nil
}

func writeFileFromReader(path string, r io.Reader) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return out.Close()
}

func validatePluginManifestEntry(pluginPath string) error {
	mf, _, ok := readPluginManifest(pluginPath)
	if !ok {
		return nil
	}
	entry := strings.TrimSpace(mf.Entry)
	if entry == "" {
		return nil
	}
	entryPath, err := resolvePathWithinBase(pluginPath, filepath.Join(pluginPath, entry))
	if err != nil {
		return fmt.Errorf("plugin manifest entry invalid: %w", err)
	}
	st, err := os.Stat(entryPath)
	if err != nil {
		return fmt.Errorf("plugin manifest entry not found: %s", entry)
	}
	if st.IsDir() {
		return fmt.Errorf("plugin manifest entry points to directory: %s", entry)
	}
	if ext := strings.ToLower(filepath.Ext(entryPath)); ext != "" && !pluginFileExtSupported(ext) {
		return fmt.Errorf("plugin manifest entry has unsupported extension: %s", ext)
	}
	return nil
}

func resolvePathWithinBase(base, target string) (string, error) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs := target
	if !filepath.IsAbs(targetAbs) {
		targetAbs = filepath.Join(baseAbs, targetAbs)
	}
	targetAbs, err = filepath.Abs(targetAbs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	return targetAbs, nil
}

func removePathSafe(base, target string) error {
	targetAbs, err := resolvePathWithinBase(base, target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetAbs); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(targetAbs)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func updatePluginEnabled(ctx context.Context, eng *engine.Engine, name string, enabled, global bool) error {
	_ = ctx
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return err
	}
	var list []string
	raw, _ := getConfigPath(currentMap, "plugins.enabled")
	switch arr := raw.(type) {
	case []any:
		for _, item := range arr {
			v := strings.TrimSpace(fmt.Sprint(item))
			if v != "" {
				list = append(list, v)
			}
		}
	case []string:
		for _, item := range arr {
			v := strings.TrimSpace(item)
			if v != "" {
				list = append(list, v)
			}
		}
	}

	if enabled {
		if !containsCI(list, name) {
			list = append(list, name)
		}
	} else {
		next := make([]string, 0, len(list))
		for _, item := range list {
			if !strings.EqualFold(item, name) {
				next = append(next, item)
			}
		}
		list = next
	}

	values := make([]any, 0, len(list))
	for _, item := range list {
		values = append(values, item)
	}
	if err := setConfigPath(currentMap, "plugins.enabled", values); err != nil {
		return err
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return fmt.Errorf("config reload failed, reverted: %w", err)
	}
	return nil
}

func discoverSkills(projectRoot string) []skillInfo {
	out := make([]skillInfo, 0, 16)
	seen := map[string]struct{}{}
	for _, item := range builtinSkills() {
		key := strings.ToLower(item.Name)
		seen[key] = struct{}{}
		out = append(out, item)
	}

	roots := []struct {
		Path   string
		Source string
	}{
		{Path: filepath.Join(projectRoot, ".dfmc", "skills"), Source: "project"},
		{Path: filepath.Join(config.UserConfigDir(), "skills"), Source: "global"},
	}

	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.Path, "*.y*ml"))
		for _, path := range files {
			item := readSkillFile(path, root.Source)
			if strings.TrimSpace(item.Name) == "" {
				continue
			}
			key := strings.ToLower(item.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func builtinSkills() []skillInfo {
	return []skillInfo{
		{
			Name:        "review",
			Description: "Code review: correctness, risk, missing tests, security smells",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the REVIEW skill. Review the changed code for correctness, risk, and test coverage — not style nits.

Playbook:
1. Scope. Identify exactly what changed: use git_diff / read_file on the target paths. If the target is "recent changes" with no path, read git_log and diff the most recent non-trivial commit. For a suspect line in a long file, git_blame on a narrow line range tells you which commit shaped it — useful when the change touches code with a non-obvious history.
2. Correctness. For each change, answer: does it do what the commit message / PR claims? Trace the happy path AND at least one error path. Name any branch that's unreachable or any condition that's always true/false.
3. Behavioral risk. Look for changes that quietly alter: public API, on-disk format, error types, side-effect ordering, concurrency semantics, allocation patterns. Flag each with file:line and the exact risk.
4. Tests. Check whether the changed code is exercised by an existing test. If not, say which test SHOULD exist (name it) and why the gap matters. Do not demand tests for trivial code.
5. Security + resource. Check for path traversal, unbounded allocation, unchecked user input, credentials in plaintext, SQL/command injection, missing ctx cancellation, leaked goroutines. Only flag real findings — stop if there are none.
6. Report. Structure: Must-fix / Should-fix / Nits / Tests to add. Use file_path:line. If the change is clean, say so and stop — padding the review wastes the reader.

Do NOT restate what the code does; the author already knows. Do NOT suggest renames, formatting changes, or "consider extracting a helper" unless the current form causes a real bug or risk.

Request:
{input}`,
		},
		{
			Name:        "explain",
			Description: "Explain code: trace the flow, name the invariants, call out surprises",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the EXPLAIN skill. Produce a working mental model of the target, not a paraphrase of the source.

Playbook:
1. Locate. Use read_file / grep_codebase to pin down the target: file, function, region. If the target is ambiguous (e.g. "how auth works"), name the entry point you chose and why.
2. Trace one real flow. Pick a representative input and walk it through the code end-to-end. Name each hop as file_path:line. Stop when the value leaves the target (returns, writes to disk, sends on a channel).
3. Name invariants. What must always be true for this code to be correct? Who enforces it — the function itself, the caller, a type, a lock? State this even when it looks obvious; obvious invariants are the ones that get broken in refactors.
4. Call out surprises. Any non-obvious decision, hidden constraint, leaky abstraction, workaround for a specific bug, or counterintuitive ordering. If the code is boring, say so rather than invent surprises.
5. Draw the shape. If the flow involves multiple files or concurrent paths, include a tiny plaintext diagram (one to six lines, arrows, no art).
6. Report. Audience-appropriate summary on top, then the trace, then invariants, then surprises. If the reader will act on this (fix a bug, add a feature), point at the exact entry point they should touch.

Do NOT produce line-by-line narration. Do NOT restate obvious type signatures. Do NOT guess — if the answer requires reading more files, read them.

Request:
{input}`,
		},
		{
			Name:        "refactor",
			Description: "Plan and execute a safe refactor: scope, invariants, step list, verification",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the REFACTOR skill. Ship a concrete, reversible refactor — not a design essay.

Playbook:
1. Scope. Use grep_codebase / list_dir / read_file to find every call site and touched file. State what's IN scope and what's explicitly OUT of scope.
2. Invariants. List the observable behaviors that must not change (public API, on-disk format, error types, side-effect ordering). If a test already pins one, name it.
3. Step plan. Break the refactor into the smallest sequence of commits that each leave the tree green. Each step: files, change, how to verify.
4. Execute. Make the edits via edit_file / write_file. Do NOT introduce new abstractions the task doesn't need. Do NOT rename things that aren't in scope.
5. Verify. Run the smallest test command that exercises the changed code. If tests don't exist for the invariants, add them first or name the risk.
6. Report. Summarise what moved, what stayed, and any invariant you could not mechanically verify.

Stop and ask if the scope is unclear or the request implies a behavior change. Refactors that quietly change behavior are the worst kind.

Request:
{input}`,
		},
		{
			Name:        "debug",
			Description: "Reproduce, bisect, and fix a bug — with a regression test",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the DEBUG skill. Root-cause the problem; do not paper over it.

Playbook:
1. Reproduce. Turn the report into a minimal command or test that fails. If you cannot reproduce, stop and say so — guessing is worse than nothing.
2. Bisect. Use git_log / git_diff / git_blame, read_file, grep_codebase to narrow the failure to a specific function, commit, or config value. git_blame on a suspect line tells you which commit introduced the current behavior — pull that commit's diff next. Name the exact line that produces the wrong behavior.
3. Explain the mechanism. Write one paragraph that traces inputs through the code to the bad output. If the explanation hand-waves ("probably a race", "might be cache"), keep digging.
4. Fix at the root. Prefer the smallest change that removes the cause. Do NOT add try/except that just swallows the error. Do NOT add a feature flag to bypass the path.
5. Regression test. Add a test that fails without the fix and passes with it. Name the file and the test function.
6. Verify. Run the new test AND the nearest existing test package. Report pass/fail output verbatim.
7. Report. One-line cause, one-line fix, test name, any nearby latent bugs you spotted but left alone.

If you are not sure the fix addresses the root cause, say that explicitly — a partial fix with a named uncertainty beats a confident patch that just moves the bug.

Request:
{input}`,
		},
		{
			Name:        "test",
			Description: "Generate or improve tests: discover framework, find gaps, implement, run",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the TEST skill. Ship tests that actually execute, not pseudocode.

Playbook:
1. Discover the framework. Check go.mod / package.json / pyproject.toml and the existing _test files under the target package. Mirror the style already used — do not introduce a new test library.
2. Map the surface. For the target code, list every exported function and every non-trivial branch (error paths, boundary values, empty slice, missing key).
3. Identify gaps. Diff what already has coverage against what doesn't. Prioritise: correctness bugs > regression risk > edge cases > happy path.
4. Write tests. Keep them isolated (no network, no shared global state). Use table-driven style when Go, parametrised when Python/TS. Each test name states the behavior it pins ("returns_error_when_path_escapes_root"), not the function name.
5. Run them. Invoke the nearest test command via run_command. Paste the actual output. If any fail, fix the test or the code (decide which is wrong — do not silently edit the test to make it pass).
6. Report. Files added, cases added, command used, final result. Call out tests you chose NOT to add and why (e.g. "skipped I/O-heavy path; would need a fixture the repo lacks").

Do NOT add mocks for code the repo does not mock elsewhere. Do NOT assert on error message text unless the codebase already does — error types are sturdier than strings.

Request:
{input}`,
		},
		{
			Name:        "doc",
			Description: "Write documentation that teaches the code, not the signature",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the DOC skill. Write documentation a future engineer can act on — not a pretty-printed function signature.

Playbook:
1. Find the target. Use read_file / grep_codebase to read the code you're documenting. If the target is a package or module, read its public surface AND at least one representative implementation.
2. Decide the shape. Prose README for packages, block comments for exported symbols, inline comments only for non-obvious WHY. Match what the codebase already does — don't introduce a new doc style.
3. Write for the reader. For each piece: what problem does it solve, who calls it, what are the inputs/outputs, what invariants does it enforce, what happens on the error path. Prefer one concrete example over three abstract sentences.
4. Name the sharp edges. Rate-limits, thread-safety, panics, cancellation semantics, side effects, ordering requirements. If none exist, say "no side effects; safe for concurrent use" — explicit is better than implied.
5. Link, don't duplicate. Reference existing docs/types/tests rather than restate them. Use file_path:line for deep pointers.
6. Report. Files written/updated and what you chose NOT to document (e.g. "internal helpers — obvious from call sites").

Do NOT write "this function does X" when X is literally the function name. Do NOT invent examples that would not actually compile. Do NOT document trivially obvious code.

Request:
{input}`,
		},
		{
			Name:        "generate",
			Description: "Generate new code that obeys the project's conventions and tests it",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the GENERATE skill. Ship working, tested code — not scaffolding.

Playbook:
1. Understand the ask. Restate what's being built in one sentence. If ambiguous, stop and ask — inventing behavior is worse than asking.
2. Learn the conventions. Before writing anything new, read_file on two or three nearby siblings (same package, similar role). Mirror their structure, naming, error handling, and test style. Do NOT introduce a new pattern the codebase doesn't already use.
3. Place the code. Decide where it goes (file, package). State why — matching an existing boundary usually beats creating a new one.
4. Write the smallest version that works. No speculative configuration, no dead options, no TODO comments. Every identifier names something that exists.
5. Write at least one test. Same framework the package already uses. Test the behavior, not the implementation. If the new code is pure, a table-driven test is usually right.
6. Wire it. If the new code needs to be registered / exported / imported somewhere, do that in the same change. Half-landed code is worse than no code.
7. Verify. Build and run the nearest test. Paste the output. Fix what breaks.
8. Report. Files touched, public API added, test added, command used, any follow-ups you chose to defer.

Do NOT add error handling for impossible conditions. Do NOT add interfaces with a single implementer. Do NOT expose fields "in case we need them later".

Request:
{input}`,
		},
		{
			Name:        "audit",
			Description: "Security audit: triaged findings with file:line, severity, and fix direction",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the AUDIT skill. Produce a triaged security report — exploitable findings first, theoretical concerns last.

Playbook:
1. Frame the surface. What inputs does this code trust (user, network, filesystem, env)? What secrets does it handle? What does it delegate to (subprocess, SQL, template engine)? State the boundary you're auditing.
2. Run the obvious checks. Use grep_codebase for dangerous patterns matched to the language: path traversal (filepath.Join without cleanroot, os.Open on user input), command injection (exec with shell, os/exec with interpolated strings), SSRF (http.Get on user URLs), deserialization (json/yaml into map[string]any with reflection), SQL (string concatenation, fmt.Sprintf into DB query), secrets (ENV, .env, hardcoded tokens, credentials in logs), weak crypto (md5, sha1, random without crypto/rand).
3. Confirm each hit. Follow the taint: is the dangerous sink actually reachable from an untrusted source? A grep hit on exec.Command inside a test fixture is not a finding. A grep hit on exec.Command with user-controlled args is. When triaging, git_blame on the suspect line surfaces who introduced it and when — useful for prioritising recent additions over code that's been battle-tested for years.
4. Triage. For each real finding: CRITICAL (remote code exec, auth bypass), HIGH (data exfiltration, privilege escalation), MEDIUM (information disclosure, DoS), LOW (defense-in-depth, non-default configs). If you find nothing, say so clearly.
5. Fix direction. Each finding gets one concrete remediation: the specific check to add, the safer API to use, or the design change required. Do NOT just say "sanitize input".
6. Report. Ordered by severity, with file_path:line, exploit sketch, fix direction. Separate section for "reviewed and not a finding" if you checked something notable.

Do NOT invent findings to pad the report. Do NOT cite CWE numbers unless you can tie them to a real line. Do NOT recommend adding a library when a few lines of code would do.

Request:
{input}`,
		},
		{
			Name:        "onboard",
			Description: "Codebase walkthrough: hot paths, surprises, where to start changing",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the ONBOARD skill. Give a new contributor the shortest path to being productive — not a table of contents.

Playbook:
1. Orient. Read README / CLAUDE.md / package docstrings for the stated purpose. State in one sentence what the project actually does; if the README oversells it, say the honest version.
2. Name the hub. Which file / package is the nerve center? Where does execution start? Use read_file on main / entry points / top-level registries. Name it as file_path:line.
3. Trace one real flow end-to-end. Pick a representative user action (e.g. "user runs dfmc ask") and walk it through the code. Every hop named as file_path:line. Keep it under ten hops — link, don't copy.
4. Map the modules. For each top-level package: one sentence on what it owns, one sentence on who calls it. Cap at six packages — group the rest as "supporting".
5. Call out surprises. Non-obvious constraints (CGO required, windows-specific fallbacks, lock files, singletons, env vars). A new contributor hitting these blind wastes an afternoon.
6. Where to start. Three concrete first-commit ideas, each scoped small enough to land in a single PR. Name the file, what to change, how to verify.
7. Report. Purpose / Hub / One real flow / Modules / Surprises / First commits. Use headers; be skimmable.

Do NOT list every file. Do NOT recite directory structures the reader can run ls on. Do NOT recommend a first commit that requires changing three packages.

Request:
{input}`,
		},
	}
}

func readSkillFile(path, source string) skillInfo {
	item := skillInfo{
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:    path,
		Source:  source,
		Builtin: false,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return item
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return item
	}
	if v, ok := raw["name"]; ok {
		name := strings.TrimSpace(fmt.Sprint(v))
		if name != "" {
			item.Name = name
		}
	}
	if v, ok := raw["description"]; ok {
		item.Description = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := raw["prompt"]; ok {
		item.Prompt = strings.TrimSpace(fmt.Sprint(v))
	}
	if item.Prompt == "" {
		if v, ok := raw["template"]; ok {
			item.Prompt = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return item
}

func containsCI(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

