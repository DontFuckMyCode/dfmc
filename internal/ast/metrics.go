package ast

import (
	"sync"
	"time"
)

type ParseMetrics struct {
	Requests             int64            `json:"requests"`
	Parsed               int64            `json:"parsed"`
	CacheHits            int64            `json:"cache_hits"`
	CacheMisses          int64            `json:"cache_misses"`
	Unsupported          int64            `json:"unsupported"`
	Errors               int64            `json:"errors"`
	TotalParseDurationMs int64            `json:"total_parse_duration_ms"`
	AvgParseDurationMs   float64          `json:"avg_parse_duration_ms"`
	LastLanguage         string           `json:"last_language,omitempty"`
	LastBackend          string           `json:"last_backend,omitempty"`
	ByLanguage           map[string]int64 `json:"by_language,omitempty"`
	ByBackend            map[string]int64 `json:"by_backend,omitempty"`
}

type parseMetricsTracker struct {
	mu                 sync.RWMutex
	requests           int64
	parsed             int64
	cacheHits          int64
	cacheMisses        int64
	unsupported        int64
	errors             int64
	totalParseDuration time.Duration
	lastLanguage       string
	lastBackend        string
	byLanguage         map[string]int64
	byBackend          map[string]int64
}

func newParseMetricsTracker() *parseMetricsTracker {
	return &parseMetricsTracker{
		byLanguage: map[string]int64{},
		byBackend:  map[string]int64{},
	}
}

func (m *parseMetricsTracker) recordRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
}

func (m *parseMetricsTracker) recordCacheHit(lang string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cacheHits++
	m.lastLanguage = lang
}

func (m *parseMetricsTracker) recordCacheMiss(lang string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cacheMisses++
	m.lastLanguage = lang
}

func (m *parseMetricsTracker) recordUnsupported(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unsupported++
	m.lastLanguage = ""
	m.lastBackend = ""
}

func (m *parseMetricsTracker) recordError(lang, backend string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors++
	m.lastLanguage = lang
	m.lastBackend = backend
}

func (m *parseMetricsTracker) recordParse(lang, backend string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parsed++
	m.totalParseDuration += duration
	m.lastLanguage = lang
	m.lastBackend = backend
	if lang != "" {
		m.byLanguage[lang]++
	}
	if backend != "" {
		m.byBackend[backend]++
	}
}

func (m *parseMetricsTracker) snapshot() ParseMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byLanguage := make(map[string]int64, len(m.byLanguage))
	for key, value := range m.byLanguage {
		byLanguage[key] = value
	}
	byBackend := make(map[string]int64, len(m.byBackend))
	for key, value := range m.byBackend {
		byBackend[key] = value
	}

	avg := 0.0
	if m.parsed > 0 {
		avg = float64(m.totalParseDuration.Milliseconds()) / float64(m.parsed)
	}

	return ParseMetrics{
		Requests:             m.requests,
		Parsed:               m.parsed,
		CacheHits:            m.cacheHits,
		CacheMisses:          m.cacheMisses,
		Unsupported:          m.unsupported,
		Errors:               m.errors,
		TotalParseDurationMs: m.totalParseDuration.Milliseconds(),
		AvgParseDurationMs:   avg,
		LastLanguage:         m.lastLanguage,
		LastBackend:          m.lastBackend,
		ByLanguage:           byLanguage,
		ByBackend:            byBackend,
	}
}

func (m *parseMetricsTracker) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = 0
	m.parsed = 0
	m.cacheHits = 0
	m.cacheMisses = 0
	m.unsupported = 0
	m.errors = 0
	m.totalParseDuration = 0
	m.lastLanguage = ""
	m.lastBackend = ""
	m.byLanguage = map[string]int64{}
	m.byBackend = map[string]int64{}
}

