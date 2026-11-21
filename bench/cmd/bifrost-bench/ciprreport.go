package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Pull-request reporting for `ci evaluate`: one comment per agent, edited in
// place on later runs (found by its marker), and a commit status on the
// pull request's head commit. GitHub uses the job's GITHUB_TOKEN; GitLab a
// project access token in GITLAB_TOKEN (the CI job token cannot write notes).

// prReporter posts the comment and the status. States: pending, success,
// failure, error.
type prReporter interface {
	Comment(ctx context.Context, marker, body string) error
	Status(ctx context.Context, state, description, link string) error
	Info() prContext
}

// prContext is where the pull request is.
type prContext struct {
	Provider string // github or gitlab
	Number   string // pull request number (GitHub) or merge request iid (GitLab)
	SHA      string // head commit
}

func prInfo(pr prReporter) prContext {
	if pr == nil {
		return prContext{}
	}
	return pr.Info()
}

// statusContext names the commit status; the agent id follows it, so two
// agents gated in one repository keep separate statuses.
const statusContext = "Heimdall evaluation"

// setReporterAgent names the agent in the status of pr.
func setReporterAgent(pr prReporter, agent string) {
	switch r := pr.(type) {
	case *githubReporter:
		r.agent = agent
	case *gitlabReporter:
		r.agent = agent
	case *filteredReporter:
		setReporterAgent(r.inner, agent)
	}
}

// detectPR builds the reporter for provider (auto reads the CI
// environment). It returns nil when there is no pull request to report on
// or no token to report with; the gate still runs and sets the job result.
func detectPR(provider string) prReporter {
	if provider == "none" {
		return nil
	}
	if provider == "github" || (provider == "auto" && os.Getenv("GITHUB_ACTIONS") == "true") {
		return githubFromEnv()
	}
	if provider == "gitlab" || (provider == "auto" && os.Getenv("GITLAB_CI") == "true") {
		return gitlabFromEnv()
	}
	return nil
}

func githubFromEnv() prReporter {
	g := &githubReporter{
		api:   strings.TrimRight(envDefault("GITHUB_API_URL", "https://api.github.com"), "/"),
		repo:  os.Getenv("GITHUB_REPOSITORY"),
		token: os.Getenv("GITHUB_TOKEN"),
		http:  &http.Client{Timeout: 30 * time.Second},
		ctx:   prContext{Provider: "github", SHA: os.Getenv("GITHUB_SHA")},
	}
	// The event payload has the pull request number and its head commit
	// (GITHUB_SHA is the test merge commit on pull_request events).
	if p := os.Getenv("GITHUB_EVENT_PATH"); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			var ev struct {
				PullRequest *struct {
					Number int `json:"number"`
					Head   struct {
						SHA string `json:"sha"`
					} `json:"head"`
				} `json:"pull_request"`
			}
			if json.Unmarshal(raw, &ev) == nil && ev.PullRequest != nil {
				g.ctx.Number = strconv.Itoa(ev.PullRequest.Number)
				if ev.PullRequest.Head.SHA != "" {
					g.ctx.SHA = ev.PullRequest.Head.SHA
				}
			}
		}
	}
	if g.repo == "" || g.token == "" {
		fmt.Fprintln(os.Stderr, "note: no GITHUB_REPOSITORY or GITHUB_TOKEN: no pull request comment or commit status")
		return nil
	}
	return g
}

func gitlabFromEnv() prReporter {
	g := &gitlabReporter{
		api:     strings.TrimRight(envDefault("CI_API_V4_URL", "https://gitlab.com/api/v4"), "/"),
		project: os.Getenv("CI_PROJECT_ID"),
		token:   os.Getenv("GITLAB_TOKEN"),
		http:    &http.Client{Timeout: 30 * time.Second},
		ctx: prContext{Provider: "gitlab", Number: os.Getenv("CI_MERGE_REQUEST_IID"),
			SHA: envDefault("CI_MERGE_REQUEST_SOURCE_BRANCH_SHA", os.Getenv("CI_COMMIT_SHA"))},
	}
	if g.project == "" || g.token == "" {
		fmt.Fprintln(os.Stderr, "note: no CI_PROJECT_ID or GITLAB_TOKEN: no merge request comment or commit status (GITLAB_TOKEN is a project access token with the api scope)")
		return nil
	}
	return g
}

func envDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// filteredReporter drops the comment or the status (-no-comment, -no-status).
type filteredReporter struct {
	inner               prReporter
	noComment, noStatus bool
}

