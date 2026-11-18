package admitscan

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// RuleMCPFilesSkipped is reported for service files the read-only sandbox
// cannot place.
const RuleMCPFilesSkipped = "mcp-files-skipped"

// MCP scan defaults.
const (
	DefaultRugWait      = 5 * time.Second
	defaultReadyTimeout = 60 * time.Second
	mcpProtocolVersion  = "2025-06-18"
	maxListPages        = 20
	scannerClientName   = "bifrost-bench-admitscan"
	// The second session names itself like a common agent, so a server that
	// shows the scanner one listing and agents another is caught.
	agentClientName    = "claude-code"
	agentClientVersion = "2.1.325"
)

// mcpRelayScript is the HTTP relay run in the sandbox's network namespace.
//
//go:embed mcprelay.py
var mcpRelayScript string

// LoadServiceSpecs reads MCP service specs from a file: one service spec
// (YAML or JSON, the shape of a task's or suite's `services:` entry), or a
// document with a `services:` list (task.yaml, suite.yaml), of which the
// services of role mcp are returned.
func LoadServiceSpecs(file string) ([]schemas.ServiceSpec, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read service config: %w", err)
	}
	var doc struct {
		Services []schemas.ServiceSpec `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse service config %s: %w", file, err)
	}
	if len(doc.Services) > 0 {
		var out []schemas.ServiceSpec
		for _, s := range doc.Services {
			if s.Role == schemas.ServiceRoleMCP || s.MCP != nil {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("service config %s lists no service of role mcp", file)
		}
		return out, nil
	}
	var one schemas.ServiceSpec
	if err := yaml.Unmarshal(b, &one); err != nil {
		return nil, fmt.Errorf("parse service spec %s: %w", file, err)
	}
	if one.ID == "" {
		return nil, fmt.Errorf("service config %s has neither an id nor a services list", file)
	}
	if one.Role != schemas.ServiceRoleMCP && one.MCP == nil {
		return nil, fmt.Errorf("service %s is not an mcp service (role %q)", one.ID, one.Role)
	}
	return []schemas.ServiceSpec{one}, nil
}

// ScanMCP starts one MCP service in the sandbox, lists what it offers,
// checks the listings for poisoning, shadowing and rug pulls, and returns
// the admission record. baseDir resolves the service's Files. The error is
// for a spec the scan cannot run or a server that never answers initialize.
func ScanMCP(ctx context.Context, spec schemas.ServiceSpec, baseDir string, opts Options) (*schemas.AdmitScanRecord, error) {
	if err := checkMCPSpec(spec); err != nil {
		return nil, err
	}
	planted, findings, err := mcpPlantedFiles(spec, baseDir)
	if err != nil {
		return nil, err
	}
	rec := &schemas.AdmitScanRecord{Kind: schemas.AdmitScanKindMCP, Subject: spec.ID, Source: spec.Image}
	dyn := &schemas.AdmitScanDynamic{Image: spec.Image, HelperImage: opts.helperImage()}
	sb, err := NewSandbox(ctx, SandboxConfig{Docker: opts.Docker, Logger: opts.logger(), Prefix: opts.prefix(), Image: spec.Image, HelperImage: opts.helperImage(), Files: planted})
	if err != nil {
		return nil, fmt.Errorf("start sandbox: %w", err)
	}
	defer func() {
		if cerr := sb.Close(context.WithoutCancel(ctx)); cerr != nil {
			opts.logger().Warn("remove admission sandbox", "error", cerr.Error())
		}
	}()
	started := time.Now()
	if err := sb.Start(ctx, spec.Argv, spec.Env, ""); err != nil {
		return nil, fmt.Errorf("start mcp server %s: %w", spec.ID, err)
	}
	dyn.Commands = []string{"server: " + strings.Join(spec.Argv, " ")}
	att, err := sb.Attach(ctx, mcpRelayScript)
	if err != nil {
		return nil, fmt.Errorf("start mcp relay: %w", err)
	}
	relay := newLineRelay(att.Stdin, att.Stdout)
	endpoint := mcpEndpoint(spec)
	res, err := probeServer(ctx, relay, spec, endpoint, opts)
	if cerr := att.Close(context.WithoutCancel(ctx)); cerr != nil {
		opts.logger().Warn("stop mcp relay", "error", cerr.Error())
	}
	if err != nil {
		logs, lerr := sb.Logs(context.WithoutCancel(ctx))
		if lerr == nil && len(logs) > 0 {
			err = fmt.Errorf("%w; server log: %s", err, clip(strings.TrimSpace(string(logs)), 600))
		}
		return nil, err
	}
	logs, err := sb.Logs(ctx)
	if err != nil {
		opts.logger().Warn("read mcp server log", "error", err.Error())
	}
	obs, err := sb.Observe(ctx)
	if err != nil {
		obs = Observation{Error: err.Error()}
	}
	output := append(append([]byte(nil), logs...), res.bodies...)
	out := RunOutput{Output: output, Duration: time.Since(started)}
	dyn, dynFindings, err := dynamicRecord(dyn, sb.Canaries(), out, obs)
	if err != nil {
		return nil, err
	}
	dyn.ExitCode = nil
	rec.Dynamic = dyn
	rec.MCP = res.record
	rec.MCP.Endpoint = endpoint
	findings = append(findings, res.findings...)
	findings = append(findings, filterSetupFindings(dynFindings)...)
	rec.Findings = dedupeFindings(findings)
	rec.ContentHash = mcpContentHash(spec, imageID(ctx, opts, spec.Image), res.first)
	finish(rec, opts)
	return rec, nil
}

// checkMCPSpec refuses a spec the scan cannot start.
func checkMCPSpec(spec schemas.ServiceSpec) error {
	switch {
	case spec.ID == "":
		return errors.New("mcp service has no id")
	case spec.Image == "" && spec.Build != nil:
		return fmt.Errorf("service %s builds its image from a Dockerfile; build it and set image to the tag", spec.ID)
	case spec.Image == "":
		return fmt.Errorf("service %s has no image", spec.ID)
	case spec.Port <= 0:
		return fmt.Errorf("service %s has no port", spec.ID)
	case spec.MCP != nil && spec.MCP.Transport != "" && spec.MCP.Transport != "http":
		return fmt.Errorf("service %s: transport %q is not supported; wrap a stdio server with `bifrost-bench toolproxy bridge` in its image", spec.ID, spec.MCP.Transport)
	}
	return nil
}

// mcpPlantedFiles reads the service's Files that land under the sandbox
// home; the rest cannot be placed on the read-only root and are reported.
func mcpPlantedFiles(spec schemas.ServiceSpec, baseDir string) ([]PlantedFile, []schemas.AdmitScanFinding, error) {
	var planted []PlantedFile
	var findings []schemas.AdmitScanFinding
	targets := make([]string, 0, len(spec.Files))
	for t := range spec.Files {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, target := range targets {
		rel := strings.TrimPrefix(path.Clean(target), SandboxHome+"/")
		if !strings.HasPrefix(path.Clean(target), SandboxHome+"/") || strings.HasPrefix(rel, "..") {
			findings = append(findings, schemas.AdmitScanFinding{Rule: RuleMCPFilesSkipped, Severity: schemas.AdmitScanSeverityWarn, File: target, Detail: "service file outside " + SandboxHome + " not placed: the sandbox root is read-only"})
			continue
		}
		b, err := os.ReadFile(filepath.Join(baseDir, spec.Files[target]))
		if err != nil {
			return nil, nil, fmt.Errorf("read service file %s: %w", spec.Files[target], err)
		}
		planted = append(planted, PlantedFile{Path: rel, Mode: 0o644, Data: b})
	}
	return planted, findings, nil
}

// mcpEndpoint is the server's URL inside the sandbox's network namespace.
func mcpEndpoint(spec schemas.ServiceSpec) string {
	p := "/mcp"
	if spec.MCP != nil && spec.MCP.Path != "" {
		p = "/" + strings.TrimPrefix(spec.MCP.Path, "/")
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", spec.Port, p)
}

// probeResult is what talking to the server produced.
type probeResult struct {
	record   *schemas.AdmitScanMCP
	first    MCPListing
	findings []schemas.AdmitScanFinding
	bodies   []byte // every answer, scanned for canary values
}

// probeServer runs the session sequence: initialize, list everything, call
// one benign tool, list tools again, wait, list again, then list from a
// second session that names itself like an agent.
func probeServer(ctx context.Context, relay roundTripper, spec schemas.ServiceSpec, endpoint string, opts Options) (*probeResult, error) {
	s1 := &mcpSession{rt: relay, url: endpoint}
	hello, err := s1.waitInitialize(ctx, readyTimeout(spec), scannerClientName, Version)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", spec.ID, err)
	}
	res := &probeResult{record: &schemas.AdmitScanMCP{ServerName: hello.ServerInfo.Name, ServerVersion: hello.ServerInfo.Version, ProtocolVersion: hello.ProtocolVersion}}
	first, listFindings := s1.listAll(ctx, spec.ID, hello)
	res.first = first
	res.findings = append(res.findings, listFindings...)
	res.findings = append(res.findings, ScanMCPListing(spec.ID, first, opts.textOptions(true))...)
	for _, t := range first.Tools {
		res.record.Tools = append(res.record.Tools, schemas.AdmitScanMCPTool{Name: t.Name, SHA256: ToolHash(t)})
	}
	res.record.Prompts = itemNames(first.Prompts)
	res.record.Resources = itemNames(first.Resources)
	res.record.Listings = 1
	if tool, args, ok := ChooseProbeTool(first.Tools); ok {
		res.record.ProbeCall = tool
		if _, err := s1.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args}); err != nil {
			res.findings = append(res.findings, schemas.AdmitScanFinding{Rule: RuleMCPListFailed, Severity: schemas.AdmitScanSeverityInfo, File: mcpWhere(spec.ID, "tools/"+tool), Detail: "probe call failed: " + err.Error()})
		}
		res.relist(ctx, s1, spec.ID, first, "after a tools/call of "+tool, opts)
	}
	if err := sleepCtx(ctx, opts.rugWait()); err != nil {
		return nil, err
	}
	res.relist(ctx, s1, spec.ID, first, fmt.Sprintf("after a wait of %s", opts.rugWait()), opts)
	s2 := &mcpSession{rt: relay, url: endpoint}
	if _, err := s2.initialize(ctx, agentClientName, agentClientVersion); err == nil {
		res.relist(ctx, s2, spec.ID, first, "in a second session whose client is named "+agentClientName, opts)
	} else {
		res.findings = append(res.findings, schemas.AdmitScanFinding{Rule: RuleMCPListFailed, Severity: schemas.AdmitScanSeverityWarn, File: mcpWhere(spec.ID, "initialize"), Detail: "second session failed: " + err.Error()})
	}
	res.record.ListChanged = s1.listChanged || s2.listChanged
	res.bodies = append(s1.bodies.Bytes(), s2.bodies.Bytes()...)
	return res, nil
}

// relist lists tools again, reports every change against the first listing
// as a rug pull, and scans the changed tools' new text too.
func (r *probeResult) relist(ctx context.Context, s *mcpSession, server string, first MCPListing, when string, opts Options) {
	tools, err := s.listTools(ctx)
	if err != nil {
		r.findings = append(r.findings, schemas.AdmitScanFinding{Rule: RuleMCPListFailed, Severity: schemas.AdmitScanSeverityWarn, File: mcpWhere(server, "tools/list"), Detail: "tools/list " + when + " failed: " + err.Error()})
		return
	}
	r.record.Listings++
	changes := DiffListings(first.Tools, tools)
	r.findings = append(r.findings, RugPullFindings(server, when, changes)...)
	changed := map[string]bool{}
	for _, c := range changes {
		changed[c.Tool] = true
	}
	var later MCPListing
	for _, t := range tools {
		if changed[t.Name] {
			later.Tools = append(later.Tools, t)
		}
	}
	if len(later.Tools) > 0 {
		r.findings = append(r.findings, ScanMCPListing(server, later, opts.textOptions(true))...)
	}
}

// probeNameRe picks a tool that by its name only reads.
var probeNameRe = regexp.MustCompile(`(?i)^(?:get|list|read|search|find|echo|ping|time|now|status|health|version|info|describe|hello|whoami|add|sum)`)

// ChooseProbeTool picks the tool the rug-pull check calls between listings:
// the first (by name) whose name reads as harmless, else the first tool, with
// arguments built from its input schema (each required property gets its
// first enum value or a placeholder of its type).
func ChooseProbeTool(tools []MCPTool) (string, map[string]any, bool) {
	if len(tools) == 0 {
		return "", nil, false
	}
	sorted := append([]MCPTool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	pick := sorted[0]
	for _, t := range sorted {
		if probeNameRe.MatchString(t.Name) {
			pick = t
			break
		}
	}
	return pick.Name, placeholderArgs(pick.InputSchema), true
}

// placeholderArgs fills the required properties of a JSON schema.
func placeholderArgs(schema json.RawMessage) map[string]any {
	args := map[string]any{}
	var s struct {
		Properties map[string]struct {
			Type any   `json:"type"`
			Enum []any `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return args
	}
	for _, name := range s.Required {
		p := s.Properties[name]
		if len(p.Enum) > 0 {
			args[name] = p.Enum[0]
			continue
		}
		typ, _ := p.Type.(string)
		if list, ok := p.Type.([]any); ok && len(list) > 0 {
			typ, _ = list[0].(string)
		}
		switch typ {
		case "integer", "number":
			args[name] = 1
		case "boolean":
			args[name] = false
		case "array":
			args[name] = []any{}
		case "object":
			args[name] = map[string]any{}
		default:
			args[name] = "test"
		}
	}
	return args
}

