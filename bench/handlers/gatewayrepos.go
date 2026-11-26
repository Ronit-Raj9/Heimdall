package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// W4 features 2 and 6 (audit/W4_DESIGN_3_BIFROST.md sections 5.5 and 9.4):
// the org's own Bifrost Prompt Repository and Skills Repository, read through
// the saved source connection, and the import of one skill version into an
// agent's upload folder through the skill scan gate.

// GatewayLink is the Gateway page, where the org connects its Bifrost.
const GatewayLink = "/gateway"

// gatewayReposSource is how the routes reach the org's Bifrost; tests may
// replace it.
var gatewayReposSource = gatewaySourceGet

// gatewayRepoFolderRe is what a skill folder name may not contain.
var gatewayRepoFolderRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func init() {
	RegisterOrgArea("gatewayrepos", func(env OrgEnv, r *router.Router) {
		newGatewayReposHandler(env).routes(r)
	})
}

type gatewayReposHandler struct{ env OrgEnv }

func newGatewayReposHandler(env OrgEnv) *gatewayReposHandler { return &gatewayReposHandler{env: env} }

func (h *gatewayReposHandler) routes(r *router.Router) {
	env := h.env
	r.GET(APIPrefix+"/gateway/prompts", env.R(h.prompts))
	r.GET(APIPrefix+"/gateway/prompts/{id}/versions", env.R(h.promptVersions))
	r.GET(APIPrefix+"/gateway/skills", env.R(h.skills))
	r.GET(APIPrefix+"/gateway/skills/{id}/versions", env.R(h.skillVersions))
	r.POST(APIPrefix+"/agents/{id}/skills/from-bifrost", env.W(h.importSkill))
}

// GatewayPrompt is one prompt of GET /gateway/prompts.
type GatewayPrompt struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GatewayPromptVersion is one version of GET /gateway/prompts/{id}/versions;
// Messages is the number of messages.
type GatewayPromptVersion struct {
	Version       int    `json:"version"`
	CommitMessage string `json:"commit_message"`
	CreatedAt     string `json:"created_at"`
	Messages      int    `json:"messages"`
	Model         string `json:"model"`
}

// GatewaySkill is one skill of GET /gateway/skills.
type GatewaySkill struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LatestVersion string `json:"latest_version"`
}

// GatewaySkillVersion is one version of GET /gateway/skills/{id}/versions;
// Size is the length of its SKILL.md body in bytes.
type GatewaySkillVersion struct {
	Version   string `json:"version"`
	CreatedAt string `json:"created_at"`
	Size      int    `json:"size"`
}

// GatewaySkillImportRequest is POST /agents/{id}/skills/from-bifrost.
type GatewaySkillImportRequest struct {
	SkillID string `json:"skill_id"`
	Version string `json:"version"`
}

// GatewaySkillImport is the answer of POST /agents/{id}/skills/from-bifrost.
type GatewaySkillImport struct {
	Folder    string          `json:"folder"`
	ExtraPath string          `json:"extra_path"`
	Scan      SkillScanResult `json:"scan"`
}

// bifrostSkillVersion is one version as Bifrost's Skills Repository answers it.
type bifrostSkillVersion struct {
	Version             json.RawMessage   `json:"version"`
	SkillMDBody         string            `json:"skill_md_body"`
	FrontmatterSnapshot map[string]any    `json:"frontmatter_snapshot"`
	CreatedAt           string            `json:"created_at"`
	Files               []json.RawMessage `json:"files"`
}

// bifrostSkill is one skill as Bifrost's Skills Repository answers it.
type bifrostSkill struct {
	ID            json.RawMessage `json:"id"`
	Name          string          `json:"name"`
	LatestVersion json.RawMessage `json:"latest_version"`
}

// gatewayRawText is a JSON string or number as text.
func gatewayRawText(m json.RawMessage) string {
	m = bytes.TrimSpace(m)
	if len(m) == 0 || string(m) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		return s
	}
	return string(m)
}

