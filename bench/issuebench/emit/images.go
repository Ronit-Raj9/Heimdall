package emit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// Workspace is the repository folder inside every emitted image.
const Workspace = "/testbed"

// Layers are the three images of one task: a shared base (language image
// plus git), the repository environment built from the EnvSpec (shared by
// every task of that repository version), and a thin per-task layer that
// only copies the scrubbed repository at its base commit.
type Layers struct {
	BaseRef        string `json:"base_ref"`
	EnvRef         string `json:"env_ref"`
	TaskRef        string `json:"task_ref"`
	BaseDockerfile string `json:"base_dockerfile"`
	EnvDockerfile  string `json:"env_dockerfile"`
	TaskDockerfile string `json:"task_dockerfile"`
}

// ImageNamespace prefixes every emitted image name.
const ImageNamespace = "heimdall-issues"

func shortHash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:6])
}

func tagSafe(s string) string {
	s = strings.ToLower(idUnsafe.ReplaceAllString(s, "-"))
	s = strings.Trim(s, "-.")
	if len(s) > 100 {
		s = s[:100]
	}
	if s == "" {
		s = "x"
	}
	return s
}

// LayersOf plans the three images. The base and env names carry a hash of
// what they are built from, so the same EnvSpec reuses the same images;
// the task tag carries the scrubbed base commit.
func LayersOf(env issuebench.EnvSpec, instanceID, base string) (Layers, error) {
	if strings.TrimSpace(env.BaseImage) == "" {
		return Layers{}, fmt.Errorf("%s: environment has no base image", env.Repo)
	}
	l := Layers{}
	l.BaseDockerfile = "FROM " + env.BaseImage + "\n" +
		"RUN (command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*))\n" +
		"WORKDIR " + Workspace + "\n"
	l.BaseRef = fmt.Sprintf("%s/base:%s-%s", ImageNamespace, tagSafe(env.BaseImage), shortHash(l.BaseDockerfile))

	var b strings.Builder
	b.WriteString("FROM " + l.BaseRef + "\n")
	b.WriteString("COPY repo/ " + Workspace + "/\n")
	b.WriteString("WORKDIR " + Workspace + "\n")
	for _, c := range env.Commands {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		b.WriteString("RUN " + RunLine(c) + "\n")
	}
	// The env layer installs from the repository files; the task layer then
	// replaces the folder, so no file of another task is left behind.
	b.WriteString("RUN rm -rf " + Workspace + "\n")
	l.EnvDockerfile = b.String()
	version := env.Version
	if version == "" {
		version = "0"
	}
	l.EnvRef = fmt.Sprintf("%s/env-%s:%s-%s", ImageNamespace, tagSafe(strings.ReplaceAll(env.Repo, "/", "-")), tagSafe(version), shortHash(l.BaseRef, l.EnvDockerfile))

	l.TaskDockerfile = "FROM " + l.EnvRef + "\n" +
		"COPY repo/ " + Workspace + "/\n" +
		"WORKDIR " + Workspace + "\n"
	l.TaskRef = fmt.Sprintf("%s/task-%s:%s", ImageNamespace, tagSafe(instanceID), shortSHA(base))
	return l, nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "latest"
	}
	return s
}
