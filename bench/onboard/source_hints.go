package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// HarborResolver fetches the tasks of a Harbor registry id (name@version)
// into dest and returns the folder the harbor importer reads. The Harbor
// registry lane sets it; nil means registry ids are recognised but not
// fetched on this server.
var HarborResolver func(ctx context.Context, env *Env, id, dest string) (string, error)

// harborIDSource reads a Harbor registry id such as terminal-bench@2.0.
type harborIDSource struct{}

func init() {
	Register(harborIDSource{})
	Register(githubSource{})
	Register(openAPISource{})
	Register(tracesSource{})
}

func (harborIDSource) Name() string { return "harbor-registry" }
func (harborIDSource) Kind() Kind   { return KindHarbor }

const harborNoResolver = "Harbor registry ids are not fetched on this server yet: download the dataset folder (git clone of its repository), zip it and drop the zip here"

func (harborIDSource) Detect(p Peek) (Match, bool) {
	if p.URL != nil || !HarborID(p.Raw) {
		return Match{}, false
	}
	m := Match{Label: "Harbor registry id " + p.Raw, Score: 100, CanDraft: HarborResolver != nil}
	if HarborResolver == nil {
		m.Note = harborNoResolver
	}
	return m, true
}

func (harborIDSource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	id := strings.TrimSpace(in.URL)
	if HarborResolver == nil {
		return &Inventory{Kind: KindHarbor, Title: id, Unit: "task", License: "per dataset", Samples: []Sample{}, Next: harborNoResolver}, nil
	}
	tmp, err := os.MkdirTemp("", "onboard-harbor-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dir, err := HarborResolver(ctx, env, id, tmp)
	if err != nil {
		return nil, err
	}
	p := Peek{Name: id, Dir: dir, Files: listFiles(dir, maxPeekFiles)}
	inv, err := inspectImporter(p, true)
	if err != nil {
		return nil, err
	}
	inv.Title = id
	inv.Suggest.Suite = slug(strings.ReplaceAll(id, "@", "-"))
	inv.Suggest.Importer = "harbor"
	return inv, nil
}

func (harborIDSource) Draft(ctx context.Context, env *Env, in Input, plan Plan, set *DraftSet) error {
	if HarborResolver == nil {
		return fmt.Errorf("%w: %s", ErrNoDraft, harborNoResolver)
	}
	dir, err := HarborResolver(ctx, env, strings.TrimSpace(in.URL), filepath.Join(set.Dir(), "source"))
	if err != nil {
		return err
	}
	return draftImporter(ctx, set, "harbor", dir, plan)
}

// githubSource recognises a GitHub repository link.
type githubSource struct{}

func (githubSource) Name() string { return "github" }
func (githubSource) Kind() Kind   { return KindGitHub }

const githubNext = "A repository becomes tasks from its fix commits: create a new benchmark and pick Commits in your repository as where its tasks come from, or open an existing benchmark's Build tab and use Tasks from commits; connect GitHub in Settings for a private repository. A repository that holds a benchmark folder (Harbor task.toml files) can be downloaded as a zip and dropped here."

func githubRepo(p Peek) (string, bool) {
	if p.URL == nil {
		return "", false
	}
	h := strings.ToLower(p.URL.Hostname())
	if h != "github.com" && h != "www.github.com" {
		return "", false
	}
	parts := strings.Split(strings.Trim(p.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), true
}

func (githubSource) Detect(p Peek) (Match, bool) {
	repo, ok := githubRepo(p)
	if !ok {
		return Match{}, false
	}
	return Match{Label: "GitHub repository " + repo, Score: 90, Note: githubNext}, true
}

func (githubSource) Inspect(_ context.Context, env *Env, in Input) (*Inventory, error) {
	p, _ := env.Peek(context.Background(), in)
	repo, _ := githubRepo(p)
	return &Inventory{Kind: KindGitHub, Title: repo, Unit: "commit", License: "the repository's", Samples: []Sample{}, Next: githubNext}, nil
}

func (githubSource) Draft(context.Context, *Env, Input, Plan, *DraftSet) error {
	return fmt.Errorf("%w: %s", ErrNoDraft, githubNext)
}

// openAPISource recognises an OpenAPI or Swagger document.
type openAPISource struct{}

func (openAPISource) Name() string { return "openapi" }
func (openAPISource) Kind() Kind   { return KindOpenAPI }

const openAPINext = "An API spec becomes tasks on the Tasks from your API page (Add a benchmark, then Build tasks from your OpenAPI file and policy): give it this spec and a policy and it drafts scenario tasks against a stateful mock of the API. A benchmark's Build tab can also host the spec as a mock (OpenAPI mock)."

func looksOpenAPI(b []byte) bool {
	h := head(b, 4096)
	return bytes.Contains(h, []byte(`"openapi"`)) || bytes.Contains(h, []byte(`"swagger"`)) ||
		bytes.HasPrefix(bytes.TrimSpace(h), []byte("openapi:")) || bytes.HasPrefix(bytes.TrimSpace(h), []byte("swagger:")) ||
		bytes.Contains(h, []byte("\nopenapi:")) || bytes.Contains(h, []byte("\nswagger:"))
}

func (openAPISource) Detect(p Peek) (Match, bool) {
	if p.Dir != "" || !looksOpenAPI(p.Data) {
		return Match{}, false
	}
	return Match{Label: "OpenAPI spec", Score: 95, Note: openAPINext}, true
}

func (openAPISource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	p, err := env.Peek(ctx, in)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Info struct {
			Title   string `yaml:"title"`
			License struct {
				Name string `yaml:"name"`
			} `yaml:"license"`
		} `yaml:"info"`
		Paths map[string]map[string]struct {
			Summary     string `yaml:"summary"`
			OperationID string `yaml:"operationId"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(p.Data, &doc); err != nil {
		return nil, fmt.Errorf("read the spec: %w", err)
	}
	inv := &Inventory{Kind: KindOpenAPI, Title: doc.Info.Title, License: doc.Info.License.Name, Unit: "operation", Samples: []Sample{}, Next: openAPINext}
	var ops []string
	for pth, methods := range doc.Paths {
		for m, op := range methods {
			switch strings.ToLower(m) {
			case "get", "post", "put", "patch", "delete":
				ops = append(ops, strings.ToUpper(m)+" "+pth+"\x00"+op.Summary)
			}
		}
	}
	sort.Strings(ops)
	inv.Units = len(ops)
	for i, o := range ops {
		if i >= 5 {
			break
		}
		name, sum, _ := strings.Cut(o, "\x00")
		inv.Samples = append(inv.Samples, Sample{Ref: name, Fields: map[string]string{"summary": sum}})
	}
	return inv, nil
}

func (openAPISource) Draft(context.Context, *Env, Input, Plan, *DraftSet) error {
	return fmt.Errorf("%w: %s", ErrNoDraft, openAPINext)
}

// tracesSource recognises an OpenTelemetry (OTLP JSON) trace export.
type tracesSource struct{}

func (tracesSource) Name() string { return "traces" }
func (tracesSource) Kind() Kind   { return KindTraces }

const tracesNext = "Traces become tasks through the failure inbox: point the inbox at your trace source (Langfuse, LangSmith, Maxim, OTLP), flag the sessions that went wrong, and each becomes a draft task to accept."

func looksOTLP(b []byte) bool {
	return bytes.Contains(head(b, 4096), []byte(`"resourceSpans"`))
}

func (tracesSource) Detect(p Peek) (Match, bool) {
	if p.Dir != "" || !looksOTLP(p.Data) {
		return Match{}, false
	}
	return Match{Label: "OpenTelemetry traces", Score: 95, Note: tracesNext}, true
}

func (tracesSource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	p, err := env.Peek(ctx, in)
	if err != nil {
		return nil, err
	}
	var doc struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []struct {
					TraceID string `json:"traceId"`
					Name    string `json:"name"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	_ = json.Unmarshal(p.Data, &doc)
	traces := map[string]bool{}
	inv := &Inventory{Kind: KindTraces, Title: p.Name, License: "your data", Unit: "trace", Samples: []Sample{}, Next: tracesNext}
	for _, rs := range doc.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if !traces[sp.TraceID] && len(inv.Samples) < 5 {
					inv.Samples = append(inv.Samples, Sample{Ref: "trace " + sp.TraceID, Fields: map[string]string{"first span": sp.Name}})
				}
				traces[sp.TraceID] = true
			}
		}
	}
	inv.Units = len(traces)
	return inv, nil
}

func (tracesSource) Draft(context.Context, *Env, Input, Plan, *DraftSet) error {
	return fmt.Errorf("%w: %s", ErrNoDraft, tracesNext)
}
