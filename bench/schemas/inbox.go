package schemas

import "time"

// Failure inbox area (bench/traceinbox, handlers/inbox.go,
// docs/failure-inbox.md): production failures pulled from observability
// tools, deduplicated by signature, and turned into task drafts by a person.
// The key lists in handlers/testdata/contract/Inbox*.keys pin these types to
// the UI's.

// InboxItem is one failure seen in production, with every occurrence of it.
type InboxItem struct {
	ID             string              `json:"id"`
	State          string              `json:"state"`       // new | accepted | rejected | duplicate | snoozed
	Source         string              `json:"source"`      // name of the source that first saw it
	SourceKind     string              `json:"source_kind"` // langfuse | langsmith | bifrost | otlp_dir | webhook
	TraceID        string              `json:"trace_id"`    // the first occurrence's trace id (redacted)
	Link           string              `json:"link,omitempty"`
	Title          string              `json:"title"` // the first user request on one line (redacted)
	Flagged        []string            `json:"flagged"`
	Signature      string              `json:"signature"`
	SignatureParts InboxSignatureParts `json:"signature_parts"`
	Occurrences    int                 `json:"occurrences"`
	FirstSeen      time.Time           `json:"first_seen"`
	LastSeen       time.Time           `json:"last_seen"`
	Traces         []InboxTraceRef     `json:"traces"`
	SuggestedKind  string              `json:"suggested_kind"`
	SuggestedSuite string              `json:"suggested_suite,omitempty"`
	Tools          []string            `json:"tools,omitempty"`
	Model          string              `json:"model,omitempty"`
	Outcome        string              `json:"outcome"`
	Redacted       map[string]int      `json:"redacted,omitempty"` // values scrubbed, by kind; never the values
	Task           *InboxTaskLink      `json:"task,omitempty"`
	Recurrences    int                 `json:"recurrences,omitempty"` // occurrences seen after the item was accepted
	LastRecurrence *time.Time          `json:"last_recurrence,omitempty"`
	Release        *InboxReleaseStatus `json:"release,omitempty"`
	Reason         string              `json:"reason,omitempty"`
	DuplicateOf    string              `json:"duplicate_of,omitempty"`
	SnoozedUntil   *time.Time          `json:"snoozed_until,omitempty"`
	Updated        time.Time           `json:"updated"`
}

// InboxSignatureParts are the three parts a failure signature hashes.
type InboxSignatureParts struct {
	Request string `json:"request"`         // sketch of the first user request's word shingles
	Tool    string `json:"tool,omitempty"`  // the first failing tool
	Error   string `json:"error,omitempty"` // the normalized error line, or the flag kind when nothing errored
}

// InboxTraceRef is one occurrence of an item.
type InboxTraceRef struct {
	ID     string    `json:"id"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	Link   string    `json:"link,omitempty"`
}

// InboxTaskLink is the task draft an accepted item became.
type InboxTaskLink struct {
	Suite      string    `json:"suite"`
	ID         string    `json:"id"`
	Dir        string    `json:"dir"`
	AcceptedAt time.Time `json:"accepted_at"`
	Admitted   bool      `json:"admitted"`
}

// InboxReleaseStatus is how an accepted item's task did in the latest
// experiment that ran it.
type InboxReleaseStatus struct {
	Status     string     `json:"status"` // passes | fails | not_run
	Experiment string     `json:"experiment,omitempty"`
	At         *time.Time `json:"at,omitempty"`
	Passed     int        `json:"passed"`
	Runs       int        `json:"runs"`
}

// InboxSourceStatus is one configured source and its last pull.
type InboxSourceStatus struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	LastPull  *time.Time `json:"last_pull,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	Pulled    int        `json:"pulled,omitempty"`
}

// InboxSuite is a suite an item can be accepted into.
type InboxSuite struct {
	ID       string `json:"id"`
	Template string `json:"template"`
}

// InboxList is GET /inbox.
type InboxList struct {
	Items           []InboxItem         `json:"items"`
	Counts          map[string]int      `json:"counts"`
	Sources         []InboxSourceStatus `json:"sources"`
	Suites          []InboxSuite        `json:"suites"`
	IngestEnabled   bool                `json:"ingest_enabled"`
	RegressionTasks int                 `json:"regression_tasks"` // tasks under the benchmarks folder with provenance.incident
}

// InboxItemDetail is GET /inbox/{id}.
type InboxItemDetail struct {
	Item    InboxItem `json:"item"`
	Preview string    `json:"preview"` // redacted transcript preview, plain text
}

// InboxAcceptRequest is POST /inbox/{id}/accept.
type InboxAcceptRequest struct {
	Suite  string `json:"suite"`
	Kind   string `json:"kind,omitempty"`
	TaskID string `json:"task_id,omitempty"`
}

// InboxRejectRequest is POST /inbox/{id}/reject.
type InboxRejectRequest struct {
	Reason      string `json:"reason"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

// InboxSnoozeRequest is POST /inbox/{id}/snooze.
type InboxSnoozeRequest struct {
	Days int `json:"days"`
}

// InboxDraft is the task draft an accept wrote.
type InboxDraft struct {
	Task     string   `json:"task"` // suite/id
	Dir      string   `json:"dir"`
	Template string   `json:"template"`
	EditMe   []string `json:"edit_me"`
	Next     []string `json:"next"`
	Warnings []string `json:"warnings,omitempty"`
}

// InboxAcceptResult answers an accept.
type InboxAcceptResult struct {
	Item  InboxItem  `json:"item"`
	Draft InboxDraft `json:"draft"`
}

// InboxActionResult answers a reject or a snooze.
type InboxActionResult struct {
	Item InboxItem `json:"item"`
}

// InboxIngestResult answers a webhook push.
type InboxIngestResult struct {
	ItemID      string `json:"item_id"`
	Created     bool   `json:"created"`
	Occurrences int    `json:"occurrences"`
}
