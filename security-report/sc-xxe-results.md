# sc-xxe — XML External Entity Injection

**Target:** `D:\Codebox\PROJECTS\DFMC` (Go 1.25)
**Scope:** Full repo, excluding `bin/`, `vendor/`, `node_modules/`, `.dfmc/`, `.git/`, `security-report/`
**Date:** 2026-04-25
**CWE:** CWE-611 (Improper Restriction of XML External Entity Reference)

## Result

**No issues found by sc-xxe.**

DFMC does not parse XML anywhere in its source. The only document-format parser in the codebase is HTML via `golang.org/x/net/html`, which is not an XML/DTD parser and does not resolve XML external entities. Go's standard `encoding/xml` is also not vulnerable to classic XXE by default — it does not resolve external entities and does not support DTDs (per Go stdlib design, `encoding/xml` ignores `<!DOCTYPE>` and `<!ENTITY>` declarations).

## Evidence

### 1. No `encoding/xml` import anywhere in the repo

Search across all `.go` files (excluding skip list):

```
Grep: encoding/xml
→ No matches found

Grep: xml\.(NewDecoder|Unmarshal|Decoder)
→ No matches found
```

### 2. No third-party XML / SOAP / SAML libraries in go.mod

```
Grep: xml|soap|saml|xxe in go.mod (case-insensitive)
→ No matches found
```

`go.sum` has zero hits for `xml`, `soap`, `saml`, `xlsx`, `docx`, or `svg` parsing libraries. The single false-positive hit (`wazero`) is the WebAssembly runtime — unrelated to XML.

Confirmed third-party libraries that could touch XML-adjacent formats: **none**. No `etree`, `goxml`, `gosaml2`, `go-soap`, `xlsx`, `excelize`, `unioffice`, `docx`, etc.

### 3. No XML payloads or DTD/ENTITY markers in source

```
Grep: DOCTYPE|ENTITY|<!ENTITY in repo source
→ No files found
```

No test fixtures, no embedded XML strings, no SVG/XLSX/DOCX/RSS/Atom processing.

### 4. `web_fetch` tool does not parse XML

The `web_fetch` tool (`internal/tools/web.go` and friends) returns fetched bytes / Markdown-converted HTML to the LLM. It uses HTTP client + HTML-to-text conversion, not XML parsing. Even if a fetched URL returned `application/xml`, DFMC treats the body as opaque bytes / text — there is no decoder that would expand `<!ENTITY>` references.

### 5. HTML parser (`golang.org/x/net/html`) is not an XXE sink

`x/net/html` is an HTML5 tokenizer/parser. It does **not** process XML DTDs, does not resolve `SYSTEM` / `PUBLIC` external entities, and does not support parameter entities. HTML5 has only a fixed set of named character references (`&amp;`, `&lt;`, etc.) — no user-defined entities, so the billion-laughs attack class does not apply either.

## Attack Vectors Considered

| Vector | Applicable? | Reason |
|---|---|---|
| Classic XXE (file:///etc/passwd) | No | No XML parser in code path |
| Blind XXE (out-of-band exfil) | No | No XML parser in code path |
| Billion Laughs (DoS via entity expansion) | No | No XML parser; HTML5 has no user entities |
| XInclude / XSLT abuse | No | No XSLT/XInclude processor present |
| SOAP-based XXE | No | No SOAP client/server in deps |
| SAML XXE | No | No SAML library; DFMC has no SSO surface |
| SVG / XLSX / DOCX entity expansion | No | None of those formats parsed |
| RSS / Atom feed XXE | No | No feed parser |

## Severity

**N/A — no vulnerable surface.** No findings to triage.

## False-Positive Notes

Per the sc-xxe SKILL §"Common False Positives" item 3: "Go encoding/xml — does not process external entities by default." DFMC does not even import `encoding/xml`, so the standard FP guidance is moot — there is no parser to harden.

## References

- https://cwe.mitre.org/data/definitions/611.html
- https://owasp.org/www-community/vulnerabilities/XML_External_Entity_(XXE)_Processing
- Go stdlib `encoding/xml` design: https://pkg.go.dev/encoding/xml (no DTD support)
