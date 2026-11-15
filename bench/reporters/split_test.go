package reporters

import (
	"bufio"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWriteSplit(t *testing.T) {
	tests := []struct {
		name string
		m    *schemas.Manifest
		want string
	}{
		{"nil manifest", nil, ""},
		{"public", &schemas.Manifest{}, ""},
		{"held out", &schemas.Manifest{HeldOutTasks: []string{"t07", "t08"}}, "> split: held_out (2 held-out tasks: t07, t08)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			w := bufio.NewWriter(&b)
			writeSplit(w, tt.m)
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if tt.want == "" && b.Len() != 0 || !strings.Contains(b.String(), tt.want) {
				t.Fatalf("got %q, want %q", b.String(), tt.want)
			}
		})
	}
}
