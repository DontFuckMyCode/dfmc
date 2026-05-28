// dependency_audit_tool.go — Dependency vulnerability and compliance scanner.
// Scans go.mod for known vulnerabilities (CVE/OSV), outdated packages, and
// license incompatibilities.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// DependencyAuditTool scans Go dependencies for security and compliance issues.
type DependencyAuditTool struct {
	httpClient *http.Client
}

// NewDependencyAuditTool creates a new audit tool.
func NewDependencyAuditTool() *DependencyAuditTool {
	return &DependencyAuditTool{
		httpClient: security.NewSafeHTTPClient(10*time.Second, "https://osv.dev"),
	}
}

func (t *DependencyAuditTool) Name() string { return "dependency_audit" }
func (t *DependencyAuditTool) Description() string {
	return "Scan Go dependencies for vulnerabilities, outdated packages, and license issues."
}
func (t *DependencyAuditTool) Risk() Risk          { return RiskRead }
func (t *DependencyAuditTool) Idempotent() bool    { return true }
func (t *DependencyAuditTool) SetEngine(_ *Engine) {}

func (t *DependencyAuditTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "dependency_audit",
		Title:   "Dependency audit",
		Summary: "Scan go.mod for CVE vulnerabilities, outdated packages, and license issues.",
		Purpose: `Use when you need to audit a Go project's dependency health — before a release, after adding a new dependency, or as part of a security scan. Checks osv.dev for CVEs and compares versions against latest.`,
		Prompt: `Dependency audit tool. Scans the project's go.mod for known vulnerabilities using the OSV database (osv.dev), version staleness, and license issues.

Results are grouped by severity (CRITICAL > HIGH > MEDIUM > LOW) and include:
- Affected package and version
- CVE ID or OSV entry
- Recommended upgrade version
- Brief description`,
		Risk: RiskRead,
		Tags: []string{"security", "dependencies", "audit", "CVE", "compliance"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Default: "go.mod", Description: "Path to go.mod (default: go.mod in project root)."},
			{Name: "severity", Type: ArgString, Default: "all", Description: "Minimum severity: critical, high, medium, low, all."},
			{Name: "check_updates", Type: ArgBoolean, Default: "true", Description: "Check for newer package versions."},
			{Name: "check_licenses", Type: ArgBoolean, Default: "false", Description: "Check for problematic licenses (GPL, LGPL, AGPL, MPL)."},
			{Name: "ignore", Type: ArgString, Description: "Comma-separated package prefixes to ignore (e.g. \"github.com/myorg,github.com/internal\")."},
		},
		Returns: "Markdown summary + JSON data with findings grouped by severity.",
		Examples: []string{
			`{"path":"go.mod"}`,
			`{"path":"go.mod","severity":"high"}`,
			`{"check_licenses":true}`,
		},
		Idempotent: true,
		CostHint:   "network-bound (API calls to osv.dev)",
	}
}

type auditResult struct {
	Summary       string         `json:"summary"`
	TotalDeps     int            `json:"total_deps"`
	ScannedDeps   int            `json:"scanned_deps"`
	SkippedDeps   int            `json:"skipped_deps"`
	Findings      []auditFinding `json:"findings"`
	VulnsBySev    map[string]int `json:"vulns_by_severity"`
	OutdatedPkgs  []outdatedPkg  `json:"outdated,omitempty"`
	LicenseIssues []licenseIssue `json:"license_issues,omitempty"`
}

