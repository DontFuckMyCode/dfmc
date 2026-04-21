package drive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestFailureClassify_Transient(t *testing.T) {
	cases := []struct {
		err  error
		want FailureClass
	}{
		{errors.New("context deadline exceeded"), RetryTransient},
		{errors.New("i/o timeout"), RetryTransient},
		{errors.New("request timed out after 30s"), RetryTransient},
		{errors.New("connection timed out"), RetryTransient},
		{errors.New("rate limit exceeded, retry after 2s"), RetryTransient},
		{errors.New("status code 429: too many requests"), RetryTransient},
	}
	for _, tc := range cases {
		got := FailureClassify(tc.err)
		if got != tc.want {
			t.Errorf("FailureClassify(%q): got %v, want %v", tc.err.Error(), got, tc.want)
		}
	}
}

func TestFailureClassify_FallbackWorthy(t *testing.T) {
	cases := []struct {
		err  error
		want FailureClass
	}{
		{errors.New("status code 500: internal server error"), RetryWithFallback},
		{errors.New("status code 502: bad gateway"), RetryWithFallback},
		{errors.New("status code 503: service unavailable"), RetryWithFallback},
		{errors.New("status code 504: gateway timeout"), RetryWithFallback},
		{errors.New("model overloaded, retry later"), RetryWithFallback},
		{errors.New("no such host"), RetryWithFallback},
		{errors.New("connection refused"), RetryWithFallback},
		{errors.New("connection reset by peer"), RetryWithFallback},
	}
	for _, tc := range cases {
		got := FailureClassify(tc.err)
		if got != tc.want {
			t.Errorf("FailureClassify(%q): got %v, want %v", tc.err.Error(), got, tc.want)
		}
	}
}

func TestFailureClassify_Fatal(t *testing.T) {
	cases := []struct {
		err  error
		want FailureClass
	}{
		{errors.New("tool read_file denied: user denied"), Fatal},
		{errors.New("tool apply_patch denied: approval denied"), Fatal},
		{errors.New("user denied the tool call"), Fatal},
		{errors.New("unauthorized: invalid API key"), Fatal},
		{errors.New("x509: certificate signed by unknown authority"), Fatal},
		{errors.New("certificate is expired"), Fatal},
		{context.Canceled, Fatal},
	}
	for _, tc := range cases {
		got := FailureClassify(tc.err)
		if got != tc.want {
			t.Errorf("FailureClassify(%q): got %v, want %v", tc.err.Error(), got, tc.want)
		}
	}
}

func TestFailureClassify_DefaultTransient(t *testing.T) {
	// Unknown errors default to RetryTransient.
	got := FailureClassify(errors.New("something completely unexpected happened"))
	if got != RetryTransient {
		t.Errorf("FailureClassify(unknown): got %v, want RetryTransient", got)
	}
}

func TestFailureClassify_NilIsFatal(t *testing.T) {
	// nil should not reach here in practice, but classify it safely.
	got := FailureClassify(nil)
	if got != Fatal {
		t.Errorf("FailureClassify(nil): got %v, want Fatal", got)
	}
}

func TestFailureClassify_Substrings(t *testing.T) {
	// Classification must use case-insensitive or substring matching
	// as documented — verify key substrings trigger the right class.
	substrings := map[string]FailureClass{
		"TOOL edit_file DENIED: user denied": Fatal,
		"STATUS CODE 429":                    RetryTransient,
		"timeout after":                      RetryTransient,
		"STATUS CODE 500":                    RetryWithFallback,
		"no such host":                       RetryWithFallback,
	}
	for msg, want := range substrings {
		got := FailureClassify(errors.New(msg))
		if got != want {
			t.Errorf("FailureClassify(%q): got %v, want %v", msg, got, want)
		}
	}
}

