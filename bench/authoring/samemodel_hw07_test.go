package authoring

import "testing"

// TestSameModelDatedSnapshotHW07 checks that this package uses the one
// "same model" rule (modelid.SameModel): a dated snapshot or a stable
// revision of the pinned alias is the pinned model, while a longer family
// name, another date of a pinned snapshot or another family is not.
func TestSameModelDatedSnapshotHW07(t *testing.T) {
	cases := []struct {
		served, pinned string
		want           bool
	}{
		{"gpt-5.4-mini-2026-03-17", "gpt-5.4-mini", true},
		{"openai/gpt-5.4-mini-2026-03-17", "gpt-5.4-mini", true},
		{"claude-sonnet-4-5@20250929", "claude-sonnet-4-5", true},
		{"gemini-2.0-flash-001", "gemini-2.0-flash", true},
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet-latest", true},
		{"", "gpt-5.4-mini", true},
		{"gpt-5.4-mini-2026-03-17", "gpt-5.4", false},
		{"gpt-4o-mini", "gpt-4o", false},
		{"gpt-4o-2024-11-20", "gpt-4o-2024-08-06", false},
		{"claude-opus-4-5-20250929", "claude-sonnet-4-5", false},
	}
	for _, c := range cases {
		if got := sameModel(c.served, c.pinned); got != c.want {
			t.Errorf("sameModel(%q, %q) = %v, want %v", c.served, c.pinned, got, c.want)
		}
	}
}
