package clbench

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// Name is the import format name.
const Name = "clbench"

// Importer settings (-set key=value).
const (
	SetDBDir     = "db_dir"    // folder holding products.db and products_drifted.db (database_exploration)
	SetRef       = "ref"       // upstream commit, when -from is not a git clone
	SetSchedules = "schedules" // all (default) or default (only each family's default schedule)
	SetSmoke     = "smoke"     // true also imports smoke schedules (quick_test)
	SetSuiteVer  = "version"   // suite version written; default 1.0.0
)

const (
	upstreamRepo    = "https://github.com/pgasawa/continual-learning-bench"
	upstreamLicense = "Apache-2.0"
	serviceID       = "clenv"
	servicePort     = 8000
	workspaceImage  = "python:3.13-slim"
	taskVersion     = "1.0.0"
	verifierTimeout = 300
	passThreshold   = 0.5
	minSessionS     = 900
	maxSessionS     = 14400
	// readyTimeoutS covers building the upstream task object at service
	// start: cohort_studies builds its frozen study databases (about 90 s).
	readyTimeoutS = 900
)

// basePins are the Python packages every stream's service installs, at the
// versions upstream's uv.lock resolves (fastmcp, mcp and treys are the
// service's own and not in upstream's lock).
var basePins = []string{
	"pydantic==2.12.5", "jinja2==3.1.6", "scipy==1.17.1", "numpy==2.4.2",
	"litellm==1.81.6", "openai==2.16.0", "python-dotenv==1.2.1", "tqdm==4.67.2",
	"pyyaml==6.0.3", "huggingface-hub==1.3.7", "fastmcp==2.11.3", "mcp==1.30.0",
}

// family is how one upstream task family imports.
type family struct {
	short    string   // task id part
	perInst  int      // agent seconds per instance, for the session wall budget
	pins     []string // extra Python packages
	draft    string   // non-empty: why the family cannot run here (imported as a draft)
	needsDB  bool     // database_exploration: products.db and products_drifted.db
	hasRef   bool     // the service has a reference policy for it
	capTitle string
}

var families = map[string]family{
	"blind_spectrum_monitoring": {short: "spectrum", perInst: 120, capTitle: "Blind spectrum monitoring"},
	"cohort_studies":            {short: "cohort", perInst: 900, pins: []string{"pandas==2.3.3", "lifelines==0.30.3"}, capTitle: "Cohort studies"},
	"database_exploration":      {short: "dbexplore", perInst: 300, needsDB: true, capTitle: "Database exploration"},
	"exploitable_poker":         {short: "poker", perInst: 90, pins: []string{"texasholdem==0.11.0", "treys==0.1.8", "matplotlib==3.10.8"}, hasRef: true, capTitle: "Exploitable poker"},
	"codebase_adaptation": {short: "codebase", perInst: 1200, capTitle: "Codebase adaptation",
		draft: "upstream runs the agent's commands in per-instance Docker containers (mini-swe-agent DockerEnvironment over SWE-bench images, plus a dataset download at setup); a bench service has no Docker daemon, so the stream cannot run inside the clenv service"},
	"sales_prediction": {short: "sales", perInst: 1200, capTitle: "Sales prediction",
		draft: "upstream runs the agent's bash commands in a Docker container it starts per run (mini-swe-agent DockerEnvironment); a bench service has no Docker daemon, so the stream cannot run inside the clenv service"},
}

var smokeSchedules = []string{"quick_test"}

//go:embed image/server.py
var serverPy string

//go:embed image/runverify.py
var runVerify string

//go:embed image/reference.py
var referencePy string

func init() {
	importers.Register(&importer{now: time.Now})
}

type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"Continual Learning Bench (CL-Bench), every family x schedule stream"},
		Source:     "git clone " + upstreamRepo + " datasets/cl-bench; database_exploration also needs products.db and products_drifted.db from huggingface.co/datasets/continual-learning-bench/database-exploration (docs/importing-clbench.md)",
		License:    upstreamLicense,
		Track:      schemas.TrackMemory,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"sessions, services and state export (one MCP service per stream, the agent's memory carried between sessions)",
			"network while each stream's service image builds (pip installs the pinned upstream dependencies)",
		},
		Summary: "CL-Bench streams as one multi-session task each: the upstream task behind an MCP service, one session per schedule stage, graded on upstream rewards normalized by r_max",
	}
}

