package adapters

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// H02: Codex carries the run's gateway headers (the heimdall plugin fault
// plan and run tags, the prompt and semantic cache headers) in the
// http_headers table of the model provider its config.toml selects. The
// shipped config (agents/codex.yaml) already has
// [model_providers.bifrost.http_headers] for its session headers; the
// headers are added there, and a header the config already names is left as
// the config wrote it.

var codexModelProvider = regexp.MustCompile(`(?m)^\s*model_provider\s*=\s*"([^"]+)"\s*$`)

// codexConfigPath is where Codex reads its config: $CODEX_HOME/config.toml.
func codexConfigPath(cfg *schemas.AgentConfig) string {
	return path.Join(envDir(cfg, "CODEX_HOME", agentHome+"/.codex"), "config.toml")
}

// codexProvider returns the Codex config path and the model provider id its
// config.toml selects, and whether the headers can go in that provider's
// http_headers table: the config sets model_provider, defines
// [model_providers.<id>] and does not give it an inline http_headers value
// (TOML forbids a table beside it).
func codexProvider(cfg *schemas.AgentConfig) (string, string, bool) {
	p := codexConfigPath(cfg)
	body := cfg.Files[p]
	m := codexModelProvider.FindStringSubmatch(body)
	if m == nil {
		return p, "", false
	}
	id := m[1]
	lines := strings.Split(body, "\n")
	start, end := tomlSection(lines, "[model_providers."+tomlKey(id)+"]")
	if start < 0 {
		return p, id, false
	}
	for _, l := range lines[start+1 : end] {
		if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok && strings.TrimSpace(k) == "http_headers" {
			return p, id, false
		}
	}
	return p, id, true
}

// addCodexHeaders writes h into the selected provider's http_headers table
// of the planned config.toml, creating the table when the config has none.
func addCodexHeaders(plan *schemas.RunPlan, cfg *schemas.AgentConfig, h map[string]string) {
	p, id, ok := codexProvider(cfg)
	if !ok || len(h) == 0 || plan.Files == nil {
		return
	}
	body, ok := plan.Files[p]
	if !ok {
		return
	}
	header := "[model_providers." + tomlKey(id) + ".http_headers]"
	lines := strings.Split(body, "\n")
	start, end := tomlSection(lines, header)
	have := map[string]bool{}
	if start >= 0 {
		for _, l := range lines[start+1 : end] {
			if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
				have[strings.ToLower(strings.Trim(strings.TrimSpace(k), `"`))] = true
			}
		}
	}
	var add []string
	for _, n := range sortedNames(h) {
		if !have[strings.ToLower(n)] {
			add = append(add, tomlKey(n)+" = "+strconv.Quote(h[n]))
		}
	}
	if len(add) == 0 {
		return
	}
	if start >= 0 {
		out := append([]string{}, lines[:start+1]...)
		out = append(out, add...)
		out = append(out, lines[start+1:]...)
		plan.Files[p] = strings.Join(out, "\n")
		return
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	plan.Files[p] = body + "\n" + header + "\n" + strings.Join(add, "\n") + "\n"
}

// tomlSection finds the table whose header line is header: the header's
// line index and the index of the next table header (or len(lines)); -1
// when there is no such table.
func tomlSection(lines []string, header string) (int, int) {
	start := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 {
			if t == header {
				start = i
			}
			continue
		}
		if strings.HasPrefix(t, "[") {
			return start, i
		}
	}
	return start, len(lines)
}

func tomlKey(k string) string {
	if tomlBareKey.MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}
