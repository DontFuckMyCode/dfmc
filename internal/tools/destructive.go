// Destructive-tool classification shared by every approval surface.
// Both ui/cli/approver.go and ui/web/approver.go consult this list when
// the operator sets DFMC_APPROVE=yes — auto-approve is restricted to
// read-only / introspection tools so a leaked env var in CI can't
// silently grant a remote agent the right to write files or shell out.
//
// To opt destructive tools into the auto-approve, the operator must
// explicitly set DFMC_APPROVE_DESTRUCTIVE=yes alongside DFMC_APPROVE=yes.
// The two-knob design forces a deliberate "yes I know" rather than one
// blanket switch.

package tools

import "strings"

// destructiveTools enumerates every built-in tool that mutates the
// filesystem, the process state outside DFMC's own DB, or escalates
// privileges by running arbitrary commands. Keep this list authoritative
// — any new built-in that does the above must be added here AND given
// an explicit destructive=true flag in its ToolSpec when that field
// lands. Sub-agents (delegate_task) are flagged because the delegated
// loop can in turn invoke any tool; gating delegation under the same
// switch as the underlying writes keeps the threat model coherent.
//
// web_fetch is included even though it neither writes the local FS
// nor shells out: prompt-injection from a malicious file in the
// scanned project can chain read_file → web_fetch to exfiltrate
// `~/.ssh/id_rsa` (or similar) as a query string to an
// attacker-controlled URL. Classifying it destructive forces operators
// running `DFMC_APPROVE=yes` to set DFMC_APPROVE_DESTRUCTIVE=yes as
// the deliberate "yes, the agent may make arbitrary outbound HTTPS
// calls without prompting" override. The egress allowlist in
// cfg.WebFetch.AllowedHosts is the orthogonal defense-in-depth —
// when set, web_fetch refuses non-allowlisted hosts even before
// approval is consulted.
var destructiveTools = map[string]struct{}{
	"write_file":    {},
	"edit_file":     {},
	"apply_patch":   {},
	"run_command":   {},
	"delegate_task": {},
	"web_fetch":     {},
}

// IsDestructive reports whether the named tool is on the destructive
// list. Case-folded so providers that normalise to upper-case (rare,
// but observed) still hit the gate.
func IsDestructive(name string) bool {
	_, ok := destructiveTools[strings.ToLower(strings.TrimSpace(name))]
	return ok
}
