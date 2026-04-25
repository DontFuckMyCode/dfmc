package langintel

// phpKB returns the embedded PHP knowledge base.
func phpKB() *Registry {
	return &Registry{
		Practices: []Practice{
			{
				ID:      "php-typed-properties",
				Summary: "Use typed properties (PHP 7.4+) instead of docblock annotations",
				Body: `Declare property types explicitly: public int $count; instead of /** @var int */. Typed properties enforce types at runtime and catch type errors earlier than docblock-only annotations.`,
				Langs: []string{"php"},
				Kinds: []string{"property"},
				Tags:  []string{"type-safety"},
			},
			{
				ID:      "php-composer-autoload",
				Summary: "Use Composer autoloading — never require/include manually",
				Body: `Use Composer's PSR-4 autoloading. Define autoload in composer.json and run composer dump-autoload. This prevents double-inclusion, handles class name case correctly on case-sensitive filesystems, and enables classmap optimization for production.`,
				Langs: []string{"php"},
				Kinds: []string{"include", "require"},
				Tags:  []string{"dependency-management"},
			},
			{
				ID:      "php-namespaces",
				Summary: "Use namespaces — PHP has no implicit package boundary without them",
				Body: `Every PHP file should declare a namespace matching its directory structure (PSR-4). Use 'use' statements for imported classes. Never use fully-qualified class names in application code — only in the global namespace or for vendor classes without PSR-4 mappings.`,
				Langs: []string{"php"},
				Kinds: []string{"namespace_statement", "use_declaration"},
				Tags:  []string{"structure", "style"},
			},
			{
				ID:      "php-pdo-prepared",
				Summary: "Use PDO with prepared statements for all database queries",
				Body: `PDO::prepare() with named or positional placeholders prevents SQL injection. Use $stmt->execute($params) with an array — never interpolate variables into the SQL string. Set PDO::ATTR_EMULATE_PREPARES = false to use real server-side prepared statements.`,
				Langs: []string{"php"},
				Kinds: []string{"method_call", "variable"},
				Tags:  []string{"security", "database"},
			},
		},
		BugPatterns: []BugPattern{
			{
				ID:       "php-eq-v-typed",
				Summary:  "Using == instead of === for comparisons — type coercion bugs",
				Body:     "PHP == compares with type coercion: 0 == 'hello' is true. Use === for all comparisons. The only exception is comparing to null: $x == null checks both null and empty string. Use ?? (null coalescing) for this instead.",
				Langs:    []string{"php"},
				Kinds:    []string{"binary_op"},
				Severity: "error",
				CWE:      "CWE-1026", // Comparison of Data of Different Types (loosely related)
			},
			{
				ID:       "php-mswitch",
				Summary:  "PHP switch uses loose comparison — use match (PHP 8+) for strict matching",
				Body:     "switch($x) uses == comparison, so case 0: matches both 0 and '0'. In PHP 8+, use match() which uses ===. In PHP 7, cast or use explicit checks before the switch.",
				Langs:    []string{"php"},
				Kinds:    []string{"switch_statement"},
				Severity: "warning",
			},
			{
				ID:       "php-extract",
				Summary:  "extract() on untrusted data overwrites local variables",
				Body:     "extract() imports array keys into the local symbol table, potentially overwriting existing variables. User-controlled input in extract() is a security risk. Use compound assignment or explicit extraction instead.",
				Langs:    []string{"php"},
				Kinds:    []string{"function_call"},
				Severity: "warning",
				CWE:      "CWE-185", // Incorrect Comparison
			},
			{
				ID:       "php-session-regenerate",
				Summary:  "Regenerate session ID on login — prevents session fixation",
				Body:     "Session fixation: attacker sets a known session ID before the victim logs in. Call session_regenerate_id(true) immediately after authentication succeeds. Also call session_regenerate_id(true) after privilege level changes.",
				Langs:    []string{"php"},
				Kinds:    []string{"function_call"},
				Severity: "warning",
				CWE:      "CWE-384", // Session Fixation
			},
		},
		SecurityRules: []SecurityRule{
			{
				ID:       "php-sql-injection",
				Summary:  "Never interpolate into SQL — use PDO prepared statements",
				Body:     "String interpolation into SQL is SQL injection in PHP. Use $pdo->prepare('SELECT * FROM users WHERE id = :id') with $stmt->execute([':id' => $id]). Never use $_GET or $_POST directly in SQL.",
				Langs:    []string{"php"},
				Kinds:    []string{"variable", "string_literal", "binary_op"},
				CWE:      "CWE-89",
				OWASP:    "A03:2021",
				Severity: "critical",
			},
			{
				ID:       "php-command-injection",
				Summary:  "Never pass unsanitized user input to exec/system/shell_exec",
				Body:     "shell_exec with user input is command injection. Use escapeshellarg() for individual arguments, or better, use proc_open with explicit argument arrays where no shell interpretation occurs. Avoid shell_exec, exec, system, passthru altogether when user input is involved.",
				Langs:    []string{"php"},
				Kinds:    []string{"function_call"},
				CWE:      "CWE-78",
				OWASP:    "A03:2021",
				Severity: "critical",
			},
			{
				ID:       "php-xss",
				Summary:  "Echo user input without escaping — use htmlspecialchars",
				Body:     "echo without escaping is XSS when the input is user-supplied. Always escape: echo htmlspecialchars($input, ENT_QUOTES | ENT_HTML5, 'UTF-8'). In frameworks (Laravel Blade, Symfony Twig), escaping is automatic — avoid raw() unless you deliberately want HTML output.",
				Langs:    []string{"php"},
				Kinds:    []string{"echo_statement", "print_statement"},
				CWE:      "CWE-79",
				OWASP:    "A03:2021",
				Severity: "critical",
			},
			{
				ID:       "php-serialize-untrusted",
				Summary:  "Never unserialize untrusted data — use json_decode instead",
				Body:     "unserialize() on untrusted input is arbitrary code execution via PHP object injection. Use json_decode for untrusted data. If you need object serialization, use a signed serialization format or a library like Symfony Serializer with explicit allowlists.",
				Langs:    []string{"php"},
				Kinds:    []string{"function_call"},
				CWE:      "CWE-502",
				OWASP:    "A08:2021",
				Severity: "critical",
			},
		},
		Idioms: []Idiom{
			{ID: "php-snake-case", Lang: "php", Rule: "Use snake_case for functions and methods, PascalCase for classes",
				Detail: "PHP convention (PSR-1): functions use snake_case (my_function). Classes use PascalCase (MyClass). Methods within classes use camelCase (myMethod) per PSR-12.",
				Kinds: []string{"function_definition", "method_definition", "class_declaration"}},
			{ID: "php-psr12", Lang: "php", Rule: "Follow PSR-12 coding style (4-space indent, braces on own lines)",
				Detail: "PSR-12 is the current PHP FIG standard: 4 spaces for indentation, opening brace on own line for classes/methods, opening brace on same line for control structures. Use PHP-CS-Fixer or php_codesniffer to enforce automatically.",
				Kinds: []string{"namespace_statement", "use_declaration"}},
		},
	}
}
