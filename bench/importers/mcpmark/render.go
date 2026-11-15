package mcpmark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// writer writes the suite and its task folders.
type writer struct {
	up          *upstream
	set         settings
	suiteDir    string
	states      map[string]*stateInfo // category -> its test environment
	missingRefs int
}

// stateInfo is one category's test environment as copied into task folders.
type stateInfo struct {
	dir     string // source folder
	hash    string // over relative paths, modes, times and contents
	bytes   int64
	skipped []string // symlinks and special files left out
}

// state reads a category's test environment once per import.
func (w *writer) state(category string) (*stateInfo, error) {
	if s, ok := w.states[category]; ok {
		return s, nil
	}
	s, err := scanState(filepath.Join(w.set.stateDir, category))
	if err != nil {
		return nil, err
	}
	w.states[category] = s
	return s, nil
}

// writeTask writes one task folder, replacing what was there.
func (w *writer) writeTask(dir string, c candidate) error {
	st, err := w.state(c.task.category())
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	desc, err := os.ReadFile(filepath.Join(c.task.Dir, "description.md"))
	if err != nil {
		return err
	}
	verify, err := os.ReadFile(filepath.Join(c.task.Dir, "verify.py"))
	if err != nil {
		return err
	}
	meta, err := os.ReadFile(filepath.Join(c.task.Dir, "meta.json"))
	if err != nil {
		return err
	}
	dockerfile := strings.ReplaceAll(fsDockerfile, "__FS_SERVER_VERSION__", fsServerVersion)
	tag := "bench/mcpmark-fs-" + strings.ReplaceAll(strings.ToLower(c.task.category()), "_", "-") + ":" + shortHash(dockerfile, st.hash)
	reference, own, err := w.reference(c)
	if err != nil {
		return err
	}
	if !own {
		w.missingRefs++
	}
	files := map[string]string{
		"task.yaml":                   w.taskYAML(c, tag, exportCap(st.bytes)),
		"instruction.md":              instruction(string(desc)),
		"repo/README.md":              "Workspace of MCPMark task " + c.task.upstreamID() + ". The task's files live in the MCP server `" + serviceID + "`; files here are not part of the result.\n",
		"service/Dockerfile":          dockerfile,
		"verifier/tests/verify.py":    string(verify),
		"verifier/tests/runverify.py": runVerify,
		"verifier/tests/meta.json":    string(meta),
		"controls/reference.sh":       reference,
		"controls/empty.sh":           "#!/bin/sh\n# Empty control: no tool call, the test environment stays as it was.\nexit 0\n",
		"controls/exploit.sh":         "#!/bin/sh\n# Exploit control (must fail): claims success without any tool call.\necho 'All task objectives are complete.'\nexit 0\n",
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
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
	return copyState(st.dir, filepath.Join(dir, "service", "state"))
}

// reference returns the task's reference control: the user's own from the
// references folder when there is one, else a script that refuses, because
// MCPMark ships no solutions and admission must not pass on a guess.
func (w *writer) reference(c candidate) (string, bool, error) {
	if w.set.references != "" {
		b, err := os.ReadFile(filepath.Join(w.set.references, c.id+".sh"))
		if err == nil {
			return string(b), true, nil
		}
		if !os.IsNotExist(err) {
			return "", false, err
		}
	}
	return "#!/bin/sh\n# Reference control. MCPMark ships no reference solutions, so this task\n" +
		"# has none until one is written: a script that solves the task through the\n" +
		"# MCP endpoint in $BENCH_MCP_SERVERS. Admission fails until then.\n" +
		"echo 'no reference solution for " + c.task.upstreamID() + "; write controls/reference.sh' >&2\nexit 1\n", false, nil
}

// q quotes s as a YAML double-quoted scalar (JSON string syntax, which YAML
// reads the same way).
func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// taskYAML renders task.yaml. The service runs the filesystem MCP server
// over the test environment; its /data folder is exported after the agent
// exits and graded by the upstream verify script.
func (w *writer) taskYAML(c candidate, tag string, exportMax int64) string {
	t := c.task
	title := t.Meta.TaskName
	if title == "" {
		title = t.Name
	}
	tags := []string{"mcpmark", familyFS, t.Difficulty, t.category()}
	quoted := make([]string, len(tags))
	for i, s := range tags {
		quoted[i] = q(s)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from MCPMark by `bifrost-bench import mcpmark`; see docs/importing-mcpmark.md.\n")
	p("id: %s\nversion: %s\n", c.id, taskVersion)
	p("title: %s\n", q("MCPMark filesystem: "+title))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("mcpmark-filesystem-"+t.category()))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: []\n  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", agentTimeoutS)
	p("tags: [%s]\n", strings.Join(quoted, ", "))
	p("capabilities: [tool_use]\n")
	if d := difficulty(t); d != "" {
		p("difficulty: %s\n", d)
	}
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), w.up.revision, upstreamLicense, q("mcpmark "+t.upstreamID()), q("bifrost-bench import mcpmark"))
	p("environment:\n  image: %s\n  build_network: true\n", q(workspaceImage))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n", serviceID)
	p("    build:\n      dockerfile: service/Dockerfile\n      tag: %s\n", q(tag))
	p("    port: %d\n", servicePort)
	p("    ready:\n      http: /healthz\n      timeout_s: 120\n")
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n      - id: data\n        path: /data\n        max_bytes: %d\n", exportMax)
	p("output:\n  kind: state\n")
	return b.String()
}

