package graders

// W4 feature 7 (D3 10.4): the judge cache by content. A vote is a pure
// function of the provider, model, prompt version, system text, user
// prompt, max tokens, temperature 0 and the vote index. When all of these
// are byte-identical to a vote already paid for, its stored reply is reused
// and the vote costs nothing. Only voteReplies reads the cache, so a
// calibration always makes every call.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// JudgeCacheEntry is one paid judge vote's reply, kept for reuse.
type JudgeCacheEntry struct {
	Reply  string          `json:"reply"`
	Served string          `json:"served_model"`
	Cut    bool            `json:"cut,omitempty"`    // the reply was empty because max_tokens ran out
	Tokens *schemas.Tokens `json:"tokens,omitempty"` // usage of the paid call, kept for the record
	At     time.Time       `json:"at"`
}

// JudgeCache stores judge replies by content key. nil turns caching off.
type JudgeCache interface {
	Get(key string) (JudgeCacheEntry, bool)
	Put(key string, e JudgeCacheEntry)
}

// JudgeCacheKey is the hex sha256 of the canonical JSON of
// {"v":1,"provider","model","prompt_version","max_tokens","temperature":0,"system","prompt","vote"}.
func JudgeCacheKey(spec schemas.JudgeSpec, system, prompt string, vote int) string {
	b, _ := json.Marshal(struct {
		V             int     `json:"v"`
		Provider      string  `json:"provider"`
		Model         string  `json:"model"`
		PromptVersion string  `json:"prompt_version"`
		MaxTokens     int     `json:"max_tokens"`
		Temperature   float64 `json:"temperature"`
		System        string  `json:"system"`
		Prompt        string  `json:"prompt"`
		Vote          int     `json:"vote"`
	}{1, spec.Provider, spec.Model, spec.PromptVersion, judgeMaxTokens(spec), 0, system, prompt, vote})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileJudgeCache is NewFileJudgeCache.
type fileJudgeCache struct {
	dir        string
	ttl        time.Duration
	maxEntries int

	mu      sync.Mutex
	counted bool
	entries int
}

// NewFileJudgeCache keeps entries as <dir>/<key[0:2]>/<key>.json (0600),
// drops entries older than ttl on read, and stops writing (never fails a
// grade) above maxEntries files.
func NewFileJudgeCache(dir string, ttl time.Duration, maxEntries int) JudgeCache {
	return &fileJudgeCache{dir: dir, ttl: ttl, maxEntries: maxEntries}
}

// validCacheKey reports whether key is 64 lowercase hex characters, so a
// key never names a path outside the cache folder.
func validCacheKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for _, c := range key {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (c *fileJudgeCache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key+".json")
}

func (c *fileJudgeCache) Get(key string) (JudgeCacheEntry, bool) {
	if !validCacheKey(key) {
		return JudgeCacheEntry{}, false
	}
	p := c.path(key)
	b, err := os.ReadFile(p)
	if err != nil {
		return JudgeCacheEntry{}, false
	}
	var e JudgeCacheEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return JudgeCacheEntry{}, false
	}
	if c.ttl > 0 && time.Since(e.At) > c.ttl {
		if os.Remove(p) == nil {
			c.mu.Lock()
			if c.counted && c.entries > 0 {
				c.entries--
			}
			c.mu.Unlock()
		}
		return JudgeCacheEntry{}, false
	}
	return e, true
}

// count walks the cache folder once to learn how many entries it holds.
func (c *fileJudgeCache) count() {
	if c.counted {
		return
	}
	n := 0
	_ = filepath.WalkDir(c.dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".json" {
			n++
		}
		return nil
	})
	c.entries, c.counted = n, true
}

func (c *fileJudgeCache) Put(key string, e JudgeCacheEntry) {
	if !validCacheKey(key) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count()
	p := c.path(key)
	_, statErr := os.Stat(p)
	exists := statErr == nil
	if !exists && c.maxEntries > 0 && c.entries >= c.maxEntries {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".entry-*")
	if err != nil {
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_ = f.Chmod(0o600)
	_, werr := f.Write(b)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return
	}
	if os.Rename(tmp, p) == nil && !exists {
		c.entries++
	}
}
