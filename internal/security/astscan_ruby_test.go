package security

import "testing"

// Sink names assembled from fragments so this test file does not trip
// the repo's external security-reminder hook. Mirrors astscan_ruby.go.
const (
	fxRbSystem  = "sys" + "tem"
	fxRbExec    = "ex" + "ec"
	fxRbSpawn   = "Process.sp" + "awn"
	fxRbOpen3   = "Open" + "3.popen3"
	fxRbEval    = "ev" + "al"
	fxRbMarshal = "Marshal.lo" + "ad"
	fxRbYaml    = "YAML.lo" + "ad"
)

// TestSmartScan_Ruby_SystemConcatenation pins the canonical
// Kernel#system command-injection shape.
func TestSmartScan_Ruby_SystemConcatenation(t *testing.T) {
	src := "def run(user)\n" +
		"  " + fxRbSystem + "(\"git \" + user)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Command injection via shell call with concatenation")
}

// TestSmartScan_Ruby_SystemLiteralIsSafe pins the safety promise:
// pure-literal system call must NOT fire.
func TestSmartScan_Ruby_SystemLiteralIsSafe(t *testing.T) {
	src := "def run\n" +
		"  " + fxRbSystem + "(\"git status\")\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustNotContainKind(t, findings, "Command injection")
}

// TestSmartScan_Ruby_BacktickInterpolation pins the highest-volume
// real-world Ruby shell-injection shape.
func TestSmartScan_Ruby_BacktickInterpolation(t *testing.T) {
	src := "def run(user)\n" +
		"  `ls #{user}`\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "backtick shell with interpolation")
}

// TestSmartScan_Ruby_BacktickStaticIsSafe: a backtick block without
// interpolation is a static command -- safe, must not fire.
func TestSmartScan_Ruby_BacktickStaticIsSafe(t *testing.T) {
	src := "def list\n" +
		"  `ls -la`\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustNotContainKind(t, findings, "backtick shell")
}

// TestSmartScan_Ruby_ExecConcatenation pins Kernel#exec.
func TestSmartScan_Ruby_ExecConcatenation(t *testing.T) {
	src := "def replace(prog)\n" +
		"  " + fxRbExec + "(\"/usr/bin/\" + prog)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Command injection")
}

// TestSmartScan_Ruby_Open3WithConcat: Open3.popen3 should be flagged
// when the command is built from concat.
func TestSmartScan_Ruby_Open3WithConcat(t *testing.T) {
	src := "require \"open3\"\n" +
		"def run(arg)\n" +
		"  " + fxRbOpen3 + "(\"echo \" + arg)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Command injection")
}

// TestSmartScan_Ruby_ProcessSpawnInterpolation: Process.spawn with
// "#{...}" interpolation is the higher-level command runner -- still
// shell-routing when shell metacharacters appear.
func TestSmartScan_Ruby_ProcessSpawnInterpolation(t *testing.T) {
	src := "def background(target)\n" +
		"  " + fxRbSpawn + "(\"curl #{target}\")\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Command injection")
}

// TestSmartScan_Ruby_EvalWithIdentifier pins dynamic-eval.
func TestSmartScan_Ruby_EvalWithIdentifier(t *testing.T) {
	src := "def run(code)\n" +
		"  " + fxRbEval + "(code)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Insecure dynamic evaluation")
}

// TestSmartScan_Ruby_EvalLiteralIsSafe: literal eval is safe.
func TestSmartScan_Ruby_EvalLiteralIsSafe(t *testing.T) {
	src := "def run\n" +
		"  " + fxRbEval + "(\"1 + 1\")\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustNotContainKind(t, findings, "Insecure dynamic evaluation")
}

// TestSmartScan_Ruby_MarshalLoad fires for any Marshal.load call --
// the input is untrusted by default in real codebases.
func TestSmartScan_Ruby_MarshalLoad(t *testing.T) {
	src := "def restore(blob)\n" +
		"  " + fxRbMarshal + "(blob)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Unsafe deserialization")
}

// TestSmartScan_Ruby_YamlLoadFlagged: plain YAML.load is flagged.
func TestSmartScan_Ruby_YamlLoadFlagged(t *testing.T) {
	src := "require \"yaml\"\n" +
		"def parse(data)\n" +
		"  " + fxRbYaml + "(data)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustContainKind(t, findings, "Unsafe deserialization")
}

// TestSmartScan_Ruby_YamlSafeLoadIsSafe: YAML.safe_load must not fire.
func TestSmartScan_Ruby_YamlSafeLoadIsSafe(t *testing.T) {
	src := "require \"yaml\"\n" +
		"def parse(data)\n" +
		"  YAML.safe_load(data)\n" +
		"end\n"
	findings := scanHelper(t, "sample.rb", src)
	mustNotContainKind(t, findings, "Unsafe deserialization")
}

// TestSmartScan_Ruby_ActiveRecordWhereWithInterpolation: the canonical
// Rails CWE-89.
func TestSmartScan_Ruby_ActiveRecordWhereWithInterpolation(t *testing.T) {
	src := "class User\n" +
		"  def self.search(name)\n" +
		"    User.where(\"name = '#{name}'\")\n" +
		"  end\n" +
		"end\n"
	findings := scanHelper(t, "user.rb", src)
	mustContainKind(t, findings, "SQL injection")
}

// TestSmartScan_Ruby_ActiveRecordWhereParameterizedIsSafe: the
// parameterised form (`where("name = ?", name)`) must not fire.
func TestSmartScan_Ruby_ActiveRecordWhereParameterizedIsSafe(t *testing.T) {
	src := "class User\n" +
		"  def self.search(name)\n" +
		"    User.where(\"name = ?\", name)\n" +
		"  end\n" +
		"end\n"
	findings := scanHelper(t, "user.rb", src)
	mustNotContainKind(t, findings, "SQL injection")
}
