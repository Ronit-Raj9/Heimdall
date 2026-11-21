package main

// Runner jobs, server side (gap G05). Container-heavy CLI job kinds never
// run in the hosted server process: their hosted variants turn the job into
// a runner job lease on the org's coordinator (engine/remotejobs.go). The
// org's runner, or the shared runner leasing with the org's token, claims
// it, runs `bifrost-bench <argv>` against a snapshot of the project files
// the job needs, streams the redacted log back into the job's log (the
// normal JobLogPanel) and uploads the JSON result plus the files the job
// wrote, which land in the org project through projectfs. Cancelling the
// job stops the command on the runner. This file sorts after every file
// that registers the local variants, so registerOrMergeJobKind finds them.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// errNoJobRunner is the error of a runner job with no live runner. The UI
// recognises its first words and offers the Runners settings next to it.
var errNoJobRunner = errors.New("No worker online: this job starts containers, so on the hosted site it runs on a worker, never on the server. " +
	"Open Settings > Workers and use \"Add a worker\" to start one on your machine (one command), " +
	"or turn on the shared worker there if your server owner offers it (Settings > Workers shows its status).")

// runnerJobSnapshotDirs are the project folders a runner job takes with it,
// after the paths its argv names.
var runnerJobSnapshotDirs = []string{"agents", "policies", "benchmarks", "uploads"}

// runnerJobFileCap is the largest single project file a snapshot carries.
const runnerJobFileCap = 8 << 20

// runnerJobResultFileCap bounds one file a runner sends back.
const runnerJobResultFileCap = 16 << 20

// runnerResultRoots are project folders outside projectfs areas a runner
// job may write back (monitor certificates).
var runnerResultRoots = map[string]bool{"monitor-certs": true}

// runnerKind describes one hosted runner job kind.
type runnerKind struct {
	kind    string
	args    cliArgs                      // params -> argv (with cliConfig placeholders)
	collect func(argv []string) []string // project paths whose new files come back
	result  func(lines []string, res json.RawMessage) any
	// inProcess, when set and it answers true for the params, runs the
	// local variant in the server process instead (a static scan reads
	// files only).
	inProcess func(raw json.RawMessage) bool
}

func init() {
	for _, rk := range []runnerKind{
		{kind: kindAgentBuild, args: agentBuildArgs, result: agentBuildResult},
		{kind: "scan.mcp"},
		{kind: "scan.skill", inProcess: func(raw json.RawMessage) bool {
			var p struct {
				Dynamic bool `json:"dynamic"`
			}
			_ = json.Unmarshal(raw, &p)
			return !p.Dynamic
		}},
		{kind: "monitor.certify", collect: func([]string) []string { return []string{"monitor-certs"} }},
		{kind: kindGPUCheck, args: gpuCheckArgs},
		{kind: kindVoiceCheck},
		{kind: kindVisualCapture, collect: func(argv []string) []string {
			if v := argvFlag(argv, "-out"); v != "" {
				return []string{v}
			}
			return nil
		}},
		{kind: kindVisualCompare},
		{kind: kindSimulatorSpread},
	} {
		registerOrMergeJobKind(runnerJobKind(rk))
	}
}

// localSpec returns the registered local variant of kind.
func localSpec(kind string) (jobKindSpec, bool) {
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, k := range jobKinds {
		if k.Kind == kind && k.Local {
			return k, true
		}
	}
	return jobKindSpec{}, false
}

// runnerJobKind builds the hosted variant of rk: params checked with the
// local argv builder, the job run on a runner.
func runnerJobKind(rk runnerKind) jobKindSpec {
	local, _ := localSpec(rk.kind)
	if rk.args == nil {
		rk.args = local.Args
	}
	if rk.args == nil {
		panic("runner job kind without an argv builder: " + rk.kind)
	}
	args := rk.args
	return jobKindSpec{
		Kind: rk.kind, Hosted: true, Args: args,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			if _, err := args(raw); err != nil {
				return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
			}
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			return raw, cliTarget(raw), nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
			argv, err := args(j.Params)
			if err != nil {
				return nil, err
			}
			if rk.inProcess != nil && rk.inProcess(j.Params) {
				return runCLI(ctx, env, argv, log)
			}
			argv = withConfigFlag(argv)
			var collect []string
			if rk.collect != nil {
				collect = rk.collect(argv)
			}
			return runOnRunner(ctx, env, j, rk, argv, collect, log)
		},
	}
}