// filterSetupFindings drops the setup exit findings, which mean nothing for
// a long-running server.
func filterSetupFindings(fs []schemas.AdmitScanFinding) []schemas.AdmitScanFinding {
	out := fs[:0:0]
	for _, f := range fs {
		if f.Rule != RuleSetupFailed && f.Rule != RuleSetupTimeout {
			out = append(out, f)
		}
	}
	return out
}

// dedupeFindings keeps the first of identical findings (a tool scanned in
// the first listing and again after a change reports unchanged text twice).
func dedupeFindings(fs []schemas.AdmitScanFinding) []schemas.AdmitScanFinding {
	seen := map[string]bool{}
	out := make([]schemas.AdmitScanFinding, 0, len(fs))
	for _, f := range fs {
		k := fmt.Sprintf("%s|%s|%d|%d|%s|%s", f.Rule, f.File, f.Line, f.Column, f.Detail, f.Decoded)
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

// mcpContentHash pins a server admission: the service spec, the image id
// and everything the first session listed.
func mcpContentHash(spec schemas.ServiceSpec, image string, l MCPListing) string {
	h := sha256.New()
	if b, err := json.Marshal(spec); err == nil {
		h.Write(b)
	}
	fmt.Fprintf(h, "\nimage %s\ninstructions %s\n", image, l.Instructions)
	for _, t := range l.Tools {
		fmt.Fprintf(h, "tool %s %s\n", t.Name, ToolHash(t))
	}
	for _, p := range l.Prompts {
		fmt.Fprintf(h, "prompt %s\n", canonicalJSON(p))
	}
	for _, r := range l.Resources {
		fmt.Fprintf(h, "resource %s\n", canonicalJSON(r))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func canonicalJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// imageID is the local image id of ref, or "" when docker cannot say.
func imageID(ctx context.Context, opts Options, ref string) string {
	res, err := opts.Docker.Run(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

func itemNames(items []json.RawMessage) []string {
	var out []string
	for i, it := range items {
		out = append(out, itemName(it, i))
	}
	return out
}

func readyTimeout(spec schemas.ServiceSpec) time.Duration {
	if spec.Ready != nil && spec.Ready.TimeoutS > 0 {
		return time.Duration(spec.Ready.TimeoutS) * time.Second
	}
	return defaultReadyTimeout
}

func (o Options) rugWait() time.Duration {
	if o.RugWait > 0 {
		return o.RugWait
	}
	return DefaultRugWait
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait between listings: %w", ctx.Err())
	}
}

// relayRequest is one line to the relay.
type relayRequest struct {
	N       int               `json:"n"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
	Want    json.RawMessage   `json:"want"`
	Timeout int               `json:"timeout"`
}

// relayResponse is one line from the relay.
type relayResponse struct {
	N           int    `json:"n"`
	Ready       bool   `json:"ready,omitempty"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Session     string `json:"session"`
	Body        string `json:"body"`
	Error       string `json:"error"`
}

// roundTripper posts one MCP message and returns the HTTP answer.
type roundTripper interface {
	RoundTrip(ctx context.Context, req relayRequest) (relayResponse, error)
}

// lineRelay speaks the relay's line protocol over a process's stdio.
type lineRelay struct {
	mu   sync.Mutex
	in   io.Writer
	out  *bufio.Reader
	next int
}

func newLineRelay(in io.Writer, out io.Reader) *lineRelay {
	return &lineRelay{in: in, out: bufio.NewReaderSize(out, 1<<20)}
}

// RoundTrip writes one request line and reads lines until the answer with
// the same number (the relay's ready line is skipped).
func (l *lineRelay) RoundTrip(ctx context.Context, req relayRequest) (relayResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	req.N = l.next
	b, err := json.Marshal(req)
	if err != nil {
		return relayResponse{}, fmt.Errorf("encode relay request: %w", err)
	}
	if _, err := l.in.Write(append(b, '\n')); err != nil {
		return relayResponse{}, fmt.Errorf("write relay request: %w", err)
	}
	type lineOrErr struct {
		r   relayResponse
		err error
	}
	ch := make(chan lineOrErr, 1)
	go func() {
		for {
			line, err := l.out.ReadBytes('\n')
			if err != nil {
				ch <- lineOrErr{err: fmt.Errorf("read relay answer: %w", err)}
				return
			}
			var r relayResponse
			if json.Unmarshal(line, &r) != nil || r.Ready || r.N != req.N {
				continue
			}
			ch <- lineOrErr{r: r}
			return
		}
	}()
	select {
	case v := <-ch:
		return v.r, v.err
	case <-ctx.Done():
		return relayResponse{}, fmt.Errorf("relay answer: %w", ctx.Err())
	}
}

// mcpSession is one MCP client session over the relay.
type mcpSession struct {
	rt          roundTripper
	url         string
	session     string
	protocol    string
	nextID      int
	listChanged bool
	bodies      bytes.Buffer
}

// initResult is the part of an initialize result the scan reads.
type initResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
	Instructions string                     `json:"instructions"`
}

// waitInitialize retries initialize until the server answers or timeout.
func (s *mcpSession) waitInitialize(ctx context.Context, timeout time.Duration, client, version string) (initResult, error) {
	deadline := time.Now().Add(timeout)
	for {
		res, err := s.initialize(ctx, client, version)
		if err == nil {
			return res, nil
		}
		if time.Now().After(deadline) || errors.Is(err, errRPC) {
			return initResult{}, fmt.Errorf("initialize: %w", err)
		}
		if err := sleepCtx(ctx, time.Second); err != nil {
			return initResult{}, err
		}
	}
}

// initialize opens the session and sends notifications/initialized.
func (s *mcpSession) initialize(ctx context.Context, client, version string) (initResult, error) {
	s.session = ""
	raw, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": client, "version": version},
	})
	if err != nil {
		return initResult{}, err
	}
	var res initResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return initResult{}, fmt.Errorf("decode initialize result: %w", err)
	}
	s.protocol = res.ProtocolVersion
	if err := s.notify(ctx, "notifications/initialized"); err != nil {
		return initResult{}, err
	}
	return res, nil
}

// listAll lists tools, prompts and resources (the latter two only when the
// server advertises them). A failed listing is a finding, not an error.
func (s *mcpSession) listAll(ctx context.Context, server string, hello initResult) (MCPListing, []schemas.AdmitScanFinding) {
	l := MCPListing{Instructions: hello.Instructions}
	var findings []schemas.AdmitScanFinding
	fail := func(method string, err error) {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleMCPListFailed, Severity: schemas.AdmitScanSeverityWarn, File: mcpWhere(server, method), Detail: method + " failed: " + err.Error()})
	}
	tools, err := s.listTools(ctx)
	if err != nil {
		fail("tools/list", err)
	}
	l.Tools = tools
	if _, ok := hello.Capabilities["prompts"]; ok {
		if l.Prompts, err = s.listItems(ctx, "prompts/list", "prompts"); err != nil {
			fail("prompts/list", err)
		}
	}
	if _, ok := hello.Capabilities["resources"]; ok {
		if l.Resources, err = s.listItems(ctx, "resources/list", "resources"); err != nil {
			fail("resources/list", err)
		}
		templates, err := s.listItems(ctx, "resources/templates/list", "resourceTemplates")
		if err != nil {
			fail("resources/templates/list", err)
		}
		l.Resources = append(l.Resources, templates...)
	}
	return l, findings
}

