package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// Web parity lane L19, Integrations:
//   - integrations.export: writes an experiment's OTLP traces, EEE records or
//     run table, or a suite's Harbor tar (verifiers withheld), to the project's
//     export folder for download from /integrations. In-process, no container,
//     so it runs on local and hosted servers alike.
//   - experiment-finished webhooks: a jobs finish hook dispatches
//     experiment.finished (and job.failed) events to the org's webhooks.

const kindIntegrationsExport = "integrations.export"

// maxExportBytes caps one export file.
const maxExportBytes = 128 << 20

var exportIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type integrationsExportParams struct {
	Format         string `json:"format"`
	ExpID          string `json:"exp_id,omitempty"`
	Suite          string `json:"suite,omitempty"`
	Payloads       bool   `json:"payloads,omitempty"`        // otlp: tool arguments and results, redacted and capped
	AllowSynthetic bool   `json:"allow_synthetic,omitempty"` // eee: export synthetic arms, labelled
}

// integrationsExportResult is the job's result: the file to download.
type integrationsExportResult struct {
	File        string `json:"file"`
	Format      string `json:"format"`
	Bytes       int    `json:"bytes"`
	DownloadURL string `json:"download_url"`
}

func (p integrationsExportParams) validate() (string, error) {
	if !slices.Contains(handlers.IntegrationExportFormats, p.Format) {
		return "", fmt.Errorf("format must be one of otlp, eee, jsonl, csv, harbor")
	}
	if p.Format == "harbor" {
		if !exportIDRe.MatchString(p.Suite) {
			return "", errors.New("harbor export needs suite: a suite id")
		}
		if p.ExpID != "" || p.Payloads || p.AllowSynthetic {
			return "", errors.New("harbor export takes suite only")
		}
		return "suite:" + p.Suite, nil
	}
	if !exportIDRe.MatchString(p.ExpID) {
		return "", errors.New("this export needs exp_id: an experiment id")
	}
	if p.Suite != "" {
		return "", errors.New("suite is only for the harbor format")
	}
	return expTarget(p.ExpID), nil
}

var exportExt = map[string]string{"otlp": ".otlp.json", "eee": ".eee.jsonl", "jsonl": ".runs.jsonl", "csv": ".runs.csv", "harbor": ".harbor.tar.gz"}

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindIntegrationsExport, Local: true, Hosted: true,
		Opts: []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p integrationsExportParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			target, err := p.validate()
			if err != nil {
				return nil, "", invalid(err)
			}
			return p, target, nil
		},
		Run: runIntegrationsExport,
	})
	serviceHooks = append(serviceHooks, rememberWebhookOrg)
	jobs.OnFinish(dispatchJobWebhooks)
}

func runIntegrationsExport(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p integrationsExportParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	if _, err := p.validate(); err != nil {
		return nil, err
	}
	if env.App == nil {
		return nil, errors.New("no project is loaded on this server")
	}
	src := serveExport{a: env.App}
	var body []byte
	var err error
	id := p.ExpID
	switch p.Format {
	case "otlp":
		log.Printf("exporting sealed runs of %s as OTLP/JSON traces", p.ExpID)
		var traces []exportedTrace
		traces, err = exportOTLP(ctx, env.App.store.Root(), p.ExpID, "", traceexport.Options{Payloads: p.Payloads, ServiceVersion: benchVersion}, log)
		if err == nil && len(traces) == 0 {
			err = fmt.Errorf("experiment %s has no sealed public run to export yet; wait for runs to finish", p.ExpID)
		}
		if err == nil {
			var buf bytes.Buffer
			err = writeMerged(&buf, traces)
			body = buf.Bytes()
			log.Printf("%d runs exported", len(traces))
		}
	case "harbor":
		id = p.Suite
		log.Printf("exporting admitted public tasks of suite %s as Harbor task folders (verifiers withheld)", p.Suite)
		body, err = src.SuiteHarbor(ctx, p.Suite)
	default:
		log.Printf("exporting %s of %s", p.Format, p.ExpID)
		body, _, err = src.ExperimentExport(ctx, p.ExpID, p.Format, p.AllowSynthetic)
	}
	if err != nil {
		return nil, err
	}
	if len(body) > maxExportBytes {
		return nil, fmt.Errorf("export is %d bytes, over the %d byte limit; export from the CLI instead", len(body), maxExportBytes)
	}
	name := fmt.Sprintf("%s-%s%s", id, time.Now().UTC().Format("20060102-150405"), exportExt[p.Format])
	if err := writeExportFile(env.ProjectRoot, name, body); err != nil {
		return nil, err
	}
	log.Printf("wrote %s (%d bytes); download it from Integrations > Exports", name, len(body))
	return integrationsExportResult{File: name, Format: p.Format, Bytes: len(body),
		DownloadURL: handlers.APIPrefix + "/integrations/exports/" + name}, nil
}

// writeExportFile writes atomically and keeps the newest MaxExportFiles files.
func writeExportFile(projectRoot, name string, body []byte) error {
	dir := handlers.IntegrationsExportDir(projectRoot)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	if xs, err := handlers.ListIntegrationExports(projectRoot); err == nil && len(xs) > handlers.MaxExportFiles {
		for _, x := range xs[handlers.MaxExportFiles:] {
			_ = os.Remove(filepath.Join(dir, x.Name))
		}
	}
	return nil
}