type settings struct {
	dbDir     string
	ref       string
	onlyDef   bool
	smoke     bool
	suiteVers string
}

func parseSettings(from string, extra map[string]string) (settings, error) {
	s := settings{dbDir: filepath.Join(from, "data", "database_exploration"), suiteVers: "1.0.0"}
	known := []string{SetDBDir, SetRef, SetSchedules, SetSmoke, SetSuiteVer}
	for k := range extra {
		if !slices.Contains(known, k) {
			return s, fmt.Errorf("unknown setting %q (known: %s)", k, strings.Join(known, ", "))
		}
	}
	if v := extra[SetDBDir]; v != "" {
		s.dbDir = v
	}
	s.ref = extra[SetRef]
	switch v := extra[SetSchedules]; v {
	case "", "all":
	case "default":
		s.onlyDef = true
	default:
		return s, fmt.Errorf("%s=%q: want all or default", SetSchedules, v)
	}
	switch v := extra[SetSmoke]; v {
	case "", "false":
	case "true":
		s.smoke = true
	default:
		return s, fmt.Errorf("%s=%q: want true or false", SetSmoke, v)
	}
	if v := extra[SetSuiteVer]; v != "" {
		s.suiteVers = v
	}
	return s, nil
}

// stage is one schedule stage: one agent session.
type stage struct {
	Label     string `json:"label"`
	Instances int    `json:"instances"`
}

// stream is one upstream (family, schedule) pair.
type stream struct {
	family   string
	schedule string
	display  string
	desc     string
	stages   []stage
	id       string
	draft    string
}

type scheduleFile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Stages      []struct {
		Label    string         `json:"label"`
		Variant  string         `json:"variant"`
		Schedule map[string]any `json:"schedule"`
	} `json:"stages"`
}

var badIDChars = regexp.MustCompile(`[^a-z0-9.-]+`)