// listTools follows tools/list cursors up to maxListPages pages.
func (s *mcpSession) listTools(ctx context.Context) ([]MCPTool, error) {
	var all []MCPTool
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		tools, next, err := ParseToolsList(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, tools...)
		if next == "" {
			return all, nil
		}
		cursor = next
	}
	return all, nil
}

// listItems follows the cursors of a prompts or resources listing.
func (s *mcpSession) listItems(ctx context.Context, method, key string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.call(ctx, method, params)
		if err != nil {
			return nil, err
		}
		items, next, err := ParseList(raw, key)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			return all, nil
		}
		cursor = next
	}
	return all, nil
}

// call sends one request and returns its result. Notifications that come
// with the answer are noted (tools/list_changed sets listChanged).
func (s *mcpSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", s.nextID))
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", method, err)
	}
	resp, err := s.post(ctx, body, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	msgs, err := ParseRPCMessages([]byte(resp.Body), resp.ContentType)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	for _, m := range msgs {
		if m.Method == "notifications/tools/list_changed" {
			s.listChanged = true
		}
		if m.Method == "" && bytes.Equal(bytes.TrimSpace(m.ID), id) {
			if m.Error != nil {
				return nil, rpcErr(method, m.Error)
			}
			return m.Result, nil
		}
	}
	return nil, fmt.Errorf("%s: no answer (http %d)", method, resp.Status)
}

// notify sends a notification; the server answers 202 with no body.
func (s *mcpSession) notify(ctx context.Context, method string) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	if _, err := s.post(ctx, body, nil); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

// post sends one body with the session headers and keeps the session id.
func (s *mcpSession) post(ctx context.Context, body []byte, want json.RawMessage) (relayResponse, error) {
	headers := map[string]string{}
	if s.session != "" {
		headers["Mcp-Session-Id"] = s.session
	}
	if s.protocol != "" {
		headers["MCP-Protocol-Version"] = s.protocol
	}
	if want == nil {
		want = json.RawMessage("null")
	}
	resp, err := s.rt.RoundTrip(ctx, relayRequest{URL: s.url, Headers: headers, Body: string(body), Want: want, Timeout: 30})
	if err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	if resp.Status >= 400 {
		return resp, fmt.Errorf("http %d: %s", resp.Status, clip(strings.TrimSpace(resp.Body), 200))
	}
	if resp.Session != "" {
		s.session = resp.Session
	}
	s.bodies.WriteString(resp.Body)
	s.bodies.WriteByte('\n')
	return resp, nil
}
