// Small helpers used across CLI subcommands: memory-tier parsing,
// line truncation, project-brief resolution, word trimming, runtime
// info, binary sizing, and browser launch by OS. Extracted from cli.go
// so the dispatcher stays focused. These are stateless utilities with
// no single owning command.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func parseTier(v string) types.MemoryTier {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "semantic":
		return types.MemorySemantic
	default:
		return types.MemoryEpisodic
	}
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func loadPromptProjectBrief(projectRoot string, maxWords int) string {
	return loadPromptProjectBriefWithPath(projectRoot, "", maxWords)
}

func loadPromptProjectBriefWithPath(projectRoot, pathFlag string, maxWords int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxWords <= 0 {
		return "(none)"
	}
	path := resolvePromptBriefPath(root, pathFlag)
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= 48 {
			break
		}
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	return trimWords(strings.Join(filtered, "\n"), maxWords)
}

func resolvePromptBriefPath(projectRoot, pathFlag string) string {
	if strings.TrimSpace(pathFlag) == "" {
		return filepath.Join(projectRoot, ".dfmc", "magic", "MAGIC_DOC.md")
	}
	if filepath.IsAbs(pathFlag) {
		return pathFlag
	}
	return filepath.Join(projectRoot, pathFlag)
}

func trimWords(text string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) <= maxWords {
		return strings.TrimSpace(text)
	}
	return strings.Join(words[:maxWords], " ")
}

// printHelp renders the top-level help text. Command listing comes from the
// shared commands.Registry so CLI, TUI, and web stay in sync; the global-flags
// block is CLI-only and stays hardcoded here.
func runtimeVersion() string {
	return runtime.Version()
}

func executableSize() int64 {
	exe, err := os.Executable()
	if err != nil {
		return 0
	}
	st, err := os.Stat(exe)
	if err != nil {
		return 0
	}
	return st.Size()
}

func tryOpenBrowser(targetURL string) error {
	name, args, ok := browserCommandForOS(runtime.GOOS, targetURL)
	if !ok {
		return fmt.Errorf("unsupported platform for browser open: %s", runtime.GOOS)
	}
	cmd := exec.Command(name, args...)
	return cmd.Start()
}

func browserCommandForOS(goos, targetURL string) (name string, args []string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return "cmd", []string{"/c", "start", "", targetURL}, true
	case "darwin":
		return "open", []string{targetURL}, true
	case "linux":
		return "xdg-open", []string{targetURL}, true
	default:
		return "", nil, false
	}
}