func clean(s string) string {
	return strings.Trim(badIDChars.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// readStreams lists every family x schedule under from/src/tasks.
func readStreams(from string, set settings) ([]stream, []importers.Skip, error) {
	root := filepath.Join(from, "src", "tasks")
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w (want a clone of %s)", root, err, upstreamRepo)
	}
	var out []stream
	var skips []importers.Skip
	for _, e := range ents {
		if !e.IsDir() || !isFile(filepath.Join(root, e.Name(), "task.py")) {
			continue
		}
		fam := e.Name()
		info, known := families[fam]
		files, _ := filepath.Glob(filepath.Join(root, fam, "schedules", "*.json"))
		sort.Strings(files)
		if len(files) == 0 {
			skips = append(skips, importers.Skip{ID: fam, Reason: "no named schedule in schedules/"})
			continue
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, nil, err
			}
			var sf scheduleFile
			if err := json.Unmarshal(b, &sf); err != nil {
				skips = append(skips, importers.Skip{ID: fam + "/" + filepath.Base(f), Reason: "schedule is not valid JSON: " + err.Error()})
				continue
			}
			if sf.ID == "" {
				sf.ID = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			short := info.short
			if short == "" {
				short = clean(fam)
			}
			st := stream{family: fam, schedule: sf.ID, display: sf.DisplayName, desc: sf.Description, id: "clb-" + short + "-" + clean(sf.ID)}
			if set.onlyDef && sf.ID != "default" {
				continue
			}
			if !set.smoke && slices.Contains(smokeSchedules, sf.ID) {
				skips = append(skips, importers.Skip{ID: st.id, Reason: "smoke-test schedule (pass -set smoke=true to import it)"})
				continue
			}
			for i, sg := range sf.Stages {
				n := intOf(sg.Schedule["num_instances"])
				if n == 0 {
					n = intOf(sg.Schedule["num_questions"])
				}
				label := sg.Label
				if label == "" {
					label = fmt.Sprintf("Stage %d", i+1)
				}
				st.stages = append(st.stages, stage{Label: label, Instances: n})
			}
			switch {
			case !known:
				st.draft = "family not mapped by this importer version"
			case info.draft != "":
				st.draft = info.draft
			case len(st.stages) == 0:
				st.draft = "schedule has no stages"
			case info.needsDB && !(isFile(filepath.Join(set.dbDir, "products.db")) && isFile(filepath.Join(set.dbDir, "products_drifted.db"))):
				st.draft = "products.db and products_drifted.db not found in " + set.dbDir + " (download them from huggingface.co/datasets/continual-learning-bench/database-exploration and pass -set db_dir=<folder>)"
			}
			if st.draft == "" {
				for _, sg := range st.stages {
					if sg.Instances <= 0 {
						st.draft = "a stage does not state its instance count (num_instances or num_questions)"
						break
					}
				}
			}
			out = append(out, st)
		}
	}
	return out, skips, nil
}

func intOf(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("clbench: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.From, opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("clbench: %w", err)
	}
	rev := set.ref
	if rev == "" {
		rev = gitHead(opts.From)
	}
	if rev == "" {
		return nil, errors.New("clbench: cannot read the upstream commit from -from/.git; pass -set ref=<commit>")
	}
	streams, skips, err := readStreams(opts.From, set)
	if err != nil {
		return nil, fmt.Errorf("clbench: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir(), Skipped: skips}
	var picked []stream
	for _, st := range streams {
		if len(opts.IDs) > 0 && !slices.Contains(opts.IDs, st.id) && !slices.Contains(opts.IDs, st.family+"/"+st.schedule) {
			continue
		}
		picked = append(picked, st)
	}
	if opts.Limit > 0 && len(picked) > opts.Limit {
		for _, st := range picked[opts.Limit:] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: st.id, Reason: fmt.Sprintf("over -limit %d", opts.Limit)})
		}
		picked = picked[:opts.Limit]
	}
	drafts := 0
	for _, st := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("clbench: %w", err)
		}
		dir := filepath.Join(res.SuiteDir, "tasks", st.id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: st.id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := writeTask(dir, opts.From, set, rev, st); err != nil {
			return nil, fmt.Errorf("clbench: task %s: %w", st.id, err)
		}
		if st.draft != "" {
			drafts++
		}
		res.Tasks = append(res.Tasks, st.id)
	}
	if err := writeSuite(res.SuiteDir, opts.SuiteID, set.suiteVers, res.Tasks, rev, im.now()); err != nil {
		return nil, fmt.Errorf("clbench: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = []string{
		fmt.Sprintf("%d of %d task(s) are drafts whose reference control refuses with the reason (see each IMPORT.md)", drafts, len(res.Tasks)),
		"the first run builds each stream's clenv service image with network (pip installs the pinned upstream dependencies)",
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID,
	}
	return res, nil
}

