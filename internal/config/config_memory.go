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
	// LLM-driven post-turn memory update — the LLM is called after each
	// turn to suggest new episodic/semantic entries worth preserving.
	EnableLLMUpdate        bool    `yaml:"enable_llm_update"`
	LLMUpdateProvider      string  `yaml:"llm_update_provider"`
	LLMUpdateModel         string  `yaml:"llm_update_model"`
	LLMUpdatePrompt        string  `yaml:"llm_update_prompt"`
	LLMUpdateTimeoutMs     int     `yaml:"llm_update_timeout_ms"`
	LLMUpdateMaxEntries    int     `yaml:"llm_update_max_entries"`
	LLMUpdateMinConfidence float64 `yaml:"llm_update_min_confidence"`
	// LLMUpdateEnabled gates whether post-turn LLM update runs.
	LLMUpdateEnabled bool `yaml:"llm_update_enabled"`
	// LLMUpdateThreshold is minimum confidence (0.0-1.0) to add an entry.
	LLMUpdateThreshold float64 `yaml:"llm_update_threshold"`
}