// difficulty maps MCPMark's own level (L1 to L3 in meta.json) and folder to
// the bench's easy, medium or hard. An unknown level is left out.
func difficulty(t upTask) string {
	switch t.Meta.Difficulty {
	case "L1":
		return "easy"
	case "L2":
		return "medium"
	case "L3":
		return "hard"
	}
	if t.Difficulty == "easy" {
		return "easy"
	}
	return ""
}

// instruction is the agent's task: where the files are, then upstream's
// description unchanged.
func instruction(desc string) string {
	var b strings.Builder
	b.WriteString("The files of this task live in the MCP server `" + serviceID + "` (filesystem tools). ")
	b.WriteString("Its allowed folder, the test environment root, is `/data`. ")
	b.WriteString("Only the state of that folder after you finish is graded; your workspace and final message are not.\n\n")
	b.WriteString(strings.TrimSpace(desc))
	b.WriteString("\n")
	return b.String()
}

// writeSuite writes suite.yaml. Tasks of an earlier import stay listed.
func (w *writer) writeSuite(id string, tasks []string, rec schemas.ImportRecord) error {
	if err := os.MkdirAll(w.suiteDir, 0o755); err != nil {
		return err
	}
	all, err := existingTasks(w.suiteDir)
	if err != nil {
		return err
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
	p("# Imported from MCPMark by `bifrost-bench import mcpmark`; see docs/importing-mcpmark.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("MCPMark filesystem tasks: the upstream filesystem MCP server over each task's test environment, graded offline by the upstream verify script on the exported folder."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  tag: %s\n", q(workspaceImage))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python3, /grader/tests/runverify.py]\n")
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n", verifierTimeout)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	p("track: %s\ngrading: %s\n", schemas.TrackMCP, schemas.GradingVerified)
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		rec.Importer, q(rec.Benchmark), q(rec.Source), rec.Revision, rec.License, rec.At.Format("2006-01-02T15:04:05Z"))
	return os.WriteFile(filepath.Join(w.suiteDir, "suite.yaml"), []byte(b.String()), 0o644)
}

// existingTasks reads the task ids of a suite.yaml already in dir, so a
// second import extends the suite instead of dropping its tasks.
func existingTasks(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s schemas.Suite
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read existing suite.yaml: %w", err)
	}
	return s.TaskIDs, nil
}

// exportCap is the export size limit for a test environment of n bytes:
// room for the agent to add as much again, never under the 16 MiB default.
func exportCap(n int64) int64 {
	return max(minExportBytes, 2*n+minExportBytes)
}

// shortHash is the first 12 hex digits of the SHA-256 of parts.
func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// scanState hashes a test environment: every regular file's relative path,
// mode, modification time and content, in walk order. Symlinks and special
// files are left out (the loader refuses links that leave the task folder,
// and upstream archives hold none); macOS metadata folders too.
func scanState(dir string) (*stateInfo, error) {
	st := &stateInfo{dir: dir}
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "__MACOSX" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			fmt.Fprintf(h, "d %s\n", filepath.ToSlash(rel))
		case info.Mode().IsRegular():
			fmt.Fprintf(h, "f %s %d %d\n", filepath.ToSlash(rel), info.Size(), info.ModTime().Unix())
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(h, f)
			f.Close()
			if cerr != nil {
				return cerr
			}
			st.bytes += info.Size()
		default:
			st.skipped = append(st.skipped, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read test environment %s: %w", dir, err)
	}
	st.hash = hex.EncodeToString(h.Sum(nil))
	return st, nil
}

// copyState copies a test environment into dst keeping modification times
// (MCPMark's file_property tasks are about them), with plain file modes:
// directories 0755, files 0644, or 0755 when upstream marked them
// executable.
func copyState(src, dst string) error {
	type dirTime struct {
		path string
		info fs.FileInfo
	}
	var dirs []dirTime
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "__MACOSX" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, dirTime{target, info})
		case info.Mode().IsRegular():
			mode := os.FileMode(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			if err := copyFile(p, target, mode); err != nil {
				return err
			}
			if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("copy test environment %s: %w", src, err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(dirs[i].path, dirs[i].info.ModTime(), dirs[i].info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
