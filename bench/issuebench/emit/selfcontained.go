package emit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// A self-contained task (hosted site, W07) carries its own image recipe:
// environment/Dockerfile plus environment/repo.tar.gz, the scrubbed
// repository at the base commit. The worker builds the image at admission
// like any other task Dockerfile, so no image has to leave this machine.

// archiveTime is the mtime of every archive entry, so the same tree gives
// the same archive.
var archiveTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// SelfContainedDockerfile is the task image recipe for env. Its build
// context is the task's environment folder.
func SelfContainedDockerfile(env issuebench.EnvSpec) string {
	var b strings.Builder
	b.WriteString("# Written by Heimdall's issue emitter. The worker builds this image at admission.\n")
	b.WriteString("FROM " + env.BaseImage + "\n")
	// The builder ran every step with bash -lc and its time pins; the image
	// at admit runs them the same way.
	b.WriteString("SHELL [\"/bin/bash\", \"-lc\"]\n")
	for _, l := range strings.Split(env.Dockerfile, "\n") {
		if strings.HasPrefix(l, "ENV ") {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("RUN (command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*))\n")
	b.WriteString("WORKDIR " + Workspace + "\n")
	b.WriteString("ADD repo.tar.gz " + Workspace + "/\n")
	for _, c := range env.Commands {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		b.WriteString("RUN " + RunLine(c) + "\n")
	}
	// The install steps may write into the repository; the task starts from
	// the clean files again.
	b.WriteString("RUN rm -rf " + Workspace + "\n")
	b.WriteString("ADD repo.tar.gz " + Workspace + "/\n")
	b.WriteString("WORKDIR " + Workspace + "\n")
	return b.String()
}

// RunLine is one install step as a RUN instruction. A multi-line step is a
// heredoc: joining its lines with backslashes broke a step that has its own
// heredoc (python - <<'PY' ...), and BuildKit refused the whole Dockerfile
// with "unterminated heredoc" at admit.
func RunLine(cmd string) string {
	c := strings.TrimSpace(cmd)
	if !strings.Contains(c, "\n") {
		return c
	}
	return "<<'HEIMDALL_STEP'\nset -e\n" + c + "\nHEIMDALL_STEP"
}

// SelfContain writes environment/Dockerfile and environment/repo.tar.gz
// into taskDir and points task.yaml's environment at them.
func SelfContain(taskDir string, env issuebench.EnvSpec, repoDir string) error {
	if strings.TrimSpace(env.BaseImage) == "" {
		return errors.New("the environment has no base image")
	}
	envDir := filepath.Join(taskDir, "environment")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(envDir, "Dockerfile"), []byte(SelfContainedDockerfile(env)), 0o644); err != nil {
		return err
	}
	if err := writeRepoArchive(filepath.Join(envDir, "repo.tar.gz"), repoDir); err != nil {
		return err
	}
	return selfContainYAML(filepath.Join(taskDir, "task.yaml"))
}

// capWriter counts every byte and writes only while the total is within
// the cap, so an oversized archive is measured but not kept.
type capWriter struct {
	w   io.Writer
	n   int64
	cap int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.n > c.cap {
		return len(p), nil
	}
	return c.w.Write(p)
}

func writeRepoArchive(dst, repoDir string) (err error) {
	root, err := filepath.Abs(repoDir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(root); err != nil {
		return err
	} else if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", repoDir)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	cw := &capWriter{w: f, cap: issuebench.MaxRepoArchiveBytes}
	zw, _ := gzip.NewWriterLevel(cw, gzip.BestCompression)
	tw := tar.NewWriter(zw)
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("%s is outside the repository", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h := &tar.Header{Name: name, ModTime: archiveTime, Format: tar.FormatPAX}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("the link %s points outside the repository", rel)
			}
			res := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(rel), target)))
			if res == ".." || strings.HasPrefix(res, "../") {
				return fmt.Errorf("the link %s points outside the repository", rel)
			}
			h.Typeflag, h.Linkname, h.Mode = tar.TypeSymlink, target, 0o777
		case d.IsDir():
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, name+"/", 0o755
		case info.Mode().IsRegular():
			h.Typeflag, h.Size, h.Mode = tar.TypeReg, info.Size(), 0o644
			if info.Mode()&0o111 != 0 {
				h.Mode = 0o755
			}
		default:
			return fmt.Errorf("%s is not a file, folder or link", rel)
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.CopyN(tw, src, h.Size); err != nil {
			return fmt.Errorf("archive %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if cw.n > cw.cap {
		mib := (cw.n + 1<<20 - 1) >> 20
		return fmt.Errorf("the repository is too large for a hosted task (%d MiB, the cap is 8 MiB)", mib)
	}
	return nil
}

// selfContainYAML points task.yaml's environment at the task's own
// Dockerfile: no image, dockerfile and context set, network during the
// build only, workspace_path kept.
func selfContainYAML(p string) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("task.yaml is not a mapping")
	}
	m := doc.Content[0]
	envNode := getKey(m, "environment")
	if envNode == nil || envNode.Kind != yaml.MappingNode {
		envNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setKey(m, "environment", envNode)
		setKey(envNode, "workspace_path", str(Workspace))
	}
	delKey(envNode, "image")
	setKey(envNode, "dockerfile", str("environment/Dockerfile"))
	setKey(envNode, "context", str("environment"))
	setKey(envNode, "build_network", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}
