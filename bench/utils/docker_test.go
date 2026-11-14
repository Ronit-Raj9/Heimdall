package utils

import (
	"reflect"
	"testing"
)

func TestMissingTools(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"all present", "", nil},
		{"python3 missing", "missing python3\n", []string{"python3"}},
		{"two missing, noise ignored", "hello\nmissing git\nmissing python3\nmissing\n", []string{"git", "python3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MissingTools(tt.out); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelperLacks(t *testing.T) {
	tests := []struct {
		tools []string
		want  string
	}{
		{[]string{"python3"}, "helper image acme/node:1 has no python3; set images.helper to the bench helper image"},
		{[]string{"git", "python3"}, "helper image acme/node:1 has no git or python3; set images.helper to the bench helper image"},
	}
	for _, tt := range tests {
		if got := HelperLacks("acme/node:1", tt.tools...); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestLabelArgs(t *testing.T) {
	got := LabelArgs("e_x", "r_y")
	want := []string{"--label", "bifrost-bench=1", "--label", "bifrost-bench.exp=e_x", "--label", "bifrost-bench.run=r_y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
