package cli

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

func addDoctorCheck(checks *[]doctorCheck, name, status, details string) {
	*checks = append(*checks, doctorCheck{
		Name:    name,
		Status:  status,
		Details: details,
	})
}

func addDoctorRuntimeChecks(eng *engine.Engine, checks *[]doctorCheck) {
	statusSnapshot := eng.Status()
	if statusSnapshot.MemoryDegraded {
		details := "episodic/semantic memory unavailable"
		if reason := strings.TrimSpace(statusSnapshot.MemoryLoadErr); reason != "" {
			details += ": " + reason
		}
		addDoctorCheck(checks, "memory.tier", "warn", details)
	} else {
		addDoctorCheck(checks, "memory.tier", "pass", "episodic/semantic tiers loaded")
	}
	if strings.TrimSpace(statusSnapshot.ASTBackend) == "" {
		addDoctorCheck(checks, "ast.backend", "warn", "ast engine backend is unavailable")
	} else {
		status := "pass"
		details := statusSnapshot.ASTBackend
		if reason := strings.TrimSpace(statusSnapshot.ASTReason); reason != "" {
			details += ": " + reason
		}
		if statusSnapshot.ASTBackend == "regex" {
			status = "warn"
		}
		addDoctorCheck(checks, "ast.backend", status, details)
	}
	for _, lang := range statusSnapshot.ASTLanguages {
		name := strings.TrimSpace(lang.Language)
		if name == "" {
			continue
		}
		checkStatus := "pass"
		active := strings.TrimSpace(lang.Active)
		if active == "" {
			checkStatus = "warn"
			active = "unavailable"
		}
		details := active
		if reason := strings.TrimSpace(lang.Reason); reason != "" {
			details += ": " + reason
		}
		if active == "regex" {
			checkStatus = "warn"
		}
		addDoctorCheck(checks, "ast."+name, checkStatus, details)
	}
	metricsDetails := formatASTMetricsSummary(statusSnapshot.ASTMetrics)
	if strings.TrimSpace(metricsDetails) == "" {
		metricsDetails = "no parse activity recorded yet"
	}
	addDoctorCheck(checks, "ast.metrics", "pass", metricsDetails)
	codemapDetails := formatCodeMapMetricsSummary(statusSnapshot.CodeMap)
	if strings.TrimSpace(codemapDetails) == "" {
		codemapDetails = "no codemap build activity recorded yet"
	}
	addDoctorCheck(checks, "codemap.metrics", "pass", codemapDetails)
	root := strings.TrimSpace(statusSnapshot.ProjectRoot)
	if root == "" {
		addDoctorCheck(checks, "project.root", "warn", "project root is empty")
	} else if st, err := os.Stat(root); err != nil {
		addDoctorCheck(checks, "project.root", "fail", err.Error())
	} else if !st.IsDir() {
		addDoctorCheck(checks, "project.root", "fail", "project root is not a directory")
	} else {
		addDoctorCheck(checks, "project.root", "pass", root)
	}
	addMagicDocHealthCheck(checks, root, 24*time.Hour)
	addPromptHealthCheck(checks, root, 450)

	if eng.Config != nil {
		addFileSystemHealthCheck(checks, "storage.data_dir", eng.Config.DataDir())
		addFileSystemHealthCheck(checks, "plugins.dir", eng.Config.PluginDir())
	}

	globalPath, projectPath := config.ConfigPaths("")
	for _, path := range []string{globalPath, projectPath} {
		if path == "" {
			continue
		}
		if msg := hooks.CheckConfigPermissions(path); msg != "" {
			addDoctorCheck(checks, "config.permissions", "warn", msg)
		} else {
			addDoctorCheck(checks, "config.permissions", "pass", "ok")
		}
	}

	for _, bin := range []string{"git", "go"} {
		if path, err := exec.LookPath(bin); err != nil {
			addDoctorCheck(checks, "dependency."+bin, "warn", "not found in PATH")
		} else {
			addDoctorCheck(checks, "dependency."+bin, "pass", path)
		}
	}
}

func addDoctorProviderChecks(eng *engine.Engine, checks *[]doctorCheck, network bool, timeout time.Duration) {
	cache := eng.Status().ModelsDevCache
	if !cache.Exists {
		addDoctorCheck(checks, "modelsdev.cache", "warn", "missing: "+cache.Path)
	} else {
		details := cache.Path
		if !cache.UpdatedAt.IsZero() {
			details += " updated " + cache.UpdatedAt.Format(time.RFC3339)
		}
		if cache.SizeBytes > 0 {
			details += fmt.Sprintf(" size=%d", cache.SizeBytes)
		}
		addDoctorCheck(checks, "modelsdev.cache", "pass", details)
	}

	names := make([]string, 0, len(eng.Config.Providers.Profiles))
	for name := range eng.Config.Providers.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prof := eng.Config.Providers.Profiles[name]
		profileStatus := "pass"
		if strings.TrimSpace(prof.Model) == "" || strings.TrimSpace(prof.Protocol) == "" || prof.MaxContext <= 0 || prof.MaxTokens <= 0 {
			profileStatus = "warn"
		}
		addDoctorCheck(checks, "provider."+name+".profile", profileStatus, formatProviderProfileSummary(engine.ProviderProfileStatus{
			Name:       name,
			Model:      prof.Model,
			Protocol:   prof.Protocol,
			BaseURL:    prof.BaseURL,
			MaxTokens:  prof.MaxTokens,
			MaxContext: prof.MaxContext,
			Configured: providerConfigured(name, prof),
		}))
		configured := providerConfigured(name, prof)
		if configured {
			addDoctorCheck(checks, "provider."+name+".configured", "pass", "credentials/endpoint present")
		} else {
			addDoctorCheck(checks, "provider."+name+".configured", "warn", "missing api_key or required endpoint")
		}
		for _, advisory := range config.ProviderProfileAdvisories(name, prof) {
			addDoctorCheck(checks, "provider."+name+".advisory", "warn", advisory)
		}
		if network && configured {
			status, details := providerReachabilityStatus(name, prof, timeout)
			addDoctorCheck(checks, "provider."+name+".network", status, details)
		}
	}
}

func summarizeDoctorChecks(checks []doctorCheck) (overall string, exitCode, passN, warnN, failN int) {
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failN++
		case "warn":
			warnN++
		default:
			passN++
		}
	}
	overall = "ok"
	if failN > 0 {
		return "fail", 1, passN, warnN, failN
	}
	if warnN > 0 {
		overall = "warn"
	}
	return overall, 0, passN, warnN, failN
}
