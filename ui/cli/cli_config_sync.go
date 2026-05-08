package cli

// cli_config_sync.go — `config sync-models` handler. Pulls the
// models.dev catalog and merges providers.profiles.* with it,
// preserving API keys and any locally-overridden fields. Existing
// cli_config_sync_test.go pins the surface (see sibling file).

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runConfigSyncModels(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("config sync-models", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	global := fs.Bool("global", false, "write to ~/.dfmc/config.yaml")
	apiURL := fs.String("url", config.DefaultModelsDevAPIURL, "models.dev catalog url")
	rewriteBaseURL := fs.Bool("rewrite-base-url", true, "replace provider base_url values from models.dev")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models error: %v\n", err)
		return 1
	}
	targetPath := projectConfigPath(cwd)
	if *global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	catalog, err := config.FetchModelsDevCatalog(ctx, *apiURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models fetch error: %v\n", err)
		return 1
	}
	if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models cache error: %v\n", err)
		return 1
	}

	cloned, err := cloneConfig(eng.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models error: %v\n", err)
		return 1
	}
	beforeProfiles := map[string]config.ModelConfig{}
	for name, prof := range cloned.Providers.Profiles {
		beforeProfiles[name] = prof
	}
	cloned.Providers.Profiles = config.MergeProviderProfilesFromModelsDev(cloned.Providers.Profiles, catalog, config.ModelsDevMergeOptions{
		RewriteBaseURL: *rewriteBaseURL,
	})
	if strings.TrimSpace(cloned.Providers.Primary) == "" {
		cloned.Providers.Primary = eng.Config.Providers.Primary
	}
	if err := cloned.Save(targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models save error: %v\n", err)
		return 1
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "config sync-models reload error: %v\n", err)
		return 1
	}

	changes := diffProviderProfiles(beforeProfiles, cloned.Providers.Profiles)
	if jsonMode {
		_ = printJSON(map[string]any{
			"status":       "ok",
			"config_file":  targetPath,
			"cache_file":   config.ModelsDevCachePath(),
			"providers":    changes,
			"provider_n":   len(changes),
			"catalog_url":  strings.TrimSpace(*apiURL),
			"rewrite_base": *rewriteBaseURL,
		})
		return 0
	}
	fmt.Printf("Synced %d provider profile(s) from %s into %s\n", len(changes), strings.TrimSpace(*apiURL), targetPath)
	for _, line := range changes {
		fmt.Printf("- %s\n", line)
	}
	return 0
}
