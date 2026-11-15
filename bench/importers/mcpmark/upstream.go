package mcpmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// upTask is one MCPMark task folder:
// tasks/<service>/<difficulty>/<category>/<task>/ with description.md,
// meta.json and verify.py.
type upTask struct {
	Service    string // filesystem, postgres, github, notion, playwright, ...
	Difficulty string // easy | standard
	Category   string // folder name; meta.json category_id wins when set
	Name       string // folder name; meta.json task_id wins when set
	Dir        string // absolute task folder
	Meta       upMeta
}

// upMeta is the part of meta.json the importer reads.
type upMeta struct {
	TaskID       string   `json:"task_id"`
	TaskName     string   `json:"task_name"`
	CategoryID   string   `json:"category_id"`
	CategoryName string   `json:"category_name"`
	Description  string   `json:"description"`
	Difficulty   string   `json:"difficulty"`
	Tags         []string `json:"tags"`
	MCP          []string `json:"mcp"`
}

// upstreamID is the task's id in MCPMark's own terms.
func (t upTask) upstreamID() string {
	return t.Service + "/" + t.Difficulty + "/" + t.Category + "/" + t.Name
}

// category is the state folder the task runs in: upstream picks the test
// environment by meta.json category_id.
func (t upTask) category() string {
	if t.Meta.CategoryID != "" {
		return t.Meta.CategoryID
	}
	return t.Category
}

// upstream is a local clone of eval-sys/mcpmark.
type upstream struct {
	root     string
	revision string
}

// openUpstream checks that root looks like an MCPMark clone and reads its
// revision (ref overrides it when root is not a git clone).
func openUpstream(root, ref string) (*upstream, error) {
	st, err := os.Stat(filepath.Join(root, "tasks"))
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s has no tasks/ folder: give -from a clone of %s", root, upstreamRepo)
	}
	if _, err := os.Stat(filepath.Join(root, "pipeline.py")); err != nil {
		return nil, fmt.Errorf("%s has no pipeline.py: give -from a clone of %s", root, upstreamRepo)
	}
	rev := ref
	if rev == "" {
		rev, err = gitHead(root)
		if err != nil {
			return nil, fmt.Errorf("read upstream revision (pass -set %s=<commit> when -from is not a git clone): %w", SetRef, err)
		}
	}
	return &upstream{root: root, revision: rev}, nil
}

// tasks lists every task folder of the given services, sorted by upstream
// id. A folder without meta.json, description.md and verify.py is not a task.
func (u *upstream) tasks(services []string) ([]upTask, error) {
	var out []upTask
	for _, svc := range services {
		base := filepath.Join(u.root, "tasks", svc)
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() || d.Name() == "__pycache__" {
				return nil
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) != 3 {
				return nil
			}
			t, ok, err := readTask(svc, parts, p)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, t)
			}
			return filepath.SkipDir
		})
		if err != nil {
			return nil, fmt.Errorf("walk tasks/%s: %w", svc, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].upstreamID() < out[j].upstreamID() })
	return out, nil
}

// readTask reads one task folder; ok is false when it is not a task.
func readTask(svc string, parts []string, dir string) (upTask, bool, error) {
	for _, f := range []string{"meta.json", "description.md", "verify.py"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || !st.Mode().IsRegular() {
			return upTask{}, false, nil
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return upTask{}, false, err
	}
	var m upMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return upTask{}, false, fmt.Errorf("%s/meta.json: %w", dir, err)
	}
	return upTask{Service: svc, Difficulty: parts[0], Category: parts[1], Name: parts[2], Dir: dir, Meta: m}, true, nil
}

var hexRev = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gitHead reads the commit HEAD points at from root/.git without running
// git: a detached HEAD, a loose ref, or packed-refs for a fresh clone.
func gitHead(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(b))
	if hexRev.MatchString(h) {
		return h, nil
	}
	ref, ok := strings.CutPrefix(h, "ref: ")
	if !ok || strings.Contains(ref, "..") {
		return "", fmt.Errorf("unreadable .git/HEAD %q", h)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".git", filepath.FromSlash(ref))); err == nil {
		if r := strings.TrimSpace(string(b)); hexRev.MatchString(r) {
			return r, nil
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, ".git", "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("ref %s not found", ref)
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref && hexRev.MatchString(sha) {
			return sha, nil
		}
	}
	return "", fmt.Errorf("ref %s not found", ref)
}
