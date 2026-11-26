package handlers

// Private repositories and share links (W4-48, design D4 8): a share link is
// opened by people outside the organization, so it must never show the task
// content, repository name or code of a benchmark built from a private
// repository. The weekly issue sync marks such a benchmark
// (issuesync.PrivateSourcesFile); a link whose answers name one is refused
// when it is made and when it is opened.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/twins"
	"github.com/valyala/fasthttp"
)

// sharePrivateSourceText is the refusal; %s is the benchmark.
const sharePrivateSourceText = "This cannot be shared outside your organization: it shows tasks of %s, a benchmark built from a private repository."

// privateSourceNames are the strings that name a private-source benchmark:
// its id, its impossible twins' id and the repository. Empty when the org
// has none (or the list cannot be read: then every share is refused, see
// privateSourceIn).
func (h *shareHandler) privateSourceNames() (map[string]string, error) {
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		return nil, nil
	}
	list, err := issuesync.PrivateSources(h.env.Cfg.Root)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range list {
		out[p.Benchmark] = p.Benchmark
		out[p.Benchmark+twins.SuiteSuffix] = p.Benchmark
		if p.Repo != "" {
			out[strings.ToLower(p.Repo)] = p.Benchmark
		}
	}
	return out, nil
}

// privateSourceRefusal fetches every part of the link and answers the
// refusal sentence when one names a private-source benchmark; "" when the
// link is safe to serve.
func (h *shareHandler) privateSourceRefusal(ctx *fasthttp.RequestCtx, kind, target, query string) string {
	names, err := h.privateSourceNames()
	if err != nil {
		return fmt.Sprintf(sharePrivateSourceText, "a benchmark")
	}
	if len(names) == 0 {
		return ""
	}
	for _, p := range sharedParts(kind, target, query) {
		status, body, _ := h.sub(ctx, p.path)
		if status != fasthttp.StatusOK {
			continue
		}
		if b := namesPrivateSource(body, names); b != "" {
			return fmt.Sprintf(sharePrivateSourceText, b)
		}
	}
	return ""
}

// refusePrivateSource answers 403 with the sentence when the share names a
// private-source benchmark and reports whether it did.
func (h *shareHandler) refusePrivateSource(ctx *fasthttp.RequestCtx, sh *sharelinks.Share) bool {
	msg := h.privateSourceRefusal(ctx, sh.Kind, sh.Target, sh.Query)
	if msg == "" {
		return false
	}
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, msg)
	return true
}

// namesPrivateSource walks a JSON answer (keys and string values) and
// answers the benchmark one of them names ("id", "id@version", the twins'
// id or the repository), else "". A body that is not JSON is searched as
// text.
func namesPrivateSource(body []byte, names map[string]string) string {
	match := func(s string) string {
		id, _, _ := strings.Cut(s, "@")
		if b, ok := names[id]; ok {
			return b
		}
		if b, ok := names[strings.ToLower(s)]; ok {
			return b
		}
		return ""
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		text := strings.ToLower(string(body))
		for n, b := range names {
			if strings.Contains(text, strings.ToLower(n)) {
				return b
			}
		}
		return ""
	}
	var walk func(any) string
	walk = func(x any) string {
		switch t := x.(type) {
		case string:
			return match(t)
		case []any:
			for _, e := range t {
				if b := walk(e); b != "" {
					return b
				}
			}
		case map[string]any:
			for k, e := range t {
				if b := match(k); b != "" {
					return b
				}
				if b := walk(e); b != "" {
					return b
				}
			}
		}
		return ""
	}
	return walk(v)
}
