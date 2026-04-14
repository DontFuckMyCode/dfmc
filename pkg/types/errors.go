package types

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
)

type ErrorKind int

const (
	ErrConfig ErrorKind = iota
	ErrProvider
	ErrRateLimit
	ErrToolExec
	ErrParse
	ErrPermission
	ErrNotFound
	ErrTimeout
	ErrInternal
)

type DFMCError struct {
	Kind    ErrorKind
	Message string
	Cause   error
	Context map[string]any
}

func (e *DFMCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *DFMCError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsRateLimit(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrRateLimit
}

func IsTimeout(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrTimeout
}

func IsConfig(err error) bool {
	var de *DFMCError
	return errors.As(err, &de) && de.Kind == ErrConfig
}

func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] %s: %v\n%s", name, r, string(debug.Stack()))
			}
		}()
		fn()
	}()
}
