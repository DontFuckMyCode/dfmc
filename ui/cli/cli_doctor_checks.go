package cli

// cli_doctor_checks.go — individual health probes invoked by runDoctor.
// Each helper appends one or more doctorCheck rows to the slice that
// runDoctor accumulates; the report rendering happens elsewhere
// (cli_doctor_render.go) so the probes don't carry presentation logic.
//
// Provider configuration + reachability lookups live here too —
// providerConfigured / providerReachabilityStatus / providerEndpoint —
// because they're a tightly coupled trio used by both runDoctor's
// per-provider loop and applyDoctorFixes' "is this provider usable
// before I pick it as primary" logic.

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

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