type auditFinding struct {
	Severity    string `json:"severity"`
	Package     string `json:"package"`
	Version     string `json:"version"`
	Type        string `json:"type"` // "vulnerability" | "outdated" | "license"
	CVE         string `json:"cve,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	FixedIn     string `json:"fixed_in,omitempty"`
	URL         string `json:"url,omitempty"`
}

type outdatedPkg struct {
	Package string `json:"package"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

type licenseIssue struct {
	Package  string `json:"package"`
	License  string `json:"license"`
	Severity string `json:"severity"`
	SPDX     string `json:"spdx_id"`
}

func (t *DependencyAuditTool) Execute(ctx context.Context, req Request) (Result, error) {
	goModPath := strings.TrimSpace(asString(req.Params, "path", "go.mod"))
	severity := strings.ToLower(asString(req.Params, "severity", "all"))
	checkUpdates := asBool(req.Params, "check_updates", true)
	checkLicenses := asBool(req.Params, "check_licenses", false)
	ignoreStr := strings.ToLower(asString(req.Params, "ignore", ""))

	ignorePrefixes := []string{}
	if ignoreStr != "" {
		for p := range strings.SplitSeq(ignoreStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				ignorePrefixes = append(ignorePrefixes, p)
			}
		}
	}

	// Resolve go.mod path
	if !filepath.IsAbs(goModPath) {
		goModPath = filepath.Join(req.ProjectRoot, goModPath)
	}

	deps, err := parseGoMod(goModPath)
	if err != nil {
		return Result{}, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	result := auditResult{
		TotalDeps:     len(deps),
		VulnsBySev:    map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0},
		Findings:      []auditFinding{},
		OutdatedPkgs:  []outdatedPkg{},
		LicenseIssues: []licenseIssue{},
	}

	// Filter ignored packages
	filtered := []pkgInfo{}
	for _, dep := range deps {
		ignored := false
		for _, prefix := range ignorePrefixes {
			if strings.HasPrefix(strings.ToLower(dep.Path), prefix) {
				ignored = true
				break
			}
		}
		if !ignored {
			filtered = append(filtered, dep)
		} else {
			result.SkippedDeps++
		}
	}
	result.ScannedDeps = len(filtered)

	// Check for vulnerabilities via OSV
	for _, dep := range filtered {
		vulns, err := t.checkOSV(ctx, dep.Path, dep.Version)
		if err == nil && len(vulns) > 0 {
			for _, v := range vulns {
				if shouldInclude(severity, v.Severity) {
					result.Findings = append(result.Findings, v)
					result.VulnsBySev[v.Severity]++
				}
			}
		}
	}

	// Check for outdated packages
	if checkUpdates {
		for _, dep := range filtered {
			latest, err := t.getLatestVersion(ctx, dep.Path)
			if err == nil && latest != "" && latest != dep.Version && latest != "v"+dep.Version {
				result.OutdatedPkgs = append(result.OutdatedPkgs, outdatedPkg{
					Package: dep.Path,
					Current: dep.Version,
					Latest:  latest,
				})
			}
		}
	}

	// Check licenses
	if checkLicenses {
		for _, dep := range filtered {
			lic, err := t.checkLicense(ctx, dep.Path)
			if err == nil && lic != "" && isProblematicLicense(lic) {
				result.LicenseIssues = append(result.LicenseIssues, licenseIssue{
					Package:  dep.Path,
					License:  lic,
					Severity: licenseSeverity(lic),
					SPDX:     lic,
				})
			}
		}
	}

	// Sort findings by severity
	sortFindings(result.Findings)

	// Build summary
	summary := buildSummary(result)

	var output bytes.Buffer
	output.WriteString("## Dependency Audit Results\n\n")
	output.WriteString(fmt.Sprintf("**Total dependencies:** %d\n", result.TotalDeps))
	output.WriteString(fmt.Sprintf("**Scanned:** %d | **Skipped:** %d\n\n", result.ScannedDeps, result.SkippedDeps))

	if len(result.Findings) == 0 && len(result.OutdatedPkgs) == 0 && len(result.LicenseIssues) == 0 {
		output.WriteString("✅ **No issues found.**\n\n")
	} else {
		if len(result.Findings) > 0 {
			output.WriteString("### Vulnerabilities\n\n")
			output.WriteString("| Severity | Package | Version | CVE | Fixed In |\n")
			output.WriteString("|----------|---------|---------|-----|----------|\n")
			for _, f := range result.Findings {
				output.WriteString(fmt.Sprintf("| %s | `%s` | %s | %s | %s |\n",
					f.Severity, f.Package, f.Version, f.CVE, f.FixedIn))
			}
			output.WriteString("\n")
		}
		if len(result.OutdatedPkgs) > 0 {
			output.WriteString("### Outdated Packages\n\n")
			output.WriteString("| Package | Current | Latest |\n")
			output.WriteString("|---------|---------|--------|\n")
			for _, o := range result.OutdatedPkgs {
				output.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", o.Package, o.Current, o.Latest))
			}
			output.WriteString("\n")
		}
		if len(result.LicenseIssues) > 0 {
			output.WriteString("### License Concerns\n\n")
			output.WriteString("| Package | License | Severity |\n")
			output.WriteString("|---------|---------|----------|\n")
			for _, l := range result.LicenseIssues {
				output.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", l.Package, l.License, l.Severity))
			}
			output.WriteString("\n")
		}
	}

	output.WriteString(fmt.Sprintf("> %s\n", summary))

	return Result{
		Output: output.String(),
		Data: map[string]any{
			"total_deps":   result.TotalDeps,
			"scanned_deps": result.ScannedDeps,
			"skipped_deps": result.SkippedDeps,
			"findings":     result.Findings,
			"vulns_by_sev": result.VulnsBySev,
			"outdated":     result.OutdatedPkgs,
			"licenses":     result.LicenseIssues,
			"summary":      summary,
		},
	}, nil
}

