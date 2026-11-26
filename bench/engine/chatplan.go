package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// chatTaskErrs refuses each arm that cannot hold a conversation on a task
// with scripted user turns (lane Y13, schemas/chat.go): only the chat
// adapters (remote_http, openai_agent) send the later turns. Any other
// agent would answer the first message alone and be graded as if it had
// held the whole conversation. The scripted control agents of the
// readiness checks write a control answer and are never refused. It is
// pure.
func chatTaskErrs(t *schemas.Task, arms map[string]schemas.ArmManifest, compat *schemas.ArmManifest) []error {
	if t == nil || t.Chat == nil || t.Chat.UserTurns == 0 {
		return nil
	}
	var errs []error
	check := func(id string, cfg *schemas.AgentConfig) {
		if cfg.Adapter == "scripted" || cfg.Scripted != nil || holdsConversation(cfg) {
			return
		}
		errs = append(errs, fmt.Errorf("task %s is a conversation with %d scripted user turns, but arm %s runs agent adapter %s, which takes one instruction: benchmark a deployed chat agent (remote_http) or an OpenAI-compatible endpoint (openai_agent) on it", t.ID, t.Chat.UserTurns, id, cfg.Adapter))
	}
	for _, id := range sortedKeys(arms) {
		am := arms[id]
		check(id, &am.Config)
	}
	if compat != nil {
		check(CompatArm, &compat.Config)
	}
	return errs
}

func holdsConversation(cfg *schemas.AgentConfig) bool {
	return isRemote(cfg) || cfg.Adapter == "openai_agent" || cfg.OpenAI != nil
}

// taskMetadata is the metadata of the used tasks that carry any, nil when
// none does (so a plan without metadata hashes as before).
func taskMetadata(suite *schemas.Suite, conds []condPlan) map[string]map[string]string {
	var out map[string]map[string]string
	cats := taskCategories(suite.Root) // W4-44: the category sidecar, read once
	for _, id := range usedTasks(conds) {
		t := suite.TaskByID(id)
		if t == nil {
			continue
		}
		cat, labelled := cats[id]
		if len(t.Metadata) == 0 && !labelled {
			continue
		}
		if out == nil {
			out = map[string]map[string]string{}
		}
		cp := make(map[string]string, len(t.Metadata)+1)
		for k, v := range t.Metadata {
			cp[k] = v
		}
		if labelled {
			cp["category"] = cat
		}
		out[id] = cp
	}
	return out
}
