package environments

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// mailboxScript is the team mailbox server (docs/multi-agent.md), run with
// the helper image's python3.
//
//go:embed mailbox.py
var mailboxScript string

// withMailbox adds the team mailbox to a team run's services: an HTTP tool
// service reached only through the tool proxy, so every message is a ledger
// row, with its own record exported as state/mailbox/messages.jsonl. A run
// that is not a team, or already declares a service named mailbox, is left
// as it is.
func (e *dockerWorkspace) withMailbox(rc *schemas.RunContext, svcs []runService) []runService {
	team := utils.TeamOf(rc)
	if len(team) == 0 {
		return svcs
	}
	for _, s := range svcs {
		if s.spec.ID == schemas.MailboxServer {
			return svcs
		}
	}
	return append(svcs, runService{spec: mailboxSpec(e.helperImage(rc), team)})
}

// mailboxSpec is the mailbox's service spec. It is pure.
func mailboxSpec(image string, team []schemas.TeamMember) schemas.ServiceSpec {
	proxy := true
	return schemas.ServiceSpec{
		ID:    schemas.MailboxServer,
		Role:  schemas.ServiceRoleTool,
		Image: image,
		Argv:  []string{"python3", "-u", "-c", mailboxScript},
		Env: map[string]string{
			"MAILBOX_PORT":   strconv.Itoa(utils.MailboxPort),
			"MAILBOX_RECORD": utils.MailboxRecord,
			"MAILBOX_TEAM":   strings.Join(utils.TeamIDs(team), ","),
		},
		Port:   utils.MailboxPort,
		Ready:  &schemas.ReadyCheck{HTTP: "/healthz"},
		Export: []schemas.StateExport{{ID: schemas.MailboxExport, Path: utils.MailboxRecord}},
		Proxy:  &proxy,
	}
}
