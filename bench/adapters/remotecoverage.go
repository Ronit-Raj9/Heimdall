package adapters

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// remoteCoverageEnv adds the launcher env of the remote coverage channels
// (docs/remote-execution.md):
//
//   - tools: BENCH_REMOTE_TOOLS (id to URL and protocol, no secret) and
//     BENCH_REMOTE_TOOLS_TOKEN (the run's bearer token for the published
//     tool gate; redacted in plan.json because the name holds TOKEN);
//   - signing: BENCH_REMOTE_SIGN_SECRET from the host variable remote.sign_env
//     (redacted: SECRET);
//   - artifacts: BENCH_REMOTE_ARTIFACTS=inline lets the launcher write the
//     response's inline files into the workspace.
func remoteCoverageEnv(rc *schemas.RunContext, spec *schemas.RemoteAgent, env map[string]string) error {
	if utils.RemoteToolsExposed(rc.Agent) && rc.RemoteTools != nil {
		urls := utils.AllowedRemoteTools(utils.RemoteToolURLs(rc.RemoteTools), spec.ToolAllow)
		if spec.Conversation.BenchDriven() { // Y11: the bench speaks to the customer, not the agent
			urls = utils.DropServices(urls, utils.UserOnlyServices(rc.Services))
		}
		if len(urls) > 0 {
			raw, err := json.Marshal(urls)
			if err != nil {
				return fmt.Errorf("encode remote tools: %w", err)
			}
			env["BENCH_REMOTE_TOOLS"] = string(raw)
			env["BENCH_REMOTE_TOOLS_TOKEN"] = rc.RemoteTools.Token
		}
	}
	switch {
	case spec.SignSecret != "": // J3: an org secret; the launcher refuses one under 16 characters
		if !envNamePattern.MatchString(spec.SignSecret) {
			return fmt.Errorf("remote.sign_secret %q is not a secret name", spec.SignSecret)
		}
		env["BENCH_REMOTE_SIGN_SECRET"] = "{{secret." + spec.SignSecret + "}}"
	case spec.SignEnv != "":
		if !envNamePattern.MatchString(spec.SignEnv) {
			return fmt.Errorf("remote.sign_env %q is not an environment variable name", spec.SignEnv)
		}
		secret := os.Getenv(spec.SignEnv)
		if len(secret) < 16 {
			return fmt.Errorf("host environment variable %s (remote.sign_env) is not set or shorter than 16 characters", spec.SignEnv)
		}
		env["BENCH_REMOTE_SIGN_SECRET"] = secret
	}
	if spec.Artifacts == schemas.RemoteArtifactsInline {
		env["BENCH_REMOTE_ARTIFACTS"] = schemas.RemoteArtifactsInline
	}
	return nil
}
