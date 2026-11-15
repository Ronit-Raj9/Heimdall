package harbor

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// upstream describes the benchmark copy an import reads.
type upstream struct {
	Benchmark string // e.g. terminal-bench@2.0
	Source    string // repository URL, or the local path when there is no git remote
	Revision  string // commit, or "" when unknown
	License   string // SPDX id, or NOASSERTION
}

// describeUpstream fills upstream from the clone at from, with -set values
// taking precedence. It reads files only; it never runs git.
func describeUpstream(from string, cfg settings) upstream {
	root := gitRoot(from)
	up := upstream{Source: from}
	if root != "" {
		if u := originURL(root); u != "" {
			up.Source = u
		}
		up.Revision = headRevision(root)
	}
	licenseDir := from
	if root != "" {
		licenseDir = root
	}
	up.License = detectLicense(licenseDir)
	up.Benchmark = detectBenchmark(from, root, up.Revision)
	if tag := headTag(root, up.Revision); tag != "" && strings.HasSuffix(up.Benchmark, "@"+shortRev(up.Revision)) {
		up.Benchmark = repoName(up.Source, root) + "@" + strings.TrimPrefix(tag, "v")
	}
	if cfg.benchmark != "" {
		up.Benchmark = cfg.benchmark
	}
	if cfg.source != "" {
		up.Source = cfg.source
	}
	if cfg.revision != "" {
		up.Revision = cfg.revision
	}
	if cfg.license != "" {
		up.License = cfg.license
	}
	return up
}

// gitRootLevels is how far above -from gitRoot looks for a clone, enough for
// -from pointing at a clone's tasks/ folder but not so far that a folder of
// tasks kept inside some other repository is described as that repository.
const gitRootLevels = 2

// gitRoot returns the nearest folder at or up to gitRootLevels above dir
// holding .git, or "".
func gitRoot(dir string) string {
	d := dir
	for i := 0; i <= gitRootLevels; i++ {
		if isDir(filepath.Join(d, ".git")) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
		d = filepath.Dir(d)
	}
	return ""
}

// originURL reads remote "origin"'s url from .git/config, without any user
// name or token embedded in it.
func originURL(root string) string {
	f, err := os.Open(filepath.Join(root, ".git", "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if inOrigin && ok && strings.TrimSpace(k) == "url" {
			return stripCredentials(strings.TrimSpace(v))
		}
	}
	return ""
}

// stripCredentials removes user info from a URL so a token in a clone URL
// never reaches the suite file.
func stripCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

var hexRE = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// headRevision reads the commit HEAD points at, following one ref through
// refs/ or packed-refs.
func headRevision(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if hexRE.MatchString(head) {
		return head
	}
	ref, ok := strings.CutPrefix(head, "ref: ")
	if !ok || strings.Contains(ref, "..") {
		return ""
	}
	if b, err := os.ReadFile(filepath.Join(root, ".git", filepath.FromSlash(ref))); err == nil {
		if s := strings.TrimSpace(string(b)); hexRE.MatchString(s) {
			return s
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, ".git", "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(packed), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && name == ref && hexRE.MatchString(sum) {
			return sum
		}
	}
	return ""
}

// headTag returns the name of a tag that points at revision (annotated tags
// through their peeled line in packed-refs), or "".
func headTag(root, revision string) string {
	if root == "" || revision == "" {
		return ""
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".git", "refs", "tags")); err == nil {
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(root, ".git", "refs", "tags", e.Name()))
			if err == nil && strings.TrimSpace(string(b)) == revision {
				return e.Name()
			}
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, ".git", "packed-refs"))
	if err != nil {
		return ""
	}
	last := ""
	for _, line := range strings.Split(string(packed), "\n") {
		line = strings.TrimSpace(line)
		if peeled, ok := strings.CutPrefix(line, "^"); ok {
			if last != "" && peeled == revision {
				return last
			}
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		last = ""
		if tag, isTag := strings.CutPrefix(name, "refs/tags/"); ok && isTag {
			if sum == revision {
				return tag
			}
			last = tag
		}
	}
	return ""
}

// repoName is the repository name of a clone URL, else the folder name.
func repoName(source, root string) string {
	name := strings.TrimSuffix(filepath.Base(strings.TrimRight(source, "/")), ".git")
	if strings.Contains(source, "://") || strings.Contains(source, "@") {
		return name
	}
	return filepath.Base(root)
}

// detectLicense maps the first lines of the LICENSE file to an SPDX id.
func detectLicense(dir string) string {
	for _, name := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		head := string(b)
		if len(head) > 2000 {
			head = head[:2000]
		}
		switch {
		case strings.Contains(head, "Apache License") && strings.Contains(head, "Version 2.0"):
			return "Apache-2.0"
		case strings.Contains(head, "MIT License") || strings.Contains(head, "Permission is hereby granted, free of charge"):
			return "MIT"
		case strings.Contains(head, "GNU GENERAL PUBLIC LICENSE") && strings.Contains(head, "Version 3"):
			return "GPL-3.0"
		case strings.Contains(head, "BSD 3-Clause"):
			return "BSD-3-Clause"
		}
		return "NOASSERTION"
	}
	return "NOASSERTION"
}

// registryEntry is one dataset version in a SkillsBench-style registry.json.
type registryEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"git_commit_id"`
	Tasks   []struct {
		Commit string `json:"git_commit_id"`
	} `json:"tasks"`
}

// detectBenchmark names the benchmark and version: a registry.json entry
// whose commit is the clone's, else the Terminal-Bench README title, else
// the folder name with the revision.
func detectBenchmark(from, root, revision string) string {
	base := root
	if base == "" {
		base = from
	}
	if b, err := os.ReadFile(filepath.Join(base, "registry.json")); err == nil {
		var reg []registryEntry
		if json.Unmarshal(b, &reg) == nil && len(reg) > 0 {
			for _, e := range reg {
				if matchesCommit(e.Commit, revision) {
					return e.Name + "@" + e.Version
				}
				for _, t := range e.Tasks {
					if matchesCommit(t.Commit, revision) {
						return e.Name + "@" + e.Version
					}
				}
			}
			return reg[0].Name + "@" + shortRev(revision)
		}
	}
	if b, err := os.ReadFile(filepath.Join(base, "README.md")); err == nil {
		if m := tbTitleRE.FindStringSubmatch(string(b)); m != nil {
			return "terminal-bench@" + m[1]
		}
	}
	return filepath.Base(base) + "@" + shortRev(revision)
}

var tbTitleRE = regexp.MustCompile(`(?m)^#\s*Terminal-Bench\s+([0-9]+\.[0-9]+)`)

func matchesCommit(c, revision string) bool {
	return c != "" && revision != "" && (strings.HasPrefix(revision, c) || strings.HasPrefix(c, revision))
}

func shortRev(r string) string {
	if r == "" {
		return "unknown"
	}
	if len(r) > 12 {
		return r[:12]
	}
	return r
}
