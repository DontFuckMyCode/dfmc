package ast

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// extractSymbols returns all top-level symbol definitions from the given
// content for non-Go languages (JS/TS, Python, Rust) using regex patterns.
// Go files are handled by extractGoSymbols in go_extract.go.
func extractSymbols(path, lang string, content []byte) []types.Symbol {
	lines := strings.Split(string(content), "\n")
	var symbols []types.Symbol

	add := func(kind types.SymbolKind, name string, line int, signature string) {
		if strings.TrimSpace(name) == "" {
			return
		}
		symbols = append(symbols, types.Symbol{
			Name:      name,
			Kind:      kind,
			Path:      path,
			Line:      line,
			Column:    1,
			Language:  lang,
			Signature: signature,
		})
	}

	switch lang {
	case "typescript", "tsx", "javascript", "jsx":
		for i, line := range lines {
			switch {
			case reJSFunc.MatchString(line):
				m := reJSFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reJSClass.MatchString(line):
				m := reJSClass.FindStringSubmatch(line)
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reJSInterface.MatchString(line):
				m := reJSInterface.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			case reJSType.MatchString(line):
				m := reJSType.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reJSEnum.MatchString(line):
				m := reJSEnum.FindStringSubmatch(line)
				name := ""
				for _, candidate := range m[1:] {
					if strings.TrimSpace(candidate) != "" {
						name = candidate
						break
					}
				}
				add(types.SymbolEnum, name, i+1, strings.TrimSpace(line))
			case reJSConstArrow.MatchString(line):
				m := reJSConstArrow.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			}
		}
	case "python":
		for i, line := range lines {
			if m := rePyClass.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := rePyAsyncFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
			if m := rePyFunc.FindStringSubmatch(line); len(m) > 1 {
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
				continue
			}
		}
	case "rust":
		for i, line := range lines {
			switch {
			case reRustFunc.MatchString(line):
				m := reRustFunc.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			case reRustStruct.MatchString(line):
				m := reRustStruct.FindStringSubmatch(line)
				add(types.SymbolType, m[1], i+1, strings.TrimSpace(line))
			case reRustEnum.MatchString(line):
				m := reRustEnum.FindStringSubmatch(line)
				add(types.SymbolEnum, m[1], i+1, strings.TrimSpace(line))
			case reRustTrait.MatchString(line):
				m := reRustTrait.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			}
		}
	case "ruby":
		// Ruby symbol extraction: classes, modules, and methods (def).
		// `def self.foo` (class method) and `def foo` (instance method)
		// both surface as SymbolFunction; callers can disambiguate via
		// the captured Signature if they need to. We don't bother with
		// attr_accessor / attr_reader / attr_writer here -- those are
		// member declarations rather than top-level symbols and the
		// extra noise dilutes hotspot ranking.
		for i, line := range lines {
			switch {
			case reRubyClass.MatchString(line):
				m := reRubyClass.FindStringSubmatch(line)
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reRubyModule.MatchString(line):
				m := reRubyModule.FindStringSubmatch(line)
				// Ruby modules behave more like namespaces than classes;
				// surface as SymbolClass so codemap groups them next to
				// classes (the natural place for a reader to expect them).
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reRubyDef.MatchString(line):
				m := reRubyDef.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			}
		}
	case "java":
		// Java symbol extraction: classes, interfaces, enums, methods.
		// Method-order matters: class/interface/enum must run BEFORE
		// the method matcher because `public static void main(...)`
		// AND `public class Main { ... }` both share the modifier
		// prefix, and the method regex would happily match a class
		// declaration if class wasn't tried first. The switch's
		// fall-through-by-default semantics here are intentional:
		// each case continues to the next line after a match.
		for i, line := range lines {
			switch {
			case reJavaClass.MatchString(line):
				m := reJavaClass.FindStringSubmatch(line)
				add(types.SymbolClass, m[1], i+1, strings.TrimSpace(line))
			case reJavaInterface.MatchString(line):
				m := reJavaInterface.FindStringSubmatch(line)
				add(types.SymbolInterface, m[1], i+1, strings.TrimSpace(line))
			case reJavaEnum.MatchString(line):
				m := reJavaEnum.FindStringSubmatch(line)
				add(types.SymbolEnum, m[1], i+1, strings.TrimSpace(line))
			case reJavaMethod.MatchString(line):
				m := reJavaMethod.FindStringSubmatch(line)
				add(types.SymbolFunction, m[1], i+1, strings.TrimSpace(line))
			}
		}
	}

	return symbols
}

// extractImports returns import/module dependency strings from content
// for non-Go languages. Go imports are handled by extractGoImports in
// go_extract.go.
func extractImports(lang string, content []byte) []string {
	if lang == "go" {
		return extractGoImports(content)
	}

	lines := strings.Split(string(content), "\n")
	set := map[string]struct{}{}

	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"`)
		v = strings.Trim(v, `'`)
		if v != "" {
			set[v] = struct{}{}
		}
	}

	switch lang {
	case "typescript", "tsx", "javascript", "jsx":
		for _, line := range lines {
			if m := reJSImport.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
			if m := reJSRequire.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "python":
		for _, line := range lines {
			if m := rePyImport.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
			if m := rePyFrom.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "rust":
		for _, line := range lines {
			if m := reRustUseDep.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "ruby":
		for _, line := range lines {
			if m := reRubyRequire.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	case "java":
		// Java imports are clearly delimited (`import x.y.z;`). We
		// include the package statement too -- callers want to see the
		// file's own namespace alongside its dependencies so the
		// codemap can group siblings sensibly.
		for _, line := range lines {
			if m := reJavaImport.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
			if m := reJavaPackage.FindStringSubmatch(line); len(m) > 1 {
				add(m[1])
			}
		}
	}

	imports := make([]string, 0, len(set))
	for k := range set {
		imports = append(imports, k)
	}
	return imports
}