// gatewaySkillFileName is one entry of a version's files list: a path, or an object with
// a path or name.
func gatewaySkillFileName(m json.RawMessage) string {
	var s string
	if json.Unmarshal(m, &s) == nil {
		return s
	}
	var o struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if json.Unmarshal(m, &o) == nil {
		if o.Path != "" {
			return o.Path
		}
		return o.Name
	}
	return string(m)
}

// source returns the org's Bifrost reader, or answers 409 itself.
func (h *gatewayReposHandler) source(ctx *fasthttp.RequestCtx, work context.Context) func(ctx context.Context, path string, out any) error {
	get := gatewayReposSource(work, h.env)
	if get == nil {
		lib.SendErrorLink(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Connect your Bifrost first", GatewayLink)
	}
	return get
}

func (h *gatewayReposHandler) readFailed(ctx *fasthttp.RequestCtx, err error) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "Heimdall could not read from your Bifrost: "+err.Error())
}

func (h *gatewayReposHandler) prompts(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	get := h.source(ctx, work)
	if get == nil {
		return
	}
	var in struct {
		Prompts []struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name"`
		} `json:"prompts"`
	}
	if err := get(work, "/api/prompt-repo/prompts", &in); err != nil {
		h.readFailed(ctx, err)
		return
	}
	out := []GatewayPrompt{}
	for _, p := range in.Prompts {
		out = append(out, GatewayPrompt{ID: gatewayRawText(p.ID), Name: p.Name})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"prompts": out})
}

func (h *gatewayReposHandler) promptVersions(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	get := h.source(ctx, work)
	if get == nil {
		return
	}
	id := pathParam(ctx, "id")
	var in struct {
		Versions []struct {
			VersionNumber int               `json:"version_number"`
			CommitMessage string            `json:"commit_message"`
			CreatedAt     string            `json:"created_at"`
			Model         string            `json:"model"`
			Messages      []json.RawMessage `json:"messages"`
		} `json:"versions"`
	}
	if err := get(work, "/api/prompt-repo/prompts/"+url.PathEscape(id)+"/versions", &in); err != nil {
		h.readFailed(ctx, err)
		return
	}
	out := []GatewayPromptVersion{}
	for _, v := range in.Versions {
		out = append(out, GatewayPromptVersion{Version: v.VersionNumber, CommitMessage: v.CommitMessage, CreatedAt: v.CreatedAt, Messages: len(v.Messages), Model: v.Model})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"versions": out})
}

// listBifrostSkills reads every skill of the org's Bifrost.
func listBifrostSkills(work context.Context, get func(ctx context.Context, path string, out any) error) ([]bifrostSkill, error) {
	var in struct {
		Skills []bifrostSkill `json:"skills"`
	}
	if err := get(work, "/api/skills", &in); err != nil {
		return nil, err
	}
	return in.Skills, nil
}

func listBifrostSkillVersions(work context.Context, get func(ctx context.Context, path string, out any) error, id string) ([]bifrostSkillVersion, error) {
	var in struct {
		Versions []bifrostSkillVersion `json:"versions"`
	}
	if err := get(work, "/api/skills/"+url.PathEscape(id)+"/versions", &in); err != nil {
		return nil, err
	}
	return in.Versions, nil
}

func (h *gatewayReposHandler) skills(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	get := h.source(ctx, work)
	if get == nil {
		return
	}
	in, err := listBifrostSkills(work, get)
	if err != nil {
		h.readFailed(ctx, err)
		return
	}
	out := []GatewaySkill{}
	for _, s := range in {
		out = append(out, GatewaySkill{ID: gatewayRawText(s.ID), Name: s.Name, LatestVersion: gatewayRawText(s.LatestVersion)})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"skills": out})
}

func (h *gatewayReposHandler) skillVersions(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	get := h.source(ctx, work)
	if get == nil {
		return
	}
	in, err := listBifrostSkillVersions(work, get, pathParam(ctx, "id"))
	if err != nil {
		h.readFailed(ctx, err)
		return
	}
	out := []GatewaySkillVersion{}
	for _, v := range in {
		out = append(out, GatewaySkillVersion{Version: gatewayRawText(v.Version), CreatedAt: v.CreatedAt, Size: len(v.SkillMDBody)})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"versions": out})
}

