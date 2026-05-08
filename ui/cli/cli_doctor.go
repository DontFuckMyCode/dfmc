// cli_doctor.go — `dfmc doctor` health-report command. Aggregates the
// engine status snapshot plus the per-domain probes in
// cli_doctor_checks.go into a list of doctorCheck rows, then either
// emits JSON or hands the rows to the renderer in cli_doctor_render.go.
// Auto-fix logic lives in cli_doctor_fix.go.

package cli

import (
	"context"
	"flag"
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

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass|warn|fail
	Details string `json:"details"`
}

func runDoctor(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	_ = ctx
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	network := fs.Bool("network", false, "check provider endpoint network reachability")
	timeout := fs.Duration("timeout", 2*time.Second, "network check timeout")
	providersOnly := fs.Bool("providers-only", false, "only run provider checks")
	fix := fs.Bool("fix", false, "attempt safe auto-fixes for config")
	globalFix := fs.Bool("global", false, "with --fix, update ~/.dfmc/config.yaml instead of project config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	checks := make([]doctorCheck, 0, 16)
	add := func(name, status, details string) {
		checks = append(checks, doctorCheck{
			Name:    name,
			Status:  status,
			Details: details,
		})
	}

	if *fix {
		details, err := applyDoctorFixes(eng, *globalFix)
		if err != nil {
			add("doctor.fix", "warn", err.Error())
		} else {
			add("doctor.fix", "pass", details)
		}
	}

	if eng.Config == nil {
		add("config.loaded", "fail", "engine config is nil")
	} else {
		if *providersOnly {
			if len(eng.Config.Providers.Profiles) == 0 {
				add("config.providers", "fail", "providers.profiles is empty")
			} else {
				add("config.providers", "pass", "provider profiles are present")
			}
		} else if err := eng.Config.Validate(); err != nil {
			add("config.valid", "fail", err.Error())
		} else {
			add("config.valid", "pass", "configuration is valid")
		}
	}

	if !*providersOnly {
		statusSnapshot := eng.Status()
		// Memory-tier degradation is a silent-killer class of issue —
		// the app still starts, conversations still run, but recall
		// won't find anything because the bbolt-backed episodic/
		// semantic tiers never loaded. Surface here so a user running
		// `dfmc doctor` after a weird startup sees it immediately.
		if statusSnapshot.MemoryDegraded {
			details := "episodic/semantic memory unavailable"
			if reason := strings.TrimSpace(statusSnapshot.MemoryLoadErr); reason != "" {
				details += ": " + reason
			}
			add("memory.tier", "warn", details)
		} else {
			add("memory.tier", "pass", "episodic/semantic tiers loaded")
		}
		if strings.TrimSpace(statusSnapshot.ASTBackend) == "" {
			add("ast.backend", "warn", "ast engine backend is unavailable")
		} else {
			status := "pass"
			details := statusSnapshot.ASTBackend
			if reason := strings.TrimSpace(statusSnapshot.ASTReason); reason != "" {
				details += ": " + reason
			}
			if statusSnapshot.ASTBackend == "regex" {
				status = "warn"
			}
			add("ast.backend", status, details)
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
			add("ast."+name, checkStatus, details)
		}
		metricsDetails := formatASTMetricsSummary(statusSnapshot.ASTMetrics)
		if strings.TrimSpace(metricsDetails) == "" {
			metricsDetails = "no parse activity recorded yet"
		}
		add("ast.metrics", "pass", metricsDetails)
		codemapDetails := formatCodeMapMetricsSummary(statusSnapshot.CodeMap)
		if strings.TrimSpace(codemapDetails) == "" {
			codemapDetails = "no codemap build activity recorded yet"
		}
		add("codemap.metrics", "pass", codemapDetails)
		root := strings.TrimSpace(statusSnapshot.ProjectRoot)
		if root == "" {
			add("project.root", "warn", "project root is empty")
		} else if st, err := os.Stat(root); err != nil {
			add("project.root", "fail", err.Error())
		} else if !st.IsDir() {
			add("project.root", "fail", "project root is not a directory")
		} else {
			add("project.root", "pass", root)
		}
		addMagicDocHealthCheck(&checks, root, 24*time.Hour)
		addPromptHealthCheck(&checks, root, 450)

		if eng.Config != nil {
			addFileSystemHealthCheck(&checks, "storage.data_dir", eng.Config.DataDir())
			addFileSystemHealthCheck(&checks, "plugins.dir", eng.Config.PluginDir())
		}

		globalPath, projectPath := config.ConfigPaths("")
		for _, path := range []string{globalPath, projectPath} {
			if path == "" {
				continue
			}
			if msg := hooks.CheckConfigPermissions(path); msg != "" {
				add("config.permissions", "warn", msg)
			} else {
				add("config.permissions", "pass", "ok")
			}
		}

		for _, bin := range []string{"git", "go"} {
			if path, err := exec.LookPath(bin); err != nil {
				add("dependency."+bin, "warn", "not found in PATH")
			} else {
				add("dependency."+bin, "pass", path)
			}
		}
	}

	if eng.Config != nil {
		cache := eng.Status().ModelsDevCache
		if !cache.Exists {
			add("modelsdev.cache", "warn", "missing: "+cache.Path)
		} else {
			details := cache.Path
			if !cache.UpdatedAt.IsZero() {
				details += " updated " + cache.UpdatedAt.Format(time.RFC3339)
			}
			if cache.SizeBytes > 0 {
				details += fmt.Sprintf(" size=%d", cache.SizeBytes)
			}
			add("modelsdev.cache", "pass", details)
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
			add("provider."+name+".profile", profileStatus, formatProviderProfileSummary(engine.ProviderProfileStatus{
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
				add("provider."+name+".configured", "pass", "credentials/endpoint present")
			} else {
				add("provider."+name+".configured", "warn", "missing api_key or required endpoint")
			}
			for _, advisory := range config.ProviderProfileAdvisories(name, prof) {
				add("provider."+name+".advisory", "warn", advisory)
			}
			if *network && configured {
				status, details := providerReachabilityStatus(name, prof, *timeout)
				add("provider."+name+".network", status, details)
			}
		}
	}

	failN, warnN, passN := 0, 0, 0
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
	exitCode := 0
	overall := "ok"
	if failN > 0 {
		exitCode = 1
		overall = "fail"
	} else if warnN > 0 {
		overall = "warn"
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status":   overall,
			"summary":  map[string]int{"pass": passN, "warn": warnN, "fail": failN},
			"checks":   checks,
			"network":  *network,
			"timeout":  timeout.String(),
			"fix":      *fix,
			"scope":    map[bool]string{true: "providers", false: "full"}[*providersOnly],
			"provider": eng.Status().Provider,
		})
		return exitCode
	}

	renderDoctorReport(checks, overall, passN, warnN, failN)
	return exitCode
}
