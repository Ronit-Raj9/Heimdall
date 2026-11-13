package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestStdLogger(t *testing.T) {
	tests := []struct {
		name  string
		debug bool
		log   func(l *StdLogger)
		want  string
		empty bool
	}{
		{"info formats args", false, func(l *StdLogger) { l.Info("run %s sealed", "r_1") }, "INFO  run r_1 sealed", false},
		{"key value pairs", false, func(l *StdLogger) { l.Info("graded", "run", "r_1", "passed", true) }, "INFO  graded run=r_1 passed=true", false},
		{"debug hidden", false, func(l *StdLogger) { l.Debug("x") }, "", true},
		{"debug shown", true, func(l *StdLogger) { l.Debug("x") }, "DEBUG x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.log(NewStdLogger(&buf, tt.debug))
			if tt.empty {
				if buf.Len() != 0 {
					t.Fatalf("want no output, got %q", buf.String())
				}
				return
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("got %q, want it to contain %q", buf.String(), tt.want)
			}
		})
	}
}
