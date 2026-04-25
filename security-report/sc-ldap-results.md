# sc-ldap Results — DFMC

**Skill:** sc-ldap (LDAP Injection — CWE-90)
**Target:** D:\Codebox\PROJECTS\DFMC
**Date:** 2026-04-25
**Status:** CLEAN — No issues found

## Result

No issues found by sc-ldap — no LDAP libraries in go.mod and no LDAP filter/DN construction in source.

## Evidence

### 1. Dependency check (go.mod / go.sum)
- `go.mod`: no `go-ldap/ldap`, `gopkg.in/ldap`, `nmcclain/ldap`, or any other LDAP client/server library declared.
- `go.sum`: case-insensitive grep for `ldap` returned zero matches.

### 2. Source string-literal check
- Case-insensitive grep for `ldaps?://` across the entire tree (excluding `bin/`, `vendor/`, `node_modules/`, `.dfmc/`, `.git/`, `security-report/`): zero matches.

### 3. Source identifier check
- Case-insensitive grep for `ldap` across all `*.go` files: zero matching files.

### 4. Architectural confirmation
DFMC has no first-party authentication backend. The project is a single-user local Go binary (CLI/TUI/embedded web on localhost:7777). No directory-server integration, no bind/search/modify code paths, no DN construction, no LDAP filter assembly anywhere in the codebase.

## Severity
None. No findings to triage.

## Discovery phase keywords scanned (all zero matches)
`ldap_search`, `ldap_bind`, `ldap_connect`, `ldap_modify`, `LdapConnection`, `DirContext`, `InitialDirContext`, `SearchControls`, `ldap3`, `python-ldap`, `ldap.search`, `DirectorySearcher`, `SearchRequest`, `ldap://`, `ldaps://`.
