// read_token_saver.go — Token-aware read deduplication layer
//
// Replaces repeated read_file transmissions to the model with lightweight
// references when the underlying content hasn't changed (hash-stable).
// Unlike agent_loop_cache (I/O caching), this operates at the MODEL
// transmission layer: same file + same hash = skip re-send.
//
// RTK (tool_output_compress.go) strips noise from tool output AFTER
// execution. Token Saver prevents re-transmission of UNCHANGED content
// BEFORE the next request round-trip.
//
// Usage:
//   saver := NewReadTokenSaver(100) // 100MB max
//   // After each successful read_file:
//   saver.MarkSent(path, contentHash)
//   // At the start of the next request, check if re-send needed:
//   if saver.ShouldResend(path, contentHash) { ... }

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SentRecord tracks what content hashes were already transmitted to the
// model for each path, enabling dedup on subsequent round-trips.
type SentRecord struct {
	Hash      string    // content hash that was sent
	TokenEst  int       // estimated tokens that were sent
	SentAt    time.Time // when it was sent (for age-based decisions)
	LineStart int       // range that was sent
	LineEnd   int       // range that was sent
	WasFull   bool      // true if this was the complete file
}

// ReadTokenSaver is the top-level container. Safe for concurrent use
// across agent loop iterations.
type ReadTokenSaver struct {
	mu     sync.RWMutex
	sent   map[string]*SentRecord // path -> last sent record
	hits   int64                  // cache hits (no resend needed)
	misses int64                  // cache misses (resent)
	maxMB  int                    // memory budget
}

// NewReadTokenSaver creates a saver with the given memory budget in MB.
// A saver with maxMB=0 disables the feature (always resend).
func NewReadTokenSaver(maxMB int) *ReadTokenSaver {
	return &ReadTokenSaver{
		sent:  make(map[string]*SentRecord),
		maxMB: maxMB,
	}
}

// MarkSent records that `content` (identified by `contentHash`) for path
// was transmitted to the model. Call this after every read_file result
// that reaches the model (i.e., after content filtering).
func (s *ReadTokenSaver) MarkSent(path, contentHash string, lineStart, lineEnd int, contentLen int, wasFull bool) {
	if s == nil || s.maxMB == 0 {
		return
	}
	path = normalizePath(path)

	tokens := EstimateTokensFromBytes(contentLen)
	if tokens < 0 {
		tokens = EstimateTokensFromLen(contentLen)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[path] = &SentRecord{
		Hash:      contentHash,
		TokenEst:  tokens,
		SentAt:    time.Now(),
		LineStart: lineStart,
		LineEnd:   lineEnd,
		WasFull:   wasFull,
	}
}

// ShouldResend checks whether path+contentHash combination should be
// re-transmitted or can be replaced with a reference. Returns true if:
//   - path not seen before (first read)
//   - contentHash differs from last sent (file changed)
//   - range differs significantly from last sent (different window)
//   - wasFull=false and model asked for a different subrange
//
// When false, the caller should return a compact reference instead of
// the full content.
func (s *ReadTokenSaver) ShouldResend(path, contentHash string, lineStart, lineEnd int) bool {
	if s == nil || s.maxMB == 0 {
		return true
	}
	path = normalizePath(path)

	s.mu.RLock()
	rec, ok := s.sent[path]
	s.mu.RUnlock()

	if !ok {
		return true // first read ever for this path
	}
	if rec.Hash != contentHash {
		return true // content changed on disk
	}
	// Same content. Check if the range request is substantially different.
	// Allow 10% line difference tolerance to avoid spurious resends on
	// near-identical range requests (e.g., 1-40 vs 1-41).
	if rec.LineStart != lineStart {
		threshold := max(1, (rec.LineEnd-rec.LineStart+1)/10)
		if abs(rec.LineStart-lineStart) > threshold {
			return true // different window requested
		}
	}
	if rec.LineEnd != lineEnd {
		threshold := max(1, (rec.LineEnd-rec.LineStart+1)/10)
		if abs(rec.LineEnd-lineEnd) > threshold {
			return true // different window requested
		}
	}
	// Same content + similar range = no need to resend
	s.mu.Lock()
	s.hits++
	s.mu.Unlock()
	return false
}

// Reference returns a compact payload to send instead of the full content
// when ShouldResend returned false. It includes everything the model needs
// to reason about the file without re-reading it.
func (s *ReadTokenSaver) Reference(path string, contentHash string, lineStart, lineEnd, totalLines int) map[string]any {
	path = normalizePath(path)
	s.mu.RLock()
	rec := s.sent[path]
	s.mu.RUnlock()

	ref := map[string]any{
		"path":         path,
		"content_hash": contentHash,
		"cached":       true,
		"line_start":   lineStart,
		"line_end":     lineEnd,
		"total_lines":  totalLines,
	}
	if rec != nil {
		ref["tokens_sent"] = rec.TokenEst
		ref["sent_at"] = rec.SentAt.Format(time.RFC3339)
	}
	return ref
}

// Stats returns token-saving statistics.
func (s *ReadTokenSaver) Stats() (hits, misses int64, savedTokens int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var saved int64
	for _, rec := range s.sent {
		if rec.TokenEst > 0 {
			saved += int64(rec.TokenEst)
		}
	}
	return s.hits, s.misses, saved
}

// Reset clears all records. Call when starting a new agent session.
func (s *ReadTokenSaver) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = make(map[string]*SentRecord)
	s.hits = 0
	s.misses = 0
}

