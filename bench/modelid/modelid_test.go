package modelid

import "testing"

// TestSameModelTable pins the one "same model" rule. The true rows are the
// provider snapshots and aliases that serve the requested model; the false
// rows are the cases that must stay different (a longer family name, a
// different date, a different provider family), so a looser rule can never
// hide a real model swap.
func TestSameModelTable(t *testing.T) {
	cases := []struct {
		requested, served string
		want              bool
	}{
		// The same model.
		{"gpt-5.4-mini", "gpt-5.4-mini", true},
		{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17", true},
		{"openai/gpt-5.4-mini", "gpt-5.4-mini-2026-03-17", true},
		{"gpt-5.4-mini", "openai/gpt-5.4-mini-2026-03-17", true},
		{"GPT-5.4-Mini", "gpt-5.4-mini-2026-03-17", true},
		{"gpt-4o", "gpt-4o-2024-08-06", true},
		{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929", true},
		{"claude-sonnet-4-5", "claude-sonnet-4-5@20250929", true},
		{"anthropic/claude-sonnet-4-5", "claude-sonnet-4-5-20250929", true},
		{"gemini-2.0-flash", "gemini-2.0-flash-001", true},
		{"models/gemini-2.0-flash", "gemini-2.0-flash-001", true},
		{"gpt-4", "gpt-4-0613", true},
		{"mistral-large", "mistral-large-2411", true},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet-20241022", true},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet", true},
		{"mistral-large-latest", "mistral-large-2411", true},
		{"claude-3-5-sonnet-20241022", "anthropic.claude-3-5-sonnet-20241022-v2:0", true},
		{"openrouter/anthropic/claude-x", "anthropic/claude-x", true},
		{"x", "", true},
		{"gpt-4o-2024-08-06", "gpt-4o-2024-08-06", true},

		// Must stay different: a longer family name.
		{"gpt-5.4", "gpt-5.4-mini-2026-03-17", false},
		{"gpt-5.4", "gpt-5.4-mini", false},
		{"gpt-4o", "gpt-4o-mini", false},
		{"gpt-4o", "gpt-4o-mini-2024-07-18", false},
		{"claude-sonnet-4-5", "claude-sonnet-4-5-thinking", false},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet-v2-20241022", false},
		{"mistral-large-latest", "mistral-large-instruct-2411", false},
		// Must stay different: a different date or a snapshot asked for by name.
		{"gpt-4o-2024-08-06", "gpt-4o-2024-11-20", false},
		{"gpt-4o-2024-08-06", "gpt-4o", false},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20251101", false},
		{"gpt-4-0613", "gpt-4-0314", false},
		{"gemini-2.0-flash-001", "gemini-2.0-flash-002", false},
		{"gpt-4o-2024-08-06", "gpt-4o-2024-08-06-2024-11-20", false},
		// Must stay different: a different provider family or model.
		{"claude-sonnet-4-5", "claude-opus-4-5-20250929", false},
		{"gpt-5.4-mini", "claude-haiku-4-5", false},
		{"gpt-5.4-mini", "gemini-2.0-flash-001", false},
		{"org/model-a", "org/model-b", false},
		{"meta-llama/Llama-3-8B", "meta-llama/Llama-3-70B", false},
		{"", "gpt-4o", false},
		{"deepseek", "deepseek-v3", false},
		{"claude-3-5-sonnet", "claude-3-5-sonnet-v2", false},
		// A Bedrock id is only unwrapped when the request was a plain id.
		{"anthropic.claude-3-5-sonnet-20241022-v1:0", "anthropic.claude-3-5-sonnet-20241022-v2:0", false},
	}
	for _, c := range cases {
		t.Run(c.requested+"|"+c.served, func(t *testing.T) {
			if got := SameModel(c.requested, c.served); got != c.want {
				t.Fatalf("SameModel(%q, %q) = %v, want %v", c.requested, c.served, got, c.want)
			}
		})
	}
}

func TestSnapshot(t *testing.T) {
	cases := []struct {
		requested, served, want string
	}{
		{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17", "-2026-03-17"},
		{"claude-sonnet-4-5", "claude-sonnet-4-5@20250929", "@20250929"},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet-20241022", "-20241022"},
		{"gpt-5.4-mini", "gpt-5.4-mini", ""},
		{"gpt-5.4", "gpt-5.4-mini-2026-03-17", ""},
		{"gemini-2.0-flash", "gemini-2.0-flash-001", ""},
	}
	for _, c := range cases {
		if got := Snapshot(c.requested, c.served); got != c.want {
			t.Fatalf("Snapshot(%q, %q) = %q, want %q", c.requested, c.served, got, c.want)
		}
	}
}
