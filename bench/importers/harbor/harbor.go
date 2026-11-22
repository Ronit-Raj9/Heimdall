package harbor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the import format name: `bifrost-bench import harbor`.
const Name = "harbor"

// Settings read from repeated -set key=value.
const (
	SetBenchmark     = "benchmark"       // benchmark@version recorded in the suite; default detected
	SetSource        = "source"          // upstream repository URL; default the clone's origin
	SetRevision      = "revision"        // upstream commit; default the clone's HEAD
	SetLicense       = "license"         // SPDX id; default read from the LICENSE file
	SetTasksDir      = "tasks_dir"       // folder of task folders, relative to -from; default detected
	SetTrack         = "track"           // terminal or skills; default skills when a task has skills
	SetAgent         = "agent"           // base agent config of the skills experiment; default agents/codex.yaml
	SetKeepNetworked = "keep_networked"  // true imports tasks whose reference or test script downloads at run time (they fail admission: drafts)
	SetImage         = "image"           // build (default) builds the upstream Dockerfile; prebuilt starts FROM the image task.toml names
	SetHackVerify    = "hack_verifiable" // skip (default) or keep tasks with a planted honeypot (Hack-Verifiable Terminal-Bench)
	SetAgentTimeout  = "agent_timeout"   // seconds: every task's agent budget (Harbor's override_timeout_sec); default task.toml's
	SetPassThreshold = "pass_threshold"  // lowest passing reward, for partial-credit benchmarks; default 1
	SetMaxSolution   = "max_solution_mb" // largest packed solution+skills the reference control embeds, in MiB; default 2
	SetLintWaivers   = "lint_waivers"    // none (default) or upstream: waive the instruction-words and high-entropy-identifier checks on verbatim upstream text (task.yaml lint_waivers)
	SetUnrunnable    = "unrunnable"      // skip (default) or draft: import tasks the bench cannot admit as drafts with the reason
)

const (
	defaultAgentS     = 900
	defaultVerifierS  = 900
	verifierMarginS   = 60
	maxSolutionBytes  = 2 << 20
	taskVersion       = "1.0.0"
	suiteVersion      = "1.0.0"
	defaultAgentFile  = "agents/codex.yaml"
	experimentFile    = "experiment-skills.yaml"
	verifyScript      = "/grader/tests/harbor/verify.sh"
	rewardResultPath  = "/grader/out/reward.json"
	importerCreatedBy = "bifrost-bench import harbor"
)

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func init() {
	importers.Register(&importer{now: time.Now})
}

// importer converts Harbor-format task folders (Terminal-Bench 2.x,
// SkillsBench, and Harbor hub downloads such as Hack-Verifiable
// Terminal-Bench) and SkillsBench's task.md packages into a suite.
type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name: Name,
		Benchmarks: []string{
			"terminal-bench@2.0",
			"skillsbench@1.0 (Harbor layout, tag v1.0)",
			"skillsbench@1.1+ (task.md layout)",
			"hv-terminal-bench-2-1 (Harbor hub download; skipped unless -set hack_verifiable=keep)",
		},
		Source:  "git clone --depth 1 https://github.com/laude-institute/terminal-bench-2 datasets/terminal-bench-2",
		License: "varies (Apache-2.0 for Terminal-Bench 2 and SkillsBench)",
		Track:   schemas.TrackTerminal,
		Grading: schemas.GradingVerified,
		Needs: []string{
			"network while each task image builds (build_network)",
			"snapshot verifier mode (a writable root file system, or at least a writable /tmp)",
			"script controls (the reference is the upstream solve.sh)",
		},
		Summary: "Harbor task folders (task.toml or task.md): one image per task, graded by the upstream test script",
	}
}

