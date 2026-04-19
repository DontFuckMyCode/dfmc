package tools

import "testing"

func TestNormalizeResolverHost_StripsIPv6ZoneID(t *testing.T) {
	if got := normalizeResolverHost("fe80::1%eth0"); got != "fe80::1" {
		t.Fatalf("expected zone-stripped IPv6 literal, got %q", got)
	}
	if got := normalizeResolverHost("::1"); got != "::1" {
		t.Fatalf("plain IPv6 literal should remain unchanged, got %q", got)
	}
	if got := normalizeResolverHost("example.com"); got != "example.com" {
		t.Fatalf("hostname should remain unchanged, got %q", got)
	}
}
