package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B2 MCP through Bifrost (docs/bifrost-features.md). For a run whose arm
// sets gateway.bifrost.mcp, the bench creates one Bifrost MCP client per MCP
// server of the task, pointed at the run's tool proxy, grants it to the run's
// own MCP key only, and removes both at cleanup. The agent reaches the server
// at Bifrost's per-server endpoint, /mcp/<slug>, with that MCP key. The MCP
// key is not the run's model key on purpose: on v2.5.1 a key that is granted
// a Code Mode client also puts that client's tools within reach of Bifrost's
// agent mode on the key's model requests, and agent mode always
// auto-executes listToolFiles and readToolFile, so Bifrost ran them inside the
// agent's model call and looped the model itself, unseen by the agent
// (checked on the hd-b2 stack, 18 Nov 2026). With a separate MCP key, which
// has no provider and so cannot make model calls, the run's model requests
// carry no MCP grant and stay the agent's own.
// Every call shape below was checked against maximhq/bifrost:v2.5.1 on 18 Nov
// 2026: client names may not contain hyphens; the slug is the name with
// underscores as hyphens, lower case; a key without the client's grant gets
// 403 on its endpoint; deleting a client also deletes the keys' grants of
// it; PUT of a key with only mcp_configs keeps its provider configs.
const (
	pathMCPClient = "/api/mcp/client"
	pathMCPLogs   = "/api/mcp-logs"
	pathVKs       = "/api/governance/virtual-keys"

	mcpLogsPage = 200 // rows per page of GET /api/mcp-logs
)

// mcpClientNameRe is what a bench client name may contain: Bifrost refuses
// hyphens, and a Code Mode client's name is a Python identifier in the
// programs the model writes.
var mcpClientNameRe = regexp.MustCompile(`[^a-z0-9_]+`)

// MCPClientName is the Bifrost MCP client name of service svc in run runID:
// the service id folded to [a-z0-9_] and the run id, which keeps names unique
// across concurrent runs and lets cleanup find a run's clients.
func MCPClientName(svc, runID string) string {
	s := strings.Trim(mcpClientNameRe.ReplaceAllString(strings.ToLower(svc), "_"), "_")
	if s == "" {
		s = "mcp"
	}
	return s + "_" + mcpClientNameRe.ReplaceAllString(strings.ToLower(runID), "_")
}

// MCPKeyName is the name of a run's MCP key. It never looks like a run id,
// so the stale-key janitor leaves it to the run's own cleanup.
func MCPKeyName(runID string) string { return "hm-mcp-" + runID }

// MCPSlug is the endpoint slug Bifrost v2.5.1 derives from a client name.
func MCPSlug(name string) string { return strings.ReplaceAll(strings.ToLower(name), "_", "-") }

// mcpClientRow is the part of GET /api/mcp/clients the bench reads.
type mcpClientRow struct {
	Config struct {
		ClientID     string `json:"client_id"`
		Name         string `json:"name"`
		EndpointSlug string `json:"endpoint_slug"`
		IsCodeMode   bool   `json:"is_code_mode_client"`
		Connection   struct {
			Value string `json:"value"`
		} `json:"connection_string"`
	} `json:"config"`
	Tools []json.RawMessage `json:"tools"`
	State string            `json:"state"`
}

// listMCPClients reads every MCP client page.
func (g *Gateway) listMCPClients(ctx context.Context) ([]mcpClientRow, error) {
	var out []mcpClientRow
	for offset := 0; ; {
		var page struct {
			Clients    []mcpClientRow `json:"clients"`
			TotalCount int            `json:"total_count"`
		}
		if err := g.call(ctx, http.MethodGet, pathMCPClients+"?limit=100&offset="+strconv.Itoa(offset), nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Clients...)
		offset += len(page.Clients)
		if len(page.Clients) == 0 || offset >= page.TotalCount {
			return out, nil
		}
	}
}

