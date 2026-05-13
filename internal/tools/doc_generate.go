// doc_generate.go — Automatic documentation generator.
// Generates docstrings, godoc comments, README templates, and changelog stubs.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DocGenerateTool generates documentation for Go code.
type DocGenerateTool struct{}

func NewDocGenerateTool() *DocGenerateTool { return &DocGenerateTool{} }
func (t *DocGenerateTool) Name() string    { return "doc_generate" }
func (t *DocGenerateTool) Description() string {
	return "Generate documentation for Go code. Modes: func, package, readme, changelog."
}
func (t *DocGenerateTool) Risk() Risk      { return RiskRead }
func (t *DocGenerateTool) Cacheable() bool { return false }

type DocRequest struct {
	Target      string `json:"target"` // File or directory path
	Mode        string `json:"mode"`   // func, package, readme, changelog
	Format      string `json:"format"` // godoc, jsdoc, rustdoc
	ProjectRoot string `json:"project_root"`
}

type DocOutput struct {
	Target    string `json:"target"`
	Mode      string `json:"mode"`
	Generated string `json:"generated"`
	Format    string `json:"format"`
}

func (t *DocGenerateTool) Execute(ctx context.Context, req Request) (Result, error) {
	target := asString(req.Params, "target", "")
	mode := asString(req.Params, "mode", "package")
	format := asString(req.Params, "format", "godoc")

	if target == "" {
		return Result{}, fmt.Errorf("doc_generate requires target parameter")
	}

	var generated string
	var err error

	switch mode {
	case "func":
		generated, err = t.generateFuncDoc(target, format)
	case "package":
		generated, err = t.generatePackageDoc(target, format)
	case "readme":
		generated, err = t.generateReadme(target)
	case "changelog":
		generated, err = t.generateChangelog(target)
	default:
		return Result{}, fmt.Errorf("unknown mode: %s (valid: func, package, readme, changelog)", mode)
	}

	if err != nil {
		return Result{}, fmt.Errorf("doc_generate failed: %w", err)
	}

	output := DocOutput{
		Target:    target,
		Mode:      mode,
		Generated: generated,
		Format:    format,
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return Result{Output: string(data)}, nil
}

func (t *DocGenerateTool) generateFuncDoc(target, format string) (string, error) {
	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	var funcs []funcInfo
	var currentFunc funcInfo

	reFunc := regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(?:\(([^)]*)\))?`)
	reParam := regexp.MustCompile(`(\w+)\s+(.+)`)

	for i, line := range lines {
		if m := reFunc.FindStringSubmatch(line); m != nil {
			if currentFunc.Name != "" {
				funcs = append(funcs, currentFunc)
			}
			currentFunc = funcInfo{
				Name:      m[1],
				Params:    m[2],
				Results:   m[3],
				Line:      i + 1,
				Signature: strings.TrimSpace(line),
			}
			// Parse param names
			params := strings.Split(m[2], ",")
			for _, p := range params {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if pm := reParam.FindStringSubmatch(p); pm != nil {
					currentFunc.ParamNames = append(currentFunc.ParamNames, pm[1])
				}
			}
		}
	}
	if currentFunc.Name != "" {
		funcs = append(funcs, currentFunc)
	}

	var doc strings.Builder
	for _, f := range funcs {
		doc.WriteString(fmt.Sprintf("// %s ", f.Name))
		if format == "godoc" {
			doc.WriteString("does ...\n\n")
			doc.WriteString(fmt.Sprintf("// %s takes %d parameters", f.Name, len(f.ParamNames)))
			if len(f.ParamNames) > 0 {
				doc.WriteString(": ")
				doc.WriteString(strings.Join(f.ParamNames, ", "))
			}
			doc.WriteString(".\n\n")
			doc.WriteString(fmt.Sprintf("func %s", f.Signature))
			doc.WriteString("\n\n---\n")
		}
	}

	return doc.String(), nil
}

type funcInfo struct {
	Name       string
	Params     string
	Results    string
	Signature  string
	ParamNames []string
	Line       int
}

func (t *DocGenerateTool) generatePackageDoc(target, format string) (string, error) {
	var files []string

	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat target: %w", err)
	}

	if info.IsDir() {
		filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
	} else {
		files = []string{target}
	}

	var pkgName string
	var doc strings.Builder

	// Parse package
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		content := string(data)
		lines := strings.Split(content, "\n")

		// Find package name
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				parts := strings.SplitN(line, " ", 2)
				if len(parts) == 2 {
					pkgName = strings.TrimSpace(parts[1])
				}
				break
			}
		}

		// Find exported functions, types, constants
		reExport := regexp.MustCompile(`^// ([A-Z]\w*) (?:is|does|represents|provides|returns|is a)`)
		reType := regexp.MustCompile(`^type\s+([A-Z]\w*)\s`)
		reConst := regexp.MustCompile(`^const\s+(?:[A-Z]\w*\s+)?(?:val\s+)?(?:=\s+)?([A-Z][A-Za-z0-9_]*)`)

		for _, line := range lines {
			if m := reExport.FindStringSubmatch(line); m != nil {
				doc.WriteString(line)
				doc.WriteString("\n")
			}
			if m := reType.FindStringSubmatch(line); m != nil {
				doc.WriteString(fmt.Sprintf("// %s represents ...\n", m[1]))
			}
			if m := reConst.FindStringSubmatch(line); m != nil {
				doc.WriteString(fmt.Sprintf("// %s ...\n", m[1]))
			}
		}
	}

	if pkgName == "" {
		pkgName = "packagename"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("// Package %s ...\n", pkgName))
	output.WriteString("//\n")
	output.WriteString("// This package provides ...\n")
	output.WriteString("//\n")
	output.WriteString("// Usage:\n")
	output.WriteString("//\n")
	output.WriteString(fmt.Sprintf("//\timport \"%s\"\n", pkgName))
	output.WriteString("//\n")
	output.WriteString(doc.String())

	return output.String(), nil
}

