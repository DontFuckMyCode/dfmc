package web

// server_context_helpers.go — small helpers for the context/prompt/
// magicdoc handlers: project-brief loading, magic-doc path resolution
// inside the project root, and a handful of list/string utilities.
// Companion siblings:
//
//   - server_context.go          HTTP handler bodies + runtimeHintsFromQuery
//   - server_context_magicdoc.go magic-doc generation pipeline
//
// resolveMagicDocPath enforces the trust boundary: an HTTP caller can
// pass `path=/etc/passwd`, but the helper falls back to the default
// .dfmc/magic/MAGIC_DOC.md whenever the requested path escapes the
// project root.

import (
	"os"
	"path/filepath"
	"strings"
)

func loadProjectBriefForPromptRender(projectRoot, pathFlag string, maxWords int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxWords <= 0 {
		return "(none)"
	}
	path := resolveMagicDocPath(root, pathFlag)
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
	return trimWordsForWeb(strings.Join(filtered, "\n"), maxWords)
}

func trimWordsForWeb(text string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) <= maxWords {
		return strings.TrimSpace(text)
	}
	return strings.Join(words[:maxWords], " ")
}

// resolveMagicDocPath resolves a user-supplied magic-doc path inside
// the project root. An absolute path is only honoured if it's still
// inside the root, so an HTTP caller can't coax the web server into
// reading or writing /etc/passwd by passing `path=/etc/passwd`. A
// blank path falls back to the default .dfmc/magic/MAGIC_DOC.md.
// When the caller passes a path that escapes the root, the default
// location is returned instead — callers treat the surfaced path as
// read-only view data; downstream writers (updateMagicDoc) also stat
// the returned path, so a benign fallback is safer than a 500.
func resolveMagicDocPath(projectRoot, pathFlag string) string {
	def := filepath.Join(projectRoot, ".dfmc", "magic", "MAGIC_DOC.md")
	if strings.TrimSpace(pathFlag) == "" {
		return def
	}
	resolved, err := resolvePathWithinRoot(projectRoot, pathFlag)
	if err != nil {
		return def
	}
	return resolved
}

func clipStringListForWeb(list []string, limit int) []string {
	if limit <= 0 || len(list) <= limit {
		out := make([]string, len(list))
		copy(out, list)
		return out
	}
	out := make([]string, limit)
	copy(out, list[:limit])
	return out
}

func relativeProjectPathForWeb(root, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absP, errP := filepath.Abs(p)
	absR, errR := filepath.Abs(strings.TrimSpace(root))
	if errP == nil && errR == nil && strings.TrimSpace(absR) != "" {
		if rel, err := filepath.Rel(absR, absP); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(p)
}

func fallbackStringForWeb(v, alt string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return alt
	}
	return v
}
