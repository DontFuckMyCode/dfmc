// config_routing.go — routing rules, pipeline steps, and routing configuration.
// Extracted from config_types.go to keep the top-level Config struct readable.
// Routing rules govern provider/model selection per-query.
// Pipeline steps define ordered fallback chains.

package config

// RoutingConfig holds the ordered list of routing rules evaluated
// by the engine's router on every request.
type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

// RoutingRule describes one match condition and its assigned provider/model.
// Rules are evaluated top-down; the first matching rule wins.
type RoutingRule struct {
	Condition     string   `yaml:"condition"`
	Provider      string   `yaml:"provider"`
	Model         string   `yaml:"model,omitempty"`
	Priority      int      `yaml:"priority"`                  // higher = evaluated first
	ProviderTag   string   `yaml:"provider_tag,omitempty"`    // tag to match against Todo.ProviderTag
	WorkerClass   string   `yaml:"worker_class,omitempty"`    // worker class filter
	Verification  string   `yaml:"verification,omitempty"`     // verification level filter
	MinConfidence float64  `yaml:"min_confidence,omitempty"`  // minimum confidence threshold
	FileScope     []string `yaml:"file_scope,omitempty"`      // glob patterns for file scope matching
	Role          string   `yaml:"role,omitempty"`            // role filter
	Profile       string   `yaml:"profile,omitempty"`         // profile name to return on match
}

// PipelineConfig is a named ordered chain of provider+model steps.
// When active, the engine walks the steps in order on failure.
type PipelineConfig struct {
	Steps []PipelineStep `yaml:"steps"`
}

// PipelineStep is one entry in a PipelineConfig fallback chain.
type PipelineStep struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}