func (t *DocGenerateTool) generateReadme(target string) (string, error) {
	var pkgName string
	var exports []string

	files, err := filepath.Glob(filepath.Join(target, "*.go"))
	if err != nil {
		return "", fmt.Errorf("glob go files: %w", err)
	}

	rePackage := regexp.MustCompile(`^package\s+(\w+)`)
	reExport := regexp.MustCompile(`^// ([A-Z]\w*) (?:does|is|represents)`)

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if m := rePackage.FindStringSubmatch(line); m != nil && pkgName == "" {
				pkgName = m[1]
			}
			if m := reExport.FindStringSubmatch(line); m != nil {
				exports = append(exports, m[1])
			}
		}
	}

	if pkgName == "" {
		pkgName = filepath.Base(target)
	}

	var readme strings.Builder
	readme.WriteString(fmt.Sprintf("# %s\n\n", pkgName))
	readme.WriteString("## Overview\n\n")
	readme.WriteString(fmt.Sprintf("%s provides ...\n\n", pkgName))
	readme.WriteString(fmt.Sprintf("## Installation\n\n```bash\ngo get %s\n```\n\n", pkgName))
	readme.WriteString(fmt.Sprintf("## Usage\n\n```go\nimport \"%s\"\n```\n\n", pkgName))

	if len(exports) > 0 {
		readme.WriteString("## API\n\n")
		for _, e := range exports {
			readme.WriteString(fmt.Sprintf("### %s\n\n", e))
			readme.WriteString("Description...\n\n")
		}
	}

	readme.WriteString("## Examples\n\n")
	readme.WriteString("```go\n// TODO: Add example code\n```\n\n")

	readme.WriteString("## License\n\nMIT\n")

	return readme.String(), nil
}

func (t *DocGenerateTool) generateChangelog(target string) (string, error) {
	var readme strings.Builder
	readme.WriteString("# Changelog\n\n")
	readme.WriteString("All notable changes to this project will be documented in this file.\n\n")
	readme.WriteString("The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),\n")
	readme.WriteString("and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).\n\n")
	readme.WriteString("## [Unreleased]\n\n### Added\n- \n\n### Changed\n- \n\n### Deprecated\n- \n\n### Removed\n- \n\n### Fixed\n- \n\n### Security\n- \n\n## [0.1.0] - YYYY-MM-DD\n\n### Added\n- Initial release\n")
	return readme.String(), nil
}