// gitHead reads HEAD of a clone without running git.
func gitHead(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(b))
	if !strings.HasPrefix(h, "ref: ") {
		return h
	}
	ref := strings.TrimPrefix(h, "ref: ")
	if b, err := os.ReadFile(filepath.Join(dir, ".git", filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".git", "packed-refs")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
				return f[0]
			}
		}
	}
	return ""
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func sessionWall(st stream, n int) int {
	return min(maxSessionS, max(minSessionS, n*families[st.family].perInst))
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeTask writes one stream's task folder, replacing what was there.
func writeTask(dir, from string, set settings, rev string, st stream) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	info := families[st.family]
	token, err := randomToken()
	if err != nil {
		return err
	}
	stagesJSON, err := json.Marshal(st.stages)
	if err != nil {
		return err
	}
	pins := append(append([]string(nil), basePins...), info.pins...)
	dockerfile := "# Written by `bifrost-bench import clbench`: the CL-Bench stream service.\n" +
		"FROM " + workspaceImage + "\n" +
		"RUN pip install --no-cache-dir " + strings.Join(pins, " ") + "\n" +
		"COPY clbench/ /opt/clbench/\n" +
		"COPY server.py /opt/clenv/server.py\n" +
		"ENV CLBENCH_ROOT=/opt/clbench PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1\n" +
		fmt.Sprintf("EXPOSE %d\n", servicePort) +
		"CMD [\"python\", \"/opt/clenv/server.py\"]\n"

	files := map[string]string{
		"task.yaml":                   taskYAML(st, rev, token, string(stagesJSON)),
		"instruction.md":              overview(st),
		"repo/README.md":              "Workspace of CL-Bench stream " + st.family + "/" + st.schedule + ". The stream lives in the MCP server `" + serviceID + "`; files here are not part of the result and are reset between sessions. Keep what you learn in your memory folder ($BENCH_MEMORY_DIR, /memory).\n",
		"service/Dockerfile":          dockerfile,
		"service/server.py":           serverPy,
		"verifier/tests/runverify.py": runVerify,
		"controls/empty.sh":           "#!/bin/sh\n# Empty control: no tool call, no instance is played.\nexit 0\n",
		"controls/exploit.sh":         "#!/bin/sh\n# Exploit control (must fail): claims the stream is done without any tool call.\necho 'All sessions complete; every instance answered optimally.'\nexit 0\n",
		"IMPORT.md":                   importNotes(st, rev),
	}
	for i, sg := range st.stages {
		files[fmt.Sprintf("sessions/session-%d.md", i+1)] = sessionInstruction(st, i, sg)
	}
	switch {
	case st.draft != "":
		files["controls/reference.sh"] = "#!/bin/sh\n# Draft: this stream cannot run in the bench.\necho " + shellQuote("draft: "+st.draft) + " >&2\nexit 1\n"
	case info.hasRef:
		ref := strings.ReplaceAll(referencePy, "__TOKEN__", token)
		ref = strings.ReplaceAll(ref, "__SESSIONS__", fmt.Sprint(len(st.stages)))
		files["controls/reference.sh"] = "#!/bin/sh\n# Reference control: upstream's reference policy, played through the clenv service.\nexec python3 - <<'PY'\n" + ref + "PY\n"
	default:
		files["controls/reference.sh"] = "#!/bin/sh\n# Reference control. CL-Bench ships a reference policy only for exploitable_poker,\n" +
			"# so this stream has none until one is written: a script that plays the\n# stream through the MCP endpoint in $BENCH_MCP_SERVERS. Admission fails until then.\n" +
			"echo 'no upstream reference policy for " + st.family + "; write controls/reference.sh' >&2\nexit 1\n"
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(files[n]), mode); err != nil {
			return err
		}
	}
	// The service's copy of upstream: src/ and LICENSE always, the family's
	// data folder, and the database files for database_exploration.
	svc := filepath.Join(dir, "service", "clbench")
	if err := copyTree(filepath.Join(from, "src"), filepath.Join(svc, "src")); err != nil {
		return err
	}
	for _, f := range []string{"LICENSE", "pyproject.toml"} {
		if isFile(filepath.Join(from, f)) {
			if err := copyFile(filepath.Join(from, f), filepath.Join(svc, f)); err != nil {
				return err
			}
		}
	}
	if d := filepath.Join(from, "data", st.family); dirExists(d) {
		if err := copyTree(d, filepath.Join(svc, "data", st.family)); err != nil {
			return err
		}
	}
	if info.needsDB && st.draft == "" {
		for _, f := range []string{"products.db", "products_drifted.db"} {
			if err := copyFile(filepath.Join(set.dbDir, f), filepath.Join(svc, "data", st.family, f)); err != nil {
				return err
			}
		}
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// copyTree copies regular files under src to dst, leaving out caches and
// symlinks.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "__pycache__" || d.Name() == ".pytest_cache") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func totalInstances(st stream) int {
	n := 0
	for _, s := range st.stages {
		n += s.Instances
	}
	return n
}

