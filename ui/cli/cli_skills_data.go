// Builtin skill definitions. Each entry has a Name/Description/Prompt
// template seeded by the review/explain/refactor/test/doc/... shortcuts.
// Extracted from cli_plugin_skill.go — pure static data with no logic,
// lives alongside runSkill in the same package.

package cli

func builtinSkills() []skillInfo {
	return []skillInfo{
		{
			Name:        "review",
			Description: "Code review: correctness, risk, missing tests, security smells",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the REVIEW skill. Review the changed code for correctness, risk, and test coverage — not style nits.

Playbook:
1. Scope. Identify exactly what changed: use git_diff / read_file on the target paths. If the target is "recent changes" with no path, read git_log and diff the most recent non-trivial commit. For a suspect line in a long file, git_blame on a narrow line range tells you which commit shaped it — useful when the change touches code with a non-obvious history.
2. Correctness. For each change, answer: does it do what the commit message / PR claims? Trace the happy path AND at least one error path. Name any branch that's unreachable or any condition that's always true/false.
3. Behavioral risk. Look for changes that quietly alter: public API, on-disk format, error types, side-effect ordering, concurrency semantics, allocation patterns. Flag each with file:line and the exact risk.
4. Tests. Check whether the changed code is exercised by an existing test. If not, say which test SHOULD exist (name it) and why the gap matters. Do not demand tests for trivial code.
5. Security + resource. Check for path traversal, unbounded allocation, unchecked user input, credentials in plaintext, SQL/command injection, missing ctx cancellation, leaked goroutines. Only flag real findings — stop if there are none.
6. Report. Structure: Must-fix / Should-fix / Nits / Tests to add. Use file_path:line. If the change is clean, say so and stop — padding the review wastes the reader.

Do NOT restate what the code does; the author already knows. Do NOT suggest renames, formatting changes, or "consider extracting a helper" unless the current form causes a real bug or risk.

Request:
{input}`,
		},
		{
			Name:        "explain",
			Description: "Explain code: trace the flow, name the invariants, call out surprises",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the EXPLAIN skill. Produce a working mental model of the target, not a paraphrase of the source.

Playbook:
1. Locate. Use read_file / grep_codebase to pin down the target: file, function, region. If the target is ambiguous (e.g. "how auth works"), name the entry point you chose and why.
2. Trace one real flow. Pick a representative input and walk it through the code end-to-end. Name each hop as file_path:line. Stop when the value leaves the target (returns, writes to disk, sends on a channel).
3. Name invariants. What must always be true for this code to be correct? Who enforces it — the function itself, the caller, a type, a lock? State this even when it looks obvious; obvious invariants are the ones that get broken in refactors.
4. Call out surprises. Any non-obvious decision, hidden constraint, leaky abstraction, workaround for a specific bug, or counterintuitive ordering. If the code is boring, say so rather than invent surprises.
5. Draw the shape. If the flow involves multiple files or concurrent paths, include a tiny plaintext diagram (one to six lines, arrows, no art).
6. Report. Audience-appropriate summary on top, then the trace, then invariants, then surprises. If the reader will act on this (fix a bug, add a feature), point at the exact entry point they should touch.

Do NOT produce line-by-line narration. Do NOT restate obvious type signatures. Do NOT guess — if the answer requires reading more files, read them.

Request:
{input}`,
		},
		{
			Name:        "refactor",
			Description: "Plan and execute a safe refactor: scope, invariants, step list, verification",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the REFACTOR skill. Ship a concrete, reversible refactor — not a design essay.

Playbook:
1. Scope. Use grep_codebase / list_dir / read_file to find every call site and touched file. State what's IN scope and what's explicitly OUT of scope.
2. Invariants. List the observable behaviors that must not change (public API, on-disk format, error types, side-effect ordering). If a test already pins one, name it.
3. Step plan. Break the refactor into the smallest sequence of commits that each leave the tree green. Each step: files, change, how to verify.
4. Execute. Make the edits via edit_file / write_file. Do NOT introduce new abstractions the task doesn't need. Do NOT rename things that aren't in scope.
5. Verify. Run the smallest test command that exercises the changed code. If tests don't exist for the invariants, add them first or name the risk.
6. Report. Summarise what moved, what stayed, and any invariant you could not mechanically verify.

Stop and ask if the scope is unclear or the request implies a behavior change. Refactors that quietly change behavior are the worst kind.

Request:
{input}`,
		},
		{
			Name:        "debug",
			Description: "Reproduce, bisect, and fix a bug — with a regression test",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the DEBUG skill. Root-cause the problem; do not paper over it.

Playbook:
1. Reproduce. Turn the report into a minimal command or test that fails. If you cannot reproduce, stop and say so — guessing is worse than nothing.
2. Bisect. Use git_log / git_diff / git_blame, read_file, grep_codebase to narrow the failure to a specific function, commit, or config value. git_blame on a suspect line tells you which commit introduced the current behavior — pull that commit's diff next. Name the exact line that produces the wrong behavior.
3. Explain the mechanism. Write one paragraph that traces inputs through the code to the bad output. If the explanation hand-waves ("probably a race", "might be cache"), keep digging.
4. Fix at the root. Prefer the smallest change that removes the cause. Do NOT add try/except that just swallows the error. Do NOT add a feature flag to bypass the path.
5. Regression test. Add a test that fails without the fix and passes with it. Name the file and the test function.
6. Verify. Run the new test AND the nearest existing test package. Report pass/fail output verbatim.
7. Report. One-line cause, one-line fix, test name, any nearby latent bugs you spotted but left alone.

If you are not sure the fix addresses the root cause, say that explicitly — a partial fix with a named uncertainty beats a confident patch that just moves the bug.

Request:
{input}`,
		},
		{
			Name:        "test",
			Description: "Generate or improve tests: discover framework, find gaps, implement, run",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the TEST skill. Ship tests that actually execute, not pseudocode.

Playbook:
1. Discover the framework. Check go.mod / package.json / pyproject.toml and the existing _test files under the target package. Mirror the style already used — do not introduce a new test library.
2. Map the surface. For the target code, list every exported function and every non-trivial branch (error paths, boundary values, empty slice, missing key).
3. Identify gaps. Diff what already has coverage against what doesn't. Prioritise: correctness bugs > regression risk > edge cases > happy path.
4. Write tests. Keep them isolated (no network, no shared global state). Use table-driven style when Go, parametrised when Python/TS. Each test name states the behavior it pins ("returns_error_when_path_escapes_root"), not the function name.
5. Run them. Invoke the nearest test command via run_command. Paste the actual output. If any fail, fix the test or the code (decide which is wrong — do not silently edit the test to make it pass).
6. Report. Files added, cases added, command used, final result. Call out tests you chose NOT to add and why (e.g. "skipped I/O-heavy path; would need a fixture the repo lacks").

Do NOT add mocks for code the repo does not mock elsewhere. Do NOT assert on error message text unless the codebase already does — error types are sturdier than strings.

Request:
{input}`,
		},
		{
			Name:        "doc",
			Description: "Write documentation that teaches the code, not the signature",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the DOC skill. Write documentation a future engineer can act on — not a pretty-printed function signature.

Playbook:
1. Find the target. Use read_file / grep_codebase to read the code you're documenting. If the target is a package or module, read its public surface AND at least one representative implementation.
2. Decide the shape. Prose README for packages, block comments for exported symbols, inline comments only for non-obvious WHY. Match what the codebase already does — don't introduce a new doc style.
3. Write for the reader. For each piece: what problem does it solve, who calls it, what are the inputs/outputs, what invariants does it enforce, what happens on the error path. Prefer one concrete example over three abstract sentences.
4. Name the sharp edges. Rate-limits, thread-safety, panics, cancellation semantics, side effects, ordering requirements. If none exist, say "no side effects; safe for concurrent use" — explicit is better than implied.
5. Link, don't duplicate. Reference existing docs/types/tests rather than restate them. Use file_path:line for deep pointers.
6. Report. Files written/updated and what you chose NOT to document (e.g. "internal helpers — obvious from call sites").

Do NOT write "this function does X" when X is literally the function name. Do NOT invent examples that would not actually compile. Do NOT document trivially obvious code.

Request:
{input}`,
		},
		{
			Name:        "generate",
			Description: "Generate new code that obeys the project's conventions and tests it",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the GENERATE skill. Ship working, tested code — not scaffolding.

Playbook:
1. Understand the ask. Restate what's being built in one sentence. If ambiguous, stop and ask — inventing behavior is worse than asking.
2. Learn the conventions. Before writing anything new, read_file on two or three nearby siblings (same package, similar role). Mirror their structure, naming, error handling, and test style. Do NOT introduce a new pattern the codebase doesn't already use.
3. Place the code. Decide where it goes (file, package). State why — matching an existing boundary usually beats creating a new one.
4. Write the smallest version that works. No speculative configuration, no dead options, no TODO comments. Every identifier names something that exists.
5. Write at least one test. Same framework the package already uses. Test the behavior, not the implementation. If the new code is pure, a table-driven test is usually right.
6. Wire it. If the new code needs to be registered / exported / imported somewhere, do that in the same change. Half-landed code is worse than no code.
7. Verify. Build and run the nearest test. Paste the output. Fix what breaks.
8. Report. Files touched, public API added, test added, command used, any follow-ups you chose to defer.

Do NOT add error handling for impossible conditions. Do NOT add interfaces with a single implementer. Do NOT expose fields "in case we need them later".

Request:
{input}`,
		},
		{
			Name:        "audit",
			Description: "Security audit: triaged findings with file:line, severity, and fix direction",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the AUDIT skill. Produce a triaged security report — exploitable findings first, theoretical concerns last.

Playbook:
1. Frame the surface. What inputs does this code trust (user, network, filesystem, env)? What secrets does it handle? What does it delegate to (subprocess, SQL, template engine)? State the boundary you're auditing.
2. Run the obvious checks. Use grep_codebase for dangerous patterns matched to the language: path traversal (filepath.Join without cleanroot, os.Open on user input), command injection (exec with shell, os/exec with interpolated strings), SSRF (http.Get on user URLs), deserialization (json/yaml into map[string]any with reflection), SQL (string concatenation, fmt.Sprintf into DB query), secrets (ENV, .env, hardcoded tokens, credentials in logs), weak crypto (md5, sha1, random without crypto/rand).
3. Confirm each hit. Follow the taint: is the dangerous sink actually reachable from an untrusted source? A grep hit on exec.Command inside a test fixture is not a finding. A grep hit on exec.Command with user-controlled args is. When triaging, git_blame on the suspect line surfaces who introduced it and when — useful for prioritising recent additions over code that's been battle-tested for years.
4. Triage. For each real finding: CRITICAL (remote code exec, auth bypass), HIGH (data exfiltration, privilege escalation), MEDIUM (information disclosure, DoS), LOW (defense-in-depth, non-default configs). If you find nothing, say so clearly.
5. Fix direction. Each finding gets one concrete remediation: the specific check to add, the safer API to use, or the design change required. Do NOT just say "sanitize input".
6. Report. Ordered by severity, with file_path:line, exploit sketch, fix direction. Separate section for "reviewed and not a finding" if you checked something notable.

Do NOT invent findings to pad the report. Do NOT cite CWE numbers unless you can tie them to a real line. Do NOT recommend adding a library when a few lines of code would do.

Request:
{input}`,
		},
		{
			Name:        "onboard",
			Description: "Codebase walkthrough: hot paths, surprises, where to start changing",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the ONBOARD skill. Give a new contributor the shortest path to being productive — not a table of contents.

Playbook:
1. Orient. Read README / CLAUDE.md / package docstrings for the stated purpose. State in one sentence what the project actually does; if the README oversells it, say the honest version.
2. Name the hub. Which file / package is the nerve center? Where does execution start? Use read_file on main / entry points / top-level registries. Name it as file_path:line.
3. Trace one real flow end-to-end. Pick a representative user action (e.g. "user runs dfmc ask") and walk it through the code. Every hop named as file_path:line. Keep it under ten hops — link, don't copy.
4. Map the modules. For each top-level package: one sentence on what it owns, one sentence on who calls it. Cap at six packages — group the rest as "supporting".
5. Call out surprises. Non-obvious constraints (CGO required, windows-specific fallbacks, lock files, singletons, env vars). A new contributor hitting these blind wastes an afternoon.
6. Where to start. Three concrete first-commit ideas, each scoped small enough to land in a single PR. Name the file, what to change, how to verify.
7. Report. Purpose / Hub / One real flow / Modules / Surprises / First commits. Use headers; be skimmable.

Do NOT list every file. Do NOT recite directory structures the reader can run ls on. Do NOT recommend a first commit that requires changing three packages.

Request:
{input}`,
		},
	}
}
