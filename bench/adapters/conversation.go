package adapters

import (
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Lane Y11: what the remote launchers share (heimdall_conv.py): the
// bench-driven conversation (audit G1 P0-2), the Heimdall pull endpoint
// (P0-9) and repo in, patch out (P0-4).

// convHelperFile is where the shared helpers land in the run container;
// the launchers import heimdall_conv from its folder.
const convHelperFile = "/tmp/bench/heimdall_conv.py"

// pullTokenMargin is how long past the agent's wall budget a run's pull
// token stays valid, for the last answer and the repository link.
const pullTokenMargin = 10 * time.Minute

//go:embed heimdall_conv.py
var convHelperSource string

// withConvHelper adds the shared helpers to a launcher plan.
func withConvHelper(plan *schemas.RunPlan) {
	if plan.Files == nil {
		plan.Files = map[string]string{}
	}
	plan.Files[convHelperFile] = convHelperSource
}

// pullToken signs the run's pull token for agent: it names cfg's org, the
// agent and the run, and expires with the run's wall budget plus a margin.
func pullToken(cfg CommandConfig, rc *schemas.RunContext, agent string, wallS int, voice *schemas.VoicePlatformAgent) (string, error) {
	exp := time.Now().Add(time.Duration(max(wallS, 60))*time.Second + pullTokenMargin)
	return pullagent.DefaultSigner().RunToken(pullagent.Claims{Org: cfg.OrgID, Agent: agent, Run: rc.Run.RunID, Exp: exp.Unix(), Voice: voice})
}

// NoPullURLReason refuses a pull or repo-tarball run on a server that has
// no address its runs can reach it at.
const NoPullURLReason = "this server has no address its runs can reach it at: set pull.url (or auth.public_url) in the bench config, or BENCH_PULL_URL"

// pullEnv adds the Heimdall pull endpoint to a launcher's env and plan:
// BENCH_PULL_URL, the run's pull token (BENCH_PULL_TOKEN, redacted in
// plan.json) and the forwarder it connects through (BENCH_PULL_CONNECT),
// one more egress destination of the plan.
func pullEnv(cfg CommandConfig, rc *schemas.RunContext, plan *schemas.RunPlan, env map[string]string, agent string) error {
	return pullEnvVoice(cfg, rc, plan, env, agent, nil)
}

// pullEnvVoice is pullEnv for a voice platform agent's text run: the token
// also names the agent's voice_platform block (handlers/pullvoice.go).
func pullEnvVoice(cfg CommandConfig, rc *schemas.RunContext, plan *schemas.RunPlan, env map[string]string, agent string, voice *schemas.VoicePlatformAgent) error {
	if cfg.PullURL == "" {
		return fmt.Errorf("%s", NoPullURLReason)
	}
	target, err := utils.RemoteEgressTarget(cfg.PullURL)
	if err != nil {
		return fmt.Errorf("pull.url: %w", err)
	}
	tok, err := pullToken(cfg, rc, agent, plan.WallS, voice)
	if err != nil {
		return fmt.Errorf("sign the run's pull token: %w", err)
	}
	idx := len(plan.Egress)
	for i, t := range plan.Egress {
		if t == target {
			idx = i
		}
	}
	if idx == len(plan.Egress) {
		plan.Egress = append(plan.Egress, target)
	}
	env["BENCH_PULL_URL"] = cfg.PullURL
	env["BENCH_PULL_TOKEN"] = tok
	env["BENCH_PULL_CONNECT"] = utils.RemoteEgressAddrN(rc.Run.RunID, idx)
	return nil
}

// repoEnv turns on repo in, patch out for a diff task: the launcher packs
// the workspace, stores it on the pull endpoint and sends the agent the
// signed, expiring link; the agent's patch is applied to the workspace and
// graded by the clean-room grader like any agent's changes.
func repoEnv(cfg CommandConfig, rc *schemas.RunContext, plan *schemas.RunPlan, env map[string]string, agent string) error {
	if utils.OutputKind(rc.Suite, rc.Task) != schemas.OutputDiff {
		return nil
	}
	if env["BENCH_PULL_URL"] == "" {
		if err := pullEnv(cfg, rc, plan, env, agent); err != nil {
			return err
		}
	}
	env["BENCH_REPO"] = schemas.RepoTarball
	env["BENCH_REPO_DIR"] = Workspace
	return nil
}

// conversationEnv adds the bench-driven conversation to a launcher's env
// when c asks for it and the run has a simulated user; it reports whether
// the bench drives this run's conversation.
func conversationEnv(rc *schemas.RunContext, c *schemas.ConversationSpec, plan *schemas.RunPlan, env map[string]string) (bool, error) {
	on, err := utils.ConversationEnv(rc, c, env)
	if err != nil || !on {
		return false, err
	}
	withConvHelper(plan)
	return true, nil
}

// itoa is strconv.Itoa, for env values.
func itoa(n int) string { return strconv.Itoa(n) }
