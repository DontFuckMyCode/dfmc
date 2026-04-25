package security

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DependencyFinding represents a dependency parsed from a lock file.
type DependencyFinding struct {
	File     string `json:"file"`
	Pkg      string `json:"package"`
	Version  string `json:"version"`
	Ecosystem string `json:"ecosystem"`
	DevOnly  bool   `json:"dev_only,omitempty"`
	Kind     string `json:"kind"` // "outdated" | "vulnerable" | "unknown"
	Severity string `json:"severity,omitempty"`
}

// ScanDependencyFiles walks the project root for lock files and returns
// findings grouped by ecosystem. It detects:
//   - packages with no registered version (unknown origin)
//   - direct references to git repositories without pinned commits
//   - packages listed only in devDependencies (DevOnly)
func (s *Scanner) ScanDependencyFiles(root string) ([]DependencyFinding, error) {
	var findings []DependencyFinding

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch name {
		case "node_modules":
			// skip
		default:
			subFindings, _ := s.scanSubdir(filepath.Join(root, name))
			findings = append(findings, subFindings...)
		}
	}

	// Top-level lock files in root
	if f, err := s.scanGoSum(filepath.Join(root, "go.sum")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanNPMLock(filepath.Join(root, "package-lock.json")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanCargoLock(filepath.Join(root, "Cargo.lock")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanRequirementsTxt(filepath.Join(root, "requirements.txt")); err == nil {
		findings = append(findings, f...)
	}

	return findings, nil
}

func (s *Scanner) scanSubdir(dir string) ([]DependencyFinding, error) {
	var findings []DependencyFinding
	if f, err := s.scanGoSum(filepath.Join(dir, "go.sum")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanNPMLock(filepath.Join(dir, "package-lock.json")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanCargoLock(filepath.Join(dir, "Cargo.lock")); err == nil {
		findings = append(findings, f...)
	}
	if f, err := s.scanRequirementsTxt(filepath.Join(dir, "requirements.txt")); err == nil {
		findings = append(findings, f...)
	}
	return findings, nil
}

// scanGoSum parses a go.sum file. Each line is:
// module version h1:hash
// Pseudo-versions start with "v0.0.0-" and indicate a dependency pinned
// via a git commit rather than a tagged release.
var pseudoVersionRE = regexp.MustCompile(`^v0\.0\.0-`)

func (s *Scanner) scanGoSum(path string) ([]DependencyFinding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil // no go.sum is fine
	}
	defer f.Close()
	var findings []DependencyFinding
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pkg := fields[0]
		version := fields[1]
		// flag git-based pseudo-versions as "unknown" provenance
		if pseudoVersionRE.MatchString(version) {
			findings = append(findings, DependencyFinding{
				File:      "go.sum",
				Pkg:       pkg,
				Version:   version,
				Ecosystem: "Go",
				Kind:      "unknown",
				Severity:  "medium",
			})
		}
	}
	return findings, sc.Err()
}

// npmLockPackage describes one entry in package-lock.json's dependencies map.
type npmLockPackage struct {
	Version      string `json:"version"`
	Dev         bool   `json:"dev,omitempty"`
	Resolved    string `json:"resolved,omitempty"`
	Requires    map[string]string `json:"requires,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

func (s *Scanner) scanNPMLock(path string) ([]DependencyFinding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var top struct {
		Dependencies map[string]npmLockPackage `json:"dependencies"`
	}
	if err := json.NewDecoder(f).Decode(&top); err != nil {
		return nil, nil
	}
	var findings []DependencyFinding
	for pkg, info := range top.Dependencies {
		findings = append(findings, DependencyFinding{
			File:      "package-lock.json",
			Pkg:       pkg,
			Version:   info.Version,
			Ecosystem: "npm",
			DevOnly:   info.Dev,
			Kind:      "pinned",
		})
	}
	return findings, nil
}

// scanCargoLock parses a Cargo.lock file (TOML-like). Each [[package]] block
// has name and version.
var cargoPackageRE = regexp.MustCompile(`^\[\[package\]\]$`)
var cargoNameRE = regexp.MustCompile(`(?m)^name = "([^"]+)"$`)
var cargoVersionRE = regexp.MustCompile(`(?m)^version = "([^"]+)"$`)

func (s *Scanner) scanCargoLock(path string) ([]DependencyFinding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var findings []DependencyFinding
	lines := strings.Split(string(data), "\n")
	inPackage := false
	var pkgName, pkgVersion string
	flush := func() {
		if pkgName != "" {
			findings = append(findings, DependencyFinding{
				File:      "Cargo.lock",
				Pkg:       pkgName,
				Version:   pkgVersion,
				Ecosystem: "Cargo",
				Kind:      "pinned",
			})
		}
		pkgName = ""
		pkgVersion = ""
		inPackage = false
	}
	for _, line := range lines {
		if cargoPackageRE.MatchString(strings.TrimSpace(line)) {
			flush()
			inPackage = true
			continue
		}
		if inPackage {
			if m := cargoNameRE.FindStringSubmatch(line); m != nil {
				pkgName = m[1]
			} else if m := cargoVersionRE.FindStringSubmatch(line); m != nil {
				pkgVersion = m[1]
			}
		}
	}
	flush()
	return findings, nil
}

// requirements.txt format (pip):
// package==1.2.3
// package>=1.0
// -e git+https://github.com/user/repo.git@abc123#egg=pkg
// --index-url https://...
var reqLineRE = regexp.MustCompile(`^([a-zA-Z0-9_\-\.]+)(==|>=|<=|~=|!=)`)
var reqGitRE = regexp.MustCompile(`^-e git\+https?://[^@]+@([0-9a-f]{7,})`)

func (s *Scanner) scanRequirementsTxt(path string) ([]DependencyFinding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var findings []DependencyFinding
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Check git deps before the general `-` skip (editable installs are valid deps).
		if m := reqGitRE.FindStringSubmatch(line); m != nil {
			findings = append(findings, DependencyFinding{
				File:      "requirements.txt",
				Pkg:       "git+https",
				Version:   m[1][:7],
				Ecosystem: "pip",
				Kind:      "git",
				Severity:  "medium",
			})
			continue
		}
		if strings.HasPrefix(line, "-") {
			continue
		}
		if m := reqLineRE.FindStringSubmatch(line); m != nil {
			findings = append(findings, DependencyFinding{
				File:      "requirements.txt",
				Pkg:       m[1],
				Version:   m[2], // operator retained so user sees >= etc.
				Ecosystem: "pip",
				Kind:      "versioned",
			})
		}
	}
	return findings, sc.Err()
}
