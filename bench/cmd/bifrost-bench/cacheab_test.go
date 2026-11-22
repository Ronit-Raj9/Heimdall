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
	if got := cacheOffArm(runs); got != "A" {
		t.Fatalf("off arm %q", got)
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
