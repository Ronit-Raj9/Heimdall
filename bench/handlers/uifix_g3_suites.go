package handlers

import (
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// minedBy is the provenance tool name `tasks mine` writes into task.yaml
// (ui components/quickstart/quickstart.utils.ts MINED_BY).
const minedBy = "tasks mine"

// SuiteTaskRef names one task: its id and title.
type SuiteTaskRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SuiteLightFacts are the task facts a light list entry carries (UI fix
// wave G3, M4-34) so the leaderboard and quickstart need no full list:
// redacted tasks (the held-out board is offered only when none is), mined
// tasks, the admitted ones among them, and the tasks whose drafted
// instruction nobody reviewed. All are absent on a partial entry, whose
// task list is not read yet.
type SuiteLightFacts struct {
	RedactedTasks      *int           `json:"redacted_tasks,omitempty"`
	MinedTasks         *int           `json:"mined_tasks,omitempty"`
	MinedAdmittedTasks *int           `json:"mined_admitted_tasks,omitempty"`
	UnreviewedTasks    []SuiteTaskRef `json:"unreviewed_tasks,omitempty"`
}

// lightFacts counts the facts from a full entry's tasks; nil for a partial
// entry or one that failed to load.
func lightFacts(s *schemas.SuiteSummary) *SuiteLightFacts {
	if s.Partial || s.Error != "" {
		return nil
	}
	var redacted, mined, admitted int
	unreviewed := []SuiteTaskRef{}
	for _, t := range s.Tasks {
		if t.Redacted {
			redacted++
		}
		if t.Provenance == nil {
			continue
		}
		if t.Provenance.CreatedBy == minedBy {
			mined++
			if t.Admitted {
				admitted++
			}
		}
		if r := t.Provenance.InstructionReviewed; r != nil && !*r {
			unreviewed = append(unreviewed, SuiteTaskRef{ID: t.ID, Title: t.Title})
		}
	}
	f := &SuiteLightFacts{RedactedTasks: &redacted, MinedTasks: &mined, MinedAdmittedTasks: &admitted}
	if len(unreviewed) > 0 {
		f.UnreviewedTasks = unreviewed
	}
	return f
}

// SuiteListEntry is one entry of the light or paged suite list: the summary
// with, on the light list, its task facts.
type SuiteListEntry struct {
	schemas.SuiteSummary
	*SuiteLightFacts
}

// SuiteListAnswer is GET /suites with fields=light or with limit, offset or
// q: the entries (q matches the suite id and description) and, when paged,
// the paging.
type SuiteListAnswer struct {
	Suites      []SuiteListEntry `json:"suites"`
	ReservedIDs []string         `json:"reserved_ids,omitempty"`
	*ListPage
}

func suiteListAnswer(l schemas.SuiteList, facts []*SuiteLightFacts, p listPaging) SuiteListAnswer {
	entries := make([]SuiteListEntry, len(l.Suites))
	for i, s := range l.Suites {
		entries[i] = SuiteListEntry{SuiteSummary: s}
		if i < len(facts) {
			entries[i].SuiteLightFacts = facts[i]
		}
	}
	out := SuiteListAnswer{Suites: entries, ReservedIDs: l.ReservedIDs}
	if p.on {
		rows, page := pageItems(p, entries, func(e SuiteListEntry) []string { return []string{e.ID, e.Description} })
		out.Suites, out.ListPage = rows, &page
	}
	return out
}

// SuiteTaskPage is GET /suites/{id}/task-list: one page of the suite's tasks in
// the suite page's order, the number that matched q, and partial when the
// suite's task list is still loading (no tasks yet; ask again).
type SuiteTaskPage struct {
	Suite   string              `json:"suite"`
	Version string              `json:"version"`
	Tasks   []schemas.SuiteTask `json:"tasks"`
	Partial bool                `json:"partial,omitempty"`
	ListPage
}

// getSuiteTasks handles GET /api/bench/suites/{id}/task-list?q=&limit=&offset=
// (UI fix wave G3, M6-1 and M2-112a): the tasks GET /suites/{id} embeds, one
// page at a time. q matches the task id and title, ignoring case; limit
// defaults to 100 (at most 500). The suite checks are GET /suites/{id}'s.
func (h *SuitesHandler) getSuiteTasks(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		sendUnavailable(ctx, h.mode, "suite loader")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return
	}
	p, ok := readListPaging(ctx, h.mode)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	sum, ok := h.oneSummary(ctx, work, id)
	if !ok {
		return
	}
	rows, page := pageItems(p, sum.Tasks, func(t schemas.SuiteTask) []string { return []string{t.ID, t.Title} })
	if rows == nil {
		rows = []schemas.SuiteTask{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, SuiteTaskPage{Suite: sum.ID, Version: sum.Version, Tasks: rows, Partial: sum.Partial, ListPage: page})
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/task-list", Tag: "suites",
			Summary: "One page of a suite's tasks, searched by id and title",
			Query:   SuiteTaskListQueryDocs, Response: SuiteTaskPage{}},
	)
}

// SuiteTaskListQueryDocs describes the query of GET /suites/{id}/task-list;
// the same limit, offset and q page the other lists (ListQueryDocs).
var SuiteTaskListQueryDocs = ListQueryDocs

// ListQueryDocs describes the paging parameters every paged list takes.
var ListQueryDocs = []QueryDoc{
	{Name: "limit", Description: "Rows per page; default 100, at most 500. Giving limit, offset or q answers total, limit and offset"},
	{Name: "offset", Description: "Rows to skip; default 0"},
	{Name: "q", Description: "Case-insensitive text the id, name or title contains"},
}
