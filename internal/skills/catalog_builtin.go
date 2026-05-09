package skills

import "regexp"

// builtinTrigger compiles a (pattern, weight) pair for the builtin
// catalog. Patterns are author-controlled constants so a regex
// syntax error is a build bug — MustCompile panics loudly on
// startup if a pattern is malformed.
func builtinTrigger(pattern string, weight float64) Trigger {
	return Trigger{
		Pattern: regexp.MustCompile("(?i)" + pattern),
		Raw:     pattern,
		Weight:  weight,
	}
}

// catalog_builtin.go — the ten skills the binary ships with. Each entry
// pairs a short description with a 6-step playbook the model is told to
// follow when the skill activates. Discover() prepends this list before
// scanning project / global skill directories, so the builtins always
// win on name collision (review/explain/refactor/debug/test/doc/generate/
// audit/onboard).
//
// Lives apart from the rest of the catalog so adjusting prose for one
// skill doesn't churn the discovery / loader code paths in catalog.go
// or catalog_loader.go.

func builtinCatalog() []Skill {
	return []Skill{
		{
			Name:        "review",
			Description: "Code review: correctness, risk, missing tests, security smells",
			Source:      "builtin",
			Builtin:     true,
			Task:        "review",
			Role:        "code_reviewer",
			Preferred:   []string{"git_diff", "read_file", "grep_codebase", "find_symbol", "git_blame"},
			Triggers: []Trigger{
				builtinTrigger(`\breview\b|code\s*review|pr\s*review|pull\s*request`, 0.85),
				builtinTrigger(`looks?\s+(good|ok)|sanity\s+check|sign\s*off`, 0.65),
			},
			System: `You are running the REVIEW skill. Review the changed code for correctness, risk, and test coverage — not style nits.

Playbook:
1. Scope. Identify exactly what changed using git and file/context tools before judging it.
2. Correctness. Trace the happy path and at least one failure path for every non-trivial change.
3. Behavioral risk. Flag silent changes to APIs, side effects, persistence, concurrency, or performance.
4. Tests. Name the exact missing test when a gap matters.
5. Security/resource. Check for real security and reliability regressions, not hypothetical lint.
6. Report. Structure findings as Must-fix / Should-fix / Nits / Tests to add with file:line evidence.

Do not restate what the code already says. Do not pad the review when the change is clean.`,
		},
		{
			Name:        "explain",
			Description: "Explain code: trace the flow, name the invariants, call out surprises",
			Source:      "builtin",
			Builtin:     true,
			Preferred:   []string{"find_symbol", "read_file", "grep_codebase", "codemap"},
			Triggers: []Trigger{
				builtinTrigger(`\bexplain\b|how\s+does|what\s+does\s+(this|that|it)\s+do|walk\s+(me\s+)?through`, 0.85),
			},
			System: `You are running the EXPLAIN skill. Produce a working mental model of the target, not a paraphrase of the source.

Playbook:
1. Locate the true entry point or slice of code being asked about.
2. Trace one representative flow end-to-end with concrete file:line evidence.
3. Name invariants and who enforces them.
4. Call out non-obvious constraints, ordering, or design surprises.
5. Use a tiny plaintext diagram when multiple files or paths matter.
6. Lead with the answer, then the evidence.

Do not narrate line-by-line. Do not guess when more files need to be read.`,
		},
		{
			Name:        "refactor",
			Description: "Plan and execute a safe refactor: scope, invariants, step list, verification",
			Source:      "builtin",
			Builtin:     true,
			Task:        "refactor",
			Preferred:   []string{"grep_codebase", "find_symbol", "edit_file", "apply_patch", "run_command"},
			Triggers: []Trigger{
				builtinTrigger(`\brefactor(ing)?\b|restructure|reorganize|extract\s+(method|function|module)`, 0.85),
			},
			System: `You are running the REFACTOR skill. Ship a concrete, reversible refactor — not a design essay.

Playbook:
1. State what is in scope and out of scope.
2. List observable behaviors that must not change.
3. Break work into the smallest safe sequence.
4. Make minimal edits that improve structure without widening the change.
5. Verify changed behavior with targeted tests or builds.
6. Summarize what moved, what stayed, and what you verified.

Stop and ask if the request implies a hidden behavior change.`,
		},
		{
			Name:        "debug",
			Description: "Reproduce, bisect, and fix a bug — with a regression test",
			Source:      "builtin",
			Builtin:     true,
			Task:        "debug",
			Preferred:   []string{"run_command", "grep_codebase", "read_file", "git_blame", "edit_file"},
			Triggers: []Trigger{
				builtinTrigger(`\bdebug(ging)?\b|stack\s*trace|panic|segfault|crash(es|ing|ed)?\b|exception`, 0.9),
				builtinTrigger(`\bbug\b|\bfix\b|broken|not\s+working|fails?\b|error(s)?\b|throws?\b`, 0.75),
			},
			System: `You are running the DEBUG skill. Root-cause the problem; do not paper over it.

Playbook:
1. Reproduce the issue with a concrete command or test when possible.
2. Narrow the fault to a specific file, function, config, or commit.
3. Explain the failure mechanism clearly before patching.
4. Fix the root cause with the smallest durable change.
5. Add or update a regression test when practical.
6. Verify the reported path and the nearest affected package.

If you cannot reproduce, say so clearly instead of guessing.`,
		},
		{
			Name:        "test",
			Description: "Generate or improve tests: discover framework, find gaps, implement, run",
			Source:      "builtin",
			Builtin:     true,
			Task:        "test",
			Preferred:   []string{"read_file", "grep_codebase", "edit_file", "write_file", "run_command"},
			Triggers: []Trigger{
				builtinTrigger(`\btest(s|ing|cases?)?\b|unit\s*test|integration\s*test|coverage\b|spec\b`, 0.85),
				builtinTrigger(`add\s+tests?|write\s+tests?|test\s+for|missing\s+tests?`, 0.85),
			},
			System: `You are running the TEST skill. Ship tests that actually execute, not pseudocode.

Playbook:
1. Mirror the repo's existing test style and framework.
2. Map important branches, edge cases, and regressions.
3. Add the smallest high-value tests first.
4. Keep tests deterministic and isolated.
5. Run them and report real output.
6. Name the residual risk you intentionally left untested.

Do not add ornate mocking layers the repository does not already use.`,
		},
		{
			Name:        "doc",
			Description: "Write documentation that teaches the code, not the signature",
			Source:      "builtin",
			Builtin:     true,
			Task:        "doc",
			Preferred:   []string{"read_file", "find_symbol", "grep_codebase"},
			Triggers: []Trigger{
				builtinTrigger(`\b(doc|docs|documentation|docstring|godoc|jsdoc)\b|readme\b`, 0.85),
			},
			System: `You are running the DOC skill. Write documentation a future engineer can act on — not a pretty-printed function signature.

Playbook:
1. Read the code before documenting it.
2. Choose the documentation shape that matches the repo.
3. Explain purpose, usage constraints, and sharp edges.
4. Prefer one concrete example over abstract prose.
5. Link to existing code/tests instead of duplicating them.
6. Keep documentation implementation-aligned and concise.

Do not document trivially obvious code.`,
		},
		{
			Name:        "generate",
			Description: "Generate new code that obeys the project's conventions and tests it",
			Source:      "builtin",
			Builtin:     true,
			Preferred:   []string{"read_file", "grep_codebase", "edit_file", "write_file", "run_command"},
			Triggers: []Trigger{
				builtinTrigger(`\bgenerate\b|scaffold|boilerplate|new\s+(component|module|package|service|endpoint)`, 0.8),
			},
			System: `You are running the GENERATE skill. Ship working, tested code — not scaffolding.

Playbook:
1. Restate the requested behavior precisely.
2. Read nearby sibling code and mirror its conventions.
3. Place the code in the existing architectural boundary that fits best.
4. Write the smallest complete version that works.
5. Add at least one meaningful test.
6. Wire registration/export/import changes in the same patch.
7. Verify with build and targeted tests.

Do not introduce speculative abstractions or dead options.`,
		},
		{
			Name:        "audit",
			Description: "Security audit: triaged findings with file:line, severity, and fix direction",
			Source:      "builtin",
			Builtin:     true,
			Task:        "security",
			Role:        "security_auditor",
			Preferred:   []string{"grep_codebase", "read_file", "find_symbol", "git_blame"},
			Triggers: []Trigger{
				builtinTrigger(`\bsecurity\b|\bvulnerabilit(y|ies)\b|\baudit(s|ing)?\b|\bcve\b|\bexploit(s|able|ation)?\b|\bpentest|penetration\s*test`, 0.95),
				builtinTrigger(`sql\s*inject(ion)?|xss\b|csrf\b|ssrf\b|rce\b|path\s*traversal|cmd\s*inject(ion)?`, 0.95),
				builtinTrigger(`hard[-\s]?coded\s+(secret|password|token|key)|api[-\s]?key\s+leak|leak(ed|s)?\s+(secret|credential|token)`, 0.9),
				builtinTrigger(`secure\s+(this|the)|threat\s*model|owasp\b|cwe[-\s]?\d+`, 0.8),
			},
			System: `You are running the AUDIT skill. Produce a triaged security report — exploitable findings first, theoretical concerns last.

Playbook:
1. Define the trust boundary being audited.
2. Check likely sinks and taint flow for the language and subsystem.
3. Confirm each hit before calling it a finding.
4. Triage by exploitability and impact.
5. Give one concrete remediation direction per finding.
6. Separate confirmed findings from things reviewed and cleared.

Do not invent findings to pad the report.`,
		},
		{
			Name:        "onboard",
			Description: "Codebase walkthrough: hot paths, surprises, where to start changing",
			Source:      "builtin",
			Builtin:     true,
			Task:        "planning",
			Role:        "planner",
			Preferred:   []string{"codemap", "read_file", "find_symbol", "list_dir"},
			Triggers: []Trigger{
				builtinTrigger(`\bonboard(ing)?\b|new\s+(to|here)|first\s+time|getting\s+started|where\s+(do\s+i\s+)?(start|begin)`, 0.85),
				builtinTrigger(`tour\s+(of|through)|walk\s*through\s+(of\s+)?(the\s+)?(code|codebase|project|repo)`, 0.8),
			},
			System: `You are running the ONBOARD skill. Give a new contributor the shortest path to being productive — not a table of contents.

Playbook:
1. State what the project actually does.
2. Identify the execution hub and entry point.
3. Trace one representative flow end-to-end.
4. Summarize the top modules and what each owns.
5. Call out non-obvious constraints and surprises.
6. End with three small, concrete first-commit ideas.

Do not list every file or merely restate the directory tree.`,
		},
	}
}