// Import implements importers.Importer. It reads every task folder under
// opts.From, skips what the bench cannot run honestly (with the reason),
// writes the rest as tasks of opts.SuiteID and writes or extends suite.yaml.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if !idRE.MatchString(opts.SuiteID) {
		return nil, fmt.Errorf("harbor: suite id %q: want lower-case letters, digits, '.', '_' or '-'", opts.SuiteID)
	}
	if !isDir(opts.From) {
		return nil, fmt.Errorf("harbor: -from %s is not a folder", opts.From)
	}
	cfg, err := readSettings(opts.Extra)
	if err != nil {
		return nil, err
	}
	dirs, err := taskDirs(opts.From, cfg.tasksDir)
	if err != nil {
		return nil, err
	}
	up := describeUpstream(opts.From, cfg)
	suiteDir := opts.SuiteDir()
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir, Tasks: []string{}, Skipped: []importers.Skip{}}
	selected, missing := selectDirs(dirs, opts.IDs)
	for _, id := range missing {
		res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "no task folder with this name under " + opts.From})
	}
	// Refuse a suite this importer cannot extend before any task folder is
	// staged, so a refused import changes nothing on disk.
	if err := checkExistingSuite(suiteDir, opts.SuiteID, up); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(suiteDir, "tasks"), 0o755); err != nil {
		return nil, fmt.Errorf("harbor: %w", err)
	}
	var written []*converted
	for _, dir := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.Limit > 0 && len(written) >= opts.Limit {
			break
		}
		c, reason, err := im.importTask(dir, suiteDir, up, cfg, opts.Force)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: filepath.Base(dir), Reason: reason})
			continue
		}
		written = append(written, c)
		res.Tasks = append(res.Tasks, c.id)
		if c.draft != "" {
			res.Notes = append(res.Notes, fmt.Sprintf("draft %s (admission will refuse it): %s", c.id, c.draft))
		}
	}
	if len(written) == 0 {
		return res, errors.New("harbor: no task was imported; see the skipped list")
	}
	track := cfg.track
	if track == "" {
		track = schemas.TrackTerminal
		for _, c := range written {
			if c.skills {
				track = schemas.TrackSkills
			}
		}
	}
	if err := writeSuite(suiteDir, opts.SuiteID, up, track, written, cfg.passThreshold, im.now()); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, notes(opts.SuiteID)...)
	if track == schemas.TrackSkills {
		if err := writeExperiment(suiteDir, opts.SuiteID, cfg.agent); err != nil {
			return nil, err
		}
		res.Notes = append(res.Notes, fmt.Sprintf("compare with and without skills: bifrost-bench run benchmarks/%s/%s", opts.SuiteID, experimentFile))
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	if opts.Logger != nil {
		opts.Logger.Info("harbor import", "suite", opts.SuiteID, "tasks", len(res.Tasks), "skipped", len(res.Skipped))
	}
	return res, nil
}

// settings are the parsed -set values.
type settings struct {
	benchmark, source, revision, license string
	tasksDir, track, agent, image        string
	keepNetworked, keepHoneypots         bool
	egress                               bool // -set egress=record (egress.go)
	agentTimeoutS                        int
	passThreshold                        float64
	drafts                               bool
	maxSolutionBytes                     int
	waive                                bool
}

func readSettings(extra map[string]string) (settings, error) {
	cfg := settings{agent: defaultAgentFile, image: "build"}
	for k, v := range extra {
		switch k {
		case SetBenchmark:
			cfg.benchmark = v
		case SetSource:
			cfg.source = v
		case SetRevision:
			cfg.revision = v
		case SetLicense:
			cfg.license = v
		case SetTasksDir:
			cfg.tasksDir = v
		case SetTrack:
			if v != schemas.TrackTerminal && v != schemas.TrackSkills {
				return cfg, fmt.Errorf("harbor: -set track=%s: want terminal or skills", v)
			}
			cfg.track = v
		case SetAgent:
			cfg.agent = v
		case SetKeepNetworked:
			cfg.keepNetworked = v == "true" || v == "1" || v == "yes"
		case SetEgress: // recorded internet (egress.go)
			if v != "record" && v != "off" {
				return cfg, fmt.Errorf("harbor: -set egress=%s: want record or off", v)
			}
			cfg.egress = v == "record"
		case SetHackVerify:
			if v != "skip" && v != "keep" {
				return cfg, fmt.Errorf("harbor: -set hack_verifiable=%s: want skip or keep", v)
			}
			cfg.keepHoneypots = v == "keep"
		case SetImage:
			if v != "build" && v != "prebuilt" {
				return cfg, fmt.Errorf("harbor: -set image=%s: want build or prebuilt", v)
			}
			cfg.image = v
		case SetAgentTimeout:
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return cfg, fmt.Errorf("harbor: -set agent_timeout=%s: want a positive number of seconds", v)
			}
			cfg.agentTimeoutS = n
		case SetPassThreshold:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 || f > 1 {
				return cfg, fmt.Errorf("harbor: -set pass_threshold=%s: want a reward above 0 and at most 1", v)
			}
			cfg.passThreshold = f
		case SetMaxSolution:
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 256 {
				return cfg, fmt.Errorf("harbor: -set max_solution_mb=%s: want a whole number of MiB from 1 to 256", v)
			}
			cfg.maxSolutionBytes = n << 20
		case SetLintWaivers:
			if v != "none" && v != "upstream" {
				return cfg, fmt.Errorf("harbor: -set lint_waivers=%s: want none or upstream", v)
			}
			cfg.waive = v == "upstream"
		case SetUnrunnable:
			if v != "skip" && v != "draft" {
				return cfg, fmt.Errorf("harbor: -set unrunnable=%s: want skip or draft", v)
			}
			cfg.drafts = v == "draft"
		default:
			return cfg, fmt.Errorf("harbor: unknown setting %q (known: benchmark, source, revision, license, tasks_dir, track, agent, keep_networked, image, hack_verifiable, egress, agent_timeout, pass_threshold, max_solution_mb, lint_waivers, unrunnable)", k)
		}
	}
	return cfg, nil
}

