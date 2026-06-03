package tui

import "testing"

// TestTimelineEventKVValueUnicodeOffset guards the same offset-aliasing class as
// the planning splitStages fix: timelineEventKVValue found the "key=" marker in
// strings.ToLower(field) but sliced the original field with that byte offset.
// strings.ToLower can change byte length (Turkish 'İ' U+0130 -> 'i'), so any
// such character before the marker shifted the slice — e.g. the "name" value in
// "title=İş name=foo" came back as "=foo".
func TestTimelineEventKVValueUnicodeOffset(t *testing.T) {
	cases := []struct {
		field, key, want string
	}{
		{"title=İş name=foo", "name", "foo"},
		{"a=İİ b=bar", "b", "bar"},                    // two İ before the marker
		{"name=foo", "name", "foo"},                   // ASCII prefix still works
		{"x=1 path=/tmp/İz.go", "path", "/tmp/İz.go"}, // İ in the value itself
		{"k=\"İ value\"", "k", "İ value"},             // quoted value with İ
	}
	for _, tc := range cases {
		if got := timelineEventKVValue(tc.field, tc.key); got != tc.want {
			t.Fatalf("timelineEventKVValue(%q, %q) = %q, want %q", tc.field, tc.key, got, tc.want)
		}
	}
}
