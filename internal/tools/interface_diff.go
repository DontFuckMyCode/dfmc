// interface_diff.go — API/Interface change impact analyzer.
// Detects breaking changes in function signatures, interface contracts,
// and provides migration guidance.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// InterfaceDiffTool analyzes interface and function signature changes.
// It detects breaking changes and provides migration guidance.
type InterfaceDiffTool struct{}

func NewInterfaceDiffTool() *InterfaceDiffTool { return &InterfaceDiffTool{} }
func (t *InterfaceDiffTool) Name() string      { return "interface_diff" }
func (t *InterfaceDiffTool) Description() string {
	return "Analyze API/interface changes and their impact on callers. " +
		"Supports: function signatures, interface contracts, struct fields."
}
func (t *InterfaceDiffTool) SetEngine(_ *Engine) {}
func (t *InterfaceDiffTool) Risk() Risk          { return RiskRead }
func (t *InterfaceDiffTool) Cacheable() bool     { return false }

// Request parameters
type InterfaceDiffRequest struct {
	BasePath    string `json:"base_path"` // Base version file/directory
	HeadPath    string `json:"head_path"` // Head version file/directory
	Target      string `json:"target"`    // Specific interface/function to analyze
	Kind        string `json:"kind"`      // function, interface, struct, all
	ProjectRoot string `json:"project_root"`
}

type Change struct {
	Kind      string `json:"kind"` // added, removed, modified
	Type      string `json:"type"` // function, field, method, type
	Name      string `json:"name"`
	Location  string `json:"location"` // file:line
	Severity  string `json:"severity"` // breaking, warning, info
	Message   string `json:"message"`
	Migration string `json:"migration"` // Suggested fix
}

type Impact struct {
	Symbol      string   `json:"symbol"`
	Kind        string   `json:"kind"`
	File        string   `json:"file"`
	Callers     int      `json:"callers"`
	CallersList []string `json:"callers_list"`
}

type InterfaceDiffResult struct {
	Summary struct {
		TotalChanges int `json:"total_changes"`
		Breaking     int `json:"breaking"`
		Warnings     int `json:"warnings"`
		Infos        int `json:"infos"`
	} `json:"summary"`
	Changes []Change `json:"changes"`
	Impacts []Impact `json:"impacts"`
}

func (t *InterfaceDiffTool) Execute(ctx context.Context, req Request) (Result, error) {
	basePath := asString(req.Params, "base_path", "")
	headPath := asString(req.Params, "head_path", "")
	target := asString(req.Params, "target", "")
	kind := asString(req.Params, "kind", "all")
	projectRoot := req.ProjectRoot

	if basePath == "" && headPath == "" {
		return Result{}, fmt.Errorf("interface_diff requires base_path or head_path")
	}

	var changes []Change
	var impacts []Impact

	// Parse files
	baseItems := parseInterfaceItems(basePath)
	headItems := parseInterfaceItems(headPath)

	// Compare
	changes = compareInterfaces(baseItems, headItems, target)

	// Analyze impacts
	if projectRoot != "" {
		impacts = analyzeImpact(projectRoot, changes, kind)
	}

	result := InterfaceDiffResult{}
	result.Summary.TotalChanges = len(changes)
	for _, c := range changes {
		switch c.Severity {
		case "breaking":
			result.Summary.Breaking++
		case "warning":
			result.Summary.Warnings++
		default:
			result.Summary.Infos++
		}
	}
	result.Changes = changes
	result.Impacts = impacts

	data, _ := json.MarshalIndent(result, "", "  ")
	return Result{Output: string(data)}, nil
}

// SymbolInfo holds parsed interface/function metadata
type SymbolInfo struct {
	Name       string
	Kind       string // function, interface, struct
	File       string
	Line       int
	Signature  string
	Methods    []string // for interfaces
	Fields     []string // for structs
	IsExported bool
}