// webhookOrg is what the finish hook needs to deliver an org's webhooks.
type webhookOrg struct {
	root   string
	hosted bool
}

var (
	webhookOrgsMu sync.Mutex
	webhookOrgs   = map[string]webhookOrg{}
)

// rememberWebhookOrg records each org's project root when its services are built.
func rememberWebhookOrg(a *app, s *handlers.Services) {
	if a == nil || a.cfg == nil || a.cfg.Root == "" {
		return
	}
	webhookOrgsMu.Lock()
	webhookOrgs[s.OrgID] = webhookOrg{root: a.cfg.Root, hosted: s.Hosted}
	webhookOrgsMu.Unlock()
}

// experimentFinishedData is the data of an experiment.finished event.
type experimentFinishedData struct {
	ExpID      string          `json:"exp_id"`
	JobID      string          `json:"job_id"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// jobFailedData is the data of a job.failed event.
type jobFailedData struct {
	JobID  string `json:"job_id"`
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Error  string `json:"error,omitempty"`
}

// webhookEventFor maps a finished job to the event its org's webhooks get, if any.
func webhookEventFor(j jobs.Job, status jobs.Status, result []byte, msg string) (webhooks.Event, bool) {
	if j.Kind == kindExperimentRun {
		var p expRunParams
		if err := json.Unmarshal(j.Params, &p); err != nil || p.ExpID == "" {
			return webhooks.Event{}, false
		}
		d := experimentFinishedData{ExpID: p.ExpID, JobID: j.ID, Status: string(status), Error: msg, FinishedAt: j.FinishedAt}
		if len(result) > 0 && len(result) <= 8<<10 && json.Valid(result) {
			d.Result = result
		}
		return webhooks.NewEvent(webhooks.EventExperimentFinished, j.OrgID, d), true
	}
	// A schedule.fire job runs its experiment in this process (scheduleRunner,
	// not an experiment.run job): its record names the experiment it ran.
	if j.Kind == kindScheduleFire {
		var rec schedule.Record
		if json.Unmarshal(result, &rec) == nil && rec.Result != nil && rec.Result.ExpID != "" {
			d := experimentFinishedData{ExpID: rec.Result.ExpID, JobID: j.ID, Status: string(status), Error: msg, FinishedAt: j.FinishedAt}
			if len(result) <= 8<<10 {
				d.Result = result
			}
			return webhooks.NewEvent(webhooks.EventExperimentFinished, j.OrgID, d), true
		}
	}
	if status == jobs.StatusFailed {
		return webhooks.NewEvent(webhooks.EventJobFailed, j.OrgID, jobFailedData{JobID: j.ID, Kind: j.Kind, Target: j.Target, Error: msg}), true
	}
	return webhooks.Event{}, false
}

// dispatchJobWebhooks is the jobs finish hook.
func dispatchJobWebhooks(j jobs.Job, status jobs.Status, result []byte, msg string) {
	if ev, ok := scheduleFailedEvent(j, status, result, msg); ok { // Y05 (notify.go)
		deliverOrgEvent(j.OrgID, ev)
	}
	if j.Kind == kindExperimentRun {
		// The experiment may have been an evaluation's last benchmark.
		defer refreshOrgEvaluations(j.OrgID)
	}
	ev, ok := webhookEventFor(j, status, result, msg)
	if !ok {
		return
	}
	deliverWebhookEvent(j.OrgID, ev)
}

// deliverOrgEvent is deliverWebhookEvent plus member email (notify.go).
func deliverOrgEvent(org string, ev webhooks.Event) {
	webhookOrgsMu.Lock()
	o, ok := webhookOrgs[org]
	webhookOrgsMu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	emitOrgEvent(ctx, org, o.root, o.hosted, ev)
}

// deliverWebhookEvent sends ev to the webhooks of org, when its services
// were built in this process.
func deliverWebhookEvent(org string, ev webhooks.Event) {
	webhookOrgsMu.Lock()
	o, ok := webhookOrgs[org]
	webhookOrgsMu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	webhooks.Dispatch(ctx, webhookStore(org, o.root, o.hosted), webhooks.Sender{AllowPrivate: !o.hosted}, ev)
}

// notifyInProcessFinished fires experiment.finished for an experiment the
// launcher ran in this process with no job runner (serve without a
// database): QuickRun, the builder and Start all land there, and no
// experiment.run job ends to trigger the finish hook.
func notifyInProcessFinished(org, expID string, result any, err error) {
	d := experimentFinishedData{ExpID: expID, Status: string(jobs.StatusSucceeded)}
	now := time.Now().UTC()
	d.FinishedAt = &now
	if err != nil {
		d.Status, d.Error = string(jobs.StatusFailed), err.Error()
	}
	if b, merr := json.Marshal(result); merr == nil && result != nil && len(b) <= 8<<10 {
		d.Result = b
	}
	deliverWebhookEvent(org, webhooks.NewEvent(webhooks.EventExperimentFinished, org, d))
}