// ServeMCP creates the run's Bifrost MCP clients, one per upstream (the
// tool proxy route of a task service, as Bifrost reaches it), as Code Mode
// clients when codeMode is set, and the run's MCP key, granted all of them
// and only them. A failure removes whatever was created. It returns the
// clients with the slug the agent must use, and the MCP key.
func (g *Gateway) ServeMCP(ctx context.Context, rc *schemas.RunContext, ups []schemas.MCPUpstream, codeMode bool) ([]schemas.BifrostMCPClient, *schemas.RunKey, error) {
	if rc == nil || rc.Run == nil || rc.Key == nil || rc.Key.ID == "" {
		return nil, nil, errors.New("serve mcp: the run has no virtual key")
	}
	runID := rc.Run.RunID
	var clients []schemas.BifrostMCPClient
	fail := func(err error) ([]schemas.BifrostMCPClient, *schemas.RunKey, error) {
		if rerr := g.RemoveMCP(context.WithoutCancel(ctx), runID); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return nil, nil, err
	}
	names := map[string]string{}
	for _, u := range ups { // 3-A-10: two services that fold to one client name would share one Bifrost identity
		name := MCPClientName(u.Service, runID)
		if other, dup := names[name]; dup && other != u.Service {
			return nil, nil, fmt.Errorf("serve mcp: services %s and %s both map to MCP client name %s; rename one", other, u.Service, name)
		}
		names[name] = u.Service
	}
	for _, u := range ups {
		name := MCPClientName(u.Service, runID)
		body := map[string]any{
			"name": name, "connection_type": "http", "connection_string": u.URL,
			"tools_to_execute": []string{"*"}, "is_code_mode_client": codeMode,
			"allow_on_all_virtual_keys": false, "allow_by_default": false,
		}
		if err := g.call(ctx, http.MethodPost, pathMCPClient, body, nil); err != nil {
			return fail(fmt.Errorf("create mcp client %s for service %s: %w", name, u.Service, err))
		}
		clients = append(clients, schemas.BifrostMCPClient{Service: u.Service, Name: name, Slug: MCPSlug(name), Upstream: u.URL})
	}
	rows, err := g.listMCPClients(ctx)
	if err != nil {
		return fail(fmt.Errorf("read mcp clients: %w", err))
	}
	grants := make([]map[string]any, 0, len(clients))
	for i := range clients {
		found := false
		for _, r := range rows {
			if r.Config.Name != clients[i].Name {
				continue
			}
			found = true
			if r.Config.EndpointSlug != "" {
				clients[i].Slug = r.Config.EndpointSlug
			}
			clients[i].Tools = len(r.Tools)
			if r.Config.IsCodeMode != codeMode {
				return fail(fmt.Errorf("mcp client %s: bifrost reports is_code_mode_client %t, the arm asked for %t", clients[i].Name, r.Config.IsCodeMode, codeMode))
			}
		}
		if !found {
			return fail(fmt.Errorf("mcp client %s is not listed after create", clients[i].Name))
		}
		if clients[i].Tools == 0 {
			return fail(fmt.Errorf("mcp client %s (service %s): bifrost discovered no tools at %s", clients[i].Name, clients[i].Service, clients[i].Upstream))
		}
		grants = append(grants, map[string]any{"mcp_client_name": clients[i].Name, "tools_to_execute": []string{"*"}})
	}
	var out struct {
		VirtualKey virtualKey `json:"virtual_key"`
	}
	body := map[string]any{"name": MCPKeyName(runID), "is_active": true, "mcp_configs": grants,
		"description": "Heimdall MCP key of run " + runID + " (MCP through Bifrost); deleted at run cleanup"}
	if err := g.call(ctx, http.MethodPost, pathVKs, body, &out); err != nil {
		return fail(fmt.Errorf("create the run's mcp key: %w", err))
	}
	if out.VirtualKey.ID == "" || out.VirtualKey.Value == "" {
		return fail(errors.New("create the run's mcp key: bifrost returned no id or value"))
	}
	key := &schemas.RunKey{ID: out.VirtualKey.ID, Name: out.VirtualKey.Name, Value: out.VirtualKey.Value}
	g.logger.Info("bifrost mcp clients created", "run_id", runID, "mcp_key_id", key.ID, "clients", len(clients), "code_mode", codeMode)
	return clients, key, nil
}

// RemoveMCP deletes the run's MCP key and every MCP client of the run
// (names ending in the run id). Bifrost deletes the key grants with the
// client. Safe to call twice.
func (g *Gateway) RemoveMCP(ctx context.Context, runID string) error {
	var errs []error
	if keys, err := g.listVirtualKeys(ctx); err != nil {
		errs = append(errs, fmt.Errorf("remove mcp key of %s: %w", runID, err))
	} else {
		for _, k := range keys {
			if k.Name != MCPKeyName(runID) || k.ID == "" {
				continue
			}
			if err := g.call(ctx, http.MethodDelete, pathVKs+"/"+url.PathEscape(k.ID), nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				errs = append(errs, fmt.Errorf("delete mcp key %s: %w", k.Name, err))
			}
		}
	}
	rows, err := g.listMCPClients(ctx)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("remove mcp clients of %s: %w", runID, err))...)
	}
	suffix := "_" + mcpClientNameRe.ReplaceAllString(strings.ToLower(runID), "_")
	for _, r := range rows {
		if !strings.HasSuffix(r.Config.Name, suffix) || r.Config.ClientID == "" {
			continue
		}
		if err := g.call(ctx, http.MethodDelete, pathMCPClient+"/"+url.PathEscape(r.Config.ClientID), nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
			errs = append(errs, fmt.Errorf("delete mcp client %s: %w", r.Config.Name, err))
		}
	}
	return errors.Join(errs...)
}

// mcpClientRunRe reads the run id at the end of a bench client name.
var mcpClientRunRe = regexp.MustCompile(`_(r_[a-z0-9]+)$`)

