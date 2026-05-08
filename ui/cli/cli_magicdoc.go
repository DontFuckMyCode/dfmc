package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

const defaultMagicDocRelPath = ".dfmc/magic/MAGIC_DOC.md"

// buildMagicDocContent + per-section formatters (formatHotspot,
// recentMessages, toProjectRelative, clipList, fallback) live in
// cli_magicdoc_render.go.

func runMagicDoc(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"update"}
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))

	switch action {
	case "update", "sync", "generate":
		fs := flag.NewFlagSet("magicdoc update", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		pathFlag := fs.String("path", "", "target magic doc path")
		titleFlag := fs.String("title", "DFMC Project Brief", "document title")
		hotspotsFlag := fs.Int("hotspots", 8, "max hotspot entries")
		depsFlag := fs.Int("deps", 8, "max dependency entries")
		recentFlag := fs.Int("recent", 5, "max recent items per section")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		st := eng.Status()
		root := strings.TrimSpace(st.ProjectRoot)
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc: cannot resolve project root: %v\n", err)
				return 1
			}
			root = cwd
		}

		target := resolveMagicDocPath(root, strings.TrimSpace(*pathFlag))
		content, err := buildMagicDocContent(ctx, eng, root, strings.TrimSpace(*titleFlag), *hotspotsFlag, *depsFlag, *recentFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc build failed: %v\n", err)
			return 1
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc mkdir failed: %v\n", err)
			return 1
		}

		previous, _ := os.ReadFile(target)
		changed := string(previous) != content
		if changed {
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc write failed: %v\n", err)
				return 1
			}
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"status":  "ok",
				"path":    target,
				"updated": changed,
				"bytes":   len(content),
			})
			return 0
		}
		fmt.Printf("magicdoc %s: %s\n", map[bool]string{true: "updated", false: "unchanged"}[changed], target)
		return 0

	case "show", "cat":
		fs := flag.NewFlagSet("magicdoc show", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		pathFlag := fs.String("path", "", "target magic doc path")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		st := eng.Status()
		root := strings.TrimSpace(st.ProjectRoot)
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "magicdoc: cannot resolve project root: %v\n", err)
				return 1
			}
			root = cwd
		}

		target := resolveMagicDocPath(root, strings.TrimSpace(*pathFlag))
		data, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magicdoc read failed: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"path":    target,
				"content": string(data),
			})
			return 0
		}
		fmt.Print(string(data))
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc magicdoc [update|show] [--path <file>] [--title <title>]")
		return 2
	}
}

func resolveMagicDocPath(projectRoot, pathFlag string) string {
	if strings.TrimSpace(pathFlag) == "" {
		return filepath.Join(projectRoot, filepath.FromSlash(defaultMagicDocRelPath))
	}
	if filepath.IsAbs(pathFlag) {
		return pathFlag
	}
	return filepath.Join(projectRoot, pathFlag)
}
