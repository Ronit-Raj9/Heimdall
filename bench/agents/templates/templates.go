// Package templates holds the agent templates `bifrost-bench agent init`
// writes into a project (docs/adding-an-agent.md):
//
//   - command: wraps any command-line agent; example-cli stands in for yours.
//   - python: a minimal coding agent in one standard-library Python file.
//   - http-service: an agent that is a server; a launcher delivered through
//     the YAML's files starts it, waits for its health URL, POSTs the task
//     and prints the answer, and server.py stands in for yours.
//
// Each template is a folder holding agent.yaml and image/, the Dockerfile and
// the files built into the image. Every Dockerfile declares ARG BASE_IMAGE and
// starts FROM it, and every agent.yaml sets build.base_arg, so the image is
// built once per suite on the suite's base image.
package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Markers replaced when a template is rendered.
const (
	markerID       = "__AGENT_ID__"
	markerTag      = "__IMAGE_TAG__"
	markerLauncher = "__LAUNCHER__"
	launcherFile   = "launcher.py"
	launcherIndent = "    "
)

// Names lists the templates, in the order usage text shows them.
var Names = []string{"command", "python", "http-service"}

// idRe is the agent id rule of spec/agent.schema.json.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

//go:embed command python http-service
var files embed.FS

// File is one rendered file, at a path relative to the project root.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// Render returns the files of template name for agent id with image tag
// tag: agents/<id>.yaml and agents/images/<id>/<file> for every file in the
// template's image folder. The http-service launcher is inlined into the
// agent YAML's files entry, indented as a YAML block scalar.
func Render(name, id, tag string) ([]File, error) {
	if !slices.Contains(Names, name) {
		return nil, fmt.Errorf("unknown agent template %q (want %s)", name, strings.Join(Names, ", "))
	}
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("agent id %q must match %s", id, idRe)
	}
	if tag == "" || strings.ContainsAny(tag, " \t\n\"'") {
		return nil, fmt.Errorf("image tag %q is not a docker image reference", tag)
	}
	yaml, err := fs.ReadFile(files, path.Join(name, "agent.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", name, err)
	}
	if yaml, err = inlineLauncher(name, yaml); err != nil {
		return nil, err
	}
	r := strings.NewReplacer(markerID, id, markerTag, tag)
	out := []File{{Path: path.Join("agents", id+".yaml"), Content: []byte(r.Replace(string(yaml))), Mode: 0o644}}
	imageDir := path.Join(name, "image")
	entries, err := fs.ReadDir(files, imageDir)
	if err != nil {
		return nil, fmt.Errorf("read template %s image: %w", name, err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(files, path.Join(imageDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", name, err)
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".py") {
			mode = 0o755
		}
		out = append(out, File{Path: path.Join("agents", "images", id, e.Name()), Content: []byte(r.Replace(string(b))), Mode: mode})
	}
	return out, nil
}

// inlineLauncher replaces the launcher marker line of a template's agent YAML
// with launcher.py, each line indented under the files entry. A template
// without the marker is returned unchanged.
func inlineLauncher(name string, yaml []byte) ([]byte, error) {
	if !strings.Contains(string(yaml), markerLauncher) {
		return yaml, nil
	}
	src, err := fs.ReadFile(files, path.Join(name, launcherFile))
	if err != nil {
		return nil, fmt.Errorf("read template %s launcher: %w", name, err)
	}
	if strings.Contains(string(src), "{{") {
		return nil, errors.New("launcher must not contain {{: file content is a template")
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(src), "\n"), "\n") {
		if line != "" {
			b.WriteString(launcherIndent)
		}
		b.WriteString(line + "\n")
	}
	return []byte(strings.Replace(string(yaml), markerLauncher+"\n", b.String(), 1)), nil
}
