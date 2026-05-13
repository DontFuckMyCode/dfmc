// Package jsonutil provides ergonomic helpers around stdlib encoding/json.
// All functions panic on error — use only when the caller owns the data
// and error is guaranteed not to occur.
//
// For non-panicking variants, use json.Marshal / json.Unmarshal directly.
package jsonutil

import (
	"encoding/json"
	"io"
	"os"
)

// MustMarshal serializes v to JSON bytes. Panics on error.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// MustMarshalIndent serializes v to pretty-printed JSON with given prefix and indent.
// Panics on error.
func MustMarshalIndent(v any, prefix, indent string) string {
	b, err := json.MarshalIndent(v, prefix, indent)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// MustMarshalToWriter marshals v and writes to w. Panics on error.
func MustMarshalToWriter(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
}

// MustUnmarshal deserializes data into v. Panics on error.
// v must be a pointer.
func MustUnmarshal(data []byte, v any) {
	if err := json.Unmarshal(data, v); err != nil {
		panic(err)
	}
}

// MustReadFile reads and unmarshals a JSON file into v. Panics on error.
func MustReadFile(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	MustUnmarshal(data, v)
}
