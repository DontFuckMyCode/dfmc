# sc-ldap — LDAP Injection

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NOT APPLICABLE — no LDAP client or server anywhere in the codebase

## Verdict

No findings. DFMC has no LDAP authentication, no LDAP directory client, and no Active-Directory binding. There is no filter-construction sink to inject against.

## Verification

### 1. `go.mod` — no LDAP libraries

```
Pattern: go-ldap|ldap.v3|go.ldap|jtblin/go-ldap
Result:  0 matches in go.mod or *.go files
```

None of the standard Go LDAP libraries are declared:
- `github.com/go-ldap/ldap`
- `github.com/go-ldap/ldap/v3`
- `gopkg.in/ldap.v3`
- `github.com/nmcclain/ldap`
- `github.com/jtblin/go-ldap-client`

### 2. Authentication architecture excludes LDAP

Per [security-report/architecture.md:213-222](architecture.md):

> | Surface | Mechanism |
> |---|---|
> | HTTP / SSE / WS (`dfmc serve`) | `auth=none` (loopback only, default) **or** `auth=token` (bearer token …) |
> | Remote server (`dfmc remote start`) | Same model |
> | MCP server | **None** (single user over stdio) |
> | CLI | OS user identity only |
> | LLM provider HTTPS | Per-provider API key |

DFMC is a **single-user, single-workstation tool**. There is no concept of directory-backed identity, no `bind` / `search` / `compare` operation. The only authenticators are bearer tokens (constant-time compare) and OS user identity.

### 3. No filter-construction sinks

Repo-wide search for the classic LDAP injection sinks turned up zero matches:

```
Pattern: \(uid=|\(cn=|\(sAMAccountName=|\(objectClass=|\(memberOf=
Result:  0 matches in *.go files
```

No DN concatenation (`"uid=" + user + ",ou=people,…"`), no filter strings.

### 4. No SCIM, no Kerberos, no SSPI/Negotiate

- No `github.com/jcmturner/gokrb5/*` (Kerberos)
- No SCIM v2 endpoint
- No `WWW-Authenticate: Negotiate` header writer

### 5. Skipped phases

These sc-ldap probes were skipped because there is no LDAP client or server:
- Filter-injection (`*)(uid=*` style)
- DN injection (`,ou=admins,dc=…` suffixing)
- Blind boolean-based LDAPi
- Anonymous-bind discovery
- LDAPS TLS validation
- Referral abuse
- Search-filter resource exhaustion

## Bottom line

DFMC has no LDAP attack surface. The trust model is single-user with bearer-token / loopback auth — there is no directory service in the picture. **sc-ldap is not applicable to this codebase.** Re-run only if a future change introduces an LDAP/AD authentication mode, which would represent a fundamental shift in the trust model and warrant a wider security re-review beyond just this scan.