// taskDirs lists the task folders: -from itself when it is one, else its
// subfolders that are, else those of its tasks/ folder.
func taskDirs(from, tasksDir string) ([]string, error) {
	if tasksDir != "" {
		from = filepath.Join(from, filepath.FromSlash(tasksDir))
	} else if detectLayout(from) != "" {
		return []string{from}, nil
	}
	list := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && detectLayout(filepath.Join(dir, e.Name())) != "" {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
		sort.Strings(out)
		return out
	}
	dirs := list(from)
	if len(dirs) == 0 && tasksDir == "" {
		dirs = list(filepath.Join(from, "tasks"))
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("harbor: no task folder (holding task.toml or task.md) under %s", from)
	}
	return dirs, nil
}

// selectDirs keeps the folders named in ids (all when empty) and returns the
// ids that matched none.
func selectDirs(dirs, ids []string) ([]string, []string) {
	if len(ids) == 0 {
		return dirs, nil
	}
	byName := map[string]string{}
	for _, d := range dirs {
		byName[filepath.Base(d)] = d
	}
	var out, missing []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if d, ok := byName[id]; ok {
			out = append(out, d)
		} else if id != "" {
			missing = append(missing, id)
		}
	}
	return out, missing
}

// converted is one task the importer wrote.
type converted struct {
	id        string
	workdir   string
	verifierS int
	skills    bool
	draft     string // why admission will refuse it (-set unrunnable=draft); empty for a runnable task
}

// importTask reads, checks and writes one task. It returns a skip reason
// for a task the bench cannot run as upstream meant it, and an error only
// when writing fails.
func (im *importer) importTask(dir, suiteDir string, up upstream, cfg settings, force bool) (*converted, string, error) {
	t, err := readTask(dir)
	if err != nil {
		return nil, "cannot read the task: " + err.Error(), nil
	}
	id := taskID(t.ID)
	if !idRE.MatchString(id) {
		return nil, fmt.Sprintf("folder name %q cannot be made a task id", t.ID), nil
	}
	p, reason := plan(t, cfg)
	if reason != "" && cfg.drafts {
		p = draftPlan(t, cfg, reason)
	}
	if p == nil {
		return nil, reason, nil
	}
	p.waive = cfg.waive
	p.passThreshold = cfg.passThreshold
	taskDir := filepath.Join(suiteDir, "tasks", id)
	if _, err := os.Stat(taskDir); err == nil {
		if !force {
			return nil, "already imported into this suite; pass -force to replace it", nil
		}
		if err := os.RemoveAll(taskDir); err != nil {
			return nil, "", fmt.Errorf("harbor: replace task %s: %w", id, err)
		}
	}
	if err := writeTask(taskDir, id, t, p, up); err != nil {
		if rmErr := os.RemoveAll(taskDir); rmErr != nil {
			err = errors.Join(err, rmErr)
		}
		var skip *skipError
		if errors.As(err, &skip) {
			return nil, skip.reason, nil
		}
		return nil, "", fmt.Errorf("harbor: write task %s: %w", id, err)
	}
	return &converted{id: id, workdir: p.workdir, verifierS: p.verifierS, skills: p.skills, draft: p.draft}, "", nil
}

