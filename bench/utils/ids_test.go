package utils

import (
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		n      int
	}{
		{"run id", "r", 10},
		{"slot id", "s", 10},
		{"nonce", "nonce", 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := NewID(tt.prefix, tt.n), NewID(tt.prefix, tt.n)
			if a == b {
				t.Fatalf("two ids are equal: %s", a)
			}
			if !strings.HasPrefix(a, tt.prefix+"_") || len(a) != len(tt.prefix)+1+tt.n {
				t.Fatalf("bad id %q", a)
			}
		})
	}
}
