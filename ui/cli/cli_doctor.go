// Doctor CLI subcommand: the `dfmc doctor` health-report surface plus
// its auto-fix helpers and the small pile of provider/config/prompt
// health probes it drives. Extracted from cli_admin.go.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
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

	fmt.Println("DFMC doctor report")
	for _, c := range checks {
		fmt.Printf("[%s] %s: %s\n", strings.ToUpper(c.Status), c.Name, c.Details)
	}
	fmt.Printf("Summary: pass=%d warn=%d fail=%d\n", passN, warnN, failN)
	return exitCode
}

func applyDoctorFixes(eng *engine.Engine, global bool) (string, error) {
	if eng == nil {
		return "", fmt.Errorf("engine is nil")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return "", err
	}
	if len(currentMap) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		currentMap = defMap
	}

	if _, ok := getConfigPath(currentMap, "version"); !ok {
		if err := setConfigPath(currentMap, "version", config.DefaultVersion); err != nil {
			return "", err
		}
	}
	if _, ok := getConfigPath(currentMap, "providers.profiles"); !ok {
		if err := setConfigPath(currentMap, "providers.profiles", config.DefaultConfig().Providers.Profiles); err != nil {
			return "", err
		}
	}

	profiles := map[string]any{}
	if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
		switch v := raw.(type) {
		case map[string]any:
			profiles = v
		case map[any]any:
			for k, val := range v {
				key := strings.TrimSpace(fmt.Sprint(k))
				if key != "" {
					profiles[key] = val
				}
			}
		}
	}
	if len(profiles) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		if err := setConfigPath(currentMap, "providers.profiles", defMap["providers"].(map[string]any)["profiles"]); err != nil {
			return "", err
		}
		if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
			if v, ok := raw.(map[string]any); ok {
				profiles = v
			}
		}
	}

	rawPrimary, _ := getConfigPath(currentMap, "providers.primary")
	primary := strings.TrimSpace(fmt.Sprint(rawPrimary))
	if primary == "" || !profilesHasKey(profiles, primary) {
		primary = choosePreferredProvider(profiles, config.DefaultConfig().Providers.Primary)
		if primary == "" {
			primary = config.DefaultConfig().Providers.Primary
		}
		if err := setConfigPath(currentMap, "providers.primary", primary); err != nil {
			return "", err
		}
	}

	if raw, ok := getConfigPath(currentMap, "web.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" {
			if err := setConfigPath(currentMap, "web.auth", "none"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "remote.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" && auth != "mtls" {
			if err := setConfigPath(currentMap, "remote.auth", "token"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "providers.profiles.zai"); ok {
		if profileMap, ok := raw.(map[string]any); ok {
			modelCfg := modelConfigFromAny(profileMap)
			if advisories := config.ProviderProfileAdvisories("zai", modelCfg); len(advisories) > 0 {
				profileMap["protocol"] = "openai-compatible"
				profileMap["base_url"] = "https://api.z.ai/api/paas/v4"
				if err := setConfigPath(currentMap, "providers.profiles.zai", profileMap); err != nil {
					return "", err
				}
			}
		}
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return "", err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return "", fmt.Errorf("fix applied but reload failed (reverted): %w", err)
	}

	return "updated " + targetPath, nil
}

func profilesHasKey(profiles map[string]any, name string) bool {
	for k := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func choosePreferredProvider(profiles map[string]any, fallback string) string {
	preferredOrder := []string{
		"anthropic",
		"openai",
		"deepseek",
		"google",
		"zai",
		"generic",
		"alibaba",
		"kimi",
		"minimax",
	}
	for _, name := range preferredOrder {
		prof, ok := profileByName(profiles, name)
		if !ok {
			continue
		}
		modelCfg := modelConfigFromAny(prof)
		if providerConfigured(name, modelCfg) {
			return name
		}
	}
	for _, name := range preferredOrder {
		if profilesHasKey(profiles, name) {
			return name
		}
	}
	if profilesHasKey(profiles, fallback) {
		return fallback
	}
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, strings.TrimSpace(k))
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func profileByName(profiles map[string]any, name string) (any, bool) {
	for k, v := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return v, true
		}
	}
	return nil, false
}

func modelConfigFromAny(v any) config.ModelConfig {
	out := config.ModelConfig{}
	switch m := v.(type) {
	case map[string]any:
		if raw, ok := m["api_key"]; ok {
			out.APIKey = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["base_url"]; ok {
			out.BaseURL = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["model"]; ok {
			out.Model = strings.TrimSpace(fmt.Sprint(raw))
		}
	case config.ModelConfig:
		out = m
	}
	return out
}

func addFileSystemHealthCheck(checks *[]doctorCheck, name, dir string) {
	if strings.TrimSpace(dir) == "" {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: "path is empty"})
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: err.Error()})
		return
	}
	probe, err := os.CreateTemp(dir, ".dfmc-health-*")
	if err != nil {
		*checks = append(*checks, doctorCheck{Name: name, Status: "fail", Details: "not writable: " + err.Error()})
		return
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	*checks = append(*checks, doctorCheck{Name: name, Status: "pass", Details: dir})
}

func addMagicDocHealthCheck(checks *[]doctorCheck, projectRoot string, staleAfter time.Duration) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: "project root is empty (cannot evaluate magic doc)",
		})
		return
	}
	if staleAfter <= 0 {
		staleAfter = 24 * time.Hour
	}
	path := resolveMagicDocPath(root, "")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			*checks = append(*checks, doctorCheck{
				Name:    "magicdoc.health",
				Status:  "warn",
				Details: fmt.Sprintf("missing: %s (run: dfmc magicdoc update)", path),
			})
			return
		}
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: fmt.Sprintf("cannot read %s: %v", path, err),
		})
		return
	}
	age := time.Since(info.ModTime())
	if age > staleAfter {
		*checks = append(*checks, doctorCheck{
			Name:    "magicdoc.health",
			Status:  "warn",
			Details: fmt.Sprintf("stale (%s): %s (run: dfmc magicdoc update)", age.Round(time.Minute), path),
		})
		return
	}
	*checks = append(*checks, doctorCheck{
		Name:    "magicdoc.health",
		Status:  "pass",
		Details: fmt.Sprintf("fresh (%s): %s", age.Round(time.Minute), path),
	})
}

