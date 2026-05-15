// Package jsonutil provides ergonomic helpers around stdlib encoding/json.
// All functions panic on error — use only when the caller owns the data
// and error is guaranteed not to occur.
//
// For non-panicking variants, use json.Marshal / json.Unmarshal directly.
package jsonutil

import "encoding/json"

// MustMarshal serializes v to JSON bytes. Panics on error.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
