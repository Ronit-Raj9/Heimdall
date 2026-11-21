package handlers

import (
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// GitHub connect routes (lane L07): hosted platform routes, documented with
// the other hosted-only routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: GitHubPrefix + "/status", Tag: "github", Summary: "Whether GitHub is configured on the server and connected for the org", Response: GitHubStatus{}},
		RouteDoc{Method: "POST", Path: GitHubPrefix + "/connect/start", Tag: "github", Summary: "Start the repo-scope GitHub connect (admin, browser session); answers the GitHub URL to open", Request: GitHubStartRequest{}, OptionalBody: true, Response: GitHubStartResponse{}},
		RouteDoc{Method: "GET", Path: auth.GitHubRepoCallbackPath, Tag: "github", Summary: "GitHub's redirect back after a connect; stores the token and redirects to the return path with github=connected or github_error=", Status: fasthttp.StatusFound},
		RouteDoc{Method: "POST", Path: GitHubPrefix + "/disconnect", Tag: "github", Summary: "Forget the org's GitHub token (admin)", Response: GitHubStatus{}},
		RouteDoc{Method: "GET", Path: GitHubPrefix + "/repos", Tag: "github", Summary: "Repositories the connected account can see, 100 per page", Query: []QueryDoc{{Name: "page", Description: "1-based page"}}, Response: GitHubReposResponse{}},
		RouteDoc{Method: "GET", Path: GitHubPrefix + "/repos/{owner}/{repo}/refs", Tag: "github", Summary: "Branches and tags of a repository", Response: auth.GitHubRefs{}},
		RouteDoc{Method: "POST", Path: GitHubPrefix + "/ci-install", Tag: "github", Summary: "Open a pull request that adds the Heimdall gate workflow (admin)", Request: GitHubCIInstallRequest{}, Status: fasthttp.StatusCreated, Response: auth.CIInstallResult{}},
	)
}