// taskPlan is everything decided about one task before it is written.
type taskPlan struct {
	workdir        string
	dockerfile     string // the task's Dockerfile, rewritten
	tests          *testPlan
	skillCopy      int  // COPY skills lines disabled
	skills         bool // the task brings skills
	honeypot       bool // a planted admin/ folder watched by inotifywait (Hack-Verifiable Terminal-Bench)
	agentS         int
	verifierS      int
	solution       []byte       // tar.gz of the solution folder
	skillsTar      []byte       // tar.gz of the skills folder, for the reference control
	skillRoots     []string     // folders the reference control installs the skills in
	compose        *composePlan // X2: the task's compose file, mapped (compose.go); nil without one
	egressFetch    string       // recorded internet: the first download line; set when the task gets an egress service (egress.go)
	egressVerifier bool         // recorded internet: test.sh downloads too, so the verifier reaches the egress service
	// draft is why the bench cannot admit the task (-set unrunnable=draft):
	// it is written with a reference control that fails with this reason,
	// so admission refuses it and it stays a draft in the suite.
	draft string
	// solveChanges are the IMPORT.md lines of a solve.sh made offline
	// (offline.go).
	solveChanges  []string
	waive         bool // -set lint_waivers=upstream
	sepNotes      []string
	passThreshold float64 // -set pass_threshold, for the IMPORT.md note
}

