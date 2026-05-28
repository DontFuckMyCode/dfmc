// Package tokens provides token count estimation for prompts, context chunks,
// and tool results.
package tokens

import (
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

// TiktokenCounter uses the tiktoken library for accurate token counting
// using the official BPE encodings.
type TiktokenCounter struct {
	encoding string
	tk       *tiktoken.Tiktoken
	mu       sync.RWMutex
}

// NewTiktokenCounter loads the tiktoken encoding lazily. encoding is the
// tiktoken encoding name (e.g. "cl100k_base", "o200k_base"). Returns an
// error if the encoding cannot be loaded.
func NewTiktokenCounter(encoding string) (*TiktokenCounter, error) {
	c := &TiktokenCounter{encoding: encoding}
	if err := c.init(); err != nil {
		return nil, err
	}
	return c, nil
}

// init loads the encoding in a thread-safe manner.
func (c *TiktokenCounter) init() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tk != nil {
		return nil
	}
	tk, err := tiktoken.EncodingForModel(c.encoding)
	if err != nil {
		return err
	}
	c.tk = tk
	return nil
}

// Count returns the number of tokens in text using the loaded tiktoken encoding.
func (c *TiktokenCounter) Count(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	c.mu.RLock()
	tk := c.tk
	c.mu.RUnlock()
	if tk == nil {
		return 0
	}
	return len(tk.Encode(text, nil, nil))
}

// CountMessages is part of the Counter interface.
func (c *TiktokenCounter) CountMessages(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}
	total := 2 // minimal sequence overhead
	for _, m := range msgs {
		total += 4 // per-message overhead
		total += c.Count(m.Role)
		total += c.Count(m.Content)
	}
	return total
}
