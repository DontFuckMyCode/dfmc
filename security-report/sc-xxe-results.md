# sc-xxe — XML External Entity

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NOT APPLICABLE — no XML parser anywhere in the codebase

## Verdict

No findings. DFMC does not parse XML. There is no `encoding/xml` import, no third-party XML parser (`etree`, `xmlquery`, `libxml2` binding), no SOAP/SAML/RSS/Atom handler, and no SVG-as-XML processing. The classic XXE attack surface (entity resolution, DTD inclusion, parameter-entity exfil) does not exist here.

## Verification

### 1. No `encoding/xml` import

```
Pattern: encoding/xml|beevik/etree|antchfx/xmlquery|JoshVarga/svg
Result:  0 matches in *.go production files
```

The Go standard library's `encoding/xml` is not imported by any DFMC source file. Even if it were, Go's `encoding/xml` does not resolve external entities and silently ignores `<!DOCTYPE>` / `<!ENTITY>` declarations by default — making it the safe choice for XML the project doesn't need.

### 2. No XML-shaped content types served or accepted

The HTTP server in [ui/web/server.go](../ui/web/server.go) `contentTypeEnforcementMiddleware` requires `application/json` for state-changing methods (per [architecture.md:241-242](architecture.md)). No route advertises `application/xml`, `application/soap+xml`, `application/xhtml+xml`, or `image/svg+xml`. Inputs that try to declare `Content-Type: application/xml` on POST/PUT/PATCH are rejected with 415 before reaching any handler.

### 3. No XML-adjacent format handlers

- **YAML**: parsed by `gopkg.in/yaml.v3` (not XML; no DTD support; no entity resolution).
- **JSON**: stdlib `encoding/json`.
- **HTML**: only `golang.org/x/net/html` — a permissive HTML5 tokenizer, not an XML parser; does not resolve external entities.
- **SVG**: not parsed; `internal/codemap` SVG export is plain string rendering, no parsing.
- **Office documents (.docx/.xlsx zip+xml)**: not handled.
- **PDF (XFA)**: not handled.

### 4. Outbound XML calls — none

LLM provider clients (`internal/provider/anthropic.go`, `openai_compat.go`, `google.go`) all speak JSON over HTTPS. No SOAP envelope, no RSS reader, no XMPP, no XML-RPC. `web_fetch` returns the raw response body to the model — it does not parse XML server-side.

### 5. Skipped phases

The following sc-xxe probes were skipped because there is no XML parser to probe:
- Classic external-entity (`<!ENTITY xxe SYSTEM "file:///etc/passwd">`)
- Parameter-entity exfil (`<!ENTITY % …`)
- Billion-laughs / quadratic-blowup DoS
- XInclude file disclosure
- Blind OOB DTD callbacks
- SAML response injection
- Office document XXE (docx, xlsx)
- SVG entity / external script in image upload

## Bottom line

DFMC has no XML attack surface. The only document-format parser is `golang.org/x/net/html` (HTML5 tokenizer, not XML). **sc-xxe is not applicable to this codebase.** If a future change introduces `encoding/xml`, SOAP, SAML, RSS, SVG parsing, or any XML-bearing upload endpoint, re-run this scan and configure parsers with `Strict: false` rejected and DTD/entity resolution explicitly disabled.

## References

- Go stdlib `encoding/xml`: https://pkg.go.dev/encoding/xml — does not resolve external entities, silently skips DTDs.
- OWASP XXE prevention cheat sheet — primary mitigation is "disable external entity processing"; for Go this is the default behaviour.
