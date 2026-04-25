# sc-iac — Infrastructure-as-Code Security Review (Stub)

**Target repo:** `D:\Codebox\PROJECTS\DFMC`
**Date:** 2026-04-25

## Counts

| Severity  | Count |
|-----------|-------|
| Critical  | 0     |
| High      | 0     |
| Medium    | 0     |
| Low       | 0     |
| Info      | 0     |
| **Total** | **0** |

---

## Result: No IaC files detected

DFMC ships as a single-binary CLI/TUI tool with an embedded HTTP server. It is not deployed as managed infrastructure from this repository — there are no Terraform configs, no Kubernetes manifests, no Helm charts, no CloudFormation templates, no Pulumi programs.

## Search evidence

The following globs were executed against the repo root and returned **zero matches** each:

| Glob | Files matched |
|---|---|
| `**/*.tf` | 0 |
| `**/*.tfvars` | 0 |
| `**/*.tfstate` | 0 |
| `{k8s,kubernetes,helm,charts,manifests}/**/*` | 0 |
| `**/Chart.yaml` (Helm chart marker) | 0 |
| `**/{cloudformation,cfn}*.{yml,yaml,json}` | 0 |
| `**/Pulumi.yaml` | 0 |

Confirmed by direct directory inspection — no `infra/`, `iac/`, `terraform/`, `deploy/`, `k8s/`, `kubernetes/`, `helm/`, `charts/`, `manifests/`, or `pulumi/` directories exist at the repo root or anywhere under `internal/`, `cmd/`, `ui/`, `pkg/`, `docs/`, `.github/`.

## Adjacent deployment artifacts (out of IaC scope)

For completeness, the deployment-related files that DO exist in this repo are:

- `Dockerfile` — covered by `sc-docker-results.md`
- `.github/workflows/{ci.yml,release.yml}` — covered by `sc-ci-cd-results.md`
- `shell/Formula.rb`, `shell/homebrew-tapFormula.rb` — Homebrew distribution recipes (package-manager metadata, not IaC)
- `shell/completions.{bash,fish,zsh}` — shell completions (distribution artifact, not IaC)

None of these declare cloud infrastructure resources.

## Recommendation

If DFMC ever adds hosted services (e.g., a SaaS dashboard, a remote drive runner pool, or a managed MCP gateway), revisit IaC scanning at that point. Until then, this skill has nothing to inspect for this repository.
