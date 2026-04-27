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
		{
			Name:        "api",
			Description: "API design and implementation: REST, GraphQL, endpoints, schemas, auth",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the API skill. Design and implement APIs that are consistent, versioned, and easy to consume — not ad-hoc endpoints.

Playbook:
1. Map the domain. Identify the core resources and their relationships. Name the top-level entities (users, orders, projects). State whether this is REST, GraphQL, or a hybrid.
2. Design the surface. For each endpoint: HTTP method, path, request shape, response shape, auth requirements, error codes. Check if a similar endpoint already exists — match its patterns before inventing new ones.
3. Validate against conventions. Read the existing API handlers and route registrations. Mirror the error response shape, the middleware chain, the logging, and the request parsing already in use.
4. Implement the handler. Use read_file on a similar handler as a template. Wire the route, parse the input, call the service layer, format the response. Return structured errors (not 500 strings).
5. Add the schema. For request/response bodies, define the shape explicitly. For REST: parameterise paths, use ?过滤 for sorting/pagination. For GraphQL: name types after the domain, not the implementation.
6. Document. One paragraph per endpoint: what it does, what's required, what's returned on error. Link to related endpoints.
7. Verify. Build and check the route table (dfmc doctor or equivalent). If an endpoint can be exercised with a dry-run flag, use it.

Do NOT return raw database rows as API responses. Do NOT skip error codes the client could meaningfully act on. Do NOT version an API before you have evidence it needs it.

Request:
{input}`,
			Preferred: []string{"read_file", "grep_codebase", "glob"},
		},
		{
			Name:        "backend",
			Description: "Server-side logic, middleware, database queries, and service layer",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the BACKEND skill. Build server-side logic that's testable, observable, and resilient — not a flat script.

Playbook:
1. Identify the layer. Is this a handler, a service, a repository, a middleware? State the layer you chose and why. The layer determines error handling, transaction scope, and how the code is tested.
2. Check the existing structure. Use read_file / grep_codebase to find similar service functions and repository functions. Match their signature style, error wrapping, and transaction patterns.
3. Write the implementation. Keep functions small enough to test in isolation. Return concrete types, not interface{} unless polymorphism is genuinely needed. Propagate context for cancellation and tracing.
4. Handle errors at the boundary. Convert storage/service errors into user-facing errors at the outermost layer. Inner layers should return errors; outer layers should decide what to do with them.
5. Add a test. Same framework the package already uses. Test the behavior at the service layer with a mock repository, not the repository itself.
6. Wire it. If the new function needs to be registered or exported, do that in the same change.
7. Verify. Build, run the nearest test package.

Do NOT open transactions and leave them uncommitted. Do NOT log sensitive data. Do NOT swallow errors with _ =.

Request:
{input}`,
			Preferred: []string{"read_file", "grep_codebase", "run_command", "edit_file"},
		},
		{
			Name:        "frontend",
			Description: "UI components, styling, state management, and frontend conventions",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the FRONTEND skill. Produce UIs that are consistent with the existing component library and follow the project's conventions.

Playbook:
1. Survey the existing surface. Use glob / read_file to find the component directory and the style guide. Identify the base components already available (Button, Input, Card, Modal). Do NOT reach for raw HTML elements if a component already exists.
2. Identify the pattern. Check whether this is React, Vue, Svelte, or plain HTML templates. Match the component authoring style already in use — functional vs class, composition vs inheritance, CSS modules vs CSS-in-JS.
3. Map state ownership. Where does the data come from? Is it local state, a shared store, URL params, or a server fetch? State the data flow before writing any component.
4. Implement the component. Start from an existing similar component as a template. Keep the surface small — expose props, not internal state. Use semantic HTML for structure.
5. Handle loading and error states. A component that only renders the happy path is incomplete. Show a meaningful loading indicator and an error message that tells the user what to do.
6. Style. Match the existing CSS approach (design tokens, component-scoped styles, utility classes). Do NOT introduce a new CSS methodology the project hasn't already adopted.
7. Verify. Build — if there's a type checker (TypeScript, PropTypes), run it. Run the nearest test.

Do NOT hardcode colors or spacing values that design tokens already cover. Do NOT put business logic in the component. Do NOT make network calls from the component — use a service layer or hooks.

Request:
{input}`,
			Preferred: []string{"read_file", "glob", "grep_codebase", "codemap"},
		},
		{
			Name:        "security",
			Description: "Authentication, authorization, encryption, and security hardening",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the SECURITY skill. Apply authentication, authorization, and hardening that stands up to real attackers — not token security theatre.

Playbook:
1. Map the trust boundary. What does this code trust? User input, network requests, cookies, JWTs, environment variables, other services? State the boundary clearly before adding any control.
2. Authentication. Check how the current system authenticates requests. Sessions, JWTs, API keys, OAuth? Match the existing mechanism before adding a new one. Verify the token is validated on every request, not just checked for existence.
3. Authorization. For each operation: who is allowed to perform it? Is ownership checked (user can only edit their own resources)? Is a role check present? Use grep_codebase to find the existing permission patterns and extend them consistently.
4. Secrets management. grep_codebase for hardcoded passwords, tokens, private keys, and credentials in environment variables. Move anything that should be secret to a vault, environment variables, or a secrets manager. Ensure .env files are gitignored.
5. Input validation. Validate all user input at the trust boundary. Use allowlists where possible. Reject obviously malicious input (null bytes, path traversal sequences, SQL comment markers) at the outermost layer.
6. Crypto. For passwords: use bcrypt, argon2, or scrypt with a cost factor appropriate for the deployment environment. For TLS: verify certificates, do not disable certificate validation, use TLS 1.2+.
7. Hardening. Rate limiting, request size limits, CORS policy, security headers (CSP, HSTS, X-Frame-Options). If the project has a hardening checklist, follow it.
8. Report. Each finding: file_path:line, what's wrong, what's at risk, concrete fix.

Do NOT add security controls that are never invoked. Do NOT log tokens or credentials. Do NOT disable security checks for "development convenience."

Request:
{input}`,
			Preferred: []string{"grep_codebase", "read_file", "git_diff", "git_log"},
			Allowed:   []string{"write_file", "edit_file"},
		},
		{
			Name:        "performance",
			Description: "Profiling, optimization, caching strategies, and bottleneck analysis",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the PERFORMANCE skill. Find the actual bottleneck before changing any code — measuring beats guessing every time.

Playbook:
1. Define the symptom. Name what is slow: a page load, an API call, a build step, a CI run? Quantify it ("the /api/users endpoint takes 4 seconds at p95"). Without a number, you cannot know if you've fixed anything.
2. Profile, not guess. Use the appropriate profiler for the language: pprof for Go, --profile for Node, pytest-profiling for Python. If no profiler is available, use timing logs. Look for: hot functions (10x slower than average), excessive allocations, N+1 queries, blocking I/O.
3. Find the cause. For each hot function: trace the call stack. Is the slowness in the algorithm (O(n²) instead of O(n)), in I/O (sync instead of async), in serialization (deep copy on every call), or in the database (missing index, N+1)? Name the exact line.
4. Fix the biggest first. Optimizing a function that accounts for 2% of the total time is not worth the risk. Fix what the profiler says is hot, not what looks suspicious in the source.
5. Cache where it pays off. Cache the result of expensive computations, not cheap ones. Use appropriate invalidation: TTL for staleacceptable data, explicit invalidation for correctness-critical data. Do not cache user-specific data across sessions without encryption.
6. Verify. Re-run the profiler or the timing measurement. Report the before/after numbers.

Do NOT optimize before profiling. Do NOT add caching to cover up a missing index or a bad algorithm. Do NOT micro-optimize code that's called once per session.

Request:
{input}`,
			Preferred: []string{"run_command", "read_file", "grep_codebase"},
			Allowed:   []string{"edit_file", "write_file"},
		},
		{
			Name:        "git",
			Description: "Commit strategy, branching, history rewriting, and collaborative git workflows",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the GIT skill. Structure commits so history is navigable and reversions are precise — not just "save my work."

Playbook:
1. Scope the change. What exactly changed? Use git_status / git_diff to see the full picture. Name the files touched. Separate logical changes into separate commits if they are independently revertable.
2. Write the commit message. First line: imperative mood, 50 chars, describes what changed (not what you did). Body: explain why this change was made, not what the diff shows. Reference issues or tickets if the project uses them.
3. Choose the branch strategy. For feature branches: name it after the feature or ticket, not the developer. For long-running branches (main, develop): state whether this commits to main directly or through a PR. For hotfixes: name it hotfix/<description>.
4. Check the impact. Use git_log / git_diff to see what this change touches. Look for large binaries, generated files, or secrets that should not be committed. Ensure .gitignore covers build artifacts.
5. Rebase vs merge. Rebase when you want a linear history and the branch is short-lived. Merge when the branch has a distinct identity and history worth preserving. Never force-push shared branches.
6. Resolve conflicts. For each conflict: read both versions, understand the intent of each change, choose the right resolution. Do not blindly take one side.
7. Verify. After push, check the CI status. If a test failed on a conflicting test file, re-run locally before asking for a re-review.

Do NOT commit generated files. Do NOT commit secrets, API keys, or credentials. Do NOT use --force on shared branches. Do NOT commit large binaries.

Request:
{input}`,
			Preferred: []string{"git_status", "git_log", "git_diff", "git_blame", "git_branch"},
		},
		{
			Name:        "architecture",
			Description: "System design, module boundaries, dependency rules, and scaling decisions",
			Source:      "builtin",
			Builtin:     true,
			Prompt: `You are running the ARCHITECTURE skill. Make design decisions that the next engineer can understand and challenge — not clever ones.

Playbook:
1. State the current design. Use read_file / glob / codemap to understand how modules relate to each other. Draw the dependency direction (who imports whom). Name the hub module.
2. Name the forces. What are the constraints? Scale (users, data volume, request rate), latency requirements, team size, deployment model (single process, microservices, serverless). State them before choosing a pattern.
3. Evaluate options. Name two or three approaches before recommending one. For each: what it solves well, what it costs, what it makes harder. Do not recommend the clever option.
4. Define the module boundaries. Which modules own which data? What is the communication protocol between modules (function calls, RPC, events, queues)? Name the module that "owns" each top-level concern.
5. Specify the contract. For each module boundary: what invariants must hold, what errors can be returned, what events are emitted. An interface that doesn't name its pre/post conditions is not a contract.
6. Make the change. Draw the new module boundary, then make the smallest commit that establishes it. One module boundary change at a time — do not move ten files in one PR.
7. Document. After the PR lands, update any relevant README, ARCHITECTURE.md, or inline docs that describe the module map.

Do NOT introduce a pattern (event sourcing, CQRS, microservices) without naming the specific problem it solves. Do NOT abstract across module boundaries before you have two implementers. Do NOT make architectural changes that require rewriting code that's already working.

Request:
{input}`,
			Preferred: []string{"codemap", "read_file", "git_log", "glob", "grep_codebase"},
		},
	}
}
