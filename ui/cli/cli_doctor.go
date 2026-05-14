// cli_doctor.go — `dfmc doctor` health-report command. Aggregates the
// engine status snapshot plus the per-domain probes in
// cli_doctor_checks.go into a list of doctorCheck rows, then either
// emits JSON or hands the rows to the renderer in cli_doctor_render.go.
// Auto-fix logic lives in cli_doctor_fix.go.

package cli

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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

	if *fix {
		details, err := applyDoctorFixes(eng, *globalFix)
		if err != nil {
			addDoctorCheck(&checks, "doctor.fix", "warn", err.Error())
		} else {
			addDoctorCheck(&checks, "doctor.fix", "pass", details)
		}
	}

	if eng.Config == nil {
		addDoctorCheck(&checks, "config.loaded", "fail", "engine config is nil")
	} else {
		if *providersOnly {
			if len(eng.Config.Providers.Profiles) == 0 {
				addDoctorCheck(&checks, "config.providers", "fail", "providers.profiles is empty")
			} else {
				addDoctorCheck(&checks, "config.providers", "pass", "provider profiles are present")
			}
		} else if err := eng.Config.Validate(); err != nil {
			addDoctorCheck(&checks, "config.valid", "fail", err.Error())
		} else {
			addDoctorCheck(&checks, "config.valid", "pass", "configuration is valid")
		}
	}

	if !*providersOnly {
		addDoctorRuntimeChecks(eng, &checks)
	}

	if eng.Config != nil {
		addDoctorProviderChecks(eng, &checks, *network, *timeout)
	}

	overall, exitCode, passN, warnN, failN := summarizeDoctorChecks(checks)

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
