package graders

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestJudgeCacheKeyChangesWithEachField(t *testing.T) {
	spec := schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: PromptQAv1, MaxTokens: 4}
	base := JudgeCacheKey(spec, "sys", "prompt", 0)
	if base != JudgeCacheKey(spec, "sys", "prompt", 0) || len(base) != 64 {
		t.Fatalf("key not stable or not hex sha256: %q", base)
	}
	mods := map[string]func() string{
		"provider": func() string { s := spec; s.Provider = "anthropic"; return JudgeCacheKey(s, "sys", "prompt", 0) },
		"model":    func() string { s := spec; s.Model = "other"; return JudgeCacheKey(s, "sys", "prompt", 0) },
		"prompt version": func() string {
			s := spec
			s.PromptVersion = PromptLongMemEvalV1
			return JudgeCacheKey(s, "sys", "prompt", 0)
		},
		"max tokens": func() string { s := spec; s.MaxTokens = 8; return JudgeCacheKey(s, "sys", "prompt", 0) },
		"system":     func() string { return JudgeCacheKey(spec, "sys2", "prompt", 0) },
		"prompt":     func() string { return JudgeCacheKey(spec, "sys", "prompt2", 0) },
		"vote":       func() string { return JudgeCacheKey(spec, "sys", "prompt", 1) },
	}
	for name, f := range mods {
		if f() == base {
			t.Errorf("changing the %s kept the key", name)
		}
	}
}

func TestFileJudgeCacheTTLAndCap(t *testing.T) {
	dir := t.TempDir()
	c := NewFileJudgeCache(dir, time.Hour, 2)
	key := func(i int) string { return JudgeCacheKey(schemas.JudgeSpec{Model: "m"}, "s", "p", i) }
	c.Put(key(0), JudgeCacheEntry{Reply: "correct", Served: "m", At: time.Now()})
	got, ok := c.Get(key(0))
	if !ok || got.Reply != "correct" {
		t.Fatalf("get = %+v, %v", got, ok)
	}
	p := filepath.Join(dir, key(0)[:2], key(0)+".json")
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("entry file: %v %v", fi, err)
	}
	c.Put(key(1), JudgeCacheEntry{Reply: "old", Served: "m", At: time.Now().Add(-2 * time.Hour)})
	if _, ok := c.Get(key(1)); ok {
		t.Fatal("an entry older than the ttl was served")
	}
	c.Put(key(2), JudgeCacheEntry{Reply: "x", Served: "m", At: time.Now()})
	c.Put(key(3), JudgeCacheEntry{Reply: "y", Served: "m", At: time.Now()})
	if _, ok := c.Get(key(3)); ok {
		t.Fatal("an entry above the cap was written")
	}
	if err := os.WriteFile(filepath.Join(dir, key(0)[:2], key(0)+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key(0)); ok {
		t.Fatal("a broken entry was served")
	}
	if _, ok := c.Get("../../etc"); ok {
		t.Fatal("a key that is not hex was looked up")
	}
}
