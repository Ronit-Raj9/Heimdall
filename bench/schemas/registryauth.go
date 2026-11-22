package schemas

import "strings"

// RegistryAuth is one private image registry login handed to a runner with
// a lease (LeaseCredentials.Registries, lane Y10). The runner writes it to a
// temporary Docker config for the one `docker pull` of an image on Host and
// removes the folder right after; it is never logged or written to a run
// folder.
type RegistryAuth struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// String hides the password, so a login printed by mistake leaks nothing.
func (a RegistryAuth) String() string {
	return "RegistryAuth{" + a.Host + " " + a.Username + " [hidden]}"
}

// GoString hides the password from %#v too.
func (a RegistryAuth) GoString() string { return a.String() }

// DockerHubHost is the registry host of images named without one.
const DockerHubHost = "docker.io"

// ImageRegistryHost returns the registry host of an image reference, the
// way the docker CLI reads it: the first path part when it holds a dot or
// a colon or is "localhost", else Docker Hub. Docker Hub aliases fold to
// DockerHubHost. The host is lower case.
func ImageRegistryHost(ref string) string {
	ref = strings.TrimSpace(ref)
	i := strings.Index(ref, "/")
	if i <= 0 {
		return DockerHubHost
	}
	first := ref[:i]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return DockerHubHost
	}
	return NormalizeRegistryHost(first)
}

// NormalizeRegistryHost lower-cases a registry host and folds the Docker
// Hub aliases to DockerHubHost.
func NormalizeRegistryHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	switch h {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com", "index.docker.io/v1":
		return DockerHubHost
	}
	return h
}
