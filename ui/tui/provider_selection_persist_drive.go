package tui

// provider_selection_persist_drive.go — drive routing + pipelines
// write-side YAML editing. These persistors share the
// read-merge-write contract used by the provider persistors in
// provider_selection_persist.go (so hand-edited fields survive every
// save) but they live in a sibling because they touch unrelated
// keys ("drive.routing", "pipelines") and depend on the project-only
// scope rather than the dynamic effectivePersistScope used by the
// provider primary/fallback writers.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) loadDriveRoutingFromProjectConfig() map[string]string {
	path, err := m.projectConfigPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	driveNode := ensureStringAnyMap(doc, "drive")
	routingNode := ensureStringAnyMap(driveNode, "routing")
	out := make(map[string]string, len(routingNode))
	for k, v := range routingNode {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (m Model) persistDriveRoutingProjectConfig(routing map[string]string) (string, error) {
	path, err := m.projectConfigPath()
	if err != nil {
		return "", err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return "", fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	driveNode := ensureStringAnyMap(doc, "drive")
	if len(routing) == 0 {
		delete(driveNode, "routing")
	} else {
		driveNode["routing"] = routing
	}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return "", fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", fmt.Errorf("write project config: %w", err)
	}
	return path, nil
}

func (m Model) persistPipelinesProjectConfig(name string, steps []config.PipelineStep) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("pipeline name is empty")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return "", err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return "", fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	pipelinesNode := ensureStringAnyMap(doc, "pipelines")
	stepsNode := []map[string]any{}
	for _, step := range steps {
		stepsNode = append(stepsNode, map[string]any{
			"provider": strings.TrimSpace(step.Provider),
			"model":    strings.TrimSpace(step.Model),
		})
	}
	pipelinesNode[name] = map[string]any{"steps": stepsNode}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return "", fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", fmt.Errorf("write project config: %w", err)
	}

	if err := m.reloadEngineConfig(); err != nil {
		return path, fmt.Errorf("reload engine: %w", err)
	}
	return path, nil
}
