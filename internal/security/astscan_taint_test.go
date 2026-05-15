package security

import (
	"testing"
)

// --- Taint tracker unit tests --------------------------------------------

func TestTaintTracker_GoSources(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		want    string // identifier expected tainted; "" means none
		notWant string // identifier expected NOT tainted; "" skips check
	}{
		{"http body assign", "body, _ := io.ReadAll(r.Body)", "body", ""},
		{"req body short", "data := req.Body", "data", ""},
		{"request body explicit", "raw := request.Body", "raw", ""},
		{"form value", "v := r.Form.Get(\"q\")", "v", ""},
		{"post form", "m := req.PostForm", "m", ""},
		{"url query", "q := r.URL.Query()", "q", ""},
		{"url path", "p := r.URL.Path", "p", ""},
		{"header get", "h := r.Header.Get(\"X-Trace\")", "h", ""},
		{"os args", "args := os.Args", "args", ""},
		{"os stdin", "in := os.Stdin", "in", ""},
		{"flag string", "name := flag.String(\"n\", \"\", \"\")", "name", ""},
		// Negative: assignment from a string literal must NOT taint.
		{"literal not tainted", "body := \"safe\"", "", "body"},
		// Negative: unrelated struct that happens to have .Body must
		// not match the http-request source (we anchor on r/req/request).
		{"unrelated struct body", "x := response.Body", "", "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := newTaintTracker("go")
			tr.observeLine(c.line)
			if c.want != "" && !tr.IsTainted(c.want) {
				t.Fatalf("expected %q tainted after observe(%q); tainted=%v",
					c.want, c.line, tr.tainted)
			}
			if c.notWant != "" && tr.IsTainted(c.notWant) {
				t.Fatalf("did NOT expect %q tainted after observe(%q); tainted=%v",
					c.notWant, c.line, tr.tainted)
			}
		})
	}
}

func TestTaintTracker_GoPropagation(t *testing.T) {
	tr := newTaintTracker("go")
	tr.observeLine("body, _ := io.ReadAll(r.Body)") // body tainted
	tr.observeLine("s := string(body)")             // s should inherit
	tr.observeLine("u := strings.ToLower(s)")       // u should inherit
	tr.observeLine("safe := \"static\"")            // safe must NOT be tainted

	if !tr.IsTainted("body") {
		t.Error("body must be tainted (direct source)")
	}
	if !tr.IsTainted("s") {
		t.Error("s must be tainted via string(body) propagation")
	}
	if !tr.IsTainted("u") {
		t.Error("u must be tainted via strings.ToLower(s) propagation")
	}
	if tr.IsTainted("safe") {
		t.Error("safe must NOT be tainted; assignment is a literal")
	}
}

func TestTaintTracker_NilSafeIsTainted(t *testing.T) {
	var tr *taintTracker
	if tr.IsTainted("anything") {
		t.Fatal("nil tracker must return false")
	}
}

// --- End-to-end rule integration -----------------------------------------

// TestSmartScan_Go_ExecCommandTaintedBody pins the new R1 capability:
// a multi-step body -> sink flow that no concat-only rule could see is
// caught by the taint-aware exec.Command rule.
func TestSmartScan_Go_ExecCommandTaintedBody(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run(r *http.Request) {\n" +
		"	body, _ := io.ReadAll(r.Body)\n" +
		"	s := string(body)\n" +
		"	cmd := " + fxExecCmd + "(s)\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustContainKind(t, findings, "Command injection via exec.Command with tainted input")
}

// TestSmartScan_Go_ExecCommandLiteralVarStaysSafe pins the negative
// case: a variable assigned from a literal is NOT tainted, so feeding
// it to exec.Command must not fire the taint-aware rule.
func TestSmartScan_Go_ExecCommandLiteralVarStaysSafe(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run() {\n" +
		"	bin := \"git\"\n" +
		"	cmd := " + fxExecCmd + "(bin, \"diff\")\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustNotContainKind(t, findings, "tainted input")
}

// TestSmartScan_Go_ExecCommandPlainLiteralUnchanged guards against
// regressions where introducing the taint pass would somehow change
// the existing "all literals are safe" behaviour. The TUI git-diff
// helper relies on this -- a fresh taint tracker with no observed
// sources must leave literal calls untouched.
func TestSmartScan_Go_ExecCommandPlainLiteralUnchanged(t *testing.T) {
	src := "package main\nimport \"os/exec\"\n" +
		"func run() {\n" +
		"	cmd := " + fxExecCmd + "(\"git\", \"-C\", \"/some/path\", \"diff\", \"--\")\n" +
		"	_ = cmd\n}\n"
	findings := scanHelper(t, "sample.go", src)
	mustNotContainKind(t, findings, "tainted input")
	mustNotContainKind(t, findings, "Command injection")
}

// TestSmartScan_Go_ExecCommandWithUserArgsDirect pins the simplest
// shape: r.Body -> exec.Command on the next line, no intermediate
// propagation step. Should still flag.
func TestSmartScan_Go_ExecCommandWithUserArgsDirect(t *testing.T) {
	src := "package main\n" +
		"import \"os/exec\"\n" +
		"func run(r *http.Request) {\n" +
		"	body := r.Body\n" +
		"	cmd := " + fxExecCmd + "(body)\n" +
		"	_ = cmd\n" +
		"}\n"
	findings := scanHelper(t, "sample.go", src)
	mustContainKind(t, findings, "tainted input")
}

// --- Python taint tracker -----------------------------------------------

func TestTaintTracker_PythonSources(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		want    string
		notWant string
	}{
		{"sys.argv index", "user = sys.argv[1]", "user", ""},
		{"input call", "name = input(\"who? \")", "name", ""},
		{"sys.stdin read", "data = sys.stdin.read()", "data", ""},
		{"flask args get", "q = request.args.get(\"q\")", "q", ""},
		{"flask json", "body = request.get_json()", "body", ""},
		{"django GET", "v = request.GET.get(\"v\")", "v", ""},
		{"django POST", "form = request.POST", "form", ""},
		{"os.environ", "key = os.environ[\"API_KEY\"]", "key", ""},
		{"type annotated", "user: str = sys.argv[1]", "user", ""},
		{"tuple unpack", "a, b = sys.argv[1], sys.argv[2]", "a", ""},
		// Negative: literal RHS doesn't taint.
		{"literal not tainted", "user = \"alice\"", "", "user"},
		// Negative: `==` (comparison) must NOT match assignment regex.
		{"equality compare", "if user == sys.argv[1]:", "", "user"},
		// Negative: unrelated `.body` (no `request.` prefix) must not taint.
		{"unrelated body", "x = response.body", "", "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := newTaintTracker("python")
			tr.observeLine(c.line)
			if c.want != "" && !tr.IsTainted(c.want) {
				t.Fatalf("expected %q tainted after observe(%q); tainted=%v",
					c.want, c.line, tr.tainted)
			}
			if c.notWant != "" && tr.IsTainted(c.notWant) {
				t.Fatalf("did NOT expect %q tainted after observe(%q); tainted=%v",
					c.notWant, c.line, tr.tainted)
			}
		})
	}
}

