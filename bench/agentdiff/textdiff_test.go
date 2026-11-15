package agentdiff

import (
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	long := strings.Repeat("same\n", 20)
	cases := []struct {
		name, old, new string
		max            int
		want           string
		truncated, ok  bool
	}{
		{name: "equal", old: "a\nb\n", new: "a\nb\n", max: 10, ok: true},
		{name: "one line changed", old: "a\nb\nc\n", new: "a\nB\nc\n", max: 10, ok: true,
			want: "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"},
		{name: "added to empty", old: "", new: "x\n", max: 10, ok: true,
			want: "--- a/f\n+++ b/f\n@@ -0,0 +1 @@\n+x\n"},
		{name: "two hunks far apart", old: "x\n" + long + "y\n", new: "X\n" + long + "Y\n", max: 100, ok: true,
			want: "--- a/f\n+++ b/f\n@@ -1,4 +1,4 @@\n-x\n+X\n same\n same\n same\n@@ -19,4 +19,4 @@\n same\n same\n same\n-y\n+Y\n"},
		{name: "cut at max lines", old: "a\nb\nc\n", new: "1\n2\n3\n", max: 2, ok: true, truncated: true,
			want: "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n-a\n-b\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, truncated, ok := unifiedDiff("f", c.old, c.new, c.max)
			if got != c.want || truncated != c.truncated || ok != c.ok {
				t.Fatalf("got %q %v %v\nwant %q %v %v", got, truncated, ok, c.want, c.truncated, c.ok)
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		big := strings.Repeat("l\n", 2100)
		if _, _, ok := unifiedDiff("f", big, big+"m\n", 10); ok {
			t.Fatal("want too large")
		}
	})
}
