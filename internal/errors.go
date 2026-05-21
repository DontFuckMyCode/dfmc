// Package errors provides sentinel errors for the DFMC project.
// These errors are used across packages to signal specific failure modes.
package errors

import (
	"errors"
	"fmt"
)

// Sentinel errors — public, reusable, errors.Is compatible.
var (
	ErrCacheMiss     = errors.New("cache miss")
	ErrNotFound      = errors.New("not found")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrRateLimited   = errors.New("rate limited")
	ErrProviderDown  = errors.New("provider unavailable")
	ErrContextBudget = errors.New("context budget exceeded")
	ErrSyntax        = errors.New("syntax error")
	ErrParse         = errors.New("parse error")
)

// New creates a formatted error with a cause.
func New(msg string) error {
	return errors.New(msg)
}

// Wrap adds context to an error.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}