// RemoveStaleMCP deletes the MCP keys and MCP clients of runs that are not
// active, for the janitor: a key named hm-mcp-<run id>, and a client whose
// name ends in _<run id> and whose connection points at that run's tool
// proxy alias (tp-<run id>), so a team's own clients are never touched. It
// returns how many it deleted.
func (g *Gateway) RemoveStaleMCP(ctx context.Context, active map[string]bool) (int, error) {
	var errs []error
	n := 0
	keys, err := g.listVirtualKeys(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list keys: %w", err))
	}
	rows, rowsErr := g.listMCPClients(ctx)
	// 3-A-08: the same liveness as RevokeStale, read after listing: a run
	// holding a live key slot claim keeps its MCP key and clients, and
	// nothing goes when the claims cannot be read.
	active, err = g.withClaimHolders(ctx, active)
	if err != nil {
		return 0, fmt.Errorf("remove stale mcp: nothing removed, the key slot claims of other bench processes could not be read: %w", err)
	}
	for _, k := range keys {
		run, ok := strings.CutPrefix(k.Name, "hm-mcp-")
		if !ok || !runIDPattern.MatchString(run) || active[run] {
			continue
		}
		if err := g.call(ctx, http.MethodDelete, pathVKs+"/"+url.PathEscape(k.ID), nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
			errs = append(errs, fmt.Errorf("delete stale mcp key %s: %w", k.Name, err))
			continue
		}
		g.logger.Info("stale mcp key deleted", "virtual_key_id", k.ID, "name", k.Name)
		n++
	}
	if rowsErr != nil {
		return n, errors.Join(append(errs, fmt.Errorf("list mcp clients: %w", rowsErr))...)
	}
	for _, r := range rows {
		m := mcpClientRunRe.FindStringSubmatch(r.Config.Name)
		if m == nil || active[m[1]] || r.Config.ClientID == "" {
			continue
		}
		u, err := url.Parse(r.Config.Connection.Value)
		if err != nil || u.Hostname() != "tp-"+m[1] {
			continue
		}
		if err := g.call(ctx, http.MethodDelete, pathMCPClient+"/"+url.PathEscape(r.Config.ClientID), nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
			errs = append(errs, fmt.Errorf("delete stale mcp client %s: %w", r.Config.Name, err))
			continue
		}
		g.logger.Info("stale mcp client deleted", "client", r.Config.Name)
		n++
	}
	return n, errors.Join(errs...)
}

// mcpLogRow is the part of one GET /api/mcp-logs row the bench reads.
type mcpLogRow struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ToolName     string    `json:"tool_name"`
	ServerLabel  string    `json:"server_label"`
	VirtualKeyID *string   `json:"virtual_key_id"`
	Status       string    `json:"status"`
	Latency      *float64  `json:"latency"`
	Arguments    any       `json:"arguments"`
}

// MCPCalls reads the tool executions Bifrost logged for the run's MCP key
// (keyID), oldest first, with the service named from the client. Rows of
// other keys are never read: the query filters on the key id. complete is
// false, with the reason, when a page could not be read.
func (g *Gateway) MCPCalls(ctx context.Context, keyID string, clients []schemas.BifrostMCPClient) ([]schemas.BifrostMCPCall, bool, string) {
	if keyID == "" {
		return nil, false, "the run has no mcp key"
	}
	svc := map[string]string{}
	for _, c := range clients {
		svc[c.Name] = c.Service
	}
	var out []schemas.BifrostMCPCall
	for offset := 0; ; {
		var page struct {
			Logs       []mcpLogRow `json:"logs"`
			Pagination struct {
				TotalCount int `json:"total_count"`
			} `json:"pagination"`
		}
		q := url.Values{"virtual_key_ids": {keyID}, "limit": {strconv.Itoa(mcpLogsPage)}, "offset": {strconv.Itoa(offset)}, "sort_by": {"timestamp"}, "order": {"asc"}}
		if err := g.call(ctx, http.MethodGet, pathMCPLogs+"?"+q.Encode(), nil, &page); err != nil {
			return out, false, fmt.Sprintf("read mcp logs: %v", err)
		}
		for _, r := range page.Logs {
			if r.VirtualKeyID == nil || *r.VirtualKeyID != keyID {
				continue
			}
			c := schemas.BifrostMCPCall{ID: r.ID, Timestamp: r.Timestamp, Service: svc[r.ServerLabel], Tool: r.ToolName, Status: r.Status, ArgsSHA256: utils.CanonicalArgsSHA256(r.Arguments)}
			if c.Service == "" {
				c.Service = r.ServerLabel
			}
			if r.Latency != nil {
				c.LatencyMS = *r.Latency
			}
			out = append(out, c)
		}
		offset += len(page.Logs)
		if len(page.Logs) == 0 || offset >= page.Pagination.TotalCount {
			return out, true, ""
		}
	}
}
