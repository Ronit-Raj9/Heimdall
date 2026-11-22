package handlers

// Agent ops area (web parity lane L09): an agent's skills (zip upload,
// scan, list, delete) and its MCP tools (the agent's
// mcp delivery block plus agents/<id>/mcp.yaml, the MCP servers the admission
// scan checks). Files go through projectfs, so every path is confined to the
// org's project and every write is validated and versioned there.
//
// Skill install is gated by a scan (fix wave 2, N22): an upload lands in
// agents/<id>/skills-pending/<name>/, is scanned in process (static, no
// container), and moves to agents/<id>/skills/<name>/ (the folder skills.extra
// lists) only when the scan admits it. The outcome is kept in
// agents/<id>/skill-scans/<name>.json; adapters.ResolveSkills refuses an
// uploaded skill without a passing scan of its current files.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/admitscan"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// AgentSkillsFile is the skill file every skill folder holds at its top.
const AgentSkillsFile = "SKILL.md"

// agentOpsNamePattern is one path segment: an agent id or a skill name.
var agentOpsNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// mcpFormats are the agent mcp.format values the adapters render.
var mcpFormats = map[string]bool{"": true, "codex_toml": true, "claude_json": true, "opencode_json": true, "generic_json": true, "env": true}

// Skill statuses (AgentSkill.Status).
const (
	SkillInstalled  = "installed"   // in skills/ with a passing scan of its current files
	SkillBlocked    = "blocked"     // the scan blocked it; kept in skills-pending/, never installed
	SkillNotScanned = "not_scanned" // no scan of its current files; runs refuse it until it passes one
)

// maxSkillFindings caps the findings a skill's scan summary lists.
const maxSkillFindings = 20

// AgentSkill is one skill folder of an agent.
type AgentSkill struct {
	Name   string            `json:"name"`
	Path   string            `json:"path"` // relative to the project, e.g. agents/codex/skills/review
	Files  int               `json:"files"`
	Bytes  int64             `json:"bytes"`
	Valid  bool              `json:"valid"`            // holds SKILL.md at its top
	Status string            `json:"status,omitempty"` // installed | blocked | not_scanned
	Scan   *SkillScanSummary `json:"scan,omitempty"`   // the last scan of this skill, when there is one
}

// SkillScanSummary is the last scan of one skill: its decision, counts and
// the findings that block or warn (at most 20).
type SkillScanSummary struct {
	Decision  string                     `json:"decision"` // admitted | blocked
	Block     int                        `json:"block"`
	Warn      int                        `json:"warn"`
	ScannedAt time.Time                  `json:"scanned_at"`
	Findings  []schemas.AdmitScanFinding `json:"findings"`
}

// SkillScanResult answers a skill upload and POST .../skills/{name}/scan:
// the skill as listed after the scan, whether it is installed, and the scan
// record.
type SkillScanResult struct {
	Skill     AgentSkill               `json:"skill"`
	Installed bool                     `json:"installed"`
	Record    *schemas.AdmitScanRecord `json:"record"`
}

// AgentSkillsResponse is GET /agents/{id}/skills.
type AgentSkillsResponse struct {
	Agent      string       `json:"agent"`
	ConfigPath string       `json:"config_path"`
	Dir        string       `json:"dir"`       // agents/<id>/skills
	Installed  bool         `json:"installed"` // the config's skills.extra lists Dir
	Skills     []AgentSkill `json:"skills"`
}

// AgentMCPResponse is GET and PUT /agents/{id}/mcp.
type AgentMCPResponse struct {
	Agent       string                `json:"agent"`
	ConfigPath  string                `json:"config_path"`
	Format      string                `json:"format"`
	MountPath   string                `json:"mount_path"`   // the agent's mcp.config_path
	ServersFile string                `json:"servers_file"` // agents/<id>/mcp.yaml
	ServersYAML string                `json:"servers_yaml"`
	Servers     []schemas.ServiceSpec `json:"servers"`
}

