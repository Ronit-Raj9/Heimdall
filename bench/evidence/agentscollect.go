package evidence

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// collectAgents derives a team run's mailbox messages (messages.jsonl) and
// builds the run's agent tree (agents.json), setting the agent of each
// timeline row in place. A run with no team, no mailbox and fewer than two
// agents writes nothing and leaves ev and steps as they were.
func collectAgents(rc *schemas.RunContext, ev *schemas.Evidence, steps []schemas.StepRecord) error {
	var errs []error
	dir := ""
	if rc != nil {
		dir = rc.Dir
	}
	team, err := ReadTeamRecord(dir)
	if err != nil {
		errs = append(errs, err)
	}
	records, err := readMailboxRecord(dir)
	if err != nil {
		errs = append(errs, fmt.Errorf("read mailbox record: %w", err))
	}
	// Outside a team run, a task's own service named mailbox is not the team
	// mailbox, so its calls are not messages.
	if msgs := BuildMessages(ev.ToolCalls, records); len(msgs) > 0 && (team != nil || len(records) > 0) {
		ev.Messages = msgs
		if err := writeJSONL(dir, schemas.MessagesFile, msgs); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", schemas.MessagesFile, err))
		}
	}
	tree := BuildAgents(ev, steps, team)
	if tree == nil {
		return errors.Join(errs...)
	}
	ev.Agents = tree
	if err := writeJSON(dir, schemas.AgentsFile, tree); err != nil {
		errs = append(errs, fmt.Errorf("write %s: %w", schemas.AgentsFile, err))
	}
	return errors.Join(errs...)
}
