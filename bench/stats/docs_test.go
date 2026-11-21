package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuidesStateSamplerCutoff: QC2-30-05. Every guide that leans on
// SignFlip states the exact-enumeration cutoff and the Monte Carlo fallback.
func TestGuidesStateSamplerCutoff(t *testing.T) {
	cutoff := fmt.Sprint(ExactMaxNonzero)
	for _, doc := range []string{"model-matrix.md", "model-migration.md", "trends.md", "DESIGN.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "docs", doc))
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if !strings.Contains(s, "Monte Carlo") || !strings.Contains(s, cutoff+" nonzero") {
			t.Errorf("docs/%s does not state the %s-difference cutoff and Monte Carlo fallback", doc, cutoff)
		}
	}
}
