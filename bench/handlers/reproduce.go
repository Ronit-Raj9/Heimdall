package handlers

// "Reproduce this" bundle (lane X14): GET /evaluations/{id}/reproduce is a
// zip laid out like a project folder, holding everything needed to run the
// same evaluation again and to check the result was measured on the same
// inputs:
//
//	REPRODUCE.md                                  the commands, and what each hash should be
//	evaluation-spec.json                          the POST /evaluations body
//	evaluation.json                               the evaluation as stored (who started it removed)
//	locks.json                                    per benchmark: suite version, suite lock hash, manifest hash
//	experiments/<name>.yaml                       each benchmark's experiment file
//	benchmarks/<suite>/suite.lock                 each benchmark's lock (task ids and content hashes)
//	agents/<id>.yaml                              each agent's config
//	.heimdall/agent-versions/<id>/v<N>.yaml       each pinned agent version's frozen snapshot
//
// Agent configs name secrets by environment variable only, so the bundle
// holds no key; every file still passes through the secret masker.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/secrets"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// ReproduceLock is one benchmark's row of locks.json.
type ReproduceLock struct {
	Benchmark     string `json:"benchmark"`
	ExperimentID  string `json:"experiment_id,omitempty"`
	Experiment    string `json:"experiment_file,omitempty"`
	SuiteID       string `json:"suite_id,omitempty"`
	SuiteVersion  string `json:"suite_version,omitempty"`
	SuiteLockHash string `json:"suite_lock_hash,omitempty"`
	ManifestHash  string `json:"manifest_hash,omitempty"`
	BenchVersion  string `json:"bench_version,omitempty"`
	Skipped       string `json:"skipped,omitempty"`
}

func init() {
	RegisterOrgArea("reproduce", func(env OrgEnv, r *router.Router) {
		h := &reproduceHandler{env: env, library: suite.LibraryDir(), now: func() time.Time { return time.Now().UTC() }}
		r.GET(APIPrefix+"/evaluations/{id}/reproduce", env.R(h.get))
	})
}

type reproduceHandler struct {
	env     OrgEnv
	library string
	now     func() time.Time
}

// childExperimentName is the experiment file name an evaluation gives a
// benchmark (handlers/evaluations.go builds it the same way).
func childExperimentName(evID, benchmark string) string {
	slug := strings.NewReplacer(".", "-", "_", "-").Replace(benchmark)
	return strings.ReplaceAll("eval-"+strings.TrimPrefix(evID, "ev_")+"-"+slug, "_", "-")
}

// readProject reads rel from the project, else (for agents and benchmarks)
// from the built-in library. ok is false when neither holds it.
func (h *reproduceHandler) readProject(rel string) ([]byte, bool) {
	if root := h.env.Cfg.Root; root != "" {
		if p, err := utils.ResolveInside(root, rel); err == nil {
			if data, err := os.ReadFile(p); err == nil {
				return data, true
			}
		}
	}
	if h.library != "" && (strings.HasPrefix(rel, "agents/") || strings.HasPrefix(rel, "benchmarks/")) {
		if p, err := utils.ResolveInside(h.library, rel); err == nil {
			if data, err := os.ReadFile(p); err == nil {
				return data, true
			}
		}
	}
	return nil, false
}

func (h *reproduceHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "evaluation")
	if !ok {
		return
	}
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		sendUnavailable(ctx, h.env.Mode, "evaluation store")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ev, err := NewFileEvaluations(h.env.Cfg.Root).Get(work, id)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return
	}
	body, err := h.bundle(ev)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "reproduce bundle", err)
		return
	}
	sendDocument(ctx, "application/zip", id+"-reproduce.zip", body)
}

