package utils

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Artifact limits and names (schemas.ArtifactSpec). The caps bound what an
// agent can make the bench copy, store, seal and serve.
const (
	// ArtifactsDir is the run folder (and /grader) folder holding a run's
	// artifact files, one sub-folder per artifact id.
	ArtifactsDir = "artifacts"
	// GraderArtifactsDir is where the verifier reads them, read-only.
	GraderArtifactsDir = "/grader/artifacts"
	// DefaultArtifactMaxBytes is the per-file cap when a spec sets none.
	DefaultArtifactMaxBytes int64 = 32 << 20
	// MaxArtifactTotalBytes caps the bytes copied for one run, all
	// artifacts together.
	MaxArtifactTotalBytes int64 = 128 << 20
	// MaxArtifactMatches caps the files one glob may copy.
	MaxArtifactMatches = 64
	// MaxArtifactSpecs caps the artifacts one task may declare.
	MaxArtifactSpecs = 32
)

var (
	// artifactIDRe is an artifact id: it names a folder in the run folder and
	// in /grader/artifacts, and a URL path segment.
	artifactIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// artifactNameRe is a file name that may be copied: the agent chooses it,
	// and it becomes a run folder file name and a URL path segment.
	artifactNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// OutputArtifacts are the artifacts the task expects: the task's own output
// artifacts, else the suite's. It is empty for a task of another kind.
func OutputArtifacts(s *schemas.Suite, t *schemas.Task) []schemas.ArtifactSpec {
	if OutputKind(s, t) != schemas.OutputArtifact {
		return nil
	}
	if t != nil && t.Output != nil && len(t.Output.Artifacts) > 0 {
		return t.Output.Artifacts
	}
	if s != nil && s.Output != nil {
		return s.Output.Artifacts
	}
	return nil
}

// ArtifactMaxBytes is a spec's per-file cap, DefaultArtifactMaxBytes when it
// sets none.
func ArtifactMaxBytes(a schemas.ArtifactSpec) int64 {
	if a.MaxBytes > 0 {
		return a.MaxBytes
	}
	return DefaultArtifactMaxBytes
}

// CheckArtifactSpec refuses an artifact spec whose id is not a safe folder
// name, whose path is empty, absolute, not clean, leaves the workspace or
// uses a recursive **, or whose cap is negative or over the run's total cap.
// It is pure.
func CheckArtifactSpec(a schemas.ArtifactSpec) error {
	if !artifactIDRe.MatchString(a.ID) {
		return fmt.Errorf("artifact id %q is not [a-z0-9][a-z0-9_-]{0,62}", a.ID)
	}
	p := a.Path
	switch {
	case p == "":
		return fmt.Errorf("artifact %s: path is empty", a.ID)
	case path.IsAbs(p) || strings.Contains(p, "\\"):
		return fmt.Errorf("artifact %s: path %q must be relative to the workspace root, with forward slashes", a.ID, p)
	case path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("artifact %s: path %q must be clean and stay inside the workspace", a.ID, p)
	case strings.Contains(p, "**"):
		return fmt.Errorf("artifact %s: path %q uses **; only *, ? and [...] are supported", a.ID, p)
	case strings.ContainsAny(p, "\x00\n\r"):
		return fmt.Errorf("artifact %s: path holds a control character", a.ID)
	}
	if a.MaxBytes < 0 || a.MaxBytes > MaxArtifactTotalBytes {
		return fmt.Errorf("artifact %s: max_bytes %d must be between 0 and %d", a.ID, a.MaxBytes, MaxArtifactTotalBytes)
	}
	return nil
}

// ArtifactIDOK reports whether id is a valid artifact id. It is pure.
func ArtifactIDOK(id string) bool {
	return artifactIDRe.MatchString(id)
}

// ArtifactNameOK reports whether a matched file's base name may be copied.
// It is pure.
func ArtifactNameOK(name string) bool {
	return artifactNameRe.MatchString(name) && name != "." && name != ".."
}

// ArtifactRunPath is the run folder path of one artifact file:
// artifacts/<id>/<name>.
func ArtifactRunPath(id, name string) string {
	return path.Join(ArtifactsDir, id, name)
}

// MissingArtifacts names every required artifact that has no usable file
// (one copied without error) among files, with the first error recorded for
// it. Empty means every required artifact is there. It is pure.
func MissingArtifacts(specs []schemas.ArtifactSpec, files []schemas.ArtifactFile) []string {
	var out []string
	for _, a := range specs {
		if !a.Required {
			continue
		}
		ok, why := false, ""
		for _, f := range files {
			if f.ID != a.ID {
				continue
			}
			if f.Error == "" && f.Path != "" {
				ok = true
				break
			}
			if why == "" {
				why = f.Error
			}
		}
		if ok {
			continue
		}
		if why == "" {
			why = "missing"
		}
		out = append(out, fmt.Sprintf("%s (%s: %s)", a.ID, a.Path, why))
	}
	return out
}
