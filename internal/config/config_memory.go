// config_memory.go — episodic and semantic memory configuration.
// Extracted from config_types.go to keep the top-level Config struct readable.

package config

// MemoryConfig governs the two-tier memory system: episodic (conversation
// events) and semantic (embedded facts). Zero values fall back to defaults
// in defaults.go.
type MemoryConfig struct {
	Enabled               bool    `yaml:"enabled"`
	MaxEpisodic           int     `yaml:"max_episodic"`
	MaxSemantic           int     `yaml:"max_semantic"`
	ConsolidationInterval string  `yaml:"consolidation_interval"`
	DecayRate             float64 `yaml:"decay_rate"`
}