func (f *filteredReporter) Comment(ctx context.Context, marker, body string) error {
	if f.noComment {
		return nil
	}
	return f.inner.Comment(ctx, marker, body)
}

func (f *filteredReporter) Status(ctx context.Context, state, desc, link string) error {
	if f.noStatus {
		return nil
	}
	return f.inner.Status(ctx, state, desc, link)
}

func (f *filteredReporter) Info() prContext { return f.inner.Info() }

// restCall sends one JSON request and decodes a JSON answer.
func restCall(ctx context.Context, hc *http.Client, method, u string, headers map[string]string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, redactQuery(u), resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// maxCommentPages bounds the search for the existing comment.
const maxCommentPages = 20

// githubReporter posts to the GitHub REST API.
type githubReporter struct {
	api, repo, token, agent string
	http                    *http.Client
	ctx                     prContext
}

func (g *githubReporter) Info() prContext { return g.ctx }

func (g *githubReporter) headers() map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + g.token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
}

func (g *githubReporter) Comment(ctx context.Context, marker, body string) error {
	if g.ctx.Number == "" {
		return nil // a workflow_dispatch run: no pull request
	}
	base := g.api + "/repos/" + g.repo + "/issues"
	for page := 1; page <= maxCommentPages; page++ {
		var cs []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := restCall(ctx, g.http, http.MethodGet, fmt.Sprintf("%s/%s/comments?per_page=100&page=%d", base, g.ctx.Number, page), g.headers(), nil, &cs); err != nil {
			return err
		}
		for _, c := range cs {
			if strings.HasPrefix(c.Body, marker) {
				return restCall(ctx, g.http, http.MethodPatch, fmt.Sprintf("%s/comments/%d", base, c.ID), g.headers(), map[string]string{"body": body}, nil)
			}
		}
		if len(cs) < 100 {
			break
		}
	}
	return restCall(ctx, g.http, http.MethodPost, base+"/"+g.ctx.Number+"/comments", g.headers(), map[string]string{"body": body}, nil)
}

func (g *githubReporter) Status(ctx context.Context, state, desc, link string) error {
	if g.ctx.SHA == "" {
		return nil
	}
	name := statusContext
	if g.agent != "" {
		name += " (" + g.agent + ")"
	}
	body := map[string]string{"state": state, "description": truncate(desc, 140), "context": name}
	if link != "" {
		body["target_url"] = link
	}
	return restCall(ctx, g.http, http.MethodPost, g.api+"/repos/"+g.repo+"/statuses/"+g.ctx.SHA, g.headers(), body, nil)
}

// gitlabReporter posts to the GitLab REST API (v4).
type gitlabReporter struct {
	api, project, token, agent string
	http                       *http.Client
	ctx                        prContext
}

func (g *gitlabReporter) Info() prContext { return g.ctx }

func (g *gitlabReporter) headers() map[string]string {
	return map[string]string{"PRIVATE-TOKEN": g.token}
}

func (g *gitlabReporter) Comment(ctx context.Context, marker, body string) error {
	if g.ctx.Number == "" {
		return nil // not a merge request pipeline
	}
	base := g.api + "/projects/" + url.PathEscape(g.project) + "/merge_requests/" + g.ctx.Number + "/notes"
	for page := 1; page <= maxCommentPages; page++ {
		var ns []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := restCall(ctx, g.http, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", base, page), g.headers(), nil, &ns); err != nil {
			return err
		}
		for _, n := range ns {
			if strings.HasPrefix(n.Body, marker) {
				return restCall(ctx, g.http, http.MethodPut, fmt.Sprintf("%s/%d", base, n.ID), g.headers(), map[string]string{"body": body}, nil)
			}
		}
		if len(ns) < 100 {
			break
		}
	}
	return restCall(ctx, g.http, http.MethodPost, base, g.headers(), map[string]string{"body": body}, nil)
}

// gitlabState maps the reporter states to GitLab commit status states.
func gitlabState(s string) string {
	switch s {
	case "pending":
		return "running"
	case "success":
		return "success"
	}
	return "failed"
}

func (g *gitlabReporter) Status(ctx context.Context, state, desc, link string) error {
	if g.ctx.SHA == "" {
		return nil
	}
	name := statusContext
	if g.agent != "" {
		name += " (" + g.agent + ")"
	}
	body := map[string]string{"state": gitlabState(state), "name": name, "description": truncate(desc, 255)}
	if link != "" {
		body["target_url"] = link
	}
	return restCall(ctx, g.http, http.MethodPost, g.api+"/projects/"+url.PathEscape(g.project)+"/statuses/"+g.ctx.SHA, g.headers(), body, nil)
}
