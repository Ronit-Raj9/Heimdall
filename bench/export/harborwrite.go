package export

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// tarTime is the modification time of every file in an exported tar, so the
// same export gives the same bytes.
var tarTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// taskTOML writes task.toml with the fields the Harbor configuration
// reference documents; Heimdall's own facts go under [metadata], which
// Harbor leaves to the author.
func taskTOML(s *schemas.Suite, t *schemas.Task, shape, status string, env *envPlan, opts HarborOptions, withheld bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema_version = %s\n", tomlStr(HarborSchemaVersion))
	fmt.Fprintf(&b, "source = %s\n\n", tomlStr(fmt.Sprintf("heimdall:%s@%s/%s@%s", s.ID, s.Version, t.ID, t.Version)))

	b.WriteString("[task]\n")
	fmt.Fprintf(&b, "name = %s\n", tomlStr(taskName(s, t)))
	if t.Title != "" {
		fmt.Fprintf(&b, "description = %s\n", tomlStr(t.Title))
	}
	kw := append([]string{}, t.Tags...)
	kw = append(kw, t.Capabilities...)
	if len(kw) > 0 {
		fmt.Fprintf(&b, "keywords = %s\n", tomlList(kw))
	}
	b.WriteString("\n[metadata]\n")
	if t.Kind != "" {
		fmt.Fprintf(&b, "category = %s\n", tomlStr(string(t.Kind)))
	}
	if t.Difficulty != "" {
		fmt.Fprintf(&b, "difficulty = %s\n", tomlStr(t.Difficulty))
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(&b, "tags = %s\n", tomlList(t.Tags))
	}
	meta := [][2]string{
		{"heimdall_suite", s.ID + "@" + s.Version},
		{"heimdall_task", t.ID + "@" + t.Version},
		{"heimdall_shape", shape},
		{"heimdall_grader", s.Grader},
		{"heimdall_lock_status", status},
		{"heimdall_exporter", opts.Exporter},
	}
	if s.Lock != nil {
		if tl, ok := s.Lock.Tasks[t.ID]; ok {
			meta = append(meta,
				[2]string{"heimdall_instruction_sha256", tl.InstructionSHA256},
				[2]string{"heimdall_repo_tree_sha256", tl.RepoTreeSHA256},
				[2]string{"heimdall_verifier_sha256", tl.VerifierSHA256})
		}
	}
	if withheld {
		meta = append(meta, [2]string{"heimdall_verifier", "withheld"})
	}
	if pv := t.Provenance; pv != nil {
		meta = append(meta, [2]string{"heimdall_license", pv.License}, [2]string{"heimdall_upstream", pv.Source}, [2]string{"heimdall_imported_from", pv.ImportedFrom})
	}
	for _, kv := range meta {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s = %s\n", kv[0], tomlStr(kv[1]))
		}
	}
	vt := s.Verifier.TimeoutS
	if vt <= 0 {
		vt = 300
	}
	b.WriteString("\n[verifier]\n")
	fmt.Fprintf(&b, "timeout_sec = %s\n", tomlFloat(float64(vt+verifierMarginS)))
	b.WriteString("user = \"root\"\n")
	b.WriteString("\n[agent]\n")
	agentS := t.Timeouts.AgentS
	if agentS <= 0 {
		agentS = 900
	}
	fmt.Fprintf(&b, "timeout_sec = %s\n", tomlFloat(float64(agentS)))
	b.WriteString("\n[environment]\nbuild_timeout_sec = 600.0\n")
	if env.dockerImage != "" {
		fmt.Fprintf(&b, "docker_image = %s\n", tomlStr(env.dockerImage))
	}
	return b.String()
}

// tomlStr is a TOML basic string.
func tomlStr(s string) string {
	return strconv.Quote(s)
}

