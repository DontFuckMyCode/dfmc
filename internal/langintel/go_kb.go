// Package langintel hosts per-language knowledge bases used to surface
// relevant tips, patterns, and warnings during analysis.
package langintel

// goKB returns the embedded Go knowledge base. The entries are grouped
// into Practices, BugPatterns, SecurityRules, and Idioms.
func goKB() *Registry {
	return &Registry{
		Practices: []Practice{
			{
				ID:      "go-error-wrap",
				Summary: "Wrap errors with context using fmt.Errorf or xerrors",
				Body: `Wrap errors at the call site to preserve the stack trace and add context.
if err != nil { return fmt.Errorf("fetchUser %d: %w", id, err) }
Never discard errors with _ or return nil for a non-nil error.`,
				Langs: []string{"go"},
				Kinds: []string{"if_statement", "return_statement"},
				Tags:  []string{"error-handling"},
			},
			{
				ID:      "go-defer-close",
				Summary: "Defer resource cleanup immediately after acquisition",
				Body: `Pair every resource acquisition with defer immediately after.
f, err := os.Open(path)
if err != nil { return err }
defer f.Close()
This prevents the "forgot to close on early return" bug.`,
				Langs: []string{"go"},
				Kinds: []string{"call_expression", "variable_declaration"},
				Tags:  []string{"resource-management"},
			},
			{
				ID:      "go-context-propagate",
				Summary: "Accept context as first parameter and propagate it",
				Body: `Accept context.Context as the first parameter of long-running functions.
Pass it to downstream calls so cancellation propagates.
func fetchUser(ctx context.Context, id int) (*User, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
}`,
				Langs: []string{"go"},
				Kinds: []string{"function_declaration", "method_declaration"},
				Tags:  []string{"concurrency", "cancellation"},
			},
			{
				ID:      "go-interface-small",
				Summary: "Keep interfaces small — one or two methods max",
				Body: `Define interfaces where you need them, not where you define them.
Small interfaces (Reader, Writer, Closer) are easier to satisfy and compose.
type Reader interface { Read(p []byte) (n int, err error) }`,
				Langs: []string{"go"},
				Kinds: []string{"type_declaration", "interface_type"},
				Tags:  []string{"design", "interfaces"},
			},
			{
				ID:      "go-zero-values",
				Summary: "Prefer zero-value initialization over explicit empty literals",
				Body: `Zero-value initialization is idiomatic Go. Prefer:
var buf bytes.Buffer
var sem chan struct{}
Over:
buf := bytes.Buffer{}`,
				Langs: []string{"go"},
				Kinds: []string{"variable_declaration", "short_var_declaration"},
				Tags:  []string{"idiom", "style"},
			},
			{
				ID:      "go-strings-builder",
				Summary: "Use strings.Builder for concatenation in loops",
				Body: `Use strings.Builder (or strings.Grow) instead of += in loops to avoid O(n^2) copying.
var b strings.Builder
for _, s := range strs { b.WriteString(s) }
return b.String()`,
				Langs: []string{"go"},
				Kinds: []string{"for_statement", "range_clause"},
				Tags:  []string{"performance", "strings"},
			},
			{
				ID:      "go-sync-once",
				Summary: "Use sync.Once for one-time initialization",
				Body: `sync.Once guarantees a block runs exactly once, even with concurrent access.
var once sync.Once
var config *Config
once.Do(func() { config = loadConfig() })`,
				Langs: []string{"go"},
				Kinds: []string{"go_statement", "call_expression"},
				Tags:  []string{"concurrency", "initialization"},
			},
			{
				ID:      "go-test-table",
				Summary: "Use table-driven tests for multiple test cases",
				Body: `Table-driven tests are more readable and easier to extend.
tests := []struct { name string; got, want int }{
    {"2+2", 2+2, 4}, {"3*3", 3*3, 9},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        if got := add(2, 2); got != tt.want { ... }
    })
}`,
				Langs: []string{"go"},
				Kinds: []string{"function_declaration"},
				Tags:  []string{"testing", "style"},
			},
		},
		BugPatterns: []BugPattern{
			{
				ID:       "go-nil-deref",
				Summary:  "Check nil before dereferencing a pointer, map, slice, or channel",
				Body:     "Dereferencing a nil pointer panics. Always check err != nil before using a pointer returned from a function. Note: map and slice zero-value is nil (not an empty map/slice), and appending to a nil slice works but reading past len(0) panics.",
				Langs:    []string{"go"},
				Kinds:    []string{"selector_expression", "index_expression"},
				Severity: "error",
				Fix:      "Add a nil check before use: if p == nil { return ErrNotFound }",
				CWE:      "CWE-476",
			},
			{
				ID:       "go-goroutine-leak",
				Summary:  "Ensure goroutines are cleaned up — always have a termination path",
				Body:     "Goroutines leak if they block forever on send/receive with no sender/receiver on the other side. Use context.WithCancel, explicit done channels, or runtime.Goexit() as appropriate.",
				Langs:    []string{"go"},
				Kinds:    []string{"go_statement", "send_statement"},
				Severity: "warning",
				CWE:      "CWE-1000",
			},
			{
				ID:       "go-mutex-value-copy",
				Summary:  "Copying a mutex-valued struct field creates two mutexes",
				Body:     "A sync.Mutex must not be copied after first use. If a struct contains a mutex, pass the struct by pointer: type Counter struct { mu sync.Mutex; n int } both fields private. Use (c *Counter) Inc() { c.mu.Lock(); defer c.mu.Unlock(); c.n++ }",
				Langs:    []string{"go"},
				Kinds:    []string{"struct_type", "method_declaration"},
				Severity: "error",
				CWE:      "CWE-675",
			},
			{
				ID:       "go-slice-bounds",
				Summary:  "Slicing a slice beyond capacity panics",
				Body:     "s[:i] where i > len(s) panics, even if i <= cap(s). Use max := min(i, len(s)) before slicing. This commonly occurs when parsing with index checks that are invalidated by prior slices.",
				Langs:    []string{"go"},
				Kinds:    []string{"index_expression", "slice_expression"},
				Severity: "error",
				CWE:      "CWE-125",
			},
			{
				ID:       "go-map-concurrent-write",
				Summary:  "Concurrent map writes cause fatal panic",
				Body:     "Go maps are not goroutine-safe. Concurrent writes panic. Use sync.Map, mutex-protected map (RWMutex), or channel-based serialization.",
				Langs:    []string{"go"},
				Kinds:    []string{"index_expression", "assignment"},
				Severity: "error",
				CWE:      "CWE-662",
			},
			{
				ID:       "go-string-to-byte",
				Summary:  "Avoid repeated []byte(s) conversions in hot paths",
				Body:     "Each conversion allocates a new copy. Cache the result when used in loops or frequently-called functions. For read-only access, use string directly.",
				Langs:    []string{"go"},
				Kinds:    []string{"type_conversion_expression", "call_expression"},
				Severity: "info",
			},
			{
				ID:       "go-http-response-body-close",
				Summary:  "Always read and close HTTP response body",
				Body:     "Failure to read and close the response body can cause connection exhaustion. Always call defer resp.Body.Close() after a successful client.Do, and drain any remaining body with io.Copy(io.Discard, resp.Body).",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression", "defer_statement"},
				Severity: "warning",
				CWE:      "CWE-400",
			},
			{
				ID:       "go-json-omitempty",
				Summary:  "omitempty on non-pointer fields returns zero value, not absence",
				Body:     "With omitempty, a non-pointer int field with value 0 is omitted, but 0 is a valid value. Use a pointer *int so only nil is omitted: struct { Count *int json:\"count,omitempty\" } // 0 preserved.",
				Langs:    []string{"go"},
				Kinds:    []string{"field_declaration", "struct_type"},
				Severity: "warning",
			},
			{
				ID:       "go-slice-prealloc",
				Summary:  "Pre-allocate slice capacity in loops that append",
				Body:     "Appending to a slice without capacity hint causes repeated reallocation. Know your rough size and pre-allocate: vs := make([]Value, 0, len(items)); for _, item := range items { vs = append(vs, transform(item)) }",
				Langs:    []string{"go"},
				Kinds:    []string{"for_statement", "append_call"},
				Severity: "info",
			},
			{
				ID:       "go-time-after-goroutine",
				Summary:  "time.After leaks a timer until it fires — use NewTicker or channel",
				Body:     "time.After creates a timer that is not garbage-collected until it fires. In long-running loops or frequent calls, this accumulates. Use time.NewTicker with defer ticker.Stop(), or a buffered channel as a one-shot timer.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression", "for_statement"},
				Severity: "warning",
				CWE:      "CWE-400",
			},
		},
		SecurityRules: []SecurityRule{
			{
				ID:       "go-panic-in-web-handler",
				Summary:  "Panic in HTTP handler can crash the server",
				Body:     "A panic in an HTTP handler is not recovered by default and kills the goroutine. Always wrap handlers with a recovery middleware: http.HandlerFunc(func(w ResponseWriter, r *Request) { defer func() { if p := recover(); p != nil { log.Error(p) } }(); handle(w, r) })",
				Langs:    []string{"go"},
				Kinds:    []string{"function_declaration", "method_declaration"},
				CWE:      "CWE-755",
				Severity: "high",
			},
			{
				ID:       "go-sql-injection",
				Summary:  "Never interpolate user input into SQL strings — use parameterized queries",
				Body:     "String concatenation into SQL is SQL injection. Use db.QueryContext with ? placeholders: rows, err := db.QueryContext(ctx, \"SELECT * FROM users WHERE id = ?\", userID). Never: \"SELECT * FROM users WHERE id = \" + userID.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression"},
				CWE:      "CWE-89",
				OWASP:    "A03:2021",
				Severity: "critical",
			},
			{
				ID:       "go-command-injection",
				Summary:  "Never pass unsanitized input to exec.Command",
				Body:     "os/exec with user-controlled arguments allows command injection. Use exec.Command with separate arguments: exec.Command(\"ls\", \"-\", \"file\") safe. NOT: exec.Command(\"ls -\" + flag) unsafe. For shell-like wrappers, use shell=false and pass args separately.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression"},
				CWE:      "CWE-78",
				OWASP:    "A03:2021",
				Severity: "critical",
			},
			{
				ID:       "go-hardcoded-credentials",
				Summary:  "Do not hardcode credentials in source code",
				Body:     "API keys, passwords, and tokens found in source code are a critical risk. Use environment variables, config files outside source control, or a secrets manager: key := os.Getenv(\"API_KEY\") safe. const key = \"sk-...\" unsafe — committed to source.",
				Langs:    []string{"go"},
				Kinds:    []string{"const_declaration", "var_declaration", "assignment"},
				CWE:      "CWE-798",
				Severity: "critical",
			},
			{
				ID:       "go-xss-html-template",
				Summary:  "Use html/template not text/template for user content",
				Body:     "text/template does not auto-escape HTML. If rendering user-provided content in HTML responses, use html/template which contextually escapes. For raw HTML that must be rendered, use template.HTML with extreme care and manual sanitization.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression"},
				CWE:      "CWE-79",
				OWASP:    "A03:2021",
				Severity: "high",
			},
			{
				ID:       "go-path-traversal",
				Summary:  "Validate and sanitize path inputs to prevent directory traversal",
				Body:     "User-supplied paths like ../../etc/passwd can escape the intended directory. Use filepath.Clean, check the resolved path stays within the allowed root: cleaned := filepath.Clean(root + \"/\" + userPath); if !strings.HasPrefix(cleaned, root) { return ErrForbidden }",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression", "binary_expression"},
				CWE:      "CWE-22",
				OWASP:    "A01:2021",
				Severity: "high",
			},
			{
				ID:       "go-weak-crypto-md5",
				Summary:  "MD5 is broken for security purposes — use SHA-256 or argon2",
				Body:     "MD5 is subject to collision attacks and should not be used for password hashing, integrity checks, or signatures. Use crypto/sha256 or a dedicated KDF like argon2id for passwords.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression"},
				CWE:      "CWE-327",
				Severity: "high",
			},
			{
				ID:       "go-tls-insecure",
				Summary:  "Insecure TLS configuration exposes connections",
				Body:     "tls.Config{InsecureSkipVerify: true} disables all certificate validation. Only use in tests. In production, either use the system CA pool or provide the correct CA certificate. Also ensure MinVersion is TLS 1.2 or higher.",
				Langs:    []string{"go"},
				Kinds:    []string{"call_expression", "struct_literal"},
				CWE:      "CWE-295",
				Severity: "high",
			},
		},
		Idioms: []Idiom{
			{ID: "go-camel-case", Lang: "go", Rule: "Use CamelCase for exported names, camelCase for unexported",
				Detail: "Go convention: exported identifiers start with uppercase. Internal packages (not exported) use lowercase_mixedCase.",
				Kinds: []string{"function_declaration", "method_declaration", "type_declaration", "const_declaration", "var_declaration"}},
			{ID: "go-error-as", Lang: "go", Rule: "Use errors.Is / errors.As for error checking",
				Detail: "Direct type assertion on error (err.(Type)) loses wrapped context. Use errors.Is(err, target) and errors.As(err, target) which unwrap correctly.",
				Kinds: []string{"if_statement", "type_assertion_expression"}},
			{ID: "go-mutex-addr", Lang: "go", Rule: "Pass mutex by pointer, not by value",
				Detail: "sync.Mutex must not be copied. Pass pointers to structs containing mutexes, or embed the mutex without a tag.",
				Kinds: []string{"method_declaration", "function_declaration"}},
			{ID: "go-slice-map-prealloc", Lang: "go", Rule: "Prefer make(T, n, cap) over append loops when size is known",
				Detail: "If you know the capacity ahead of time, pre-allocate to avoid reallocation: make([]T, 0, n) or make(map[K]V, sz).",
				Kinds: []string{"make_call", "variable_declaration"}},
			{ID: "go-test-t-run", Lang: "go", Rule: "Use t.Run for subtests to get structured output",
				Detail: "Subtests with t.Run produce hierarchical test output. This enables go test -run 'TestFoo/bar' for selective runs.",
				Kinds: []string{"for_statement", "call_expression"}},
			{ID: "go-generics-type-set", Lang: "go", Rule: "Use type sets, not type parameters, for interface constraints",
				Detail: "Go 1.18+ generics: use ~T in constraints when you need the underlying type's methods: type Integer interface{ ~int | ~int64 | ~float64 }.",
				Kinds: []string{"type_declaration", "interface_type"}},
		},
	}
}
