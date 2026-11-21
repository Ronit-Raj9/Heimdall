package benchcatalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Limits of one copy into an org project.
const (
	MaxCopyFiles = 5000
	MaxCopyBytes = 200 << 20
)

// ErrNotCopyable answers Add on an entry that is not a file copy.
var ErrNotCopyable = errors.New("entry is not added by a copy")

// AddResult is what one Add wrote.
type AddResult struct {
	Entry   string   `json:"entry"`
	Suites  []string `json:"suites"`
	Agents  []string `json:"agents"`
	Written []string `json:"written"` // project-relative files written
	Skipped []string `json:"skipped"` // project-relative files that already existed
}

// exampleAreas are the folders of an example project copied into the org.
var exampleAreas = []string{"agents", "benchmarks", "experiments", "policies", "tools"}

// Add copies a bundled or example entry into project. Files the project
// already has are kept and listed as skipped.
func (s Source) Add(project string, e Entry) (*AddResult, error) {
	if s.Root == "" {
		return nil, errors.New("no catalog source on this server: set HEIMDALL_CATALOG_ROOT to the bench folder")
	}
	c := &copier{project: project, res: &AddResult{Entry: e.ID}}
	switch e.Kind {
	case KindBundled:
		if !validName(e.SuiteID) {
			return nil, fmt.Errorf("bad suite id %q", e.SuiteID)
		}
		if err := c.tree(filepath.Join(s.Root, "benchmarks", e.SuiteID), filepath.Join("benchmarks", e.SuiteID)); err != nil {
			return nil, err
		}
		c.res.Suites = []string{e.SuiteID}
	case KindExample:
		name := strings.TrimPrefix(e.ID, "example-")
		if !validName(name) {
			return nil, fmt.Errorf("bad example %q", name)
		}
		src := filepath.Join(s.Root, "examples", name)
		for _, a := range exampleAreas {
			if isDir(filepath.Join(src, a)) {
				if err := c.tree(filepath.Join(src, a), a); err != nil {
					return nil, err
				}
			}
		}
		// Agent images the example's agents build from the shared agents/images.
		if err := c.agentImages(s.Root, filepath.Join(src, "agents")); err != nil {
			return nil, err
		}
		c.res.Suites, c.res.Agents = e.Suites, e.Agents
	default:
		return nil, ErrNotCopyable
	}
	return c.res, nil
}

// StarterAgents are the agents every new org starts with.
var StarterAgents = []string{"codex", "claude-code", "mini-swe-agent", "fw-plain-openai", "custom-python", "oracle", "noop"}

// StarterSuites are the suites every new org starts with.
var StarterSuites = []string{"coding-core"}

// SeedMarker records that an org project got the starter catalog.
const SeedMarker = ".heimdall-catalog-seeded"

// Seed copies the starter agents (with the image folders they build from)
// and suites into project once. Missing sources are skipped: a server
// without them seeds what it has.
func (s Source) Seed(project string) (*AddResult, error) {
	if s.Root == "" || project == "" {
		return &AddResult{}, nil
	}
	marker := filepath.Join(project, SeedMarker)
	if _, err := os.Lstat(marker); err == nil {
		return &AddResult{}, nil
	}
	if filepath.Clean(s.Root) == filepath.Clean(project) {
		// Local serve from the bench folder: the starters are already there.
		return &AddResult{}, nil
	}
	c := &copier{project: project, res: &AddResult{Entry: "starter"}}
	for _, a := range StarterAgents {
		src := filepath.Join(s.Root, "agents", a+".yaml")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := c.file(src, filepath.Join("agents", a+".yaml")); err != nil {
			return nil, err
		}
		c.res.Agents = append(c.res.Agents, a)
		if err := c.imagesOf(s.Root, src); err != nil {
			return nil, err
		}
	}
	for _, id := range StarterSuites {
		src := filepath.Join(s.Root, "benchmarks", id)
		if !isDir(src) {
			continue
		}
		if err := c.tree(src, filepath.Join("benchmarks", id)); err != nil {
			return nil, err
		}
		c.res.Suites = append(c.res.Suites, id)
	}
	if err := os.WriteFile(marker, []byte("starter agents and suites\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", marker, err)
	}
	return c.res, nil
}

func validName(n string) bool {
	if n == "" || n == "." || n == ".." || strings.HasPrefix(n, ".") {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

type copier struct {
	project string
	res     *AddResult
	files   int
	bytes   int64
}

// dest resolves a project-relative path, refusing escapes.
func (c *copier) dest(rel string) (string, error) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the project", rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".git" || part == ".heimdall" {
			return "", fmt.Errorf("path %q is reserved", rel)
		}
	}
	dst := filepath.Join(c.project, rel)
	// Refuse a symlinked parent inside the project.
	for d := filepath.Dir(dst); d != filepath.Clean(c.project) && strings.HasPrefix(d, filepath.Clean(c.project)); d = filepath.Dir(d) {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink", d)
		}
	}
	return dst, nil
}

func (c *copier) tree(src, rel string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil // never follow or recreate links
		case d.IsDir():
			if r != "." && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			// Make every folder, empty ones too: a task's repo_dir may be an
			// empty repo/ the suite loader requires to exist.
			if dst, err := c.dest(filepath.Join(rel, r)); err == nil {
				_ = os.MkdirAll(dst, 0o755)
			}
			return nil
		case d.Type().IsRegular():
			return c.file(path, filepath.Join(rel, r))
		}
		return nil
	})
}

func (c *copier) file(src, rel string) error {
	dst, err := c.dest(rel)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if _, err := os.Lstat(dst); err == nil {
		c.res.Skipped = append(c.res.Skipped, rel)
		return nil
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	c.files++
	c.bytes += fi.Size()
	if c.files > MaxCopyFiles || c.bytes > MaxCopyBytes {
		return fmt.Errorf("copy too large (over %d files or %d MiB)", MaxCopyFiles, MaxCopyBytes>>20)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm()|0o200)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			c.res.Skipped = append(c.res.Skipped, rel)
			return nil
		}
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	c.res.Written = append(c.res.Written, rel)
	return nil
}

// agentImages copies, for every agent YAML in dir, the image folder its
// dockerfile names when that folder lives in the source root.
func (c *copier) agentImages(root, dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			if err := c.imagesOf(root, filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *copier) imagesOf(root, agentYAML string) error {
	b, err := readSmall(agentYAML, 1<<20)
	if err != nil {
		return nil
	}
	for _, m := range dockerfileRe.FindAllStringSubmatch(string(b), -1) {
		rel := filepath.Clean(filepath.Dir(m[1]))
		if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		src := filepath.Join(root, rel)
		if !isDir(src) {
			continue
		}
		dst, err := c.dest(rel)
		if err != nil {
			return err
		}
		if isDir(dst) {
			continue
		}
		if err := c.tree(src, rel); err != nil {
			return err
		}
	}
	return nil
}
