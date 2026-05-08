// status_types_analyze.go — JSON-shaped report types returned by
// Engine.AnalyzeWithOptions and its sub-pass collaborators (dead-code,
// complexity, duplication). Sibling of status_types.go which keeps the
// Status / context / prompt / context-budget shapes.
//
// Same purity rule as the parent file: no behaviour, just field tags.
// TodoReport stays adjacent to its scanner in todos.go because the
// scanner is the single producer; everything here is consumed by every
// surface that renders the analyze report (CLI, web, TUI Analyze tab).

package engine

import (
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/security"
)

type AnalyzeReport struct {
	ProjectRoot string             `json:"project_root"`
	Files       int                `json:"files"`
	Nodes       int                `json:"nodes"`
	Edges       int                `json:"edges"`
	Cycles      int                `json:"cycles"`
	HotSpots    []codemap.Node     `json:"hotspots"`
	Security    *security.Report   `json:"security,omitempty"`
	DeadCode    []DeadCodeItem     `json:"dead_code,omitempty"`
	Complexity  *ComplexityReport  `json:"complexity,omitempty"`
	Duplication *DuplicationReport `json:"duplication,omitempty"`
	Todos       *TodoReport        `json:"todos,omitempty"`
}

type AnalyzeOptions struct {
	Path        string
	Full        bool
	Security    bool
	DeadCode    bool
	Complexity  bool
	Duplication bool
	Todos       bool
}

type DeadCodeItem struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Occurrences int    `json:"occurrences"`
}

type FunctionComplexity struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Score int    `json:"score"`
}

type ComplexityReport struct {
	Files         int                  `json:"files"`
	Average       float64              `json:"average"`
	Max           int                  `json:"max"`
	TopFunctions  []FunctionComplexity `json:"top_functions,omitempty"`
	TopFiles      []FunctionComplexity `json:"top_files,omitempty"`
	TotalSymbols  int                  `json:"total_symbols"`
	ScannedSymbol int                  `json:"scanned_symbols"`
}

// DuplicationLocation marks where one copy of a duplicate block sits.
type DuplicationLocation struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// DuplicationGroup clusters all locations that share the same
// normalized window of code. Length is the number of non-blank
// normalized lines in the window — NOT raw end-start+1, because
// blanks + comments are stripped before matching.
type DuplicationGroup struct {
	Length    int                   `json:"length"`
	Locations []DuplicationLocation `json:"locations"`
}

type DuplicationReport struct {
	MinLines        int                `json:"min_lines"`
	FilesScanned    int                `json:"files_scanned"`
	WindowsHashed   int                `json:"windows_hashed"`
	Groups          []DuplicationGroup `json:"groups,omitempty"`
	DuplicatedLines int                `json:"duplicated_lines"`
}
