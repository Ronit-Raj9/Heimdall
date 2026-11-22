package main

// MCP tools of the Add benchmark flow (lane Z20): a coding agent can turn
// a file or a link into a checked benchmark. They call /api/v1/onboard.
// This file sorts after mcpserve.go, so its init adds to the table that
// file's init made.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

var onboardInputSchema = obj(map[string]any{
	"url":        str("A link: an https file, a Hugging Face dataset, a GitHub repository, or a Harbor id such as terminal-bench@2.0"),
	"data":       str("Pasted rows (CSV, JSON Lines or tickets) instead of a link"),
	"name":       str("File name of the pasted data, for its format (faq.csv)"),
	"upload":     str("A path under uploads/ from an earlier upload"),
	"connection": str("Saved secret id holding a token for the source (gated Hugging Face datasets)"),
})

func init() {
	if mcpToolTable == nil {
		mcpToolTable = map[string]mcpTool{}
	}
	mcpToolTable["inspect_source"] = mcpTool{
		description: "Say what an input is and what is inside, before anything is written: kind, units, five redacted samples, columns, licence, warnings, cost estimate and a suggested plan. No model call.",
		schema:      obj(map[string]any{"input": onboardInputSchema, "source": str("Read it with this source; empty picks the best match")}, "input"),
		readOnly:    true,
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var args struct {
				Input  json.RawMessage `json:"input"`
				Source string          `json:"source"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || len(args.Input) == 0 {
				return nil, errors.New("input is required")
			}
			var det, inv map[string]any
			if err := s.api.postJSON(ctx, "/onboard/detect", map[string]any{"input": args.Input}, &det); err != nil {
				return nil, err
			}
			if err := s.api.postJSON(ctx, "/onboard/inspect", map[string]any{"input": args.Input, "source": args.Source}, &inv); err != nil {
				return map[string]any{"matches": det["matches"], "next": det["next"], "error": err.Error()}, nil
			}
			return map[string]any{"matches": det["matches"], "inventory": inv}, nil
		},
	}
	mcpToolTable["draft_benchmark"] = mcpTool{
		description: "Draft tasks from an input into a staging area (nothing joins a benchmark yet). Pass the plan from inspect_source, edited (suite is the benchmark id). Returns the draft: each task's instruction, checks in plain sentences, controls and gate results.",
		schema: obj(map[string]any{
			"input": onboardInputSchema,
			"plan":  map[string]any{"type": "object", "description": "The suggested plan from inspect_source; suite names the benchmark (new or existing)"},
		}, "input", "plan"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var args struct {
				Input json.RawMessage `json:"input"`
				Plan  json.RawMessage `json:"plan"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || len(args.Input) == 0 || len(args.Plan) == 0 {
				return nil, errors.New("input and plan are required")
			}
			var out map[string]any
			return out, s.api.postJSON(ctx, "/onboard/drafts", map[string]any{"input": args.Input, "plan": args.Plan}, &out)
		},
	}
	mcpToolTable["run_readiness"] = mcpTool{
		description: "Run the readiness checks on a draft in the real sandbox: each task's reference must pass and its empty answer, wrong answers and exploit must fail. With wait true, waits up to wait_minutes and returns each task's result and control catch rate.",
		schema: obj(map[string]any{
			"draft":        str("Draft id from draft_benchmark"),
			"task":         str("One task; empty checks every task"),
			"wait":         map[string]any{"type": "boolean", "description": "Wait for the checks to finish"},
			"wait_minutes": map[string]any{"type": "integer", "description": "How long to wait, default 20, at most 120"},
		}, "draft"),
		run: runReadinessTool,
	}
	mcpToolTable["publish_benchmark"] = mcpTool{
		description: "Publish a draft's accepted, ready tasks privately into its benchmark: the canary is stamped on held-out tasks and readiness records are carried into the benchmark's lock. include_unready also publishes tasks readiness did not admit (they stay not ready).",
		schema: obj(map[string]any{
			"draft":           str("Draft id"),
			"tasks":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only these tasks; empty: every accepted, ready task"},
			"include_unready": map[string]any{"type": "boolean"},
		}, "draft"),
		run: func(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
			var args struct {
				Draft          string   `json:"draft"`
				Tasks          []string `json:"tasks"`
				IncludeUnready bool     `json:"include_unready"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || args.Draft == "" {
				return nil, errors.New("draft is required")
			}
			var started map[string]any
			if err := s.api.postJSON(ctx, "/onboard/drafts/"+url.PathEscape(args.Draft)+"/publish",
				map[string]any{"tasks": args.Tasks, "include_unready": args.IncludeUnready}, &started); err != nil {
				return nil, err
			}
			deadline := time.Now().Add(2 * time.Minute)
			for {
				var set map[string]any
				if err := s.api.getJSON(ctx, "/onboard/drafts/"+url.PathEscape(args.Draft), nil, &set); err != nil {
					return nil, err
				}
				if p, ok := set["published"].(map[string]any); ok && p["job_id"] != nil && p["job_id"] != "" {
					return map[string]any{"published": p}, nil
				}
				if time.Now().After(deadline) {
					return map[string]any{"job": started["job"], "note": "publishing is still running; read the draft again"}, nil
				}
				if err := sleepCtx(ctx, s.poll); err != nil {
					return nil, err
				}
			}
		},
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		d = time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runReadinessTool(ctx context.Context, s *mcpServer, raw json.RawMessage) (any, error) {
	var args struct {
		Draft       string `json:"draft"`
		Task        string `json:"task"`
		Wait        bool   `json:"wait"`
		WaitMinutes int    `json:"wait_minutes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Draft == "" {
		return nil, errors.New("draft is required")
	}
	body := map[string]any{}
	if args.Task != "" {
		body["task"] = args.Task
	}
	var started struct {
		Job map[string]any `json:"job"`
	}
	path := "/onboard/drafts/" + url.PathEscape(args.Draft)
	if err := s.api.postJSON(ctx, path+"/ready", body, &started); err != nil {
		return nil, err
	}
	if !args.Wait {
		return map[string]any{"job": started.Job, "next": "call run_readiness again later with wait, or read the draft"}, nil
	}
	mins := args.WaitMinutes
	if mins <= 0 {
		mins = 20
	}
	if mins > 120 {
		mins = 120
	}
	jobID, _ := started.Job["id"].(string)
	deadline := time.Now().Add(time.Duration(mins) * time.Minute)
	for {
		data, _, err := s.api.do(ctx, http.MethodGet, path, nil, nil)
		if err != nil {
			return nil, err
		}
		var set struct {
			Readiness *struct {
				JobID  string `json:"job_id"`
				Status string `json:"status"`
			} `json:"readiness"`
			Tasks []map[string]any `json:"tasks"`
		}
		if err := json.Unmarshal(data, &set); err != nil {
			return nil, err
		}
		if r := set.Readiness; r != nil && r.JobID == jobID && (r.Status == "done" || r.Status == "failed") {
			var full map[string]any
			_ = json.Unmarshal(data, &full)
			rows := make([]map[string]any, 0, len(set.Tasks))
			for _, t := range set.Tasks {
				rows = append(rows, map[string]any{"task": t["id"], "review": t["review"], "held_out": t["held_out"],
					"ready":      t["readiness"] != nil && t["readiness"].(map[string]any)["admitted"] == true,
					"catch_rate": t["catch_rate"], "reason": t["reason"]})
			}
			return map[string]any{"readiness": full["readiness"], "tasks": rows}, nil
		}
		if time.Now().After(deadline) {
			return map[string]any{"job": started.Job, "note": "the checks are still running"}, nil
		}
		if err := sleepCtx(ctx, s.poll); err != nil {
			return nil, err
		}
	}
}
