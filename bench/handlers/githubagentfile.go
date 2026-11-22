package handlers

import (
	"errors"
	"net/http"

	"github.com/maximhq/bifrost/bench/agentinfer"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// GitHubAgentFilePath reads a repository's committed .heimdall/agent.yaml
// (lane Z15): the repository import of an agent.
const GitHubAgentFilePath = GitHubPrefix + "/repos/{owner}/{repo}/agent-file"

// GitHubAgentFile answers GET /github/repos/{owner}/{repo}/agent-file.
// Found is false (200) when the repository has no agent file at that ref.
type GitHubAgentFile struct {
	Found bool   `json:"found"`
	Path  string `json:"path"`
	Ref   string `json:"ref,omitempty"`
	YAML  string `json:"yaml,omitempty"`
	AgentFileCheckResult
}

func init() {
	OrgRouteDocs = append(OrgRouteDocs, RouteDoc{Method: "GET", Path: GitHubAgentFilePath, Tag: "github",
		Summary:  "Read and check a repository's .heimdall/agent.yaml: the agent request or config it becomes; found false when there is none",
		Query:    []QueryDoc{{Name: "ref", Description: "branch or tag (default: the default branch)"}},
		Response: GitHubAgentFile{}})
}

// getAgentFile handles GET /github/repos/{owner}/{repo}/agent-file?ref=.
func (h *GitHubHandler) getAgentFile(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx)
	if !ok {
		return
	}
	owner, repo := pathParam(ctx, "owner"), pathParam(ctx, "repo")
	ref := string(ctx.QueryArgs().Peek("ref"))
	if !auth.ValidRepo(owner, repo) || ref != "" && !auth.ValidBranch(ref) {
		h.fail(ctx, http.StatusBadRequest, "invalid owner, repository or ref")
		return
	}
	wctx, cancel := workContext(ctx, githubTimeout)
	defer cancel()
	tok, ok := h.token(ctx, wctx, o.ID)
	if !ok {
		return
	}
	out := GitHubAgentFile{Path: agentinfer.AgentFilePath, Ref: ref}
	b, err := h.conn.ReadFile(wctx, tok, owner, repo, ref, agentinfer.AgentFilePath, agentinfer.MaxAgentFileBytes)
	var apiErr *auth.GitHubAPIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	if err != nil {
		h.apiErr(ctx, err)
		return
	}
	out.Found, out.YAML = true, string(b)
	out.AgentFileCheckResult = CheckAgentFile(b)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
