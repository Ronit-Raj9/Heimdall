package ci

import _ "embed"

//go:embed heimdall.gitlab-ci.yml
var gitlabTemplate []byte

// GitLabTemplate is the GitLab CI template (heimdall.gitlab-ci.yml) the
// Heimdall server serves at /downloads/heimdall.gitlab-ci.yml with its own
// address as the HEIMDALL_URL default (handlers.GitLabTemplateFor). The
// result is a copy.
func GitLabTemplate() []byte { return append([]byte(nil), gitlabTemplate...) }