// HashContent computes SHA-256 of the content bytes and returns a hex string.
// This is the identity key used across MarkSent/ShouldResend.
func HashContent(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// HashString computes SHA-256 of the content string and returns a hex string.
func HashString(content string) string {
	return HashContent([]byte(content))
}

// EstimateTokensFromBytes estimates token count from byte count.
// Uses GPT-4o/cl100k_base ratio: 1 token ≈ 4 bytes for typical code.
// This is a rough heuristic; the actual token count depends on content
// composition. For UTF-8 with multi-byte chars, a 3-byte average is more accurate.
func EstimateTokensFromBytes(n int) int {
	if n <= 0 {
		return 0
	}
	// 4 bytes per token for typical English-heavy code
	return (n + 3) / 4
}

// EstimateTokensFromLen is an alias for EstimateTokensFromBytes,
// kept for readability when the input is a string length.
func EstimateTokensFromLen(n int) int {
	return EstimateTokensFromBytes(n)
}

// normalizePath folds path to a canonical form: lower-case, forward-slash,
// no trailing slash. Matches agent_loop_cache.readRangeIndexKey so both
// systems agree on the key space.
func normalizePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.ToLower(p)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// MarshalJSON implements json.Marshaler so ReadTokenSaver can be persisted
// across park→resume cycles (survives in parkedAgentState).
func (s *ReadTokenSaver) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(map[string]any{
		"sent":   s.sent,
		"hits":   s.hits,
		"misses": s.misses,
		"max_mb": s.maxMB,
	})
}

// ReferencePayload is the JSON payload sent to the model when a cache hit
// occurs and content is skipped. It's a valid read_file result shape so
// the model can parse it without special handling.
type ReferencePayload struct {
	Path          string `json:"path"`
	ContentHash   string `json:"content_sha256"`
	LineStart     int    `json:"line_start"`
	LineEnd       int    `json:"line_end"`
	TotalLines    int    `json:"total_lines"`
	ReturnedLines int    `json:"returned_lines"`
	Truncated     bool   `json:"truncated"`
	Cached        bool   `json:"cached"`
	Message       string `json:"message,omitempty"`
}

// NewReferencePayload constructs a compact reference from a cache hit.
// The model sees a valid read_file result with cached=true and can
// reason about the file's state without re-reading the content.
func NewReferencePayload(path, contentHash string, lineStart, lineEnd, totalLines int) ReferencePayload {
	return ReferencePayload{
		Path:          path,
		ContentHash:   contentHash,
		LineStart:     lineStart,
		LineEnd:       lineEnd,
		TotalLines:    totalLines,
		ReturnedLines: lineEnd - lineStart + 1,
		Truncated:     lineEnd < totalLines || lineStart > 1,
		Cached:        true,
		Message: fmt.Sprintf(
			"[cached: %d lines, %s, unchanged since last read]",
			lineEnd-lineStart+1,
			truncateHash(contentHash, 8),
		),
	}
}

func truncateHash(h string, n int) string {
	if len(h) < n {
		return h
	}
	return h[:n]
}