// plan checks t against what the bench can run and prepares the rewritten
// Dockerfile and test script. The reason is empty when the task can be
// imported.
func plan(t *upstreamTask, cfg settings) (*taskPlan, string) {
	dockerfile := filepath.Join(t.EnvDir, "Dockerfile")
	hasDockerfile := isFile(dockerfile)
	switch {
	case t.GPUs > 0:
		return nil, fmt.Sprintf("needs %d GPU(s); the bench schedules none", t.GPUs)
	case t.SeparateVerifier && len(t.verifierCollect) > 0:
		return nil, "grades in a separate verifier container with [[verifier.collect]], which the importer does not map"
	case t.SeparateVerifier && t.VerifierImage != "":
		return nil, "grades in a separate verifier container from a prebuilt image ([verifier.environment] docker_image), which the importer does not map"
	case t.SeparateVerifier && !isFile(filepath.Join(t.TestsDir, "Dockerfile")):
		return nil, "grades in a separate verifier container whose image is not built from tests/Dockerfile (none there), which the importer does not map"
	case t.MCPServers > 0:
		return nil, "declares MCP servers in task.toml; the importer does not map them to services"
	case !hasDockerfile && (cfg.image != "prebuilt" || t.DockerImage == ""):
		return nil, "no environment/Dockerfile"
	case cfg.image == "prebuilt" && t.DockerImage == "":
		return nil, "-set image=prebuilt, but task.toml names no docker_image"
	case !isFile(filepath.Join(t.SolutionDir, "solve.sh")):
		return nil, fmt.Sprintf("no %s/solve.sh, so there is no reference control", filepath.Base(t.SolutionDir))
	case !isFile(filepath.Join(t.TestsDir, "test.sh")):
		return nil, fmt.Sprintf("no %s/test.sh", filepath.Base(t.TestsDir))
	}
	for _, env := range []map[string]string{t.EnvEnv, t.VerifierEnv, t.SolutionEnv} {
		for _, k := range sortedEnvKeys(env) {
			if strings.Contains(env[k], "${") {
				return nil, fmt.Sprintf("environment variable %s is taken from the host (%s), which a run never passes in", k, short(env[k]))
			}
		}
	}
	solve, err := os.ReadFile(filepath.Join(t.SolutionDir, "solve.sh"))
	if err != nil {
		return nil, "cannot read solve.sh: " + err.Error()
	}
	fetch := solutionFetch(string(solve))
	var solveSrc string
	var solveBake, solveChanges []string
	if fetch != "" && !cfg.keepNetworked && !cfg.egress { // with -set egress=record the recorded internet serves the download (egress.go)
		line := fetch
		var bad string
		solveSrc, solveBake, solveChanges, bad = planSolveScript(string(solve), buildRequires(t.EnvDir))
		if bad != "" || solveSrc == "" {
			if bad != "" {
				line = bad
			}
			return nil, "the reference solution downloads at run time (" + line + "); runs have no network, so the agent could not do it either; -set egress=record imports it with a recorded internet"
		}
	}
	testSrc, err := os.ReadFile(filepath.Join(t.TestsDir, "test.sh"))
	if err != nil {
		return nil, "cannot read test.sh: " + err.Error()
	}
	var sep *sepPlan
	var tests *testPlan
	if t.SeparateVerifier {
		var reason string
		if sep, reason = planSeparate(t); reason != "" {
			return nil, reason
		}
		tests = &testPlan{Script: separateTestScript(t, sep), Changes: []string{"replaced by the separate-verifier driver: upstream test.sh runs unchanged, chrooted in the verifier image's file system (" + verifierRoot + ")"}}
	} else if tests, err = planTestScriptKeep(string(testSrc), cfg.keepNetworked || cfg.egress); err != nil {
		return nil, err.Error()
	}
	p := &taskPlan{tests: tests, agentS: agentSeconds(t, cfg), verifierS: seconds(t.VerifierTimeoutS, defaultVerifierS), solveChanges: solveChanges}
	var src string
	if hasDockerfile {
		b, err := os.ReadFile(dockerfile)
		if err != nil {
			return nil, "cannot read the Dockerfile: " + err.Error()
		}
		src = string(b)
	}
	p.honeypot = isDir(filepath.Join(t.EnvDir, "admin")) && strings.Contains(src, "inotifywait")
	if p.honeypot && !cfg.keepHoneypots {
		return nil, "hack-verifiable task: the image plants the solution and tests in admin/ and detects reads with inotifywait watchers started by its ENTRYPOINT, " +
			"which the bench replaces with its own, so reading them would go unseen; -set hack_verifiable=keep imports it with admin/** protected"
	}
	info := parseDockerfile(src)
	p.workdir = info.Workdir
	if t.Workdir != "" {
		p.workdir = t.Workdir
	}
	if p.workdir == "" || p.workdir == "/" {
		return nil, "the Dockerfile's final stage sets no literal WORKDIR, so the workspace path is unknown (Harbor's test scripts refuse to run from /)"
	}
	if t.Compose { // X2: a compose file is mapped to services (compose.go)
		cp, reason := planCompose(t, p.workdir)
		if reason != "" {
			return nil, reason
		}
		p.compose = cp
	}
	if cfg.image == "prebuilt" {
		src = fmt.Sprintf("# Upstream's prebuilt image, as task.toml names it (bifrost-bench import harbor -set image=prebuilt).\nFROM %s\nWORKDIR %s\n", t.DockerImage, p.workdir)
		info.User = ""
	}
	src, p.skillCopy = stripSkillCopies(src)
	p.skills = hasSkills(filepath.Join(t.EnvDir, "skills"))
	dirs := []string{"/logs/verifier", t.TestsMount, t.SolutionMount}
	if p.skills {
		dirs = append(dirs, defaultSkillRoot)
	}
	if sep != nil {
		src = sep.stage + "\n" + strings.TrimRight(src, "\n") + "\n# The separate verifier's file system (sepverify.go).\nCOPY --from=harbor-verifier / " + verifierRoot + "/\n"
		p.sepNotes = sep.notes
	}
	if cfg.egress && (fetch != "" || len(tests.Networked) > 0) { // recorded internet (egress.go)
		if reason := planEgress(p, t, fetch, cfg.keepNetworked); reason != "" {
			return nil, reason
		}
		src = strings.TrimRight(src, "\n") + "\n" + egressDockerfile()
	}
	p.dockerfile = strings.TrimRight(src, "\n") + "\n" + bakeSection(append(append([]string{}, tests.Bake...), solveBake...), dirs, t.EnvEnv, info.User)
	if p.egressFetch != "" {
		p.dockerfile += egressTail(info.User) // recorded internet: no build-date apt lists (egress.go)
	}
	var override map[string][]byte
	if solveSrc != "" {
		override = map[string][]byte{"solve.sh": []byte(solveSrc)}
	}
	if p.solution, err = tarGzWith(t.SolutionDir, override); err != nil {
		return nil, "cannot pack the solution folder: " + err.Error()
	}
	if p.skills {
		if p.skillsTar, err = tarGz(filepath.Join(t.EnvDir, "skills")); err != nil {
			return nil, "cannot pack the skills folder: " + err.Error()
		}
		p.skillRoots = skillRoots(t.SolutionDir)
	}
	limit := maxSolutionBytes
	if cfg.maxSolutionBytes > 0 {
		limit = cfg.maxSolutionBytes
	}
	if n := len(p.solution) + len(p.skillsTar); n > limit {
		return nil, fmt.Sprintf("the solution and skills folders pack to %d bytes, over the %d the reference control embeds (-set max_solution_mb raises it)", n, limit)
	}
	return p, ""
}