// tomlList is a TOML array of basic strings.
func tomlList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = tomlStr(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// tomlFloat writes a float with a decimal point, as Harbor's examples do.
func tomlFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// taskReadme explains the exported task and what changed on the way.
func taskReadme(s *schemas.Suite, t *schemas.Task, task *HarborTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", firstNonEmpty(t.Title, t.ID))
	fmt.Fprintf(&b, "Exported from Heimdall suite `%s@%s`, task `%s@%s`, by `bifrost-bench export harbor` (docs/export.md in the bench).\n\n", s.ID, s.Version, t.ID, t.Version)
	fmt.Fprintf(&b, "Shape: %s. Grader in the bench: `%s`.\n\n", task.Shape, s.Grader)
	b.WriteString("## What differs from the bench\n\n")
	b.WriteString("- The verifier runs in the agent's own container after the agent (Harbor's shared verifier), as root. The bench grades in a fresh container with no network, the workspace read-only and the hidden files arriving only then.\n")
	b.WriteString("- No network rule is set: the bench gives the agent no network except model calls through its gateway; Harbor's default is public network.\n")
	b.WriteString("- No probes, fault conditions, integrity gates, cost or token accounting: those are the bench's, not the task's.\n")
	b.WriteString("- The project notes fixture is not installed (the bench writes it under the agent's own notes file name).\n")
	for _, n := range task.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	if pv := t.Provenance; pv != nil && (pv.License != "" || pv.Source != "") {
		fmt.Fprintf(&b, "\nProvenance: source %s, license %s.\n", firstNonEmpty(pv.Source, "unknown"), firstNonEmpty(pv.License, "unknown"))
	}
	return b.String()
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// WriteHarborDir writes each planned task folder under out. An existing task
// folder is refused unless force; with force it is replaced.
func WriteHarborDir(out string, trees map[string]harborTree, res *HarborResult, force bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, task := range res.Tasks {
		dir := filepath.Join(out, task.Folder)
		if _, err := os.Lstat(dir); err == nil {
			if !force {
				return fmt.Errorf("%s exists; pass -force to replace it", dir)
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
	}
	for _, task := range res.Tasks {
		tree := trees[task.Folder]
		dir := filepath.Join(out, task.Folder)
		for _, d := range tree.dirsOf() {
			if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
				return err
			}
		}
		for _, p := range tree.sortedPaths() {
			f := tree[p]
			dst := filepath.Join(dir, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dst, f.data, f.mode); err != nil {
				return err
			}
			if err := os.Chmod(dst, f.mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteHarborTar writes the planned task folders as one gzip-compressed tar
// with a top folder named root. The bytes depend only on the files.
func WriteHarborTar(w io.Writer, root string, trees map[string]harborTree, res *HarborResult) error {
	if root == "" || strings.ContainsAny(root, "/\\") {
		return errors.New("export harbor: tar root must be one folder name")
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.ModTime = tarTime
	tw := tar.NewWriter(gz)
	hdr := func(name string, mode int64, size int64, typ byte) *tar.Header {
		return &tar.Header{Name: name, Mode: mode, Size: size, Typeflag: typ, ModTime: tarTime, Format: tar.FormatPAX, Uname: "", Gname: ""}
	}
	if err := tw.WriteHeader(hdr(root+"/", 0o755, 0, tar.TypeDir)); err != nil {
		return err
	}
	for _, task := range res.Tasks {
		tree := trees[task.Folder]
		base := root + "/" + task.Folder
		if err := tw.WriteHeader(hdr(base+"/", 0o755, 0, tar.TypeDir)); err != nil {
			return err
		}
		for _, d := range tree.dirsOf() {
			if err := tw.WriteHeader(hdr(base+"/"+d+"/", 0o755, 0, tar.TypeDir)); err != nil {
				return err
			}
		}
		for _, p := range tree.sortedPaths() {
			f := tree[p]
			if err := tw.WriteHeader(hdr(base+"/"+p, int64(f.mode), int64(len(f.data)), tar.TypeReg)); err != nil {
				return err
			}
			if _, err := tw.Write(f.data); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