// AgentMCPRequest is the body of PUT /agents/{id}/mcp.
type AgentMCPRequest struct {
	Format      string `json:"format"`
	MountPath   string `json:"mount_path"`
	ServersYAML string `json:"servers_yaml"`
}

type agentOpsHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("agentops", registerAgentOps)
}

// registerAgentOps registers the agent ops routes.
func registerAgentOps(env OrgEnv, r *router.Router) {
	{
		h := &agentOpsHandler{env: env}
		r.GET(APIPrefix+"/agents/{id}/skills", env.R(h.listSkills))
		r.POST(APIPrefix+"/agents/{id}/skills", env.U(h.uploadSkill))
		r.DELETE(APIPrefix+"/agents/{id}/skills/{name}", env.W(h.deleteSkill))
		r.POST(APIPrefix+"/agents/{id}/skills/{name}/scan", env.W(h.scanSkill))
		r.GET(APIPrefix+"/agents/{id}/mcp", env.R(h.getMCP))
		r.PUT(APIPrefix+"/agents/{id}/mcp", env.W(h.putMCP))
	}
}

func (h *agentOpsHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

// agentRef is one resolved agent: its id and config path (relative to the
// agents area and to the project).
type agentRef struct {
	ID      string
	Rel     string // inside the agents area, e.g. codex.yaml
	Project string // e.g. agents/codex.yaml
}

// agent resolves the {id} path parameter to an agent config, answering the
// error itself when it cannot.
func (h *agentOpsHandler) agent(ctx *fasthttp.RequestCtx) (agentRef, projectfs.Store, bool) {
	ps := h.env.Services.Projects
	if ps == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "project files are not available on this server: start it with a project folder (bifrost-bench serve) or ask your server owner")
		return agentRef{}, nil, false
	}
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return agentRef{}, nil, false
	}
	ref, err := findAgent(ctx, h.env.Services.Agents, ps, id)
	if err != nil {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return agentRef{}, nil, false
	}
	return ref, ps, true
}

// findAgent finds the config of agent id: through the agent source when the
// server has one (the id inside the file wins), else agents/<id>.yaml.
func findAgent(ctx context.Context, src AgentSource, ps projectfs.Store, id string) (agentRef, error) {
	if src != nil {
		files, err := src.AgentConfigs(ctx)
		if err == nil {
			for _, f := range files {
				if f.Config != nil && f.Config.ID == id && strings.HasPrefix(f.Path, "agents/") {
					return agentRef{ID: id, Rel: strings.TrimPrefix(f.Path, "agents/"), Project: f.Path}, nil
				}
			}
		}
	}
	for _, ext := range []string{".yaml", ".yml"} {
		if _, _, err := ps.Read(ctx, projectfs.AreaAgents, id+ext); err == nil {
			return agentRef{ID: id, Rel: id + ext, Project: "agents/" + id + ext}, nil
		}
	}
	return agentRef{}, fmt.Errorf("agent %q not found: create it first (Agents, New agent) or check agents/%s.yaml", id, id)
}

func actor(ctx *fasthttp.RequestCtx) string {
	if p := auth.PrincipalFrom(ctx); p != nil {
		if a := p.ActorID(); a != "" {
			return a
		}
	}
	return "web"
}

// fsError maps a projectfs error to a status and message.
func (h *agentOpsHandler) fsError(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, projectfs.ErrNotFound):
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": not found")
	case errors.Is(err, projectfs.ErrOutside):
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": path leaves the project folder")
	case errors.Is(err, projectfs.ErrTooLarge):
		h.fail(ctx, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, what+": too large ("+err.Error()+")")
	case errors.Is(err, projectfs.ErrInvalid):
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, projectfs.ErrConflict):
		h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": changed on the server, reload and try again")
	case errors.Is(err, projectfs.ErrNotImplemented):
		h.fail(ctx, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable, what+": this server cannot unpack zips yet, upload the skill files one by one under Files")
	default:
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}

