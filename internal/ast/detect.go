package ast

import (
	"path/filepath"
	"strings"
)

// extensionLanguageMap returns the extension → language tag map.
// It is defined here so both detect.go (detectLanguage) and engine.go (Engine.extToLang)
// can share the same source of truth.
func extensionLanguageMap() map[string]string {
	return map[string]string{
		// JavaScript / TypeScript
		".js":  "javascript",
		".jsx": "jsx",
		".ts":  "typescript",
		".tsx": "tsx",
		".mjs": "javascript",
		".cjs": "javascript",
		// Python
		".py":  "python",
		".pyw": "python",
		".pyx": "python",
		// Rust
		".rs": "rust",
		// Go
		".go": "go",
		// C family
		".c":   "c",
		".h":   "c",
		".cpp": "cpp",
		".hpp": "cpp",
		".cc":  "cpp",
		".cxx": "cpp",
		// Java
		".java": "java",
		// C#
		".cs": "csharp",
		// Ruby
		".rb": "ruby",
		// PHP
		".php": "php",
		// Shell
		".sh":   "shell",
		".bash": "bash",
		".zsh":  "zsh",
		// YAML
		".yml":  "yaml",
		".yaml": "yaml",
		// JSON
		".json": "json",
		// Markdown
		".md": "markdown",
		// Lua
		".lua": "lua",
		// Swift
		".swift": "swift",
		// Kotlin
		".kt":  "kotlin",
		".kts": "kotlin",
		// Dockerfile
		"Dockerfile":    "dockerfile",
		"Containerfile": "dockerfile",
		// Ruby scripts (by filename, not extension)
		"Gemfile":  "ruby",
		"Rakefile": "ruby",
	}
}

// detectLanguage returns the language tag for the given file path
// by inspecting its extension, or "" if no mapping exists.
func detectLanguage(path string) string {
	ext := filepath.Ext(path)
	base := filepath.Base(path)
	if lang, ok := extensionLanguageMap()[ext]; ok {
		return lang
	}
	if lang, ok := extensionLanguageMap()[base]; ok {
		return lang
	}
	return ""
}

// detectLanguageFromContent inspects the first 512 bytes of content
// for a shebang line and returns a language hint if one is found
// (e.g. "#!/usr/bin/env python3" → "python").
// This complements extension-based detection; it is only consulted
// when extension lookup returns "".
func detectLanguageFromContent(content []byte) string {
	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		firstLine := string(content)
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = firstLine[:idx]
		}
		switch {
		case strings.Contains(firstLine, "python"):
			return "python"
		case strings.Contains(firstLine, "node"):
			return "javascript"
		case strings.Contains(firstLine, "bash"), strings.Contains(firstLine, "/sh"):
			return "bash"
		}
	}
	return ""
}