// bundle builds the zip of ev.
func (h *reproduceHandler) bundle(ev *schemas.Evaluation) ([]byte, error) {
	files := map[string][]byte{}
	clean := *ev
	clean.CreatedBy = ""
	enc := func(v any) []byte {
		b, _ := json.MarshalIndent(v, "", "  ")
		return append(b, '\n')
	}
	files["evaluation.json"] = enc(clean)
	files["evaluation-spec.json"] = enc(ev.Spec)

	var locks []ReproduceLock
	var expFiles []string
	for _, c := range ev.Children {
		row := ReproduceLock{Benchmark: c.Benchmark, ExperimentID: c.ExperimentID, Skipped: c.Skipped}
		name := "experiments/" + childExperimentName(ev.ID, c.Benchmark) + ".yaml"
		if data, ok := h.readProject(name); ok {
			files[name] = data
			row.Experiment = name
			expFiles = append(expFiles, name)
		}
		suiteID, _, _ := strings.Cut(c.Benchmark, "@")
		if c.ExperimentID != "" && h.env.Services.Index != nil {
			if m, ok := h.env.Services.Index.Manifest(c.ExperimentID); ok && m != nil {
				row.SuiteID, row.SuiteVersion, row.SuiteLockHash = m.SuiteID, m.SuiteVersion, m.SuiteLockHash
				row.ManifestHash, row.BenchVersion = m.Hash, m.BenchVersion
				if m.SuiteID != "" {
					suiteID = m.SuiteID
				}
				if row.Experiment == "" {
					alt := "experiments/" + c.ExperimentID + ".json"
					files[alt] = enc(m.Experiment)
					row.Experiment = alt
				}
			}
		}
		if validID(suiteID) {
			lock := path.Join("benchmarks", suiteID, "suite.lock")
			if data, ok := h.readProject(lock); ok {
				files[lock] = data
			}
		}
		locks = append(locks, row)
	}
	files["locks.json"] = enc(locks)

	for _, a := range ev.Spec.Arms {
		if !validID(a.Agent) {
			continue
		}
		cfg := "agents/" + a.Agent + ".yaml"
		if data, ok := h.readProject(cfg); ok {
			files[cfg] = data
		}
		if a.Version > 0 {
			snap := schemas.AgentVersionSnapshotPath(a.Agent, a.Version)
			if data, ok := h.readProject(snap); ok {
				files[snap] = data
			} else if a.Version == 1 {
				// A built-in agent's only version is its library file.
				if data, ok := h.readProject(cfg); ok {
					files[snap] = data
				}
			}
		}
	}
	files["REPRODUCE.md"] = []byte(reproduceReadme(ev, locks, expFiles, h.env.Cfg.Auth.PublicURL))

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	stamp := h.now()
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(n), Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(secrets.Redact(string(files[n])))); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func reproduceReadme(ev *schemas.Evaluation, locks []ReproduceLock, expFiles []string, publicURL string) string {
	var b strings.Builder
	title := ev.Spec.Name
	if title == "" {
		title = ev.ID
	}
	fmt.Fprintf(&b, "# Reproduce %s\n\n", title)
	fmt.Fprintf(&b, "Evaluation `%s`, mode `%s`, %d task(s) per benchmark, %d repeat(s) per task.\n\n", ev.ID, ev.Spec.Mode, ev.Spec.TasksPerBenchmark, ev.Spec.Trials)
	b.WriteString("Setups:\n\n")
	for _, a := range ev.Spec.Arms {
		v := ""
		if a.Version > 0 {
			v = fmt.Sprintf(" v%d", a.Version)
		}
		prov := ""
		if a.Provider != "" {
			prov = a.Provider + "/"
		}
		fmt.Fprintf(&b, "- %s: agent `%s`%s on `%s%s`\n", a.Label, a.Agent, v, prov, a.Model)
	}
	b.WriteString("\n## On a Heimdall server\n\n")
	b.WriteString("Start the same evaluation with your own API token (Settings > Access tokens) and your own provider key added in Settings > Model keys:\n\n")
	server := strings.TrimRight(publicURL, "/")
	if server == "" {
		server = "https://<your heimdall>"
	}
	fmt.Fprintf(&b, "```sh\ncurl -fsS -X POST %s/api/bench/evaluations \\\n  -H \"Authorization: Bearer $HEIMDALL_TOKEN\" -H 'Content-Type: application/json' \\\n  --data @evaluation-spec.json\n```\n\n", server)
	b.WriteString("Agents and benchmarks are looked up by id in your organization, then in the built-in library. Upload the agent configs in `agents/` first if they are your own.\n\n")
	b.WriteString("## With the CLI\n\n")
	b.WriteString("Unzip into a Heimdall project folder (the `bench/` folder of a checkout), so `agents/`, `benchmarks/` and `experiments/` land in place. Then, per benchmark:\n\n```sh\n")
	if len(expFiles) == 0 {
		b.WriteString("# no experiment file was saved for this evaluation; start it on a server instead\n")
	}
	for _, f := range expFiles {
		fmt.Fprintf(&b, "heimdall plan %s   # check the plan first: the suite lock hash must match the table below\n", f)
		fmt.Fprintf(&b, "heimdall run %s\n", f)
	}
	b.WriteString("```\n\n")
	b.WriteString("Provider keys come from your environment (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, ...): the bundle holds none.\n\n")
	b.WriteString("## What the original run used\n\n")
	b.WriteString("A run reproduces this evaluation when its plan shows the same suite version and suite lock hash for each benchmark. The manifest hash also covers the agent configs, models and task order.\n\n")
	b.WriteString("| Benchmark | Suite version | Suite lock hash | Manifest hash | Note |\n|---|---|---|---|---|\n")
	for _, l := range locks {
		note := ""
		if l.Skipped != "" {
			note = "skipped: " + strings.ReplaceAll(l.Skipped, "|", "/")
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | `%s` | %s |\n", l.Benchmark, dash(l.SuiteVersion), dash(l.SuiteLockHash), dash(l.ManifestHash), note)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