func taskYAML(st stream, rev, token, stagesJSON string) string {
	info := families[st.family]
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	title := info.capTitle
	if title == "" {
		title = st.family
	}
	p("# Imported from CL-Bench by `bifrost-bench import clbench`; see docs/importing-clbench.md.\n")
	p("id: %s\nversion: %s\n", st.id, taskVersion)
	p("title: %s\n", q("CL-Bench "+title+": "+firstNonEmpty(st.display, st.schedule)))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("clbench-"+clean(st.family)))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: []\n  exploit: controls/exploit.sh\n")
	total := 0
	for i, sg := range st.stages {
		total += sessionWall(st, sg.Instances)
		_ = i
	}
	if total == 0 {
		total = minSessionS
	}
	p("timeouts:\n  agent_s: %d\n", total)
	tags := []string{"clbench", "continual-learning", st.family, "schedule-" + clean(st.schedule)}
	if st.draft != "" {
		tags = append(tags, "draft")
	}
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = q(t)
	}
	p("tags: [%s]\n", strings.Join(quoted, ", "))
	p("capabilities: [tool_use, planning]\n")
	p("difficulty: hard\n")
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), rev, upstreamLicense, q("clbench "+st.family+" --schedule "+st.schedule), q("bifrost-bench import clbench"))
	p("environment:\n  image: %s\n  build_network: true\n", q(workspaceImage))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n", serviceID)
	p("    build:\n      dockerfile: service/Dockerfile\n      tag: %s\n", q("bench/clbench-"+info.shortOr(st.family)+":"+shortHash(rev, st.family, stagesJSON)))
	p("    port: %d\n", servicePort)
	p("    env:\n      CLBENCH_FAMILY: %s\n      CLBENCH_SCHEDULE: %s\n      CLBENCH_STAGES: %s\n      CLBENCH_PORT: %s\n      CLBENCH_REFERENCE_TOKEN: %s\n",
		q(st.family), q(st.schedule), q(stagesJSON), q(fmt.Sprint(servicePort)), q(token))
	p("    ready:\n      http: /healthz\n      timeout_s: %d\n", readyTimeoutS)
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n      - id: result\n        argv: [python3, -c, %s]\n        max_bytes: %d\n",
		q(fmt.Sprintf("import sys,urllib.request as u;sys.stdout.write(u.urlopen(u.Request('http://127.0.0.1:%d/finalize',method='POST'),timeout=600).read().decode())", servicePort)), 16<<20)
	p("sessions:\n")
	for i, sg := range st.stages {
		p("  - id: %s\n    instruction: %s\n    wall_s: %d\n", q(fmt.Sprintf("stage-%d", i+1)), q(fmt.Sprintf("sessions/session-%d.md", i+1)), sessionWall(st, sg.Instances))
	}
	p("output:\n  kind: state\n")
	return b.String()
}

func (f family) shortOr(name string) string {
	if f.short != "" {
		return f.short
	}
	return clean(name)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func overview(st stream) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CL-Bench stream: %s (schedule %s)\n\n", st.family, st.schedule)
	if st.desc != "" {
		fmt.Fprintf(&b, "%s\n\n", st.desc)
	}
	fmt.Fprintf(&b, "This is a continual-learning task from Continual Learning Bench. You work through a stream of %d related instances that share a structure you can learn, over %d sessions, one per stage. Each session is a fresh start of you as an agent: only what you write to your memory folder (`$BENCH_MEMORY_DIR`, `/memory`) carries over.\n\n", totalInstances(st), len(st.stages))
	b.WriteString("The stream is served by the MCP server `clenv`: `get_brief`, `start_session`, `get_query`, `submit`. Your score is the reward the benchmark gives your responses across the whole stream.\n")
	return b.String()
}

