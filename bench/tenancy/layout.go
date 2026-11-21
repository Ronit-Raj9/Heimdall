package tenancy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Folder layout of the hosted platform (docs/platform-design.md, Tenancy):
//
//	<storage root>/orgs/<org_id>/            the org's evidence store: sealed experiment
//	                                         folders, exactly the format of a local runs/
//	<storage root>/projects/<org_id>/        the org's project folder:
//	    benchmarks/<suite>/                  suites (coding-core is copied in at creation)
//	    agents/*.yaml                        agent configs
//	    experiments/*.yaml                   experiment files
//	    policies/*.yaml                      release policies
//	    releases/                            the org's hash-chained release log
//
// The project folder is not inside the store root on purpose: the store reads
// every folder under its root as an experiment, and release.New refuses a
// releases root inside the runs root. The local mode keeps today's layout:
// org "local" is the configured runs root and the bench root.

// Layout subfolder names.
const (
	OrgsDir     = "orgs"
	ProjectsDir = "projects"

	BenchmarksDir  = "benchmarks"
	AgentsDir      = "agents"
	ExperimentsDir = "experiments"
	PoliciesDir    = "policies"
	ReleasesDir    = "releases"
)

// DefaultSuite is the practice suite a new hosted org starts with on a
// development server only (HEIMDALL_DEV, cmd/bifrost-bench seedBenchmarks).
const DefaultSuite = "coding-core"

// orgIDPattern is what an org id may look like before it becomes a folder
// name: P1's prefixed random ids (org_...) and the local org. It is the
// store's own id alphabet, so an org id is never a path.
var orgIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// ValidOrgID reports whether id can name an org folder.
func ValidOrgID(id string) error {
	if !orgIDPattern.MatchString(id) {
		return fmt.Errorf("invalid org id %q: want ^[a-z0-9_]{1,64}$", id)
	}
	return nil
}

// OrgPaths are the folders of one org.
type OrgPaths struct {
	// StoreRoot is the evidence store root (runs/orgs/<org_id>, or the
	// configured runs root for the local org).
	StoreRoot string
	// ProjectRoot anchors suites, agents, experiments and policies, the way
	// the bench root does in local mode.
	ProjectRoot string
	// ReleasesRoot is the org's release log.
	ReleasesRoot string
}

// BenchmarksRoot is the org's benchmarks folder.
func (p OrgPaths) BenchmarksRoot() string { return filepath.Join(p.ProjectRoot, BenchmarksDir) }

// Layout maps org ids to folders.
type Layout struct {
	// Hosted switches the per-org layout on. Off, every org id but the
	// local org is refused.
	Hosted bool
	// StorageRoot holds orgs/ and projects/ in hosted mode.
	StorageRoot string
	// LocalRunsRoot and LocalBenchRoot are local mode's runs root and bench
	// root.
	LocalRunsRoot  string
	LocalBenchRoot string
	// LocalReleasesRoot is local mode's release log (release.DefaultRoot).
	LocalReleasesRoot string
}

// ErrLocalOnly is returned for any org but "local" in local mode.
var ErrLocalOnly = errors.New("local mode serves only the local organization")

// Paths returns the folders of orgID. In hosted mode they are always inside
// StorageRoot: the id is checked against the id alphabet first.
func (l Layout) Paths(orgID string) (OrgPaths, error) {
	if !l.Hosted {
		if orgID != LocalOrgID {
			return OrgPaths{}, ErrLocalOnly
		}
		return OrgPaths{StoreRoot: l.LocalRunsRoot, ProjectRoot: l.LocalBenchRoot, ReleasesRoot: l.LocalReleasesRoot}, nil
	}
	if err := ValidOrgID(orgID); err != nil {
		return OrgPaths{}, err
	}
	if strings.TrimSpace(l.StorageRoot) == "" {
		return OrgPaths{}, errors.New("tenancy layout: empty storage root")
	}
	root, err := filepath.Abs(l.StorageRoot)
	if err != nil {
		return OrgPaths{}, fmt.Errorf("tenancy layout: %w", err)
	}
	project := filepath.Join(root, ProjectsDir, orgID)
	return OrgPaths{
		StoreRoot:    filepath.Join(root, OrgsDir, orgID),
		ProjectRoot:  project,
		ReleasesRoot: filepath.Join(project, ReleasesDir),
	}, nil
}

// SeedMarker is the file in an org's project folder that records that the
// built-in suite was seeded once; an org that later deletes the suite does
// not get it back.
const SeedMarker = ".seeded"

// EnsureProject creates the org's folders and seeds its project once: when
// seedBenchmarks names a benchmarks folder holding DefaultSuite and the
// project has no SeedMarker yet, the suite is copied in (unless the org
// already has a folder of that name) and the marker written. It is the one
// function every org creation path goes through: the org API and `org
// create` call it when they create the org, and the org runtime calls it
// when it is first built, which covers orgs created by signup, GitHub
// sign-in, `user create -org` and invitations. An empty seedBenchmarks only
// creates the folders. Idempotent: an org's edits are never overwritten.
func EnsureProject(p OrgPaths, seedBenchmarks string) error {
	for _, d := range []string{p.StoreRoot, p.ProjectRoot, p.BenchmarksRoot(),
		filepath.Join(p.ProjectRoot, AgentsDir), filepath.Join(p.ProjectRoot, ExperimentsDir),
		filepath.Join(p.ProjectRoot, PoliciesDir), p.ReleasesRoot} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create org folder %s: %w", d, err)
		}
	}
	if seedBenchmarks == "" {
		return nil
	}
	marker := filepath.Join(p.ProjectRoot, SeedMarker)
	if _, err := os.Lstat(marker); err == nil {
		return nil
	}
	src := filepath.Join(seedBenchmarks, DefaultSuite)
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return nil // no built-in suite to copy: the org starts empty, and a later start with one seeds it
	}
	dst := filepath.Join(p.BenchmarksRoot(), DefaultSuite)
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		if err := CopyTree(src, dst); err != nil {
			return err
		}
	}
	if err := os.WriteFile(marker, []byte(DefaultSuite+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", marker, err)
	}
	return nil
}

// CopyTree copies the regular files and folders under src to dst, which
// must not exist. Symlinks are refused rather than followed or recreated:
// a link copied into an org could point at another org's files. The copy
// goes to a sibling temp folder first and is renamed into place, so a
// failed copy leaves nothing behind.
func CopyTree(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("copy %s: destination %s exists", src, dst)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dst)+".tmp-")
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()
	type dirTime struct {
		path string
		at   time.Time
	}
	var dirs []dirTime
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; org folders take regular files only", path)
		case d.IsDir():
			if info, err := d.Info(); err == nil {
				dirs = append(dirs, dirTime{target, info.ModTime()})
			}
			if rel == "." {
				return nil
			}
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(path, target)
		default:
			return fmt.Errorf("%s is not a regular file", path)
		}
	})
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	// Folder times last, deepest first, once nothing more is written in
	// them: the newest-analysis picker reads them.
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chtimes(dirs[i].path, dirs[i].at, dirs[i].at)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	ok = true
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Keep modification times: the store's verification cache and the
	// analysis picker (newest analysis folder) read them.
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}