// agentBuildArgs: {agent, suite?} -> agent build -config X [-suite s] <agent>.
func agentBuildArgs(raw json.RawMessage) ([]string, error) {
	var p agentBuildParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	agent, err := checkRelPath("agent", p.Agent, true)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(agent)); (ext != ".yaml" && ext != ".yml") || !strings.HasPrefix(agent, "agents/") {
		return nil, errors.New("agent must be a .yaml agent config under agents/, for example agents/codex.yaml")
	}
	argv := []string{"agent", "build", "-config", cliConfig}
	if p.Suite != "" {
		if err := checkID("suite", strings.SplitN(p.Suite, "@", 2)[0], true); err != nil {
			return nil, err
		}
		argv = append(argv, "-suite", p.Suite)
	}
	return append(argv, agent), nil
}

// agentBuildResult reads the tag and image id agent build prints.
func agentBuildResult(lines []string, _ json.RawMessage) any {
	out := map[string]any{}
	var digests []string
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "tag":
			out["tag"] = f[1]
		case "image":
			out["image_id"] = f[1]
		case "digest":
			digests = append(digests, f[1])
		}
	}
	if len(out) == 0 {
		return nil
	}
	out["digests"] = digests
	out["built_on"] = "runner"
	return out
}

// gpuCheckArgs: {suite?} -> gpu check -config X -json [-suite s].
func gpuCheckArgs(raw json.RawMessage) ([]string, error) {
	p, err := parseOperateParams(raw)
	if err != nil {
		return nil, err
	}
	if err := operateCheckID("suite", p.Suite, false); err != nil {
		return nil, err
	}
	argv := []string{"gpu", "check", "-config", cliConfig, "-json"}
	if p.Suite != "" {
		argv = append(argv, "-suite", p.Suite)
	}
	return argv, nil
}

// noConfigCommands take no -config flag.
var noConfigCommands = map[string]bool{"visual capture": true}

// withConfigFlag adds -config <placeholder> after the command words when
// argv has none, so the runner points the command at its own config.
func withConfigFlag(argv []string) []string {
	if len(argv) < 2 || noConfigCommands[argv[0]+" "+argv[1]] {
		return argv
	}
	for _, a := range argv {
		if a == "-config" || strings.HasPrefix(a, "-config=") {
			return argv
		}
	}
	return append([]string{argv[0], argv[1], "-config", cliConfig}, argv[2:]...)
}

func argvFlag(argv []string, name string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == name {
			return argv[i+1]
		}
	}
	return ""
}

// jobCoordinator is the org's runner coordinator, or nil.
func jobCoordinator(env jobEnv) *engine.Coordinator {
	if env.Services.Launcher == nil {
		return nil
	}
	if bl := baseLauncher(env.Services.Launcher); bl != nil {
		return bl.coord
	}
	return nil
}