// areaRel is an entry path relative to the agents area (entries may name the
// area or not).
func areaRel(p string) string {
	p = path.Clean(p)
	if strings.HasPrefix(p, "agents/") && !strings.HasPrefix(p, "agents/skills/") {
		return strings.TrimPrefix(p, "agents/")
	}
	return p
}

func skillsDir(id string) string { return id + "/" + adapters.SkillUploadDir }

func pendingSkillsDir(id string) string { return id + "/" + adapters.SkillPendingDir }

func skillScanRel(id, name string) string {
	return id + "/" + adapters.SkillScansDir + "/" + name + ".json"
}

// listSkillFolders lists the skill folders under dir (relative to the agents
// area).
func listSkillFolders(ctx context.Context, ps projectfs.Store, dir string) (map[string]*AgentSkill, error) {
	byName := map[string]*AgentSkill{}
	entries, err := ps.List(ctx, projectfs.AreaAgents, dir, true)
	if errors.Is(err, projectfs.ErrNotFound) {
		return byName, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := dir + "/"
	for _, e := range entries {
		rel := areaRel(e.Path)
		if !strings.HasPrefix(rel, prefix) || rel == prefix {
			continue
		}
		p := strings.TrimPrefix(rel, prefix)
		name, rest, _ := strings.Cut(p, "/")
		s := byName[name]
		if s == nil {
			s = &AgentSkill{Name: name, Path: "agents/" + prefix + name}
			byName[name] = s
		}
		if e.Dir {
			continue
		}
		if rest == "" { // a loose file beside the skill folders
			delete(byName, name)
			continue
		}
		s.Files++
		s.Bytes += e.Size
		if rest == AgentSkillsFile {
			s.Valid = true
		}
	}
	return byName, nil
}

// readSkillScan reads agents/<id>/skill-scans/<name>.json, nil when there is
// none or it cannot be read.
func readSkillScan(ctx context.Context, ps projectfs.Store, id, name string) *adapters.SkillScanFile {
	data, _, err := ps.Read(ctx, projectfs.AreaAgents, skillScanRel(id, name))
	if err != nil {
		return nil
	}
	var f adapters.SkillScanFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return &f
}

// skillTreeSHA256 hashes a skill folder (relative to the agents area).
func skillTreeSHA256(ps projectfs.Store, dir string) (string, error) {
	abs, err := ps.Resolve(projectfs.AreaAgents, dir)
	if err != nil {
		return "", err
	}
	return adapters.SkillTreeSHA256(abs)
}

// scanSummary is the list view of a scan file.
func scanSummary(f *adapters.SkillScanFile) *SkillScanSummary {
	if f == nil {
		return nil
	}
	out := &SkillScanSummary{Decision: f.Decision, ScannedAt: f.ScannedAt, Findings: []schemas.AdmitScanFinding{}}
	if f.Record != nil {
		out.Block, out.Warn = f.Record.Counts.Block, f.Record.Counts.Warn
		for _, fd := range f.Record.Findings {
			if fd.Severity != schemas.AdmitScanSeverityBlock && fd.Severity != schemas.AdmitScanSeverityWarn {
				continue
			}
			if len(out.Findings) < maxSkillFindings {
				out.Findings = append(out.Findings, fd)
			}
		}
	}
	return out
}

// listAgentSkills lists an agent's skill folders: installed ones under
// agents/<id>/skills and blocked uploads under agents/<id>/skills-pending,
// each with its status read from its scan file (never scanned here: a read
// writes nothing). A name in both lists once, as the installed folder.
func listAgentSkills(ctx context.Context, ps projectfs.Store, id string) ([]AgentSkill, error) {
	installed, err := listSkillFolders(ctx, ps, skillsDir(id))
	if err != nil {
		return nil, err
	}
	pending, err := listSkillFolders(ctx, ps, pendingSkillsDir(id))
	if err != nil {
		return nil, err
	}
	out := make([]AgentSkill, 0, len(installed)+len(pending))
	for name, s := range installed {
		scan := readSkillScan(ctx, ps, id, name)
		s.Scan = scanSummary(scan)
		s.Status = SkillNotScanned
		if scan != nil {
			if sum, err := skillTreeSHA256(ps, skillsDir(id)+"/"+name); err == nil && sum == scan.TreeSHA256 {
				s.Status = SkillBlocked
				if scan.Decision == schemas.AdmitScanAdmitted {
					s.Status = SkillInstalled
				}
			}
		}
		out = append(out, *s)
	}
	for name, s := range pending {
		if installed[name] != nil {
			continue
		}
		scan := readSkillScan(ctx, ps, id, name)
		s.Scan = scanSummary(scan)
		s.Status = SkillBlocked
		if scan == nil {
			s.Status = SkillNotScanned
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (h *agentOpsHandler) listSkills(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	skills, err := listAgentSkills(ctx, ps, ref.ID)
	if err != nil {
		h.fsError(ctx, "list skills", err)
		return
	}
	installed := false
	if data, _, err := ps.Read(ctx, projectfs.AreaAgents, ref.Rel); err == nil {
		var cfg struct {
			Skills *schemas.AgentSkills `yaml:"skills"`
		}
		if yaml.Unmarshal(data, &cfg) == nil && cfg.Skills != nil {
			for _, e := range cfg.Skills.Extra {
				if path.Clean(e) == "agents/"+skillsDir(ref.ID) {
					installed = true
				}
			}
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, AgentSkillsResponse{Agent: ref.ID, ConfigPath: ref.Project,
		Dir: "agents/" + skillsDir(ref.ID), Installed: installed, Skills: skills})
}

// uploadSkill unpacks a zip (the request body, application/zip) into
// agents/<id>/skills-pending/<name>/, scans it, and installs it (moves it to
// agents/<id>/skills/<name>/ and lists that folder in the agent's
// skills.extra) only when the scan admits it. A blocked skill stays in
// skills-pending/ and is never installed. ?name= names the skill.
func (h *agentOpsHandler) uploadSkill(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	name := string(ctx.QueryArgs().Peek("name"))
	if !agentOpsNamePattern.MatchString(name) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("skill name %q: want letters, digits, '.', '-' or '_' (for example code-review)", name))
		return
	}
	body := ctx.PostBody()
	if len(body) == 0 {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "send the skill as a .zip file in the request body")
		return
	}
	if int64(len(body)) > projectfs.MaxZipBytes {
		h.fail(ctx, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, fmt.Sprintf("skill zip is larger than %d MiB", projectfs.MaxZipBytes>>20))
		return
	}
	if !bytes.HasPrefix(body, []byte("PK\x03\x04")) && !bytes.HasPrefix(body, []byte("PK\x05\x06")) {
		h.fail(ctx, fasthttp.StatusUnsupportedMediaType, lib.ErrTypeUnsupportedMedia, "the upload is not a .zip file: zip the skill folder (it must hold SKILL.md) and upload that")
		return
	}
	replace := ctx.QueryArgs().GetBool("replace")
	existing, _ := listAgentSkills(ctx, ps, ref.ID)
	for _, s := range existing {
		if s.Name == name && !replace {
			h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, fmt.Sprintf("skill %q already exists: delete it first or upload with replace", name))
			return
		}
	}
	o := projectfs.WriteOpts{Actor: actor(ctx), Note: "upload skill " + name}
	if replace {
		if _, err := removeSkill(ctx, ps, ref.ID, name, o); err != nil {
			h.fsError(ctx, "replace skill", err)
			return
		}
	}
	dir := pendingSkillsDir(ref.ID) + "/" + name
	if _, err := ps.ExtractZip(ctx, projectfs.AreaAgents, dir, bytes.NewReader(body), int64(len(body)), o); err != nil {
		h.fsError(ctx, "unpack skill zip", err)
		return
	}
	if err := hoistSingleFolder(ctx, ps, dir, o); err != nil {
		h.fsError(ctx, "unpack skill zip", err)
		return
	}
	if _, _, err := ps.Read(ctx, projectfs.AreaAgents, dir+"/"+AgentSkillsFile); err != nil {
		_ = ps.Delete(ctx, projectfs.AreaAgents, dir, o)
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the zip holds no SKILL.md at its top (or inside one top folder): a skill is a folder with SKILL.md, nothing was kept")
		return
	}
	res, ok := h.scanAndApply(ctx, ps, ref, name, false, o)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, res)
}

