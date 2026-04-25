package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// AnthropicProvider accessors

func TestAnthropicProvider_Name(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	if p.Name() != "anthropic" {
		t.Errorf("Name(): got %q", p.Name())
	}
}

func TestAnthropicProvider_Name_Custom(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	// The Name() method returns "anthropic" regardless when name field is not set
	if p.Name() == "" {
		t.Error("Name() should not be empty")
	}
}

func TestAnthropicProvider_Model(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	if p.Model() != "claude-opus-4-7" {
		t.Errorf("Model(): got %q", p.Model())
	}
}

func TestAnthropicProvider_Models(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	models := p.Models()
	if len(models) != 1 || models[0] != "claude-opus-4-7" {
		t.Errorf("Models(): got %v", models)
	}
}

func TestAnthropicProvider_CountTokens(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	got := p.CountTokens("one two three four five")
	if got != 5 {
		t.Errorf("CountTokens: got %d", got)
	}
}

func TestAnthropicProvider_MaxContext(t *testing.T) {
	// maxTokens=64000, maxContext=1000000
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	if p.MaxContext() != 1000000 {
		t.Errorf("MaxContext with maxContext>0: got %d", p.MaxContext())
	}

	// maxTokens=64000, maxContext=0 -> falls back to 1000000
	p2 := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 0)
	if p2.MaxContext() != 1000000 {
		t.Errorf("MaxContext with maxContext=0: got %d", p2.MaxContext())
	}
}

func TestAnthropicProvider_Hints(t *testing.T) {
	p := NewAnthropicProvider("claude-opus-4-7", "test-key", "https://api.anthropic.com", 64000, 1000000)
	hints := p.Hints()
	if hints.ToolStyle != "tool_use" {
		t.Errorf("Hints().ToolStyle: got %q", hints.ToolStyle)
	}
	if !hints.Cache {
		t.Error("Hints().Cache should be true")
	}
	if len(hints.BestFor) == 0 {
		t.Error("Hints().BestFor should not be empty")
	}
}

// GoogleProvider accessors

func TestGoogleProvider_Name(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	p := NewGoogleProvider("gemini-2.5-pro", "test-key", srv.URL, 64000, 1000000, 30*time.Second)
	if p.Name() != "google" {
		t.Errorf("Name(): got %q", p.Name())
	}
}

func TestGoogleProvider_Model(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	p := NewGoogleProvider("gemini-2.5-pro", "test-key", srv.URL, 64000, 1000000, 30*time.Second)
	if p.Model() != "gemini-2.5-pro" {
		t.Errorf("Model(): got %q", p.Model())
	}
}

func TestGoogleProvider_Models(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	p := NewGoogleProvider("gemini-2.5-pro", "test-key", srv.URL, 64000, 1000000, 30*time.Second)
	models := p.Models()
	if len(models) != 1 || models[0] != "gemini-2.5-pro" {
		t.Errorf("Models(): got %v", models)
	}
}

// OfflineProvider accessors

func TestOfflineProvider_Name(t *testing.T) {
	p := NewOfflineProvider()
	if p.Name() != "offline" {
		t.Errorf("Name(): got %q", p.Name())
	}
}

func TestOfflineProvider_Model(t *testing.T) {
	p := NewOfflineProvider()
	if p.Model() != "offline-analyzer-v1" {
		t.Errorf("Model(): got %q", p.Model())
	}
}

func TestOfflineProvider_Models(t *testing.T) {
	p := NewOfflineProvider()
	models := p.Models()
	if len(models) != 1 || models[0] != "offline-analyzer-v1" {
		t.Errorf("Models(): got %v", models)
	}
}

func TestOfflineProvider_CountTokens(t *testing.T) {
	p := NewOfflineProvider()
	got := p.CountTokens("one two three")
	if got != 3 {
		t.Errorf("CountTokens: got %d", got)
	}
}

func TestOfflineProvider_MaxContext(t *testing.T) {
	p := NewOfflineProvider()
	if p.MaxContext() != 12000 {
		t.Errorf("MaxContext: got %d", p.MaxContext())
	}
}

func TestOfflineProvider_Hints(t *testing.T) {
	p := NewOfflineProvider()
	hints := p.Hints()
	if hints.ToolStyle != "none" {
		t.Errorf("Hints().ToolStyle: got %q", hints.ToolStyle)
	}
	if hints.Cache {
		t.Error("Hints().Cache should be false for offline")
	}
	if !hints.LowLatency {
		t.Error("Hints().LowLatency should be true for offline")
	}
	if len(hints.BestFor) == 0 {
		t.Error("Hints().BestFor should not be empty")
	}
}