func addPromptHealthCheck(checks *[]doctorCheck, projectRoot string, maxTemplateTokens int) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(strings.TrimSpace(projectRoot))
	report := promptlib.BuildStatsReport(lib.List(), promptlib.StatsOptions{
		MaxTemplateTokens: maxTemplateTokens,
	})
	if report.TemplateCount == 0 {
		*checks = append(*checks, doctorCheck{
			Name:    "prompt.health",
			Status:  "warn",
			Details: "no prompt templates loaded",
		})
		return
	}
	if report.WarningCount > 0 {
		first := ""
		for _, t := range report.Templates {
			if len(t.Warnings) == 0 {
				continue
			}
			first = t.ID + ": " + t.Warnings[0]
			break
		}
		msg := fmt.Sprintf("warnings=%d templates=%d threshold=%d", report.WarningCount, report.TemplateCount, report.MaxTemplateTokens)
		if strings.TrimSpace(first) != "" {
			msg += " first=" + first
		}
		*checks = append(*checks, doctorCheck{
			Name:    "prompt.health",
			Status:  "warn",
			Details: msg,
		})
		return
	}
	*checks = append(*checks, doctorCheck{
		Name:    "prompt.health",
		Status:  "pass",
		Details: fmt.Sprintf("templates=%d total_tokens=%d max_tokens=%d", report.TemplateCount, report.TotalTokens, report.MaxTokens),
	})
}

func providerConfigured(name string, prof config.ModelConfig) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	apiKey := strings.TrimSpace(prof.APIKey)
	baseURL := strings.TrimSpace(prof.BaseURL)

	switch name {
	case "generic":
		return baseURL != ""
	default:
		return apiKey != "" || baseURL != ""
	}
}

func providerReachabilityStatus(name string, prof config.ModelConfig, timeout time.Duration) (string, string) {
	target, err := providerEndpoint(name, prof)
	if err != nil {
		return "warn", err.Error()
	}
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return "warn", fmt.Sprintf("dial %s failed: %v", target, err)
	}
	_ = conn.Close()
	return "pass", "reachable: " + target
}

func providerEndpoint(name string, prof config.ModelConfig) (string, error) {
	if strings.TrimSpace(prof.BaseURL) != "" {
		u, err := url.Parse(strings.TrimSpace(prof.BaseURL))
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		if strings.TrimSpace(u.Host) == "" {
			return "", fmt.Errorf("invalid base_url host")
		}
		if strings.Contains(u.Host, ":") {
			return u.Host, nil
		}
		if strings.EqualFold(u.Scheme, "http") {
			return net.JoinHostPort(u.Host, "80"), nil
		}
		return net.JoinHostPort(u.Host, "443"), nil
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return "api.anthropic.com:443", nil
	case "openai":
		return "api.openai.com:443", nil
	case "google":
		return "generativelanguage.googleapis.com:443", nil
	case "deepseek":
		return "api.deepseek.com:443", nil
	case "kimi":
		return "api.moonshot.cn:443", nil
	case "minimax":
		return "api.minimax.chat:443", nil
	case "zai":
		return "api.z.ai:443", nil
	case "alibaba":
		return "dashscope.aliyuncs.com:443", nil
	default:
		return "", fmt.Errorf("no endpoint mapping for provider %q", name)
	}
}