// scanSkill scans one skill where it is (installed or pending) and applies
// the decision: a blocked installed skill moves back to skills-pending/ (it
// is uninstalled), an admitted pending skill is installed.
func (h *agentOpsHandler) scanSkill(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	name, _ := ctx.UserValue("name").(string)
	if !agentOpsNamePattern.MatchString(name) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("skill name %q is not valid", name))
		return
	}
	installed := skillFolderExists(ctx, ps, skillsDir(ref.ID)+"/"+name)
	if !installed && !skillFolderExists(ctx, ps, pendingSkillsDir(ref.ID)+"/"+name) {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no skill named "+name)
		return
	}
	res, ok := h.scanAndApply(ctx, ps, ref, name, installed, projectfs.WriteOpts{Actor: actor(ctx), Note: "scan skill " + name})
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, res)
}

func skillFolderExists(ctx context.Context, ps projectfs.Store, dir string) bool {
	_, err := ps.List(ctx, projectfs.AreaAgents, dir, false)
	return err == nil
}

// scanAndApply scans skill name of ref in skills/ (installed) or
// skills-pending/, saves the scan file, installs an admitted skill and
// uninstalls a blocked one. It answers the error itself when it fails.
func (h *agentOpsHandler) scanAndApply(ctx *fasthttp.RequestCtx, ps projectfs.Store, ref agentRef, name string, installed bool, o projectfs.WriteOpts) (SkillScanResult, bool) {
	dir := pendingSkillsDir(ref.ID) + "/" + name
	if installed {
		dir = skillsDir(ref.ID) + "/" + name
	}
	abs, err := ps.Resolve(projectfs.AreaAgents, dir)
	if err != nil {
		h.fsError(ctx, "scan skill "+name, err)
		return SkillScanResult{}, false
	}
	sum, err := adapters.SkillTreeSHA256(abs)
	var rec *schemas.AdmitScanRecord
	if err == nil {
		rec, err = admitscan.ScanSkill(ctx, abs, admitscan.DefaultOptions())
	}
	if err != nil {
		h.fail(ctx, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			fmt.Sprintf("the scan could not read skill %s: it may hold a link or a special file. Upload it again as a plain folder.", name))
		return SkillScanResult{}, false
	}
	rec.Source = "agents/" + dir // a project path, never the server's folder
	decision := schemas.AdmitScanBlocked
	if rec.Decision == schemas.AdmitScanAdmitted {
		decision = schemas.AdmitScanAdmitted
	}
	data, err := json.MarshalIndent(adapters.SkillScanFile{Decision: decision, TreeSHA256: sum, ScannedAt: time.Now().UTC(), Record: rec}, "", "  ")
	if err == nil {
		_, err = ps.Write(ctx, projectfs.AreaAgents, skillScanRel(ref.ID, name), append(data, '\n'), o)
	}
	if err != nil {
		h.fsError(ctx, "save the scan of skill "+name, err)
		return SkillScanResult{}, false
	}
	switch {
	case decision == schemas.AdmitScanAdmitted && !installed:
		err = moveSkillFolder(ctx, ps, dir, skillsDir(ref.ID)+"/"+name, o)
	case decision != schemas.AdmitScanAdmitted && installed:
		err = moveSkillFolder(ctx, ps, dir, pendingSkillsDir(ref.ID)+"/"+name, o)
	}
	if err != nil {
		h.fsError(ctx, "move skill "+name, err)
		return SkillScanResult{}, false
	}
	if decision == schemas.AdmitScanAdmitted {
		if err := h.ensureSkillsExtra(ctx, ps, ref, o); err != nil {
			h.fsError(ctx, "add the skill to "+ref.Project, err)
			return SkillScanResult{}, false
		}
	}
	res := SkillScanResult{Installed: decision == schemas.AdmitScanAdmitted, Record: rec}
	skills, err := listAgentSkills(ctx, ps, ref.ID)
	if err != nil {
		h.fsError(ctx, "list skills", err)
		return SkillScanResult{}, false
	}
	for _, s := range skills {
		if s.Name == name {
			res.Skill = s
		}
	}
	return res, true
}