type pkgInfo struct {
	Path    string
	Version string
}

// parseGoMod reads and parses a go.mod file for dependencies.
func parseGoMod(path string) ([]pkgInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open go.mod: %w", err)
	}
	defer f.Close()

	var deps []pkgInfo
	scanner := bufio.NewScanner(f)
	inRequire := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "require (" {
			inRequire = true
			continue
		}
		if line == ")" && inRequire {
			inRequire = false
			continue
		}

		// Single-line require: require github.com/foo v1.2.3
		if strings.HasPrefix(line, "require ") && !strings.HasPrefix(line, "require (") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				deps = append(deps, pkgInfo{Path: parts[1], Version: cleanVersion(parts[2])})
			}
			continue
		}

		if inRequire {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				deps = append(deps, pkgInfo{Path: parts[0], Version: cleanVersion(parts[1])})
			}
		}
	}

	return deps, scanner.Err()
}

var versionRe = regexp.MustCompile(`^v?([\d.]+)`)

func cleanVersion(v string) string {
	m := versionRe.FindStringSubmatch(v)
	if m != nil {
		return m[1]
	}
	return v
}

// checkOSV queries the OSV.dev API for vulnerabilities in a package version.
func (t *DependencyAuditTool) checkOSV(ctx context.Context, pkg, version string) ([]auditFinding, error) {
	type osvQuery struct {
		Pkg struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"package"`
	}
	type osvResponse struct {
		Vulns []struct {
			ID       string `json:"id"`
			Summary  string `json:"summary"`
			Severity []struct {
				Type  string `json:"type"`
				Score string `json:"score"`
			} `json:"severity"`
			Affected []struct {
				Package struct {
					Name string `json:"name"`
				} `json:"package"`
				Ranges []struct {
					Events []struct {
						Introduced string `json:"introduced"`
						Fixed      string `json:"fixed"`
					} `json:"events"`
				} `json:"ranges"`
			} `json:"affected"`
			References []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"references"`
		} `json:"vulns"`
	}

	query := osvQuery{}
	query.Pkg.Name = pkg
	query.Pkg.Version = version

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.osv.dev/v1/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv.dev returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var osvResp osvResponse
	if err := json.Unmarshal(data, &osvResp); err != nil {
		return nil, err
	}

	var findings []auditFinding
	for _, v := range osvResp.Vulns {
		sev := "medium"
		for _, s := range v.Severity {
			if s.Type == "CVSS_V3" {
				sev = cvssToSeverity(s.Score)
			}
		}

		fixedIn := ""
		if len(v.Affected) > 0 && len(v.Affected[0].Ranges) > 0 {
			for _, r := range v.Affected[0].Ranges {
				for _, e := range r.Events {
					if e.Fixed != "" {
						fixedIn = e.Fixed
						break
					}
				}
			}
		}

		url := ""
		for _, ref := range v.References {
			if ref.Type == "WEB" {
				url = ref.URL
				break
			}
		}

		findings = append(findings, auditFinding{
			Severity:    sev,
			Package:     pkg,
			Version:     version,
			Type:        "vulnerability",
			CVE:         v.ID,
			Title:       v.Summary,
			Description: v.Summary,
			FixedIn:     fixedIn,
			URL:         url,
		})
	}

	return findings, nil
}

// getLatestVersion fetches the latest version of a package from proxy.golang.org.
func (t *DependencyAuditTool) getLatestVersion(ctx context.Context, pkg string) (string, error) {
	// Use Go proxy to get latest version list
	url := fmt.Sprintf("https://proxy.golang.org/%s/@v/list", pkg)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var versions []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		v := strings.TrimSpace(scanner.Text())
		if v != "" {
			versions = append(versions, v)
		}
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("no versions found")
	}

	// Sort by version (simple sort, good enough for "latest" check)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})

	return versions[0], nil
}

// checkLicense fetches LICENSE file from proxy.golang.org and identifies the license type.
func (t *DependencyAuditTool) checkLicense(ctx context.Context, pkg string) (string, error) {
	// Try to get LICENSE from proxy
	url := fmt.Sprintf("https://proxy.golang.org/%s/@v/LICENSE", pkg)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("no license found")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Simple license detection from header
	return detectLicense(data), nil
}