// runOnRunner runs argv as a runner job of the org and writes the files it
// sent back into the org project.
func runOnRunner(ctx context.Context, env jobEnv, j *jobs.Job, rk runnerKind, argv, collect []string, log jobs.Logger) (any, error) {
	c := jobCoordinator(env)
	if c == nil || !c.HasJobRunner() {
		return nil, errNoJobRunner
	}
	snap, skipped, err := projectSnapshot(env.ProjectRoot, argv)
	if err != nil {
		return nil, err
	}
	for _, s := range skipped {
		log.Warnf("not sent to the runner: %s", s)
	}
	log.Printf("queued for a runner: bifrost-bench %s", strings.Join(argv, " "))
	var linesMu sync.Mutex
	var lines []string
	out, err := c.RunJob(ctx, schemas.JobLease{JobID: j.ID, JobKind: rk.kind, Argv: argv, Files: snap, Collect: collect}, func(line string) {
		linesMu.Lock()
		if len(lines) < 10000 {
			lines = append(lines, line)
		}
		linesMu.Unlock()
		log.Printf("%s", line)
	})
	if errors.Is(err, engine.ErrNoRunner) {
		return nil, errNoJobRunner
	}
	if err != nil {
		return nil, err
	}
	log.Printf("runner %s finished the job", out.Worker)
	var result json.RawMessage
	var files []engine.JobResultFile
	if out.Archive != "" {
		defer os.Remove(out.Archive)
		result, files, err = engine.ReadJobResult(out.Archive, runnerJobResultFileCap)
		if err != nil {
			return nil, err
		}
	}
	var written []string
	for _, f := range files {
		if err := writeRunnerFile(ctx, env, rk.kind, out.Worker, f); err != nil {
			log.Warnf("could not save %s: %v", f.Path, err)
			continue
		}
		written = append(written, f.Path)
		log.Printf("saved %s", f.Path)
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	var res any
	if rk.result != nil {
		linesMu.Lock()
		res = rk.result(lines, result)
		linesMu.Unlock()
	} else if len(result) > 0 {
		_ = json.Unmarshal(result, &res)
	}
	if len(written) > 0 {
		m, ok := res.(map[string]any)
		if !ok {
			m = map[string]any{}
			if res != nil {
				m["result"] = res
			}
		}
		m["files"] = written
		res = m
	}
	return res, nil
}

// writeRunnerFile writes one returned file into the org project: through
// projectfs for its areas, or confined under the project for result roots
// outside them (monitor certificates).
func writeRunnerFile(ctx context.Context, env jobEnv, kind, worker string, f engine.JobResultFile) error {
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(f.Path)))
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return errors.New("path leaves the project")
	}
	top, rest, _ := strings.Cut(rel, "/")
	if rest == "" {
		return errors.New("a returned file must be inside a project folder")
	}
	switch projectfs.Area(top) {
	case projectfs.AreaAgents, projectfs.AreaBenchmarks, projectfs.AreaExperiments, projectfs.AreaPolicies, projectfs.AreaUploads:
		if env.Services.Projects == nil {
			return errors.New("project files are not available on this server")
		}
		_, err := env.Services.Projects.Write(ctx, projectfs.Area(top), rest, f.Data,
			projectfs.WriteOpts{Actor: "runner:" + worker, Note: "runner job " + kind})
		return err
	}
	if !runnerResultRoots[top] {
		return fmt.Errorf("%s is not a folder runner jobs may write", top)
	}
	root, err := filepath.EvalSymlinks(env.ProjectRoot)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(dst)); err != nil || !strings.HasPrefix(real+string(filepath.Separator), root+string(filepath.Separator)) {
		return errors.New("path leaves the project")
	}
	if st, err := os.Lstat(dst); err == nil && !st.Mode().IsRegular() {
		return errors.New("refusing to replace a link or folder")
	}
	tmp := dst + ".tmp-runner"
	if err := os.WriteFile(tmp, f.Data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// projectSnapshot zips what a runner job needs from the project: every
// path its argv names that exists in the project, then the agents,
// policies, benchmarks and uploads folders, while the snapshot stays under
// engine.MaxJobFilesBytes. A folder that would pass the cap is skipped and
// reported; a path the argv names that does not fit fails the job.
func projectSnapshot(root string, argv []string) ([]byte, []string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	var total int64
	var skipped []string
	add := func(rel string, required bool) error {
		var files []string
		var size int64
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(rel)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() && (name == ".git" || name == ".heimdall" || name == jobConfigDir || name == "node_modules") {
				return filepath.SkipDir
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > runnerJobFileCap {
				return nil
			}
			r, err := filepath.Rel(root, p)
			if err != nil || !filepath.IsLocal(r) || seen[filepath.ToSlash(r)] {
				return nil
			}
			files = append(files, filepath.ToSlash(r))
			size += info.Size()
			return nil
		})
		if err != nil {
			return err
		}
		if total+size > engine.MaxJobFilesBytes*3/4 { // headroom for the zip and base64
			if required {
				return fmt.Errorf("%s is larger than a runner job can carry (%d MiB)", rel, engine.MaxJobFilesBytes>>20)
			}
			skipped = append(skipped, rel+" (the snapshot would pass its size cap)")
			return nil
		}
		for _, r := range files {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(r)))
			if err != nil {
				continue
			}
			w, err := zw.Create(r)
			if err != nil {
				return err
			}
			if _, err := w.Write(data); err != nil {
				return err
			}
			seen[r] = true
		}
		total += size
		return nil
	}
	for _, a := range argv {
		if a == "" || strings.HasPrefix(a, "-") || a == cliConfig {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(a))
		if !filepath.IsLocal(clean) || !strings.ContainsAny(a, "/.") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, clean)); err != nil {
			continue
		}
		if err := add(filepath.ToSlash(clean), true); err != nil {
			return nil, nil, err
		}
	}
	for _, d := range runnerJobSnapshotDirs {
		if err := add(d, false); err != nil {
			return nil, nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), skipped, nil
}
