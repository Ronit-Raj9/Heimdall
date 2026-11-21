// Package replay builds and reads the recordings that debug runs replay
// (lane X20b): a cassette of one sealed run's model calls, made from its
// proxy ledger and gateway.raw.jsonl, and the request normalization that
// tells a replayed call from a divergent one. It is pure: no I/O beyond the
// bytes it is handed, so the bench, the handlers and the fault proxy share
// it.
package replay

import (
	"encoding/json"
	"regexp"

	"github.com/maximhq/bifrost/bench/utils"
)

// droppedRequestKeys are top-level request fields that change between two
// runs of the same conversation without changing what the model is asked:
// session and user tags and cache keys.
var droppedRequestKeys = []string{"metadata", "user", "prompt_cache_key", "safety_identifier", "stream_options"}

// Noise patterns replaced inside every string of a request before it is
// hashed: ids, timestamps and temp paths a rerun of the same tools changes.
var noise = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`), "<uuid>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?`), "<time>"},
	{regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(\.\d+)?\b`), "<time>"},
	{regexp.MustCompile(`/tmp/[A-Za-z0-9._-]+`), "/tmp/<tmp>"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`), "<hex>"},
	{regexp.MustCompile(`\b\d+(\.\d+)?\s?(ms|s|sec|seconds)\b`), "<duration>"},
}

// RequestHash is the normalized hash of a model request body: the JSON with
// session fields dropped and noise inside strings replaced, canonically
// encoded. Two requests of a faithful replay hash the same even though tool
// output timestamps and temp paths moved. A body that is not JSON hashes as
// its raw bytes. Empty input gives "".
func RequestHash(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return utils.SHA256Bytes(body)
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range droppedRequestKeys {
			delete(m, k)
		}
	}
	h, err := utils.HashJSON(scrub(v))
	if err != nil {
		return utils.SHA256Bytes(body)
	}
	return h
}

// scrub replaces noise in every string of v.
func scrub(v any) any {
	switch t := v.(type) {
	case string:
		for _, n := range noise {
			t = n.re.ReplaceAllString(t, n.with)
		}
		return t
	case []any:
		for i := range t {
			t[i] = scrub(t[i])
		}
		return t
	case map[string]any:
		for k, x := range t {
			t[k] = scrub(x)
		}
		return t
	default:
		return v
	}
}