// SPDX license identifiers for common problematic licenses.
var problematicLicenses = map[string]bool{
	"GPL-2.0":       true,
	"GPL-3.0":       true,
	"LGPL-2.1":      true,
	"LGPL-3.0":      true,
	"AGPL-3.0":      true,
	"MPL-1.0":       true,
	"MPL-1.1":       true,
	"MPL-2.0":       true,
	"EPL-1.0":       true,
	"EPL-2.0":       true,
	"SSPL-1.0":      true,
	"BSL-1.0":       false, // allowed
	"MIT":           false, // allowed
	"Apache-2.0":    false, // allowed
	"BSD-2-Clause":  false, // allowed
	"BSD-3-Clause":  false, // allowed
	"ISC":           false, // allowed
	"GPL-2.0-only":  true,
	"GPL-3.0-only":  true,
	"LGPL-2.1-only": true,
	"LGPL-3.0-only": true,
	"AGPL-3.0-only": true,
}

func isProblematicLicense(lic string) bool {
	// Check exact match
	if problematicLicenses[lic] {
		return true
	}
	// Check prefix (GPL, LGPL, AGPL, MPL, EPL, SSPL)
	upper := strings.ToUpper(lic)
	for k := range problematicLicenses {
		if strings.HasPrefix(upper, strings.ToUpper(k)) {
			return problematicLicenses[k]
		}
	}
	return false
}

func licenseSeverity(lic string) string {
	upper := strings.ToUpper(lic)
	if strings.HasPrefix(upper, "GPL") || strings.HasPrefix(upper, "AGPL") {
		return "high"
	}
	if strings.HasPrefix(upper, "LGPL") || strings.HasPrefix(upper, "MPL") || strings.HasPrefix(upper, "EPL") {
		return "medium"
	}
	return "low"
}

func detectLicense(data []byte) string {
	// Look for SPDX identifier in first 2000 bytes
	header := data
	if len(header) > 2000 {
		header = header[:2000]
	}

	// Content-based detection (more reliable than regex for GNU licenses)
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("GNU GENERAL PUBLIC LICENSE")) {
		if bytes.Contains(upper, []byte("VERSION 3")) {
			return "GPL-3.0"
		}
		if bytes.Contains(upper, []byte("VERSION 2")) {
			return "GPL-2.0"
		}
		return "GPL"
	}
	if bytes.Contains(upper, []byte("GNU LESSER GENERAL PUBLIC LICENSE")) {
		if bytes.Contains(upper, []byte("VERSION 3")) {
			return "LGPL-3.0"
		}
		return "LGPL-2.1"
	}
	if bytes.Contains(upper, []byte("GNU AFFERO GENERAL PUBLIC LICENSE")) {
		return "AGPL-3.0"
	}
	if bytes.Contains(upper, []byte("MOZILLA PUBLIC LICENSE")) {
		if bytes.Contains(upper, []byte("2.0")) {
			return "MPL-2.0"
		}
		return "MPL"
	}

	spdxPatterns := []string{
		`(?i)SPDX-License-Identifier:\s*([\w\-\.]+)`,
		`(?i)License:\s*([\w\-\.]+)`,
	}
	for _, pattern := range spdxPatterns {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(string(header))
		if len(m) > 1 {
			return m[1]
		}
	}

	return "unknown"
}

func shouldInclude(minSev, findingSev string) bool {
	order := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
	minVal := order[minSev]
	if minVal == 0 {
		return true // "all" case
	}
	return order[findingSev] >= minVal
}

func cvssToSeverity(score string) string {
	var f float64
	fmt.Sscanf(score, "%f", &f)
	if f >= 9.0 {
		return "critical"
	}
	if f >= 7.0 {
		return "high"
	}
	if f >= 4.0 {
		return "medium"
	}
	return "low"
}

func sortFindings(findings []auditFinding) {
	sort.Slice(findings, func(i, j int) bool {
		sevOrder := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
		if sevOrder[findings[i].Severity] != sevOrder[findings[j].Severity] {
			return sevOrder[findings[i].Severity] > sevOrder[findings[j].Severity]
		}
		return findings[i].Package < findings[j].Package
	})
}

func buildSummary(r auditResult) string {
	totalVulns := len(r.Findings)
	totalOutdated := len(r.OutdatedPkgs)
	totalLic := len(r.LicenseIssues)

	if totalVulns == 0 && totalOutdated == 0 && totalLic == 0 {
		return "✅ No vulnerabilities, outdated packages, or license issues detected."
	}

	var parts []string
	if totalVulns > 0 {
		parts = append(parts, fmt.Sprintf("%d vulnerability(ies)", totalVulns))
	}
	if totalOutdated > 0 {
		parts = append(parts, fmt.Sprintf("%d outdated package(s)", totalOutdated))
	}
	if totalLic > 0 {
		parts = append(parts, fmt.Sprintf("%d license concern(s)", totalLic))
	}

	return "⚠️ Found: " + strings.Join(parts, ", ")
}
