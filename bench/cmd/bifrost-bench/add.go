package main

// heimdall add (lane Z20): the CLI twin of the Add benchmark page. It talks
// to a Heimdall server's /api/v1/onboard routes, like `mcp serve`: inspect
// any input, draft it, run the readiness checks, publish.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/scaffold"
)

func init() {
	register("add", "add a benchmark from anything: add <file|folder|url|harbor-id> [-suite id] [-inspect] [-ready] [-publish]; add drafts | show <draft> | ready <draft> | publish <draft>", runAdd)
}

const addUsage = `usage: bifrost-bench add <file|folder|url|name@version> [flags]
       bifrost-bench add inspect <file|folder|url|name@version>
       bifrost-bench add drafts
       bifrost-bench add show <draft>
       bifrost-bench add ready <draft> [-wait 30m]
       bifrost-bench add publish <draft> [-include-unready]

Detects what the input is (CSV, JSONL, tickets, a Hugging Face dataset, a
benchmark folder or zip, a Harbor id), shows what was found, drafts tasks
into a staging area (nothing joins a benchmark yet), runs the readiness
checks (the reference must pass, the empty answer, wrong answers and exploit
must fail) and publishes the ready tasks privately to your organization.

The server is -url (env HEIMDALL_URL); the org API token is read from
HEIMDALL_TOKEN (never a flag).`

// addFlags are the plan flags of `add`.
type addFlags struct {
	suite, source, importer, grader, judge, split, format, connection string
	limit, heldOut                                                    int
	maps                                                              multiFlag
	inspectOnly, ready, publish, unready, jsonOut                     bool
	wait                                                              time.Duration
}

