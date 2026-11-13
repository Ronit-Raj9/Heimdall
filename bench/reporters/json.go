package reporters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ReporterJSON names the JSON reporter.
const ReporterJSON = "json"

// jsonReporter writes the analysis as indented JSON (report.json).
type jsonReporter struct{}

// newJSONReporter creates the JSON reporter.
func newJSONReporter() *jsonReporter {
	return &jsonReporter{}
}

// GetName implements schemas.Part.
func (r *jsonReporter) GetName() string { return ReporterJSON }

// GetVersion implements schemas.Part.
func (r *jsonReporter) GetVersion() string { return Version }

// Write encodes a with encoding/json, like every artifact on disk. The
// manifest is not repeated: the analysis names it by hash.
func (r *jsonReporter) Write(ctx context.Context, a *schemas.Analysis, _ *schemas.Manifest, w io.Writer) error {
	if a == nil {
		return errors.New("json report: analysis is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("json report: %w", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return fmt.Errorf("json report for %s: %w", a.ExpID, err)
	}
	return nil
}
