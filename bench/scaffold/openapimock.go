package scaffold

// Stateful mocks from OpenAPI: a customer's API description becomes a task
// service that serves the same routes (and one MCP tool per operation) over
// seeded, exported state, so a task can use the customer's internal API
// shape without the real system. The service is generic (mockapi/server.py);
// what is generated is its mock.json, derived from the spec.

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
)

//go:embed mockapi/server.py
var mockServerPy []byte

//go:embed mockapi/Dockerfile
var mockDockerfile []byte

// Mock operation kinds (MockOperation.Kind).
const (
	MockList   = "list"
	MockGet    = "get"
	MockCreate = "create"
	MockUpdate = "update"
	MockDelete = "delete"
	MockAction = "action"
)

// Limits of a mock.
const (
	MaxMockSpecBytes  = 4 << 20
	MaxMockOperations = 200
	MaxMockFixtures   = 2000
)

// MockParent is the parent of a nested collection path: the path parameter
// and the field of the child that holds it.
type MockParent struct {
	Param string `json:"param"`
	Field string `json:"field"`
}

// MockOperation is one API operation the mock serves.
type MockOperation struct {
	Name         string         `json:"name"`
	Method       string         `json:"method"`
	Path         string         `json:"path"`
	Kind         string         `json:"kind"`
	Collection   string         `json:"collection,omitempty"`
	IDParam      string         `json:"id_param,omitempty"`
	Parent       *MockParent    `json:"parent,omitempty"`
	PathParams   []string       `json:"path_params,omitempty"`
	QueryParams  []string       `json:"query_params,omitempty"`
	Body         bool           `json:"body,omitempty"`
	BodyRequired bool           `json:"body_required,omitempty"`
	BodySchema   map[string]any `json:"body_schema,omitempty"`
	Description  string         `json:"description,omitempty"`
}

// MockFixture is a recorded answer the mock gives before its state does.
type MockFixture struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Body     any    `json:"body,omitempty"`
	Status   int    `json:"status"`
	Response any    `json:"response"`
	Times    *int   `json:"times,omitempty"`
}

// MockSpec is mock.json.
type MockSpec struct {
	Name       string          `json:"name"`
	Title      string          `json:"title,omitempty"`
	Source     string          `json:"source,omitempty"` // the OpenAPI file's title and version
	Operations []MockOperation `json:"operations"`
	Fixtures   []MockFixture   `json:"fixtures"`
}

