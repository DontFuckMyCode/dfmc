package ast

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// --- Python alias extraction --------------------------------------------

func TestImportAliases_PythonImportShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []ImportAlias
	}{
		{
			name: "plain import",
			line: "import os",
			want: []ImportAlias{{Module: "os", Symbol: "", Local: "os"}},
		},
		{
			name: "import with alias",
			line: "import numpy as np",
			want: []ImportAlias{{Module: "numpy", Symbol: "", Local: "np"}},
		},
		{
			name: "from import single",
			line: "from os import path",
			want: []ImportAlias{{Module: "os", Symbol: "path", Local: "path"}},
		},
		{
			name: "from import alias",
			line: "from os import path as p",
			want: []ImportAlias{{Module: "os", Symbol: "path", Local: "p"}},
		},
		{
			name: "from import multiple with alias",
			line: "from os import path as p, sep",
			want: []ImportAlias{
				{Module: "os", Symbol: "path", Local: "p"},
				{Module: "os", Symbol: "sep", Local: "sep"},
			},
		},
		{
			name: "import comma list",
			line: "import os, sys",
			want: []ImportAlias{
				{Module: "os", Symbol: "", Local: "os"},
				{Module: "sys", Symbol: "", Local: "sys"},
			},
		},
		{
			name: "from import dotted module",
			line: "from xml.etree import ElementTree as ET",
			want: []ImportAlias{
				{Module: "xml.etree", Symbol: "ElementTree", Local: "ET"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractPythonImportAliases([]string{c.line})
			assertAliasesEqual(t, c.want, got)
		})
	}
}

// --- JS / TS alias extraction -------------------------------------------

func TestImportAliases_JSESMShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []ImportAlias
	}{
		{
			name: "default import",
			line: `import React from "react";`,
			want: []ImportAlias{{Module: "react", Symbol: "default", Local: "React"}},
		},
		{
			name: "namespace import",
			line: `import * as fs from "fs";`,
			want: []ImportAlias{{Module: "fs", Symbol: "*", Local: "fs"}},
		},
		{
			name: "named imports",
			line: `import { readFile, writeFile } from "fs/promises";`,
			want: []ImportAlias{
				{Module: "fs/promises", Symbol: "readFile", Local: "readFile"},
				{Module: "fs/promises", Symbol: "writeFile", Local: "writeFile"},
			},
		},
		{
			name: "named import with rename",
			line: `import { foo as bar } from "pkg";`,
			want: []ImportAlias{{Module: "pkg", Symbol: "foo", Local: "bar"}},
		},
		{
			name: "default + named combo",
			line: `import X, { a, b as c } from "pkg";`,
			want: []ImportAlias{
				{Module: "pkg", Symbol: "default", Local: "X"},
				{Module: "pkg", Symbol: "a", Local: "a"},
				{Module: "pkg", Symbol: "b", Local: "c"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractJSImportAliases([]string{c.line})
			assertAliasesEqual(t, c.want, got)
		})
	}
}

func TestImportAliases_JSRequireShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []ImportAlias
	}{
		{
			name: "whole-module require",
			line: `const fs = require("fs");`,
			want: []ImportAlias{{Module: "fs", Symbol: "", Local: "fs"}},
		},
		{
			name: "destructured require",
			line: `const { readFile, writeFile } = require("fs");`,
			want: []ImportAlias{
				{Module: "fs", Symbol: "readFile", Local: "readFile"},
				{Module: "fs", Symbol: "writeFile", Local: "writeFile"},
			},
		},
		{
			name: "destructured require with rename",
			line: `const { readFile: rf } = require("fs");`,
			want: []ImportAlias{{Module: "fs", Symbol: "readFile", Local: "rf"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractJSImportAliases([]string{c.line})
			assertAliasesEqual(t, c.want, got)
		})
	}
}

// --- Rust alias extraction ----------------------------------------------

