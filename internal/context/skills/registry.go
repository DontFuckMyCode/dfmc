// registry.go - skill registry and configuration loading.
//
// The registry maintains a global list of skills that can be:
// - Extended at startup via Register()
// - Overridden via YAML config
// - Queried at runtime for skill metadata

package skills

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	mu       sync.RWMutex
	registry []Skill = DefaultSkills()
)

// Register adds a skill to the registry.
// If a skill with the same Name already exists, it is replaced.
func Register(s Skill) {
	mu.Lock()
	defer mu.Unlock()

	// Remove existing skill with same name
	newRegistry := make([]Skill, 0, len(registry)+1)
	for _, existing := range registry {
		if existing.Name != s.Name {
			newRegistry = append(newRegistry, existing)
		}
	}
	newRegistry = append(newRegistry, s)
	registry = newRegistry
}

// Unregister removes a skill from the registry by name.
func Unregister(name string) {
	mu.Lock()
	defer mu.Unlock()

	newRegistry := make([]Skill, 0, len(registry))
	for _, skill := range registry {
		if skill.Name != name {
			newRegistry = append(newRegistry, skill)
		}
	}
	registry = newRegistry
}

// Get returns a skill by name, or nil if not found.
func Get(name string) *Skill {
	mu.RLock()
	defer mu.RUnlock()

	for i := range registry {
		if registry[i].Name == name {
			return &registry[i]
		}
	}
	return nil
}

// List returns all registered skills.
func List() []Skill {
	mu.RLock()
	defer mu.RUnlock()

	result := make([]Skill, len(registry))
	copy(result, registry)
	return result
}

// LoadFromFile loads skill definitions from a YAML file.
// The file format:
//
//   skills:
//     - name: custom_skill
//       role: custom_role
//       profile: deep
//       keywords:
//         - keyword1
//         - keyword2
//       tools:
//         - tool1
//         - tool2
//
// Environment variables in the file are expanded.
func LoadFromFile(path string) error {
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read skills config: %w", err)
	}

	// Expand environment variables
	data = []byte(os.ExpandEnv(string(data)))

	var cfg struct {
		Skills []Skill `yaml:"skills"`
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse skills config: %w", err)
	}

	// Merge or replace based on merge strategy
	for _, skill := range cfg.Skills {
		found := false
		for i, existing := range registry {
			if existing.Name == skill.Name {
				registry[i] = skill
				found = true
				break
			}
		}
		if !found {
			registry = append(registry, skill)
		}
	}

	return nil
}

// Reset restores the registry to default skills.
func Reset() {
	mu.Lock()
	defer mu.Unlock()

	registry = DefaultSkills()
}