func TestTaintTracker_PythonPropagation(t *testing.T) {
	tr := newTaintTracker("python")
	tr.observeLine("user = sys.argv[1]")     // user tainted
	tr.observeLine("s = str(user)")          // s inherits
	tr.observeLine("cleaned = s.strip()")    // cleaned inherits (through s)
	tr.observeLine("safe = \"static value\"") // safe must NOT be tainted

	if !tr.IsTainted("user") {
		t.Error("user must be tainted (direct sys.argv)")
	}
	if !tr.IsTainted("s") {
		t.Error("s must inherit taint from str(user)")
	}
	if !tr.IsTainted("cleaned") {
		t.Error("cleaned must inherit taint via s.strip()")
	}
	if tr.IsTainted("safe") {
		t.Error("safe must NOT be tainted; assignment is a literal")
	}
}

// --- Python end-to-end --------------------------------------------------

const (
	fxPySubpRun  = "subp" + "rocess.run"
	fxPySubpCall = "subp" + "rocess.call"
	fxPyOsSys    = "o" + "s." + "sy" + "st" + "em"
)

// TestSmartScan_Python_SubprocessTaintedFromArgv pins the multi-step
// Python flow: command-line input -> subprocess.run.
func TestSmartScan_Python_SubprocessTaintedFromArgv(t *testing.T) {
	src := "import sys\n" +
		"import subprocess\n" +
		"def main():\n" +
		"    user = sys.argv[1]\n" +
		"    " + fxPySubpRun + "(user)\n"
	findings := scanHelper(t, "sample.py", src)
	mustContainKind(t, findings, "subprocess/shell call with tainted input")
}

// TestSmartScan_Python_OsSystemTaintedFromFlask pins the Flask shape:
// HTTP request data flowing into the host-shell wrapper.
func TestSmartScan_Python_OsSystemTaintedFromFlask(t *testing.T) {
	src := "from flask import request\n" +
		"def handler():\n" +
		"    q = request.args.get(\"q\")\n" +
		"    " + fxPyOsSys + "(q)\n"
	findings := scanHelper(t, "sample.py", src)
	mustContainKind(t, findings, "subprocess/shell call with tainted input")
}

// TestSmartScan_Python_SubprocessLiteralStaysSafe: passing a literal
// (or a variable assigned from a literal) must not fire the
// tainted-input rule.
func TestSmartScan_Python_SubprocessLiteralStaysSafe(t *testing.T) {
	src := "import subprocess\n" +
		"def run():\n" +
		"    cmd = \"ls\"\n" +
		"    " + fxPySubpCall + "(cmd)\n"
	findings := scanHelper(t, "sample.py", src)
	mustNotContainKind(t, findings, "tainted input")
}

// TestSmartScan_Python_SubprocessPropagationThroughStr ensures taint
// propagates through one level of wrapping (str(x)) and the rule
// still fires when the wrapped value lands in a sink.
func TestSmartScan_Python_SubprocessPropagationThroughStr(t *testing.T) {
	src := "import sys\n" +
		"import subprocess\n" +
		"def main():\n" +
		"    raw = sys.argv[1]\n" +
		"    s = str(raw)\n" +
		"    " + fxPySubpRun + "(s)\n"
	findings := scanHelper(t, "sample.py", src)
	mustContainKind(t, findings, "tainted input")
}
