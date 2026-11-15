package reporters

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMarkdownExternalLabel(t *testing.T) {
	cases := []struct {
		external string
		want     bool
	}{
		{"", false},
		{schemas.ExternalReplay, false},
		{schemas.ExternalLive, true},
		{schemas.ExternalRecord, true},
	}
	for _, c := range cases {
		t.Run("external="+c.external, func(t *testing.T) {
			m := &schemas.Manifest{ExpID: "e_x", Experiment: schemas.Experiment{External: c.external}}
			var buf bytes.Buffer
			if err := newMarkdownReporter().Write(context.Background(), testAnalysis(), m, &buf); err != nil {
				t.Fatal(err)
			}
			label := "> external APIs: " + c.external + " (not repeatable)"
			if got := strings.Contains(buf.String(), label); got != c.want {
				t.Fatalf("label %q present=%v, want %v", label, got, c.want)
			}
		})
	}
	var buf bytes.Buffer
	if err := newMarkdownReporter().Write(context.Background(), testAnalysis(), nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "external APIs") {
		t.Fatal("report without a manifest mentions external APIs")
	}
}