// defaultSkillRoot is where the reference control always installs a task's
// skills; the importer bakes it into the image as a writable folder.
const defaultSkillRoot = "/skills"

var skillPathRE = regexp.MustCompile(`(?:/[A-Za-z0-9_.-]+)*/skills\b`)

// skillRoots lists the folders the reference control installs the skills in:
// /skills, then every absolute .../skills path the solution files name.
// SkillsBench solutions import helper scripts from a skill and probe such
// paths, because each harness installs skills somewhere else.
func skillRoots(solutionDir string) []string {
	roots := []string{defaultSkillRoot}
	_ = filepath.WalkDir(solutionDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range skillPathRE.FindAllString(string(b), -1) {
			if strings.HasPrefix(m, "/") && !strings.Contains(m, "..") {
				roots = append(roots, path.Clean(m))
			}
		}
		return nil
	})
	return dedupe(roots)
}

// skipError is a problem found while writing that makes the task a skip
// rather than a failed import, such as a symlink that leaves the task folder.
type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

// hasSkills reports whether dir holds at least one skill folder.
func hasSkills(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// taskID makes a folder name a task id: lower case, other characters as '-'.
func taskID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// agentSeconds is the task's agent budget: -set agent_timeout when given
// (Harbor's override_timeout_sec), else task.toml's.
func agentSeconds(t *upstreamTask, cfg settings) int {
	if cfg.agentTimeoutS > 0 {
		return cfg.agentTimeoutS
	}
	return seconds(t.AgentTimeoutS, defaultAgentS)
}

// draftPlan prepares a task plan() refused, so it can be written as a draft
// under -set unrunnable=draft: the environment and the tests as upstream has
// them (download lines in test.sh kept), no compose services, and a reference
// control that fails with the reason. It returns nil when not even that can
// be written (no Dockerfile or prebuilt image, no test.sh, no workspace path).
func draftPlan(t *upstreamTask, cfg settings, reason string) *taskPlan {
	dockerfile := filepath.Join(t.EnvDir, "Dockerfile")
	var src string
	if b, err := os.ReadFile(dockerfile); err == nil {
		src = string(b)
	} else if cfg.image != "prebuilt" || t.DockerImage == "" {
		return nil
	}
	testSrc, err := os.ReadFile(filepath.Join(t.TestsDir, "test.sh"))
	if err != nil {
		return nil
	}
	tests, err := planTestScriptKeep(string(testSrc), true)
	if err != nil {
		tests = &testPlan{Script: string(testSrc), Changes: []string{"left as upstream wrote it (" + err.Error() + ")"}}
	}
	info := parseDockerfile(src)
	workdir := info.Workdir
	if t.Workdir != "" {
		workdir = t.Workdir
	}
	if workdir == "" || workdir == "/" {
		return nil
	}
	if cfg.image == "prebuilt" && t.DockerImage != "" {
		src = fmt.Sprintf("# Upstream's prebuilt image, as task.toml names it (bifrost-bench import harbor -set image=prebuilt).\nFROM %s\nWORKDIR %s\n", t.DockerImage, workdir)
		info.User = ""
	}
	src, skillCopy := stripSkillCopies(src)
	p := &taskPlan{
		workdir:   workdir,
		tests:     tests,
		skillCopy: skillCopy,
		skills:    hasSkills(filepath.Join(t.EnvDir, "skills")),
		agentS:    agentSeconds(t, cfg),
		verifierS: seconds(t.VerifierTimeoutS, defaultVerifierS),
		draft:     reason,
	}
	dirs := []string{"/logs/verifier", t.TestsMount, t.SolutionMount}
	if p.skills {
		dirs = append(dirs, defaultSkillRoot)
	}
	p.dockerfile = strings.TrimRight(src, "\n") + "\n" + bakeSection(tests.Bake, dirs, t.EnvEnv, info.User)
	return p
}

// seconds rounds a timeout up to whole seconds, with a default for none.
func seconds(v float64, def int) int {
	if v <= 0 {
		return def
	}
	return int(math.Ceil(v))
}

// notes are the next steps after an import.
func notes(suiteID string) []string {
	return []string{
		fmt.Sprintf("check the suite: bifrost-bench suite lint %s", suiteID),
		fmt.Sprintf("pin it: bifrost-bench suite lock %s", suiteID),
		fmt.Sprintf("admit the tasks (builds each task image, runs the upstream solution and the empty control): bifrost-bench suite admit %s", suiteID),
	}
}