// Collections are the state collections the mock keeps, sorted.
func (m *MockSpec) Collections() []string {
	seen := map[string]bool{}
	for _, op := range m.Operations {
		if op.Collection != "" {
			seen[op.Collection] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// openAPIDoc is the part of an OpenAPI 3 document the mock reads.
type openAPIDoc struct {
	OpenAPI string `yaml:"openapi"`
	Swagger string `yaml:"swagger"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

var (
	paramRE    = regexp.MustCompile(`\{([^}/]+)\}`)
	opNameRE   = regexp.MustCompile(`[^A-Za-z0-9_]+`)
	httpMethod = []string{"get", "post", "put", "patch", "delete"}
)

// MockFromOpenAPI derives a mock from an OpenAPI 3 document (JSON or YAML).
// name is the service id.
func MockFromOpenAPI(name string, data []byte) (*MockSpec, []string, error) {
	if len(data) > MaxMockSpecBytes {
		return nil, nil, fmt.Errorf("the OpenAPI file is over %d MiB", MaxMockSpecBytes>>20)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, errors.New("This is not an OpenAPI file Heimdall can read. It must be YAML or JSON with an openapi: 3.x field.")
	}
	switch {
	case doc.Swagger != "":
		return nil, nil, errors.New("this is a Swagger 2 file; convert it to OpenAPI 3 (for example with swagger2openapi) and upload that")
	case !strings.HasPrefix(doc.OpenAPI, "3."):
		return nil, nil, errors.New("not an OpenAPI 3 document (no openapi: 3.x field)")
	case len(doc.Paths) == 0:
		return nil, nil, errors.New("the OpenAPI file has no paths")
	}
	var warnings []string
	m := &MockSpec{Name: name, Title: doc.Info.Title, Source: strings.TrimSpace(doc.Info.Title + " " + doc.Info.Version), Fixtures: []MockFixture{}}
	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	used := map[string]bool{}
	for _, p := range paths {
		item := doc.Paths[p]
		shared := paramNames(item["parameters"], "query")
		for _, method := range httpMethod {
			raw, ok := item[method]
			if !ok {
				continue
			}
			op, _ := raw.(map[string]any)
			mo := classifyOperation(strings.ToUpper(method), p)
			mo.Name = operationName(op, mo, used)
			mo.QueryParams = append(append([]string{}, shared...), paramNames(op["parameters"], "query")...)
			mo.Description = strings.TrimSpace(firstString(op, "summary") + " " + firstString(op, "description"))
			if rb, ok := op["requestBody"].(map[string]any); ok {
				mo.Body = true
				mo.BodyRequired, _ = rb["required"].(bool)
				if schema := jsonSchema(rb, doc.Components.Schemas); schema != nil {
					mo.BodySchema = schema
				}
			}
			m.Operations = append(m.Operations, mo)
			if len(m.Operations) > MaxMockOperations {
				return nil, nil, fmt.Errorf("the API has more than %d operations; upload the part the agent uses", MaxMockOperations)
			}
		}
	}
	if len(m.Operations) == 0 {
		return nil, nil, errors.New("the OpenAPI file has no get, post, put, patch or delete operation")
	}
	actions := 0
	for _, op := range m.Operations {
		if op.Kind == MockAction {
			actions++
		}
	}
	if actions > 0 {
		warnings = append(warnings, fmt.Sprintf("%d operations are not plain create, read, update or delete; the mock records them in _actions and answers {\"ok\": true} unless a fixture answers them", actions))
	}
	return m, warnings, nil
}

// classifyOperation reads what an operation does from its method and path:
// /orders and /orders/{id} are a collection and its items; a nested path
// (/customers/{customer_id}/orders) filters by the parent; a literal after
// an item that is not a plural noun (/orders/{id}/refund) is an action.
func classifyOperation(method, p string) MockOperation {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	mo := MockOperation{Method: method, Path: p, Kind: MockAction}
	isParam := func(s string) bool {
		return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && paramRE.MatchString(s)
	}
	for _, s := range segs {
		if isParam(s) {
			mo.PathParams = append(mo.PathParams, paramRE.FindStringSubmatch(s)[1])
		}
	}
	n := len(segs)
	if n == 0 || segs[0] == "" {
		return mo
	}
	collAt, item := -1, false
	switch {
	case isParam(segs[n-1]) && n >= 2 && !isParam(segs[n-2]):
		collAt, item = n-2, true
	case !isParam(segs[n-1]):
		collAt = n - 1
	default:
		return mo
	}
	coll := segs[collAt]
	nested := collAt >= 2 && isParam(segs[collAt-1]) && !isParam(segs[collAt-2])
	if !item && collAt >= 1 && isParam(segs[collAt-1]) && !strings.HasSuffix(coll, "s") {
		return mo // /orders/{id}/refund
	}
	switch {
	case item && method == "GET":
		mo.Kind = MockGet
	case item && (method == "PUT" || method == "PATCH"):
		mo.Kind = MockUpdate
	case item && method == "DELETE":
		mo.Kind = MockDelete
	case !item && method == "GET":
		mo.Kind = MockList
	case !item && method == "POST":
		mo.Kind = MockCreate
	default:
		return mo
	}
	if item {
		mo.IDParam = paramRE.FindStringSubmatch(segs[n-1])[1]
	}
	if nested {
		param := paramRE.FindStringSubmatch(segs[collAt-1])[1]
		mo.Parent = &MockParent{Param: param, Field: singular(segs[collAt-2]) + "_id"}
		if strings.HasSuffix(param, "_id") {
			mo.Parent.Field = param
		}
	}
	mo.Collection = strings.ReplaceAll(coll, "-", "_")
	return mo
}

func singular(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "ses"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// operationName is the operationId (made a tool name), else one made from
// the kind and collection, unique within the mock.
func operationName(op map[string]any, mo MockOperation, used map[string]bool) string {
	name := firstString(op, "operationId")
	if name == "" {
		switch mo.Kind {
		case MockAction:
			name = strings.ToLower(mo.Method) + "_" + strings.Trim(paramRE.ReplaceAllString(mo.Path, "by_$1"), "/")
		default:
			c := mo.Collection
			if mo.Kind != MockList {
				c = singular(c)
			}
			name = mo.Kind + "_" + c
		}
	}
	name = strings.Trim(opNameRE.ReplaceAllString(name, "_"), "_")
	if len(name) > 60 {
		name = name[:60]
	}
	if name == "" {
		name = "operation"
	}
	base := name
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[name] = true
	return name
}

func firstString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func paramNames(v any, in string) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		p, _ := x.(map[string]any)
		if p == nil || p["in"] != in {
			continue
		}
		if n, _ := p["name"].(string); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// jsonSchema is the requestBody's application/json schema with local $refs
// resolved one level deep (enough for a tool's input description).
func jsonSchema(rb map[string]any, comps map[string]any) map[string]any {
	content, _ := rb["content"].(map[string]any)
	for ct, v := range content {
		if !strings.Contains(ct, "json") {
			continue
		}
		mt, _ := v.(map[string]any)
		s, _ := mt["schema"].(map[string]any)
		return resolveRef(s, comps, 0)
	}
	return nil
}

func resolveRef(s map[string]any, comps map[string]any, depth int) map[string]any {
	if s == nil || depth > 4 {
		return s
	}
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if target, ok := comps[name].(map[string]any); ok {
			return resolveRef(target, comps, depth+1)
		}
		return map[string]any{"type": "object"}
	}
	out := map[string]any{}
	for k, v := range s {
		out[k] = v
	}
	if props, ok := s["properties"].(map[string]any); ok {
		np := map[string]any{}
		for k, v := range props {
			if pm, ok := v.(map[string]any); ok {
				np[k] = resolveRef(pm, comps, depth+1)
			} else {
				np[k] = v
			}
		}
		out["properties"] = np
	}
	return out
}

// MockServiceFiles are the files of the mock's service folder
// (services/<id>/ in the suite): the server, its image and mock.json.
func MockServiceFiles(m *MockSpec, openapi []byte, openapiName string) ([]File, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	files := []File{
		{Path: "server.py", Data: mockServerPy, Mode: 0o755},
		{Path: "Dockerfile", Data: mockDockerfile, Mode: 0o644},
		{Path: "mock.json", Data: append(b, '\n'), Mode: 0o644},
	}
	if len(openapi) > 0 {
		files = append(files, File{Path: openapiName, Data: openapi, Mode: 0o644})
	}
	return files, nil
}

// MockSeed is an empty seed of the mock's collections, for a task's
// seed/<id>.json.
func MockSeed(m *MockSpec) []byte {
	seed := map[string]any{}
	for _, c := range m.Collections() {
		seed[c] = []any{}
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	return append(b, '\n')
}

// MockServiceYAML is the services: entry a task adds to use the mock.
func MockServiceYAML(suiteID, id string) string {
	return fmt.Sprintf(`  - id: %[2]s
    role: mcp
    # Built from services/%[2]s/ (bifrost-bench mock openapi); suite admit builds it when missing.
    image: bench/%[1]s-%[2]s:1
    port: 8000
    mcp: {path: /mcp}
    files:
      /seed/state.json: seed/%[2]s.json
    ready:
      argv: ["python3", "-I", "/srv/server.py", "ready"]
      timeout_s: 30
    export:
      - {id: state.json, path: /state/state.json}
      - {id: calls.jsonl, path: /state/calls.jsonl}
`, suiteID, id)
}

// FixturesFromLedger turns a run's recorded calls of one mock service
// (tools.jsonl rows of server service) into fixtures: each call answered as
// it was, in order, once. Calls that failed are kept, so a replay shows the
// same errors.
func FixturesFromLedger(m *MockSpec, service string, rows []schemas.ToolCall) []MockFixture {
	byName := map[string]MockOperation{}
	for _, op := range m.Operations {
		byName[op.Name] = op
	}
	once := 1
	var out []MockFixture
	for _, r := range rows {
		if r.Server != service || r.Kind != "" || r.Tool == "" {
			continue
		}
		var args map[string]any
		_ = json.Unmarshal([]byte(r.Arguments), &args)
		var resp any
		if json.Unmarshal([]byte(r.Result), &resp) != nil {
			resp = r.Result
		}
		status := 200
		if r.IsError {
			status = 400
		}
		if r.Status != 0 {
			status = r.Status
		}
		fx := MockFixture{Status: status, Response: resp, Times: &once}
		if op, ok := byName[r.Tool]; ok {
			fx.Method, fx.Path = op.Method, op.Path
			p := op.Path
			for _, pp := range op.PathParams {
				if v, ok := args[pp]; ok {
					p = strings.ReplaceAll(p, "{"+pp+"}", fmt.Sprint(v))
				}
			}
			fx.Path = p
			if b, ok := args["body"]; ok {
				fx.Body = b
			}
		} else if method, p, ok := strings.Cut(r.Tool, " "); ok { // a plain HTTP call: "METHOD /path"
			fx.Method, fx.Path = method, strings.SplitN(p, "?", 2)[0]
			if len(args) > 0 {
				fx.Body = args
			}
		} else {
			continue
		}
		out = append(out, fx)
		if len(out) == MaxMockFixtures {
			break
		}
	}
	return out
}

// MockResult is what WriteMock wrote.
type MockResult struct {
	Dir      string    `json:"dir"`
	Mock     *MockSpec `json:"mock"`
	Warnings []string  `json:"warnings,omitempty"`
	// Service is the services: entry a task adds, Seed an empty seed of the
	// collections for the task's seed/<id>.json.
	Service string `json:"service_yaml"`
	Seed    string `json:"seed"`
}

// mockIDRE is a service id a mock may take.
var mockIDRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// MockFiles derives a mock from an OpenAPI file and returns its service
// folder's files (services/<id>/ of the suite) and what a task adds to use it.
func MockFiles(suiteID, id string, openapi []byte, fileName string) ([]File, *MockResult, error) {
	if !mockIDRE.MatchString(id) {
		return nil, nil, fmt.Errorf("service id %q: lowercase letters, digits, '_' or '-', starting with a letter, at most 32", id)
	}
	ext := ".yaml"
	if strings.HasSuffix(strings.ToLower(fileName), ".json") || strings.HasPrefix(strings.TrimSpace(string(openapi)), "{") {
		ext = ".json"
	}
	m, warns, err := MockFromOpenAPI(id, openapi)
	if err != nil {
		return nil, nil, err
	}
	files, err := MockServiceFiles(m, openapi, "openapi"+ext)
	if err != nil {
		return nil, nil, err
	}
	return files, &MockResult{Mock: m, Warnings: warns, Service: MockServiceYAML(suiteID, id), Seed: string(MockSeed(m))}, nil
}

// WriteMock writes the mock's service folder into a suite folder.
func WriteMock(suiteDir, id string, openapi []byte, fileName string, force bool) (*MockResult, error) {
	files, res, err := MockFiles(filepath.Base(suiteDir), id, openapi, fileName)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(suiteDir, "services", id)
	if _, err := os.Stat(dir); err == nil && !force {
		return nil, fmt.Errorf("services/%s already exists; pick another id or replace it", id)
	}
	if err := writeFolder(dir, files, "", force); err != nil {
		return nil, err
	}
	res.Dir = dir
	return res, nil
}

// ReadMock reads a mock service folder's mock.json.
func ReadMock(serviceDir string) (*MockSpec, error) {
	b, err := os.ReadFile(filepath.Join(serviceDir, "mock.json"))
	if err != nil {
		return nil, err
	}
	var m MockSpec
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("mock.json: %v", err)
	}
	return &m, nil
}

// SaveMock writes mock.json back after its fixtures changed.
func SaveMock(serviceDir string, m *MockSpec) error {
	if len(m.Fixtures) > MaxMockFixtures {
		return fmt.Errorf("%d fixtures; at most %d", len(m.Fixtures), MaxMockFixtures)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(serviceDir, "mock.json"), append(b, '\n'), 0o644)
}