func addClient(base string) (*heimdallAPI, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("-url must be an http(s) server root, got %q", base)
	}
	return &heimdallAPI{base: u.String(), token: strings.TrimSpace(os.Getenv("HEIMDALL_TOKEN")), client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func runAdd(args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	base := fs.String("url", envOr("HEIMDALL_URL", "http://127.0.0.1:8090"), "Heimdall server root (env HEIMDALL_URL)")
	var f addFlags
	fs.StringVar(&f.suite, "suite", "", "benchmark id to draft into (new or an existing one); default suggested from the input")
	fs.StringVar(&f.source, "source", "", "read the input with this source (a detect match: rows, huggingface, benchmark, ...)")
	fs.StringVar(&f.importer, "importer", "", "benchmark format of a folder or zip (harbor, swebench, ...)")
	fs.StringVar(&f.grader, "grader", "", "rows: answer_match (exact answer) or judge (rubric)")
	fs.StringVar(&f.judge, "judge", "", "rows graded by the rubric judge: provider/model to pin")
	fs.StringVar(&f.split, "split", "", "Hugging Face: config/split to read")
	fs.StringVar(&f.format, "format", "", "rows: csv, jsonl or tickets (default from the file)")
	fs.StringVar(&f.connection, "connection", "", "saved secret id holding a token for the source (a gated Hugging Face dataset)")
	fs.IntVar(&f.limit, "limit", 0, "at most this many tasks (default: the suggestion)")
	fs.IntVar(&f.heldOut, "held-out", -1, "percent of tasks held out (default 25 for your own data, 0 for public data)")
	fs.Var(&f.maps, "map", "rows: field=column, repeated (question=inputs.q, expected=outputs.answer, rubric=..., wrong_answers=...)")
	fs.BoolVar(&f.inspectOnly, "inspect", false, "print what was found and stop: nothing is written")
	fs.BoolVar(&f.ready, "ready", false, "run the readiness checks on the draft and wait for them")
	fs.BoolVar(&f.publish, "publish", false, "publish the ready tasks (implies -ready)")
	fs.BoolVar(&f.unready, "include-unready", false, "publish: also publish accepted tasks readiness did not admit")
	fs.BoolVar(&f.jsonOut, "json", false, "print JSON")
	fs.DurationVar(&f.wait, "wait", 30*time.Minute, "how long -ready and -publish wait")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, renameUsage(addUsage, progName)); fs.PrintDefaults() }
	// Flags may follow the input: parse, take the first positional, parse the rest.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return exitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) == 0 {
		fs.Usage()
		return exitUsage
	}
	api, err := addClient(*base)
	if err != nil {
		return fail("add: %v", err)
	}
	ctx := context.Background()
	out := os.Stdout
	switch pos[0] {
	case "drafts":
		var list struct {
			Drafts []onboard.SetSummary `json:"drafts"`
		}
		if err := api.getJSON(ctx, "/onboard/drafts", nil, &list); err != nil {
			return fail("add drafts: %v", err)
		}
		if f.jsonOut {
			return addJSON(out, list)
		}
		for _, d := range list.Drafts {
			fmt.Fprintf(out, "%-28s %-24s %3d tasks %3d ready  published=%v  %s\n", d.ID, d.Suite, d.Tasks, d.Ready, d.Published, d.Input)
		}
		return exitOK
	case "show", "ready", "publish":
		if len(pos) < 2 {
			fs.Usage()
			return exitUsage
		}
		switch pos[0] {
		case "show":
			set, err := getDraft(ctx, api, pos[1])
			if err != nil {
				return fail("add show: %v", err)
			}
			if f.jsonOut {
				return addJSON(out, set)
			}
			printOnboardDraft(out, set)
			return exitOK
		case "ready":
			return addReady(ctx, api, pos[1], f, out)
		default:
			return addPublish(ctx, api, pos[1], f, out)
		}
	case "inspect":
		if len(pos) < 2 {
			fs.Usage()
			return exitUsage
		}
		f.inspectOnly = true
		pos = pos[1:]
	}
	in, err := addInput(ctx, api, pos[0], f.connection)
	if err != nil {
		return fail("add: %v", err)
	}
	var inv onboard.Inventory
	if err := api.postJSON(ctx, "/onboard/inspect", map[string]any{"input": in, "source": f.source}, &inv); err != nil {
		return fail("add: inspect: %v", err)
	}
	if f.inspectOnly {
		if f.jsonOut {
			return addJSON(out, inv)
		}
		printOnboardInventory(out, &inv)
		return exitOK
	}
	if !f.jsonOut {
		printOnboardInventory(out, &inv)
	}
	if !inv.CanDraft {
		return fail("add: this input cannot be drafted: %s", inv.Next)
	}
	plan, err := addPlan(inv.Suggest, f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: add:", err)
		return exitUsage
	}
	var set onboard.DraftSet
	if err := api.postJSON(ctx, "/onboard/drafts", map[string]any{"input": in, "plan": plan}, &set); err != nil {
		return fail("add: draft: %v", err)
	}
	if !f.jsonOut {
		fmt.Fprintln(out)
		printOnboardDraft(out, &set)
	}
	if !f.ready && !f.publish {
		if f.jsonOut {
			return addJSON(out, set)
		}
		fmt.Fprintf(out, "\nnext: %s add ready %s   (then: %s add publish %s)\n", progName, set.ID, progName, set.ID)
		return exitOK
	}
	if code := addReady(ctx, api, set.ID, f, out); code != exitOK || !f.publish {
		return code
	}
	return addPublish(ctx, api, set.ID, f, out)
}

// addPlan applies the flags to the suggested plan.
func addPlan(p onboard.Plan, f addFlags) (onboard.Plan, error) {
	if f.suite != "" {
		p.Suite = f.suite
	}
	if f.source != "" {
		p.Source = f.source
	}
	if f.importer != "" {
		p.Importer = f.importer
	}
	if f.grader != "" {
		p.Grader = f.grader
	}
	if f.judge != "" {
		prov, model, ok := strings.Cut(f.judge, "/")
		if !ok || prov == "" || model == "" {
			return p, errors.New("-judge wants provider/model, for example openai/gpt-4.1-mini")
		}
		p.Judge = &scaffold.JudgeConfig{Provider: prov, Model: model}
		p.Grader = scaffold.RowGraderJudge
	}
	if f.split != "" {
		p.Split = f.split
	}
	if f.format != "" {
		p.Format = f.format
	}
	if f.limit > 0 {
		p.Limit = f.limit
	}
	if f.heldOut >= 0 {
		p.Hold.Pct = f.heldOut
		if p.Hold.Seed == 0 {
			p.Hold.Seed = 1
		}
	}
	for _, m := range f.maps {
		k, v, ok := strings.Cut(m, "=")
		if !ok || k == "" {
			return p, fmt.Errorf("-map %q: want field=column", m)
		}
		if p.Mapping == nil {
			p.Mapping = map[string]string{}
		}
		p.Mapping[k] = v
	}
	return p, nil
}

