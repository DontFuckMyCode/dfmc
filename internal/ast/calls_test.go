package ast

import (
	"reflect"
	"sort"
	"testing"
)

// --- Go --------------------------------------------------------------

func TestExtractCalls_GoBasic(t *testing.T) {
	src := []byte(`package main

import "fmt"

func main() {
	fmt.Println("hello")
	greet("world")
	x := compute(1, 2)
	_ = x
}

func greet(name string) {
	fmt.Printf("hi %s\n", name)
}

func compute(a, b int) int {
	return a + b
}
`)
	got := ExtractCalls("go", src)
	want := []Call{
		{Callee: "fmt.Println", Line: 6},
		{Callee: "greet", Line: 7},
		{Callee: "compute", Line: 8},
		{Callee: "fmt.Printf", Line: 13},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_GoIgnoresControlFlow(t *testing.T) {
	src := []byte(`package main
func main() {
	if check(x) {
		for i := range items() {
			defer close(ch)
		}
	}
	return wrap(err)
}
`)
	got := ExtractCalls("go", src)
	want := []Call{
		{Callee: "check", Line: 3},
		{Callee: "items", Line: 4},
		{Callee: "close", Line: 5},
		{Callee: "wrap", Line: 8},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_GoIgnoresDeclarationLine(t *testing.T) {
	src := []byte(`package main
func myFunc(a int, b string) {
	return
}
func main() {
	myFunc(1, "x")
}
`)
	got := ExtractCalls("go", src)
	want := []Call{
		{Callee: "myFunc", Line: 6},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_GoIgnoresLineComment(t *testing.T) {
	src := []byte(`package main
func main() {
	// notACall(1, 2)
	actuallyCall()
}
`)
	got := ExtractCalls("go", src)
	want := []Call{
		{Callee: "actuallyCall", Line: 4},
	}
	assertCalls(t, want, got)
}

// --- Python ----------------------------------------------------------

func TestExtractCalls_PythonBasic(t *testing.T) {
	src := []byte(`import os

def main():
    print("hello")
    greet("world")
    p = os.path.join("/", "a")
    return p
`)
	got := ExtractCalls("python", src)
	want := []Call{
		{Callee: "print", Line: 4},
		{Callee: "greet", Line: 5},
		{Callee: "os.path.join", Line: 6},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_PythonIgnoresKeywords(t *testing.T) {
	src := []byte(`def main():
    if check(x):
        for i in range(10):
            yield wrap(i)
    return None
`)
	got := ExtractCalls("python", src)
	want := []Call{
		{Callee: "check", Line: 2},
		{Callee: "range", Line: 3},
		{Callee: "wrap", Line: 4},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_PythonIgnoresDef(t *testing.T) {
	src := []byte(`def my_func(a, b):
    return a + b

def main():
    my_func(1, 2)
`)
	got := ExtractCalls("python", src)
	want := []Call{
		{Callee: "my_func", Line: 5},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_PythonAsyncDef(t *testing.T) {
	src := []byte(`async def fetch(url):
    return await get(url)

async def main():
    await fetch("x")
`)
	got := ExtractCalls("python", src)
	want := []Call{
		{Callee: "get", Line: 2},
		{Callee: "fetch", Line: 5},
	}
	assertCalls(t, want, got)
}

// --- JavaScript / TypeScript ----------------------------------------

func TestExtractCalls_JSBasic(t *testing.T) {
	src := []byte(`function main() {
  console.log("hello");
  greet("world");
  const x = fs.readFile("/tmp/x");
  return x;
}
`)
	got := ExtractCalls("javascript", src)
	want := []Call{
		{Callee: "console.log", Line: 2},
		{Callee: "greet", Line: 3},
		{Callee: "fs.readFile", Line: 4},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_JSIgnoresControlFlow(t *testing.T) {
	src := []byte(`function main() {
  if (check(x)) {
    for (const i of items()) {
      doThing(i);
    }
  }
  return wrap(err);
}
`)
	got := ExtractCalls("javascript", src)
	want := []Call{
		{Callee: "check", Line: 2},
		{Callee: "items", Line: 3},
		{Callee: "doThing", Line: 4},
		{Callee: "wrap", Line: 7},
	}
	assertCalls(t, want, got)
}

func TestExtractCalls_JSArrowFunctionsAndAwait(t *testing.T) {
	src := []byte(`const fetch = async (url) => {
  const res = await get(url);
  return res.json();
};
`)
	got := ExtractCalls("javascript", src)
	want := []Call{
		{Callee: "get", Line: 2},
		{Callee: "res.json", Line: 3},
	}
	assertCalls(t, want, got)
}

// --- Unsupported language ------------------------------------------

func TestExtractCalls_UnsupportedReturnsNil(t *testing.T) {
	src := []byte(`fn main() { foo(); }`)
	got := ExtractCalls("rust", src)
	if got != nil {
		t.Fatalf("rust is unsupported in phase 1; expected nil, got %v", got)
	}
}

// --- helpers --------------------------------------------------------

func assertCalls(t *testing.T, want, got []Call) {
	t.Helper()
	sortFn := func(cs []Call) {
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].Line != cs[j].Line {
				return cs[i].Line < cs[j].Line
			}
			return cs[i].Callee < cs[j].Callee
		})
	}
	w := append([]Call(nil), want...)
	g := append([]Call(nil), got...)
	sortFn(w)
	sortFn(g)
	if !reflect.DeepEqual(w, g) {
		t.Errorf("calls mismatch:\nwant: %#v\ngot:  %#v", w, g)
	}
}
