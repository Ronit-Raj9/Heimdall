package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
)

func TestJudgeCacheOffOnSharedRunner(t *testing.T) {
	cfg := &lib.Config{Root: t.TempDir()}
	if c := judgeCache(cfg, appOptions{sharedRunner: true}); c != nil {
		t.Fatalf("the shared worker got a judge cache: %T", c)
	}
	if c := judgeCache(cfg, appOptions{}); c == nil {
		t.Fatal("an own worker got no judge cache")
	}
}