// moveSkillFolder moves a skill folder (relative to the agents area): every
// file is read and written under to, then from is deleted. A folder already
// at to is replaced.
func moveSkillFolder(ctx context.Context, ps projectfs.Store, from, to string, o projectfs.WriteOpts) error {
	files, err := ps.List(ctx, projectfs.AreaAgents, from, true)
	if err != nil {
		return err
	}
	if err := ps.Delete(ctx, projectfs.AreaAgents, to, o); err != nil && !errors.Is(err, projectfs.ErrNotFound) {
		return err
	}
	for _, f := range files {
		if f.Dir {
			continue
		}
		rel := strings.TrimPrefix(areaRel(f.Path), from+"/")
		data, _, err := ps.Read(ctx, projectfs.AreaAgents, from+"/"+rel)
		if err != nil {
			return err
		}
		if _, err := ps.Write(ctx, projectfs.AreaAgents, to+"/"+rel, data, o); err != nil {
			return err
		}
	}
	return ps.Delete(ctx, projectfs.AreaAgents, from, o)
}

// removeSkill deletes skill name of agent id wherever it is (skills/ or
// skills-pending/) and its scan file. found is false when there was nothing
// to delete.
func removeSkill(ctx context.Context, ps projectfs.Store, id, name string, o projectfs.WriteOpts) (found bool, err error) {
	for _, rel := range []string{skillsDir(id) + "/" + name, pendingSkillsDir(id) + "/" + name, skillScanRel(id, name)} {
		err := ps.Delete(ctx, projectfs.AreaAgents, rel, o)
		switch {
		case err == nil:
			found = found || !strings.HasSuffix(rel, ".json")
		case !errors.Is(err, projectfs.ErrNotFound):
			return found, err
		}
	}
	return found, nil
}