func TestImportAliases_RustUseShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []ImportAlias
	}{
		{
			name: "use single segment",
			line: "use foo;",
			want: []ImportAlias{{Module: "", Symbol: "foo", Local: "foo"}},
		},
		{
			name: "use path",
			line: "use foo::bar;",
			want: []ImportAlias{{Module: "foo", Symbol: "bar", Local: "bar"}},
		},
		{
			name: "use path with rename",
			line: "use foo::bar as baz;",
			want: []ImportAlias{{Module: "foo", Symbol: "bar", Local: "baz"}},
		},
		{
			name: "use deep path",
			line: "use std::collections::HashMap;",
			want: []ImportAlias{{Module: "std::collections", Symbol: "HashMap", Local: "HashMap"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractRustImportAliases([]string{c.line})
			assertAliasesEqual(t, c.want, got)
		})
	}
}

// --- Go alias extraction -----------------------------------------------

func TestImportAliases_GoImportShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []ImportAlias
	}{
		{
			name: "single import",
			src:  `import "fmt"`,
			want: []ImportAlias{{Module: "fmt", Symbol: "", Local: "fmt"}},
		},
		{
			name: "single import with alias",
			src:  `import f "fmt"`,
			want: []ImportAlias{{Module: "fmt", Symbol: "", Local: "f"}},
		},
		{
			name: "blank import",
			src:  `import _ "github.com/lib/pq"`,
			want: []ImportAlias{{Module: "github.com/lib/pq", Symbol: "", Local: "_"}},
		},
		{
			name: "dot import",
			src:  `import . "math"`,
			want: []ImportAlias{{Module: "math", Symbol: "", Local: "."}},
		},
		{
			name: "block import",
			src: `import (
	"fmt"
	"os"
	custom "github.com/foo/bar"
)`,
			want: []ImportAlias{
				{Module: "fmt", Symbol: "", Local: "fmt"},
				{Module: "os", Symbol: "", Local: "os"},
				{Module: "github.com/foo/bar", Symbol: "", Local: "custom"},
			},
		},
		{
			name: "import path implies last-segment local",
			src:  `import "github.com/foo/bar/baz"`,
			want: []ImportAlias{{Module: "github.com/foo/bar/baz", Symbol: "", Local: "baz"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractGoImportAliases(splitLines(c.src))
			assertAliasesEqual(t, c.want, got)
		})
	}
}

// --- End-to-end through ParseContent -----------------------------------

func TestImportAliases_PopulatedByParseContent(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`from os import path as p
import numpy as np

def f():
    return p.join("/", "a")
`)

	res, err := eng.ParseContent(context.Background(), "x.py", src)
	if err != nil {
		t.Fatalf("ParseContent error: %v", err)
	}
	want := []ImportAlias{
		{Module: "os", Symbol: "path", Local: "p"},
		{Module: "numpy", Symbol: "", Local: "np"},
	}
	assertAliasesEqual(t, want, res.ImportAliases)
}

// assertAliasesEqual compares two ImportAlias slices ignoring source
// order -- the regex walker is line-by-line so order matches input
// in practice, but the test contract is membership, not ordering.
func assertAliasesEqual(t *testing.T, want, got []ImportAlias) {
	t.Helper()
	sortFn := func(s []ImportAlias) {
		sort.SliceStable(s, func(i, j int) bool {
			if s[i].Module != s[j].Module {
				return s[i].Module < s[j].Module
			}
			if s[i].Symbol != s[j].Symbol {
				return s[i].Symbol < s[j].Symbol
			}
			return s[i].Local < s[j].Local
		})
	}
	wantCopy := append([]ImportAlias(nil), want...)
	gotCopy := append([]ImportAlias(nil), got...)
	sortFn(wantCopy)
	sortFn(gotCopy)
	if !reflect.DeepEqual(wantCopy, gotCopy) {
		t.Errorf("aliases mismatch:\nwant: %#v\ngot:  %#v", wantCopy, gotCopy)
	}
}

func splitLines(s string) []string {
	return splitOnNewline(s)
}

func splitOnNewline(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}