var (
	reFuncSig   = regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(?:\(([^)]*)\))?`)
	reInterface = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+interface\s*\{`)
	reStruct    = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+struct\s*\{`)
	reMethod    = regexp.MustCompile(`^\s+([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(?:\(([^)]*)\))?`)
)

func parseInterfaceItems(path string) []SymbolInfo {
	if path == "" {
		return nil
	}

	var items []SymbolInfo
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil
	}

	var files []string
	if info.IsDir() {
		filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
	} else {
		files = []string{absPath}
	}

	for _, f := range files {
		items = append(items, parseFileInterfaces(f)...)
	}

	return items
}

func parseFileInterfaces(file string) []SymbolInfo {
	var items []SymbolInfo
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var currentType string
	var currentKind string
	var inBlock bool

	for i, line := range lines {
		if m := reInterface.FindStringSubmatch(line); m != nil {
			currentType = m[1]
			currentKind = "interface"
			inBlock = true
			items = append(items, SymbolInfo{
				Name:       m[1],
				Kind:       "interface",
				File:       file,
				Line:       i + 1,
				IsExported: len(m[1]) > 0 && m[1][0] >= 'A' && m[1][0] <= 'Z',
			})
			continue
		}

		if m := reStruct.FindStringSubmatch(line); m != nil {
			currentType = m[1]
			currentKind = "struct"
			inBlock = true
			items = append(items, SymbolInfo{
				Name:       m[1],
				Kind:       "struct",
				File:       file,
				Line:       i + 1,
				IsExported: len(m[1]) > 0 && m[1][0] >= 'A' && m[1][0] <= 'Z',
			})
			continue
		}

		if strings.TrimSpace(line) == "}" && inBlock {
			inBlock = false
			currentType = ""
			continue
		}

		if inBlock && currentKind == "interface" {
			if m := reMethod.FindStringSubmatch(line); m != nil {
				idx := len(items) - 1
				if idx >= 0 {
					items[idx].Methods = append(items[idx].Methods, m[1])
					items[idx].Signature = line
				}
			}
		}

		if !inBlock {
			if m := reFuncSig.FindStringSubmatch(line); m != nil && currentType == "" {
				items = append(items, SymbolInfo{
					Name:       m[1],
					Kind:       "function",
					File:       file,
					Line:       i + 1,
					Signature:  strings.TrimSpace(line),
					IsExported: len(m[1]) > 0 && m[1][0] >= 'A' && m[1][0] <= 'Z',
				})
			}
		}
	}

	return items
}

func compareInterfaces(base, head []SymbolInfo, target string) []Change {
	var changes []Change

	baseMap := make(map[string]SymbolInfo)
	for _, b := range base {
		if target == "" || b.Name == target || b.Kind == target {
			baseMap[b.Name] = b
		}
	}

	headMap := make(map[string]SymbolInfo)
	for _, h := range head {
		if target == "" || h.Name == target || h.Kind == target {
			headMap[h.Name] = h
		}
	}

	// Find removed items
	for name, b := range baseMap {
		if h, exists := headMap[name]; exists {
			// Modified
			if b.Signature != h.Signature && b.Signature != "" && h.Signature != "" {
				changes = append(changes, Change{
					Kind:      "modified",
					Type:      b.Kind,
					Name:      name,
					Location:  h.File,
					Severity:  "breaking",
					Message:   fmt.Sprintf("Signature changed: %s -> %s", b.Signature, h.Signature),
					Migration: fmt.Sprintf("Update all callers of %s to match new signature", name),
				})
			}
			// Method changes for interfaces
			if b.Kind == "interface" && len(b.Methods) != len(h.Methods) {
				removed := diffSlices(b.Methods, h.Methods)
				for _, m := range removed {
					changes = append(changes, Change{
						Kind:      "modified",
						Type:      "interface",
						Name:      name,
						Location:  h.File,
						Severity:  "breaking",
						Message:   fmt.Sprintf("Method %s removed from interface", m),
						Migration: fmt.Sprintf("Implement %s or update interface consumers", m),
					})
				}
			}
		} else {
			// Removed
			changes = append(changes, Change{
				Kind:      "removed",
				Type:      b.Kind,
				Name:      name,
				Location:  b.File,
				Severity:  "breaking",
				Message:   fmt.Sprintf("%s %s removed", b.Kind, name),
				Migration: fmt.Sprintf("Remove all references to %s or implement replacement", name),
			})
		}
	}

	// Find added items
	for name, h := range headMap {
		if _, exists := baseMap[name]; !exists {
			changes = append(changes, Change{
				Kind:      "added",
				Type:      h.Kind,
				Name:      name,
				Location:  h.File,
				Severity:  "info",
				Message:   fmt.Sprintf("%s %s added", h.Kind, name),
				Migration: "Update consumers if needed",
			})
		}
	}

	// Sort: breaking first
	sort.Slice(changes, func(i, j int) bool {
		order := map[string]int{"breaking": 0, "warning": 1, "info": 2}
		return order[changes[i].Severity] < order[changes[j].Severity]
	})

	return changes
}

func diffSlices(a, b []string) []string {
	bMap := make(map[string]bool)
	for _, s := range b {
		bMap[s] = true
	}
	var diff []string
	for _, s := range a {
		if !bMap[s] {
			diff = append(diff, s)
		}
	}
	return diff
}

func analyzeImpact(projectRoot string, changes []Change, kind string) []Impact {
	var impacts []Impact

	// This would use call_graph or ast_query to find actual callers
	// For now, return empty list as placeholders
	// Real implementation would:
	// 1. Find all exported symbols that changed
	// 2. Use call_graph to find references
	// 3. Count and list callers

	return impacts
}