// skillVersionFolder is <skill name>-v<version> folded to the skill name
// pattern.
func skillVersionFolder(name, version string) string {
	s := gatewayRepoFolderRe.ReplaceAllString(name+"-v"+version, "-")
	s = strings.TrimLeft(s, "._-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func (h *gatewayReposHandler) importSkill(ctx *fasthttp.RequestCtx) {
	ops := &agentOpsHandler{env: h.env}
	ref, ps, ok := ops.agent(ctx)
	if !ok {
		return
	}
	var req GatewaySkillImportRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		ops.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the import request could not be read: "+err.Error())
		return
	}
	req.SkillID, req.Version = strings.TrimSpace(req.SkillID), strings.TrimSpace(req.Version)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	get := h.source(ctx, work)
	if get == nil {
		return
	}
	if req.SkillID == "" || req.Version == "" {
		ops.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Pick a skill and a version.")
		return
	}
	skills, err := listBifrostSkills(work, get)
	if err != nil {
		h.readFailed(ctx, err)
		return
	}
	var skill *bifrostSkill
	for i := range skills {
		if gatewayRawText(skills[i].ID) == req.SkillID {
			skill = &skills[i]
		}
	}
	if skill == nil {
		ops.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "Your Bifrost has no skill with this id.")
		return
	}
	versions, err := listBifrostSkillVersions(work, get, req.SkillID)
	if err != nil {
		h.readFailed(ctx, err)
		return
	}
	var ver *bifrostSkillVersion
	for i := range versions {
		if gatewayRawText(versions[i].Version) == req.Version {
			ver = &versions[i]
		}
	}
	if ver == nil {
		ops.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("Skill %s has no version %s.", skill.Name, req.Version))
		return
	}
	more := 0
	for _, f := range ver.Files {
		if gatewaySkillFileName(f) != AgentSkillsFile {
			more++
		}
	}
	if more > 0 {
		msg := "This version has " + strconv.Itoa(more) + " more files. Bifrost serves past versions' files only as the current version, so Heimdall can import only SKILL.md. Make it the current version in Bifrost, or test the current version."
		if gatewayRawText(skill.LatestVersion) == req.Version {
			// The zip of the current version cannot be read through the
			// source connection, which decodes JSON answers only (notes I24).
			msg = "This version has " + strconv.Itoa(more) + " more files, and Heimdall cannot download them from your Bifrost yet, so it can import only versions that hold SKILL.md alone."
		}
		ops.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
		return
	}
	folder := skillVersionFolder(skill.Name, req.Version)
	if !agentOpsNamePattern.MatchString(folder) {
		ops.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("Skill %s cannot be made into a folder name.", skill.Name))
		return
	}
	front, err := yaml.Marshal(ver.FrontmatterSnapshot)
	if err != nil || len(ver.FrontmatterSnapshot) == 0 {
		front = nil
	}
	md := "---\n" + string(front) + "---\n" + ver.SkillMDBody
	if len(md) > int(projectfs.MaxZipBytes) {
		ops.fail(ctx, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "This skill version is too large to import.")
		return
	}
	o := projectfs.WriteOpts{Actor: actor(ctx), Note: "import skill " + folder + " from Bifrost"}
	if _, err := ps.Write(ctx, projectfs.AreaAgents, pendingSkillsDir(ref.ID)+"/"+folder+"/"+AgentSkillsFile, []byte(md), o); err != nil {
		ops.fsError(ctx, "write skill "+folder, err)
		return
	}
	res, ok := ops.scanAndApply(ctx, ps, ref, folder, false, o)
	if !ok {
		return
	}
	status := fasthttp.StatusOK
	if !res.Installed {
		status = fasthttp.StatusUnprocessableEntity
	}
	lib.SendJSON(ctx, h.env.Mode, status, GatewaySkillImport{Folder: folder, ExtraPath: "agents/" + skillsDir(ref.ID) + "/" + folder, Scan: res})
}
