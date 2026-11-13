package reporters

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestJSONReporter(t *testing.T) {
	a := testAnalysis()
	var buf bytes.Buffer
	if err := newJSONReporter().Write(context.Background(), a, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\n  \"analysis_id\": \"a_1\"") {
		t.Errorf("not indented: %s", buf.String()[:80])
	}
	var back schemas.Analysis
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Verdict.Decision != a.Verdict.Decision || back.Meters[0].PerArm["A"].State != schemas.ValueUnknown {
		t.Errorf("round trip lost data: %+v", back.Verdict)
	}
	if err := newJSONReporter().Write(context.Background(), nil, nil, &buf); err == nil {
		t.Error("nil analysis accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newJSONReporter().Write(ctx, a, nil, &buf); err == nil {
		t.Error("cancelled context accepted")
	}
}