// hoistSingleFolder moves the files of a zip that held one top folder
// (review/SKILL.md) up into dir, the common shape of a zipped folder.
func hoistSingleFolder(ctx context.Context, ps projectfs.Store, dir string, o projectfs.WriteOpts) error {
	if _, _, err := ps.Read(ctx, projectfs.AreaAgents, dir+"/"+AgentSkillsFile); err == nil {
		return nil
	}
	top, err := ps.List(ctx, projectfs.AreaAgents, dir, false)
	if err != nil || len(top) != 1 || !top[0].Dir {
		return nil
	}
	sub := dir + "/" + path.Base(top[0].Path)
	files, err := ps.List(ctx, projectfs.AreaAgents, sub, true)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Dir {
			continue
		}
		rel := strings.TrimPrefix(areaRel(f.Path), sub+"/")
		data, _, err := ps.Read(ctx, projectfs.AreaAgents, sub+"/"+rel)
		if err != nil {
			return err
		}
		if _, err := ps.Write(ctx, projectfs.AreaAgents, dir+"/"+rel, data, o); err != nil {
			return err
		}
	}
	return ps.Delete(ctx, projectfs.AreaAgents, sub, o)
}

// ensureSkillsExtra adds agents/<id>/skills to the config's skills.extra,
// keeping the rest of the YAML (and its comments) as it was.
func (h *agentOpsHandler) ensureSkillsExtra(ctx context.Context, ps projectfs.Store, ref agentRef, o projectfs.WriteOpts) error {
	data, ent, err := ps.Read(ctx, projectfs.AreaAgents, ref.Rel)
	if err != nil {
		return err
	}
	want := "agents/" + skillsDir(ref.ID)
	out, changed, err := agentOpsEditYAML(data, func(root *yaml.Node) bool {
		skills := mappingChild(root, "skills", true)
		extra := mappingChild(skills, "extra", false)
		if extra == nil {
			setMapping(skills, "extra", &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{scalar(want)}})
			return true
		}
		if extra.Kind != yaml.SequenceNode {
			extra.Kind, extra.Tag, extra.Value, extra.Content = yaml.SequenceNode, "", "", nil
		}
		for _, n := range extra.Content {
			if path.Clean(n.Value) == want {
				return false
			}
		}
		extra.Content = append(extra.Content, scalar(want))
		return true
	})
	if err != nil || !changed {
		return err
	}
	o.IfMatch = ent.SHA256
	_, err = ps.Write(ctx, projectfs.AreaAgents, ref.Rel, out, o)
	return err
}

