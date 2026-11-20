package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// B1: Bifrost's agent gateway for a2a runs (docs/a2a-agents.md). Verified on
// maximhq/bifrost:v2.5.1 on 18 Nov 2026: POST/GET/DELETE /api/agents for
// registrations, GET /api/agents/history (agent_logs) and
// /api/agents/history/{id} for the recorded request and response, and the
// agent_correlation_id filter of GET /api/logs and GET /api/mcp-logs.

var _ schemas.A2AGateway = (*Gateway)(nil)

// a2aHistoryPage is the page size of agent log reads (Bifrost caps it at 1000).
const a2aHistoryPage = 500

// RegisterA2A implements schemas.A2AGateway. The registration is enabled,
// allows only the listed keys, and either forwards the caller's credential
// or sends RuntimeKey as the agent's x-bf-vk header.
func (g *Gateway) RegisterA2A(ctx context.Context, r schemas.A2ARegistrationSpec) error {
	body := map[string]any{
		"name":                        r.Name,
		"agent_card_url":              r.CardURL,
		"enabled":                     true,
		"allow_by_default":            false,
		"virtual_key_ids":             r.VirtualKeyIDs,
		"forward_accepted_credential": r.Forward && r.RuntimeKey == "",
	}
	if r.RuntimeKey != "" {
		body["runtime_auth"] = map[string]any{"type": "headers", "advanced": true, "headers": map[string]string{"x-bf-vk": r.RuntimeKey}}
	}
	if err := g.call(ctx, http.MethodPost, "/api/agents", body, nil); err != nil {
		return fmt.Errorf("register agent %s: %w", r.Name, err)
	}
	return nil
}

// GetA2A implements schemas.A2AGateway.
func (g *Gateway) GetA2A(ctx context.Context, name string) (*schemas.A2ARegistrationInfo, error) {
	var out schemas.A2ARegistrationInfo
	if err := g.call(ctx, http.MethodGet, "/api/agents/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, fmt.Errorf("read agent %s: %w", name, err)
	}
	return &out, nil
}

// DeleteA2A implements schemas.A2AGateway. A registration already gone is
// not an error.
func (g *Gateway) DeleteA2A(ctx context.Context, name string) error {
	err := g.call(ctx, http.MethodDelete, "/api/agents/"+url.PathEscape(name), nil, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete agent %s: %w", name, err)
	}
	return nil
}

// historyRow is an agent log row as GET /api/agents/history returns it.
type historyRow struct {
	schemas.A2ALogRow
	ErrorDetails *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"error_details,omitempty"`
}

// A2AHistory implements schemas.A2AGateway. Request rows are read again by
// id for their recorded response body (the list omits it). A row of another
// registration is dropped even if the gateway returned it.
func (g *Gateway) A2AHistory(ctx context.Context, agents []string, since time.Time) ([]schemas.A2ALogRow, error) {
	if len(agents) == 0 {
		return nil, nil
	}
	var out []schemas.A2ALogRow
	for offset := 0; ; offset += a2aHistoryPage {
		q := url.Values{
			"agent_name": {strings.Join(agents, ",")},
			"start_time": {since.UTC().Add(-time.Second).Format(time.RFC3339Nano)},
			"limit":      {strconv.Itoa(a2aHistoryPage)},
			"offset":     {strconv.Itoa(offset)},
			"order":      {"asc"},
		}
		var page struct {
			Logs []historyRow `json:"logs"`
		}
		if err := g.call(ctx, http.MethodGet, "/api/agents/history?"+q.Encode(), nil, &page); err != nil {
			return nil, fmt.Errorf("read agent log: %w", err)
		}
		for _, r := range page.Logs {
			if !slices.Contains(agents, r.AgentName) {
				continue
			}
			row := r.A2ALogRow
			if r.ErrorDetails != nil && r.ErrorDetails.Error != nil {
				row.Error = r.ErrorDetails.Error.Message
			}
			if row.RecordKind == "request" {
				var detail struct {
					RequestBody  string `json:"request_body"`
					ResponseBody string `json:"response_body"`
				}
				if err := g.call(ctx, http.MethodGet, "/api/agents/history/"+url.PathEscape(row.ID), nil, &detail); err == nil {
					row.RequestBody, row.ResponseBody = detail.RequestBody, detail.ResponseBody
				}
			}
			out = append(out, row)
		}
		if len(page.Logs) < a2aHistoryPage {
			return out, nil
		}
	}
}

// correlatedRow is the part of an LLM or MCP log row the witness reads.
type correlatedRow struct {
	ID                 string `json:"id"`
	AgentCorrelationID string `json:"agent_correlation_id"`
	VirtualKeyID       string `json:"virtual_key_id"`
}

// A2ACorrelated implements schemas.A2AGateway.
func (g *Gateway) A2ACorrelated(ctx context.Context, taskIDs []string, since time.Time) ([]schemas.A2ALinkedRow, error) {
	var out []schemas.A2ALinkedRow
	var errs []error
	for _, task := range taskIDs {
		for _, src := range []struct{ kind, path string }{{"llm", "/api/logs"}, {"mcp", "/api/mcp-logs"}} {
			q := url.Values{
				"agent_correlation_id": {task},
				"start_time":           {since.UTC().Add(-time.Second).Format(time.RFC3339)},
				"limit":                {"500"},
			}
			var page struct {
				Logs []correlatedRow `json:"logs"`
			}
			if err := g.call(ctx, http.MethodGet, src.path+"?"+q.Encode(), nil, &page); err != nil {
				errs = append(errs, fmt.Errorf("read %s rows of task %s: %w", src.kind, task, err))
				continue
			}
			for _, r := range page.Logs {
				if r.AgentCorrelationID != task { // never trust a filter the gateway may ignore
					continue
				}
				out = append(out, schemas.A2ALinkedRow{Kind: src.kind, ID: r.ID, TaskID: task, VirtualKeyID: r.VirtualKeyID})
			}
		}
	}
	return out, errors.Join(errs...)
}

// a2aRunName reads the run id of a bench a2a registration name
// (utils.A2ARegistrationName): "hb-r-<id>-<member>". A name whose run id
// was cut is not matched, so it is never taken for another run's.
var a2aRunName = regexp.MustCompile(`^hb-r-([a-z0-9]+)-[a-z0-9][a-z0-9-]*$`)

// listA2ANames lists every a2a registration name; none on a gateway
// without the agent endpoints.
func (g *Gateway) listA2ANames(ctx context.Context) ([]string, error) {
	var page struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	err := g.call(ctx, http.MethodGet, "/api/agents", nil, &page)
	if isStatus(err, http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	names := make([]string, 0, len(page.Agents))
	for _, a := range page.Agents {
		names = append(names, a.Name)
	}
	return names, nil
}

// removeStaleA2A deletes bench a2a registrations of runs that are not
// active, for the janitor (3-A-04): a crashed run, a failed DELETE or a
// POST whose answer was lost leaves one behind.
func (g *Gateway) removeStaleA2A(ctx context.Context, names []string, listErr error, active map[string]bool) []error {
	if listErr != nil {
		return []error{fmt.Errorf("remove stale a2a registrations: %w", listErr)}
	}
	var errs []error
	for _, name := range names {
		m := a2aRunName.FindStringSubmatch(name)
		if m == nil || active["r_"+m[1]] {
			continue
		}
		if err := g.DeleteA2A(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("remove stale a2a registration: %w", err))
			continue
		}
		g.logger.Info("stale a2a registration deleted", "name", name)
	}
	return errs
}
