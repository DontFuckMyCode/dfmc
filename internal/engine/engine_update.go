package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// UpdateInfo holds version information from GitHub.
type UpdateInfo struct {
	LatestVersion   string    `json:"latest_version"`
	CurrentVersion  string    `json:"current_version"`
	UpdateAvailable bool      `json:"update_available"`
	ReleaseURL      string    `json:"release_url"`
	CheckedAt       time.Time `json:"checked_at"`
}

// CheckForUpdates polls GitHub for the latest release tag.
func (e *Engine) CheckForUpdates(ctx context.Context, currentVersion string) (UpdateInfo, error) {
	if strings.TrimSpace(currentVersion) == "" || currentVersion == "dev" {
		return UpdateInfo{CurrentVersion: "dev"}, nil
	}

	client := security.NewSafeHTTPClient(5*time.Second, "https://api.github.com")
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/dontfuckmycode/dfmc/releases/latest", nil)
	if err != nil {
		return UpdateInfo{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return UpdateInfo{}, err
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	info := UpdateInfo{
		LatestVersion:   release.TagName,
		CurrentVersion:  currentVersion,
		UpdateAvailable: latest != current,
		ReleaseURL:      release.HTMLURL,
		CheckedAt:       time.Now(),
	}

	e.mu.Lock()
	e.latestUpdate = info
	e.mu.Unlock()

	if info.UpdateAvailable {
		e.EventBus.Publish(Event{
			Type:   "engine:update_available",
			Source: "engine",
			Payload: map[string]any{
				"latest": release.TagName,
				"url":    release.HTMLURL,
			},
		})
	}

	return info, nil
}

// StartUpdateChecker kicks off a background goroutine that checks for updates
// periodically (default every 6 hours).
func (e *Engine) StartUpdateChecker(ctx context.Context, currentVersion string) {
	if currentVersion == "" || currentVersion == "dev" {
		return
	}

	go func() {
		// Initial check
		_, _ = e.CheckForUpdates(ctx, currentVersion)

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = e.CheckForUpdates(ctx, currentVersion)
			}
		}
	}()
}

// LatestUpdate returns the last known update info.
func (e *Engine) LatestUpdate() UpdateInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.latestUpdate
}