func (h *agentOpsHandler) deleteSkill(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	name, _ := ctx.UserValue("name").(string)
	if !agentOpsNamePattern.MatchString(name) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("skill name %q is not valid", name))
		return
	}
	found, err := removeSkill(ctx, ps, ref.ID, name, projectfs.WriteOpts{Actor: actor(ctx), Note: "delete skill " + name})
	if err == nil && !found {
		err = projectfs.ErrNotFound
	}
	if err != nil {
		h.fsError(ctx, "delete skill "+name, err)
		return
	}
	h.listSkills(ctx)
}

func mcpServersRel(id string) string { return id + "/mcp.yaml" }

// parseMCPServers reads the servers file: a services: list of MCP services.
func parseMCPServers(data []byte) ([]schemas.ServiceSpec, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []schemas.ServiceSpec{}, nil
	}
	var doc struct {
		Services []schemas.ServiceSpec `yaml:"services"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("servers YAML: %v", err)
	}
	seen := map[string]bool{}
	for i, s := range doc.Services {
		if !agentOpsNamePattern.MatchString(s.ID) {
			return nil, fmt.Errorf("services[%d]: id %q: want letters, digits, '.', '-' or '_'", i, s.ID)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("services[%d]: id %q is listed twice", i, s.ID)
		}
		seen[s.ID] = true
		if s.Role == "" {
			doc.Services[i].Role = schemas.ServiceRoleMCP
		} else if s.Role != schemas.ServiceRoleMCP {
			return nil, fmt.Errorf("services[%d] (%s): role must be mcp", i, s.ID)
		}
		if s.Image == "" && s.Build == nil {
			return nil, fmt.Errorf("services[%d] (%s): set image (pin it with @sha256:) or build", i, s.ID)
		}
	}
	if doc.Services == nil {
		doc.Services = []schemas.ServiceSpec{}
	}
	return doc.Services, nil
}

func (h *agentOpsHandler) mcpResponse(ctx context.Context, ps projectfs.Store, ref agentRef) (AgentMCPResponse, error) {
	resp := AgentMCPResponse{Agent: ref.ID, ConfigPath: ref.Project, ServersFile: "agents/" + mcpServersRel(ref.ID), Servers: []schemas.ServiceSpec{}}
	data, _, err := ps.Read(ctx, projectfs.AreaAgents, ref.Rel)
	if err != nil {
		return resp, err
	}
	var cfg struct {
		MCP *schemas.AgentMCP `yaml:"mcp"`
	}
	if yaml.Unmarshal(data, &cfg) == nil && cfg.MCP != nil {
		resp.Format, resp.MountPath = cfg.MCP.Format, cfg.MCP.ConfigPath
	}
	servers, _, err := ps.Read(ctx, projectfs.AreaAgents, mcpServersRel(ref.ID))
	if err == nil {
		resp.ServersYAML = string(servers)
		if specs, perr := parseMCPServers(servers); perr == nil {
			resp.Servers = specs
		}
	} else if !errors.Is(err, projectfs.ErrNotFound) {
		return resp, err
	}
	return resp, nil
}

func (h *agentOpsHandler) getMCP(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	resp, err := h.mcpResponse(ctx, ps, ref)
	if err != nil {
		h.fsError(ctx, "read MCP settings", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, resp)
}

func (h *agentOpsHandler) putMCP(ctx *fasthttp.RequestCtx) {
	ref, ps, ok := h.agent(ctx)
	if !ok {
		return
	}
	var req AgentMCPRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body: want JSON {format, mount_path, servers_yaml}")
		return
	}
	if !mcpFormats[req.Format] {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("format %q: want codex_toml, claude_json, opencode_json, generic_json, env or empty (the adapter's own)", req.Format))
		return
	}
	if req.MountPath != "" && (!strings.HasPrefix(req.MountPath, "/") || strings.Contains(req.MountPath, "..") || len(req.MountPath) > 256) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "mount_path must be an absolute container path such as /root/.codex/config.toml")
		return
	}
	if _, err := parseMCPServers([]byte(req.ServersYAML)); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	o := projectfs.WriteOpts{Actor: actor(ctx), Note: "edit MCP tools"}
	data, ent, err := ps.Read(ctx, projectfs.AreaAgents, ref.Rel)
	if err != nil {
		h.fsError(ctx, "read "+ref.Project, err)
		return
	}
	out, changed, err := agentOpsEditYAML(data, func(root *yaml.Node) bool {
		cur := mappingChild(root, "mcp", false)
		if req.Format == "" && req.MountPath == "" {
			if cur == nil {
				return false
			}
			removeMapping(root, "mcp")
			return true
		}
		m := &yaml.Node{Kind: yaml.MappingNode}
		if req.Format != "" {
			setMapping(m, "format", scalar(req.Format))
		}
		if req.MountPath != "" {
			setMapping(m, "config_path", scalar(req.MountPath))
		}
		setMapping(root, "mcp", m)
		return true
	})
	if err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, ref.Project+": "+err.Error())
		return
	}
	if changed {
		co := o
		co.IfMatch = ent.SHA256
		if _, err := ps.Write(ctx, projectfs.AreaAgents, ref.Rel, out, co); err != nil {
			h.fsError(ctx, "save "+ref.Project, err)
			return
		}
	}
	if strings.TrimSpace(req.ServersYAML) == "" {
		if err := ps.Delete(ctx, projectfs.AreaAgents, mcpServersRel(ref.ID), o); err != nil && !errors.Is(err, projectfs.ErrNotFound) {
			h.fsError(ctx, "remove the MCP servers file", err)
			return
		}
	} else if _, err := ps.Write(ctx, projectfs.AreaAgents, mcpServersRel(ref.ID), []byte(req.ServersYAML), o); err != nil {
		h.fsError(ctx, "save the MCP servers file", err)
		return
	}
	resp, err := h.mcpResponse(ctx, ps, ref)
	if err != nil {
		h.fsError(ctx, "read MCP settings", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, resp)
}

// editAgentYAML decodes an agent config as a node tree, applies f to its
// top mapping and encodes it again when f reports a change.
func agentOpsEditYAML(data []byte, f func(root *yaml.Node) bool) ([]byte, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parse YAML: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, errors.New("an agent config is a YAML mapping")
	}
	if !f(doc.Content[0]) {
		return data, false, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

// mappingChild returns key's value in m; with create a missing (or null)
// value becomes an empty mapping.
func mappingChild(m *yaml.Node, key string, create bool) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if create && v.Kind != yaml.MappingNode {
				v.Kind, v.Tag, v.Value, v.Content = yaml.MappingNode, "", "", nil
			}
			return v
		}
	}
	if !create {
		return nil
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, scalar(key), v)
	return v
}

func setMapping(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

func removeMapping(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
