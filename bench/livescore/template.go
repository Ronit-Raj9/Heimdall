package livescore

import (
	"errors"
	"fmt"
	"strings"
)

// InitOptions fill the template `livescore init` writes.
type InitOptions struct {
	URL          string // gateway URL; empty leaves the bench config's gateway.url
	TokenEnv     string // read credential variable; empty leaves gateway.admin_token_env
	BaselineKey  string // virtual key id of the version in production
	CandidateKey string // virtual key id of the new version
	TagHeader    string // or: a header both versions send, with these two values
	BaselineTag  string
	CandidateTag string
}

// Template renders livescore/config.yaml. Without keys or tags it writes
// EDIT ME placeholders that fail validation, so an unedited file never
// scores anything.
func Template(o InitOptions) (string, error) {
	if (o.BaselineKey == "") != (o.CandidateKey == "") {
		return "", errors.New("give both -baseline-key and -candidate-key, or neither")
	}
	if o.TagHeader != "" && (o.BaselineTag == "" || o.CandidateTag == "") {
		return "", errors.New("-tag-header needs -baseline-tag and -candidate-tag")
	}
	var b strings.Builder
	b.WriteString(`# Live traffic scoring (docs/live-scoring.md). Read-only: only GET requests to
# the gateway's log API leave, and no prompt or output is stored here.
# Live scores are their own evidence class: no ground truth, no control of
# inputs, never part of an experiment verdict.
source:
`)
	if o.URL != "" {
		fmt.Fprintf(&b, "  url: %s\n", o.URL)
	} else {
		b.WriteString("  # url: http://127.0.0.1:8080        # default: gateway.url of the bench config\n")
	}
	if o.TokenEnv != "" {
		fmt.Fprintf(&b, "  token_env: %s\n", o.TokenEnv)
	} else {
		b.WriteString("  # token_env: BIFROST_READ_TOKEN     # default: gateway.admin_token_env; the value stays in the environment\n")
	}
	b.WriteString("  window: 24h                          # the first pull looks this far back\n\nversions:\n")
	switch {
	case o.BaselineKey != "":
		fmt.Fprintf(&b, "  - name: baseline\n    virtual_keys: [%s]\n  - name: candidate\n    virtual_keys: [%s]\n", o.BaselineKey, o.CandidateKey)
	case o.TagHeader != "":
		fmt.Fprintf(&b, "  - name: baseline\n    tag: {header: %s, value: %s}\n  - name: candidate\n    tag: {header: %s, value: %s}\n",
			o.TagHeader, o.BaselineTag, o.TagHeader, o.CandidateTag)
	default:
		b.WriteString(`  # One entry per live agent version. Select its traffic by virtual key ids,
  # team ids, customer ids, models, or a header the agent sends
  # (x-bf-lh-agent-version: v2 is logged by Bifrost with no setting).
  - name: baseline
    virtual_keys: [EDIT ME]
  - name: candidate
    virtual_keys: [EDIT ME]
`)
	}
	b.WriteString(`
compare:
  baseline: baseline
  candidate: candidate
  alpha: 0.05        # chance of any false decision over every signal and every look
  min_pairs: 30      # pairs of sessions before a signal may decide

sessions:
  gap: 10m           # idle time that ends a session without a session id

sampling:
  rate: 1.0          # share of sessions scored, chosen by a hash of the session

retention:
  days: 30

thresholds:
  - signal: abandoned
    above: 0.2
    window: 24h
    min_sessions: 20

# alerts:            # receivers named by environment variable only
#   - url_env: LIVE_ALERT_WEBHOOK
#     events: [live_signal_threshold, live_comparison_worse]

inbox:
  max_per_pull: 5    # worst failure signatures sent to the failure inbox per pull

# schedule: "*/15 * * * *"   # pulled by the schedule daemon as job livescore-pull
`)
	return b.String(), nil
}