// addInput is the input of arg: a local file or folder is uploaded (a
// folder as a zip), anything else is sent as a link or id.
func addInput(ctx context.Context, api *heimdallAPI, arg, connection string) (onboard.Input, error) {
	st, err := os.Stat(arg)
	if err != nil {
		return onboard.Input{URL: arg, Connection: connection}, nil
	}
	name := filepath.Base(arg)
	var data []byte
	if st.IsDir() {
		if data, err = zipFolder(arg); err != nil {
			return onboard.Input{}, err
		}
		name += ".zip"
	} else if data, err = os.ReadFile(arg); err != nil {
		return onboard.Input{}, err
	}
	up, err := uploadRaw(ctx, api, name, data)
	if err != nil {
		return onboard.Input{}, fmt.Errorf("upload %s: %w", arg, err)
	}
	return onboard.Input{Upload: up, Name: name, Connection: connection}, nil
}

func uploadRaw(ctx context.Context, api *heimdallAPI, name string, data []byte) (string, error) {
	u := api.base + "/api/v1/onboard/upload?" + url.Values{"filename": {name}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if api.token != "" {
		req.Header.Set("Authorization", "Bearer "+api.token)
	}
	resp, err := api.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var eb struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(body, &eb) == nil && eb.Error.Message != "" {
			return "", errors.New(eb.Error.Message)
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r struct {
		Upload string `json:"upload"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Upload == "" {
		return "", errors.New("the server did not say where the upload went")
	}
	return r.Upload, nil
}

// zipFolder zips a folder (no .git, no node_modules) in memory.
func zipFolder(dir string) ([]byte, error) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); p != dir && (n == ".git" || n == "node_modules" || n == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name, h.Method = filepath.ToSlash(rel), zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func getDraft(ctx context.Context, api *heimdallAPI, id string) (*onboard.DraftSet, error) {
	var set onboard.DraftSet
	return &set, api.getJSON(ctx, "/onboard/drafts/"+url.PathEscape(id), nil, &set)
}

// addPoll is how often ready and publish poll the draft.
var addPoll = 5 * time.Second

func addReady(ctx context.Context, api *heimdallAPI, id string, f addFlags, out io.Writer) int {
	var started struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if err := api.postJSON(ctx, "/onboard/drafts/"+url.PathEscape(id)+"/ready", map[string]any{}, &started); err != nil {
		return fail("add ready: %v", err)
	}
	if !f.jsonOut {
		fmt.Fprintf(out, "\nreadiness checks started (job %s); waiting up to %s\n", started.Job.ID, f.wait)
	}
	deadline := time.Now().Add(f.wait)
	for {
		set, err := getDraft(ctx, api, id)
		if err != nil {
			return fail("add ready: %v", err)
		}
		r := set.Readiness
		if r != nil && r.JobID == started.Job.ID && (r.Status == onboard.StatusDone || r.Status == onboard.StatusFailed) {
			if f.jsonOut && !f.publish {
				return addJSON(out, set)
			}
			if !f.jsonOut {
				printOnboardDraft(out, set)
			}
			if r.Status == onboard.StatusFailed {
				return fail("add ready: %s", r.Error)
			}
			return exitOK
		}
		if time.Now().After(deadline) {
			return fail("add ready: still running after %s; check later with %s add show %s", f.wait, progName, id)
		}
		time.Sleep(addPoll)
	}
}

func addPublish(ctx context.Context, api *heimdallAPI, id string, f addFlags, out io.Writer) int {
	body := map[string]any{}
	if f.unready {
		body["include_unready"] = true
	}
	var started map[string]any
	if err := api.postJSON(ctx, "/onboard/drafts/"+url.PathEscape(id)+"/publish", body, &started); err != nil {
		return fail("add publish: %v", err)
	}
	deadline := time.Now().Add(f.wait)
	for {
		set, err := getDraft(ctx, api, id)
		if err != nil {
			return fail("add publish: %v", err)
		}
		if p := set.Published; p != nil && p.JobID != "" {
			if f.jsonOut {
				return addJSON(out, set)
			}
			fmt.Fprintf(out, "\npublished %d tasks into %s (private to your organization)", len(p.Tasks), p.Suite)
			if p.HeldOut > 0 {
				fmt.Fprintf(out, ", %d held out with canary %s", p.HeldOut, p.Canary)
			}
			fmt.Fprintln(out)
			if len(p.Recheck) > 0 {
				fmt.Fprintf(out, "%d tasks need the readiness checks on the benchmark: %s\n", len(p.Recheck), strings.Join(p.Recheck, ", "))
			}
			return exitOK
		}
		if time.Now().After(deadline) {
			return fail("add publish: not finished after %s; check with %s add show %s", f.wait, progName, id)
		}
		time.Sleep(addPoll)
	}
}

func addJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func printOnboardInventory(w io.Writer, inv *onboard.Inventory) {
	fmt.Fprintf(w, "found: %s (%s), %d %ss, licence %s\n", inv.Title, inv.Kind, inv.Units, inv.Unit, inv.License)
	if len(inv.Columns) > 0 {
		fmt.Fprintf(w, "columns: %s\n", strings.Join(inv.Columns, ", "))
	}
	for _, s := range inv.Samples {
		var parts []string
		for _, k := range []string{"question", "instruction", "expected", "rubric"} {
			if v := s.Fields[k]; v != "" {
				parts = append(parts, k+": "+oneLine(v, 90))
			}
		}
		fmt.Fprintf(w, "  %-10s %s\n", s.Ref, strings.Join(parts, " | "))
	}
	for _, n := range inv.Needs {
		fmt.Fprintf(w, "needs: %s\n", n)
	}
	for _, x := range inv.Warnings {
		fmt.Fprintf(w, "warning: %s\n", x)
	}
	fmt.Fprintf(w, "plan: benchmark %s, about %d tasks, %d readiness runs, %d judge calls\n",
		inv.Suggest.Suite, inv.Estimate.Tasks, inv.Estimate.ReadinessRuns, inv.Estimate.JudgeCalls)
	if inv.Next != "" {
		fmt.Fprintf(w, "%s\n", inv.Next)
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func printOnboardDraft(w io.Writer, set *onboard.DraftSet) {
	fmt.Fprintf(w, "draft %s into benchmark %s: %d tasks\n", set.ID, set.Suite, len(set.Tasks))
	for _, t := range set.Tasks {
		state := "not checked"
		if t.Readiness != nil {
			state = "NOT READY"
			if t.Ready() {
				state = "READY"
			}
		}
		catch := ""
		if t.CatchRate != nil {
			catch = fmt.Sprintf(" caught %d/%d controls", t.CatchRate.Caught, t.CatchRate.Total)
		}
		held := ""
		if t.HeldOut {
			held = " held out"
		}
		fmt.Fprintf(w, "  %-11s %-40s %s%s%s\n", state, t.ID, t.Review, held, catch)
		for _, g := range t.Gates {
			if !g.OK {
				fmt.Fprintf(w, "              %s: %s\n", g.Gate, g.Message)
			}
		}
		if t.Reason != "" {
			fmt.Fprintf(w, "              %s\n", oneLine(t.Reason, 160))
		}
	}
	for _, p := range set.Problems {
		fmt.Fprintf(w, "  skipped %s: %s\n", p.Ref, p.Message)
	}
	if r := set.Readiness; r != nil {
		fmt.Fprintf(w, "readiness: %s on %s, %d of %d done, %d ready\n", r.Status, r.Where, r.Done, r.Total, r.Admitted)
		if r.Error != "" {
			fmt.Fprintf(w, "readiness error: %s\n", r.Error)
		}
	}
}
