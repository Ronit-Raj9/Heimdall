package tenancy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// suiteRefPattern is a suite named by id, optionally pinned: coding-core or
// coding-core@1.0.0. A ref with a slash would be read as a folder path by
// the suite loader, so hosted orgs may not use one.
var suiteRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(@[A-Za-z0-9][A-Za-z0-9._+-]*)?$`)

// CheckExperimentPaths refuses an experiment of a hosted org that names a
// file outside the org's project folder: the suite must be an id (looked up
// in the org's benchmarks folder), and the base, arm, compatibility and
// matrix agent configs and the release policy must be relative paths that
// resolve, symlinks included, inside projectRoot.
func CheckExperimentPaths(projectRoot string, exp *schemas.Experiment) error {
	return CheckExperimentPathsWithLibrary(projectRoot, "", exp)
}

// CheckExperimentPathsWithLibrary is CheckExperimentPaths with the server's
// built-in benchmark library (suite.LibraryDir) as a read-only layer: an
// agent config agents/<file> the project does not hold may resolve inside
// libraryRoot instead. Suite ids are looked up by the layered loader, so
// they need nothing here. Empty libraryRoot equals CheckExperimentPaths.
func CheckExperimentPathsWithLibrary(projectRoot, libraryRoot string, exp *schemas.Experiment) error {
	if exp == nil {
		return nil
	}
	if exp.Suite != "" {
		id, _, _ := strings.Cut(exp.Suite, "@")
		if !suiteRefPattern.MatchString(exp.Suite) || id == "." || id == ".." {
			return fmt.Errorf("suite %q: name a suite of this organization by id (id or id@version)", exp.Suite)
		}
	}
	check := func(field, p string) error {
		if p == "" {
			return nil
		}
		if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
			return fmt.Errorf("%s %q: want a path relative to the organization's project", field, p)
		}
		if base, id, n, ok := schemas.ParseAgentVersionRef(p); ok && field != "policy" { // A06: a pinned agent version reads its frozen snapshot
			if _, err := utils.ResolveInside(projectRoot, schemas.AgentVersionSnapshotPath(id, n)); err == nil {
				return nil
			}
			p = base // a built-in library agent's only version, v1, is its library file
			if n != 1 || libraryRoot == "" || !absentIn(projectRoot, p) {
				return fmt.Errorf("%s: agent %s has no version %d in this project", field, id, n)
			}
		}
		_, err := utils.ResolveInside(projectRoot, p)
		if err != nil && libraryRoot != "" && field != "policy" && strings.HasPrefix(filepath.ToSlash(filepath.Clean(p)), "agents/") && absentIn(projectRoot, p) {
			if _, lerr := utils.ResolveInside(libraryRoot, p); lerr == nil {
				err = nil
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		return nil
	}
	if err := check("base", exp.Base); err != nil {
		return err
	}
	labels := make([]string, 0, len(exp.Arms))
	for k := range exp.Arms {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	for _, k := range labels {
		if err := check("arms."+k+".agent", exp.Arms[k].Agent); err != nil {
			return err
		}
	}
	if exp.Compatibility != nil {
		if err := check("compatibility.agent", exp.Compatibility.Agent); err != nil {
			return err
		}
	}
	if exp.Matrix != nil {
		for i, a := range exp.Matrix.Agents {
			if err := check(fmt.Sprintf("matrix.agents[%d]", i), a); err != nil {
				return err
			}
		}
	}
	return check("policy", exp.Policy)
}

// absentIn reports that rel names nothing at all under root, not even a
// link: only then may the library stand in for it.
func absentIn(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return errors.Is(err, fs.ErrNotExist)
}