func TestFailureClassify_Helpers(t *testing.T) {
	errTransient := errors.New("i/o timeout")
	errFallback := errors.New("status code 503")
	errFatal := errors.New("tool denied: user denied")

	if !FailureClassifyIsTransient(errTransient) {
		t.Error("IsTransient should be true for transient error")
	}
	if !FailureClassifyIsFallbackWorthy(errFallback) {
		t.Error("IsFallbackWorthy should be true for fallback error")
	}
	if !FailureClassifyIsFatal(errFatal) {
		t.Error("IsFatal should be true for fatal error")
	}
}

func TestDecide_FatalNoRetry(t *testing.T) {
	d := &Driver{cfg: Config{Retries: 2}}
	dec := d.Decide(errors.New("tool denied: user denied"), 0, 2)
	if dec.Class != Fatal || dec.Retried {
		t.Fatalf("fatal error: class=%v retried=%v; want Fatal,false", dec.Class, dec.Retried)
	}
}

func TestDecide_TransientRetries(t *testing.T) {
	d := &Driver{cfg: Config{Retries: 2}}
	// First attempt — should retry.
	dec := d.Decide(errors.New("i/o timeout"), 0, 2)
	if dec.Class != RetryTransient || !dec.Retried {
		t.Fatalf("attempt 0: class=%v retried=%v; want RetryTransient,true", dec.Class, dec.Retried)
	}
	// Second attempt — still under cap, retry again.
	dec = d.Decide(errors.New("i/o timeout"), 1, 2)
	if dec.Class != RetryTransient || !dec.Retried {
		t.Fatalf("attempt 1: class=%v retried=%v; want RetryTransient,true", dec.Class, dec.Retried)
	}
	// Third attempt — over cap, stop.
	dec = d.Decide(errors.New("i/o timeout"), 2, 2)
	if dec.Class != RetryTransient || dec.Retried {
		t.Fatalf("attempt 2: class=%v retried=%v; want RetryTransient,false", dec.Class, dec.Retried)
	}
}

func TestDecide_FallbackWorthyWithAttempts(t *testing.T) {
	d := &Driver{cfg: Config{Retries: 1}}
	dec := d.Decide(errors.New("status code 503"), 0, 1)
	if dec.Class != RetryWithFallback || !dec.Retried {
		t.Fatalf("attempt 0: class=%v retried=%v; want RetryWithFallback,true", dec.Class, dec.Retried)
	}
	dec = d.Decide(errors.New("status code 503"), 1, 1)
	if dec.Class != RetryWithFallback || dec.Retried {
		t.Fatalf("attempt 1: class=%v retried=%v; want RetryWithFallback,false", dec.Class, dec.Retried)
	}
}

func TestFailureClass_String(t *testing.T) {
	cases := []struct {
		c    FailureClass
		want string
	}{
		{RetryTransient, "transient"},
		{RetryWithFallback, "fallback"},
		{Fatal, "fatal"},
		{FailureClass(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("FailureClass(%d).String(): got %q, want %q", tc.c, got, tc.want)
		}
	}
}

// Verify Decide is consistent with FailureClassify.
func TestDecide_ConsistentWithClassify(t *testing.T) {
	d := &Driver{cfg: Config{Retries: 1}}
	testCases := []error{
		errors.New("i/o timeout"),
		errors.New("status code 503"),
		errors.New("tool denied: user denied"),
		errors.New("rate limit exceeded"),
		errors.New("connection reset"),
	}
	for _, err := range testCases {
		class := FailureClassify(err)
		dec := d.Decide(err, 0, 1)
		if dec.Class != class {
			t.Errorf("Decide(attempt=0).Class = %v, but Classify = %v for %q", dec.Class, class, err.Error())
		}
	}
}

func TestDecide_ZeroRetries(t *testing.T) {
	d := &Driver{cfg: Config{Retries: 0}}
	// With maxRetries=0, even a transient error should not retry.
	dec := d.Decide(errors.New("i/o timeout"), 0, 0)
	if dec.Class != RetryTransient || dec.Retried {
		t.Fatalf("maxRetries=0: class=%v retried=%v; want RetryTransient,false", dec.Class, dec.Retried)
	}
}

var _ = fmt.Sprintf // for vet