func sessionInstruction(st stream, i int, sg stage) string {
	var b strings.Builder
	n := len(st.stages)
	fmt.Fprintf(&b, "# CL-Bench stream %s, session %d of %d\n\n", st.family, i+1, n)
	fmt.Fprintf(&b, "Stage: %s (%d instances).\n\n", sg.Label, sg.Instances)
	b.WriteString("You are working through a Continual Learning Bench stream: a sequence of related instances served by the MCP server `clenv`. The instances share structure you can learn from the feedback you get, and the stream continues across sessions.\n\n")
	b.WriteString("1. Read your memory folder (`$BENCH_MEMORY_DIR`, normally `/memory`) first")
	if i == 0 {
		b.WriteString(" (it is empty in session 1)")
	}
	b.WriteString(". It is the only thing that carries over from earlier sessions.\n")
	fmt.Fprintf(&b, "2. Call `start_session` with `session: %d`, then `get_brief` if you need the objective and reward definition.\n", i+1)
	b.WriteString("3. Loop: `get_query` returns the prompt, the JSON schema your response must follow (`response_schema`) and feedback on your previous response. Answer with `submit` (`response`: a JSON object matching the schema). Some instances take several turns (tool calls inside the task); keep going until the instance completes.\n")
	b.WriteString("4. When a call returns status `session_complete` or `stream_complete`, write what you learned (patterns, rules, mistakes to avoid, anything that will help later instances) to your memory folder, then stop.\n\n")
	b.WriteString("Every instance you leave unanswered scores zero. Do not try to reach the server other than through these tools.\n")
	return b.String()
}

func importNotes(st stream, rev string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Import notes: %s\n\n", st.id)
	fmt.Fprintf(&b, "- Upstream: %s at %s (%s), family `%s`, schedule `%s`.\n", upstreamRepo, rev, upstreamLicense, st.family, st.schedule)
	fmt.Fprintf(&b, "- %d instances in %d stages; one agent session per stage:\n", totalInstances(st), len(st.stages))
	for i, sg := range st.stages {
		fmt.Fprintf(&b, "  - session %d: %s, %d instances, wall %d s\n", i+1, sg.Label, sg.Instances, sessionWall(st, sg.Instances))
	}
	b.WriteString("- The service `clenv` builds the upstream task as `clbench run " + st.family + " --schedule " + st.schedule + "` does for run index 0 and steps it with every submitted response.\n")
	b.WriteString("- Reward: sum of instance rewards / (instances x upstream `r_max`), clipped to 0..1; pass at 0.5. Dimensions: normalized_reward, completion, mean_instance_reward, sessions_used, invalid_submissions.\n")
	b.WriteString("- Session gating is soft: the agent could start a later session early; each outcome records the session it was played in and `sessions_used` is reported.\n")
	if st.draft != "" {
		b.WriteString("\n**Draft:** " + st.draft + "\n")
	} else if !families[st.family].hasRef {
		b.WriteString("\n**No reference:** CL-Bench ships a reference policy only for exploitable_poker; this task stays a draft until a reference control is written.\n")
	}
	return b.String()
}

// writeSuite writes suite.yaml, keeping the task ids of an earlier import.
func writeSuite(dir, id, version string, tasks []string, rev string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	all := []string{}
	if b, err := os.ReadFile(filepath.Join(dir, "suite.yaml")); err == nil {
		var s schemas.Suite
		if err := yaml.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("read existing suite.yaml: %w", err)
		}
		if s.Imported == nil || s.Imported.Importer != Name {
			return fmt.Errorf("suite %s exists and was not written by the %s importer", id, Name)
		}
		all = append(all, s.TaskIDs...)
	}
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from CL-Bench by `bifrost-bench import clbench`; see docs/importing-clbench.md.\n")
	p("id: %s\nversion: %s\n", id, version)
	p("description: %s\n", q("Continual Learning Bench (CL-Bench): each stream (task family x schedule) is one multi-session task; the upstream task runs behind an MCP service, one agent session per stage, and only the agent's memory carries over between sessions."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  tag: %s\n", q(workspaceImage))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python3, /grader/tests/runverify.py]\n")
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: %g\n", verifierTimeout, passThreshold)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	p("track: %s\ngrading: %s\n", schemas.TrackMemory, schemas.GradingVerified)
	p("requires: [mcp, sessions, tools]\n")
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		Name, q("CL-Bench (continual-learning-bench)"), q(upstreamRepo), rev, upstreamLicense, now.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"))
	return os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(b.String()), 0o644)
}
