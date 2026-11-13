package environments

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists the environments that ship with the bench.
func Builtin(cfg DockerWorkspaceConfig) []schemas.Environment {
	return []schemas.Environment{newDockerWorkspace(cfg)}
}
