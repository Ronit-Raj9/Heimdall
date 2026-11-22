package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/bifrostfeatures"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPrintCacheAB(t *testing.T) {
	ok := true
	usd := 0.01
	runs := []bifrostfeatures.CacheRun{
		{RunRows: bifrostfeatures.RunRows{RunID: "r1", Task: "t1", Arm: "A", Rows: []schemas.GatewayRequest{{Kind: "message", LatencyMS: 900, BifrostUSD: &usd}}}, Success: &ok},
		{RunRows: bifrostfeatures.RunRows{RunID: "r2", Task: "t1", Arm: schemas.EvaluationCacheArm, Rows: []schemas.GatewayRequest{{Kind: "message", LatencyMS: 10, CacheHit: true}}}, Success: &ok},
	}
	if off, on := cacheArms(nil, runs); off != "A" || on != schemas.EvaluationCacheArm {
		t.Fatalf("arms off %q on %q", off, on)
	}
	var buf bytes.Buffer
	if code := printCacheAB(&buf, "exp1", bifrostfeatures.CacheAB(runs, "A", schemas.EvaluationCacheArm), false); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	out := buf.String()
	for _, want := range []string{"over 1 task(s)", "CACHE HITS", "100%", "Success was the same."} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

// D5: a score evaluation's cache-on copy is arm B; it is found by its label.
func TestCacheArmsByLabel(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Label: "x on m"}, "B": {Label: "x on m" + schemas.EvaluationCacheSuffix}}}
	runs := []bifrostfeatures.CacheRun{
		{RunRows: bifrostfeatures.RunRows{RunID: "r2", Task: "t1", Arm: "B"}},
		{RunRows: bifrostfeatures.RunRows{RunID: "r1", Task: "t1", Arm: "A"}},
	}
	if off, on := cacheArms(m, runs); off != "A" || on != "B" {
		t.Fatalf("arms off %q on %q, want A and B", off, on)
	}
}
