package tenancy

import "github.com/maximhq/bifrost/bench/auth"

// GitHub connect (lane L07, handlers/github.go): platform routes. Admins
// connect, disconnect and open the CI-install pull request; members list
// repositories and refs (private repository names); the callback is the
// signed-in user who started the connect (checked by the handler).
func init() {
	PlatformPolicy[key("GET", api+"/github/status")] = org(viewer)
	PlatformPolicy[key("POST", api+"/github/connect/start")] = org(admin)
	PlatformPolicy[key("GET", auth.GitHubRepoCallbackPath)] = userOnly()
	PlatformPolicy[key("POST", api+"/github/disconnect")] = org(admin)
	PlatformPolicy[key("GET", api+"/github/repos")] = org(member)
	PlatformPolicy[key("GET", api+"/github/repos/{owner}/{repo}/refs")] = org(member)
	PlatformPolicy[key("POST", api+"/github/ci-install")] = org(admin)
}
