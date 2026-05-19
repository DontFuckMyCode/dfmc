package context

import (
	"testing"
)

func TestDetectLanguageFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		// Go extensions
		{name: "go_file", path: "foo.go", expected: "go"},
		{name: "go_file_deep", path: "a/b/c/main.go", expected: "go"},

		// TypeScript / TSX
		{name: "ts_file", path: "app.ts", expected: "typescript"},
		{name: "tsx_file", path: "component.tsx", expected: "typescript"},
		{name: "ts_file_deep", path: "src/app/user.ts", expected: "typescript"},
		{name: "tsx_file_deep", path: "src/components/Button.tsx", expected: "typescript"},

		// JavaScript
		{name: "js_file", path: "index.js", expected: "javascript"},
		{name: "jsx_file", path: "App.jsx", expected: "javascript"},
		{name: "mjs_file", path: "module.mjs", expected: "javascript"},
		{name: "cjs_file", path: "common.cjs", expected: "javascript"},

		// Python
		{name: "py_file", path: "script.py", expected: "python"},
		{name: "py_file_deep", path: "pkg/utils/helper.py", expected: "python"},

		// Rust
		{name: "rs_file", path: "lib.rs", expected: "rust"},

		// Java
		{name: "java_file", path: "Main.java", expected: "java"},

		// C#
		{name: "cs_file", path: "Program.cs", expected: "csharp"},

		// PHP
		{name: "php_file", path: "index.php", expected: "php"},

		// Kotlin
		{name: "kt_file", path: "Main.kt", expected: "kotlin"},
		{name: "kts_file", path: "script.kts", expected: "kotlin"},

		// Swift
		{name: "swift_file", path: "main.swift", expected: "swift"},

		// Unknown extensions
		{name: "xyz_unknown", path: "file.xyz", expected: ""},
		{name: "txt_unknown", path: "notes.txt", expected: ""},
		{name: "no_extension", path: "Makefile", expected: ""},
		{name: "empty_extension", path: "file.", expected: ""},

		// Case insensitivity
		{name: "uppercase_GO", path: "main.GO", expected: "go"},
		{name: "mixed_TS", path: "file.TS", expected: "typescript"},
		{name: "mixed_PY", path: "script.PY", expected: "python"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectLanguageFromPath(tt.path)
			if got != tt.expected {
				t.Errorf("detectLanguageFromPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}