package handlers

// Web parity lane L17: schedules and webhook alerts from the website.
//
// The org's web schedules live in policies/schedules.yaml (schedule.File:
// file-level alerts plus schedules). Alerts never hold a URL or a secret:
// url_env and secret_env name either an org secret (keystore Kind secret,
// matched by label) or, on a local server only, an environment variable.
// The server ticker (cmd/bifrost-bench/jobs_canary.go) enqueues a
// schedule.fire job at each cron time. A hosted server fires them too: the
// job plans and starts the experiment through the org launcher, so it runs
// on runners (cmd/bifrost-bench/schedules_hosted.go), under the hosted
// guardrails of schedules_hosted.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// WebSchedulesFile is the org's web schedules file inside the policies area.
const WebSchedulesFile = "schedules.yaml"

// KindScheduleFire is the job kind that runs one schedule now.
const KindScheduleFire = "schedule.fire"

// MaxSchedules bounds one org's schedules file.
const MaxSchedules = 100

// ScheduleExperimentLoader reads a schedule's experiment file for its spend
// cap; cmd/bifrost-bench sets it to its strict loader (canary files too). Nil
// skips the cap checks that need the experiment.
var ScheduleExperimentLoader schedule.ExperimentLoader

// ScheduleExtras adds schedules that come from elsewhere (scheduled
// canaries) to a loaded file; cmd/bifrost-bench sets it. The extras are
// listed read-only and never saved back.
var ScheduleExtras func(root string, f *schedule.File)

// WebSchedulesPath is the absolute path of the org's web schedules file.
func WebSchedulesPath(root string) string {
	return filepath.Join(root, string(projectfs.AreaPolicies), WebSchedulesFile)
}

// LoadWebSchedules reads the org's web schedules file; missing is empty.
func LoadWebSchedules(root string) (*schedule.File, error) {
	return schedule.Load(WebSchedulesPath(root))
}

// parseSchedules decodes a schedules file strictly (unknown keys refused).
func parseSchedules(data []byte) (*schedule.File, error) {
	f := &schedule.File{}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse schedules: %w", err)
	}
	return f, nil
}

func init() {
	projectfs.RegisterValidator(projectfs.AreaPolicies, WebSchedulesFile, func(_ projectfs.Area, _ string, data []byte) error {
		f, err := parseSchedules(data)
		if err != nil {
			return err
		}
		return checkScheduleShape(f)
	})
	RegisterOrgArea("schedules", func(env OrgEnv, r *router.Router) {
		h := &schedulesHandler{env: env}
		r.GET(APIPrefix+"/schedules", env.R(h.get))
		r.PUT(APIPrefix+"/schedules", env.W(h.put))
		r.POST(APIPrefix+"/schedules/test-alert", env.W(h.testAlert))
		r.POST(APIPrefix+"/schedules/{name}/run", env.W(h.runNow))
	})
}

// checkScheduleShape checks what needs no project files: size, names, cron,
// alerts.
func checkScheduleShape(f *schedule.File) error {
	if len(f.Schedules) > MaxSchedules {
		return fmt.Errorf("at most %d schedules per organization", MaxSchedules)
	}
	var errs []error
	for _, a := range f.Alerts {
		if err := a.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("alerts: %w", err))
		}
	}
	seen := map[string]bool{}
	for _, s := range f.Schedules {
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("schedule %q appears more than once", s.Name))
		}
		seen[s.Name] = true
		if s.MaxUSDPerDay < 0 || math.IsNaN(s.MaxUSDPerDay) || math.IsInf(s.MaxUSDPerDay, 0) {
			errs = append(errs, fmt.Errorf("schedule %s: max_usd_per_day must be a finite, non-negative number", s.Name))
		}
		// Check the project-free parts (name, cron, timezone, fail_on,
		// alerts) through the job-schedule path, which stats no files.
		c := s
		if c.Job == "" {
			c.Job = schedule.JobInboxPull
		}
		c.Experiment, c.Evaluation, c.Policy, c.MaxUSDPerDay = "", "", "", 0
		if err := c.Validate("", nil); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type schedulesHandler struct{ env OrgEnv }

// ScheduleRow is one schedule as the UI lists it.
type ScheduleRow struct {
	schedule.Schedule
	Kind     string           `json:"kind"`              // experiment, evaluation, canary or job
	Source   string           `json:"source"`            // web (editable) or canary (from canaries/*.yaml)
	Next     string           `json:"next,omitempty"`    // next fire, RFC 3339
	Last     *schedule.Record `json:"last,omitempty"`    // last trigger
	Problem  string           `json:"problem,omitempty"` // why it would not run now
	SpentUSD float64          `json:"spent_today_usd"`
}

// SchedulesResponse is GET /schedules.
type SchedulesResponse struct {
	Mode    schemas.Mode    `json:"mode"`
	File    schedule.File   `json:"file"`
	YAML    string          `json:"yaml"`
	Rows    []ScheduleRow   `json:"rows"`
	Events  []string        `json:"events"`
	Secrets []string        `json:"secrets"` // org secret labels usable as url_env / secret_env
	Hosted  bool            `json:"hosted"`
	Ticker  bool            `json:"ticker"`           // schedule.fire can run on this server
	Limits  *ScheduleLimits `json:"limits,omitempty"` // hosted guardrails
}

// SchedulesPut is PUT /schedules: the whole file, as JSON or YAML text.
type SchedulesPut struct {
	File *schedule.File `json:"file,omitempty"`
	YAML string         `json:"yaml,omitempty"`
}

// TestAlertRequest is POST /schedules/test-alert.
type TestAlertRequest struct {
	URLEnv    string `json:"url_env"`
	SecretEnv string `json:"secret_env,omitempty"`
	Format    string `json:"format,omitempty"`
}

// TestAlertResponse is the outcome of a test delivery.
type TestAlertResponse struct {
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
}

// ScheduleRunResponse is POST /schedules/{name}/run.
type ScheduleRunResponse struct {
	JobID string `json:"job_id"`
	Kind  string `json:"kind"`
}

// ScheduleStateDir is where web schedule records and deliveries live, under
// the project's hidden folder (never served by projectfs).
func ScheduleStateDir(root string) string {
	return filepath.Join(root, ".heimdall", "schedules")
}

func (h *schedulesHandler) root() string { return h.env.Cfg.Root }

func (h *schedulesHandler) get(ctx *fasthttp.RequestCtx) {
	root := h.root()
	f, err := LoadWebSchedules(root)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			err.Error()+"; fix it in the YAML editor or in Files > policies/schedules.yaml")
		return
	}
	raw, _ := os.ReadFile(WebSchedulesPath(root))
	resp := SchedulesResponse{Mode: h.env.Mode, File: *f, YAML: string(raw), Events: alerts.Events,
		Hosted: h.env.Services.Hosted, Ticker: h.env.Services.Jobs != nil && slices.Contains(h.env.Services.Jobs.Kinds(), KindScheduleFire)}
	if resp.File.Schedules == nil {
		resp.File.Schedules = []schedule.Schedule{}
	}
	if resp.Hosted {
		l := HostedScheduleLimits()
		resp.Limits = &l
	}
	full := &schedule.File{Alerts: f.Alerts, Schedules: slices.Clone(f.Schedules)}
	if ScheduleExtras != nil {
		ScheduleExtras(root, full)
	}
	last, _ := schedule.LastRecords(ScheduleStateDir(root))
	now := time.Now()
	for i, s := range full.Schedules {
		row := ScheduleRow{Schedule: s, Kind: scheduleKind(s), Source: "web"}
		if i >= len(f.Schedules) {
			row.Source = "canary"
		}
		if c, err := schedule.ParseCron(s.Cron); err == nil {
			if loc, err := s.Location(); err == nil {
				if n := c.Next(now.In(loc)); !n.IsZero() {
					row.Next = n.Format(time.RFC3339)
				}
			}
		}
		if rec, ok := last[s.Name]; ok {
			rec := rec
			row.Last = &rec
		}
		row.SpentUSD, _ = schedule.SpentToday(ScheduleStateDir(root), s, now)
		if err := s.Validate(root, ScheduleExperimentLoader); err != nil {
			row.Problem = err.Error()
		} else if resp.Hosted {
			if err := CheckHostedSchedule(s, now); err != nil {
				row.Problem = err.Error()
			}
		}
		resp.Rows = append(resp.Rows, row)
	}
	if resp.Rows == nil {
		resp.Rows = []ScheduleRow{}
	}
	resp.Secrets = []string{}
	if ks := h.env.Services.Keys; ks != nil {
		if keys, err := ks.List(ctx, h.env.Services.OrgID, keystore.KindSecret); err == nil {
			for _, k := range keys {
				resp.Secrets = append(resp.Secrets, k.Label)
			}
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, resp)
}

// scheduleKind names what a schedule runs.
func scheduleKind(s schedule.Schedule) string {
	switch {
	case s.Job != "":
		return "job"
	case s.Evaluation != "":
		return "evaluation"
	case IsCanaryExperiment(s.Experiment):
		return "canary"
	default:
		return "experiment"
	}
}

// IsCanaryExperiment reports whether a schedule's experiment names a canary
// definition (canaries/<name>.yaml); CanaryName returns that name.
func IsCanaryExperiment(p string) bool {
	return filepath.Base(filepath.Dir(filepath.FromSlash(p))) == "canaries" && filepath.Ext(p) == ".yaml"
}

// CanaryName returns the canary a canary schedule runs.
func CanaryName(p string) string {
	return strings.TrimSuffix(filepath.Base(filepath.FromSlash(p)), ".yaml")
}

func (h *schedulesHandler) put(ctx *fasthttp.RequestCtx) {
	var in SchedulesPut
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON {file} or {yaml}: "+err.Error())
		return
	}
	f := in.File
	if in.YAML != "" {
		var err error
		if f, err = parseSchedules([]byte(in.YAML)); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
			return
		}
	}
	if f == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "send the schedules file as file or yaml")
		return
	}
	if err := checkScheduleShape(f); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err := f.Validate(h.root(), ScheduleExperimentLoader); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if h.env.Services.Hosted {
		full := &schedule.File{Alerts: f.Alerts, Schedules: slices.Clone(f.Schedules)}
		if ScheduleExtras != nil {
			ScheduleExtras(h.root(), full)
		}
		if err := CheckHostedSchedules(full, time.Now()); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
			return
		}
	}
	for _, s := range f.Schedules {
		if strings.HasPrefix(s.Name, "canary-") {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
				"schedule "+s.Name+": names starting with canary- are reserved; schedule a canary from its own page")
			return
		}
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	data = append([]byte("# Web schedules (Schedules page). Alerts name org secrets or environment\n# variables only; never put a webhook URL or a secret in this file.\n"), data...)
	if err := h.write(ctx, data); err != nil {
		sendProjectErr(ctx, h.env.Mode, err)
		return
	}
	h.get(ctx)
}

// write saves the file through projectfs when it is wired, else directly.
func (h *schedulesHandler) write(ctx *fasthttp.RequestCtx, data []byte) error {
	actor := auth.PrincipalFrom(ctx).UserID
	if ps := h.env.Services.Projects; ps != nil {
		_, err := ps.Write(ctx, projectfs.AreaPolicies, WebSchedulesFile, data, projectfs.WriteOpts{Actor: actor, Note: "schedules page"})
		return err
	}
	p := WebSchedulesPath(h.root())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := parseSchedules(data)
	if err != nil {
		return err
	}
	return schedule.Save(p, f)
}

func sendProjectErr(ctx *fasthttp.RequestCtx, mode schemas.Mode, err error) {
	switch {
	case errors.Is(err, projectfs.ErrInvalid):
		lib.SendError(ctx, mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, projectfs.ErrConflict):
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error()+"; reload the page and try again")
	case errors.Is(err, projectfs.ErrTooLarge):
		lib.SendError(ctx, mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, err.Error())
	default:
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save schedules: "+err.Error())
	}
}

func (h *schedulesHandler) runNow(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("name").(string)
	if h.env.Services.Jobs == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the job runner is not available on this server; start serve without -offline")
		return
	}
	f, err := LoadWebSchedules(h.root())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if ScheduleExtras != nil {
		ScheduleExtras(h.root(), f)
	}
	s, ok := f.Find(name)
	if !ok {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "schedule "+name+" not found")
		return
	}
	if h.env.Services.Hosted {
		if err := CheckHostedSchedule(s, time.Now()); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
			return
		}
	}
	kind, params := FireJobFor(s, h.env.Services.Hosted)
	if !slices.Contains(h.env.Services.Jobs.Kinds(), kind) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"this server cannot run "+kind+" jobs: start serve without -offline, or ask your server owner")
		return
	}
	p := auth.PrincipalFrom(ctx)
	by := p.UserID
	if by == "" {
		by = string(p.Kind)
	}
	j, err := h.env.Services.Jobs.Create(ctx, jobs.NewJob{OrgID: h.env.Services.OrgID, Kind: kind, Params: params, CreatedBy: by})
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ScheduleRunResponse{JobID: j.ID, Kind: kind})
}

// FireJobFor is the job a schedule trigger queues: schedule.fire, local and
// hosted. The hosted schedule.fire runs canaries and experiments on runners
// through the org launcher, under the same overlap lock, daily cap and
// alerts as local.
func FireJobFor(s schedule.Schedule, _ bool) (string, json.RawMessage) {
	b, _ := json.Marshal(map[string]string{"name": s.Name})
	return KindScheduleFire, b
}

func (h *schedulesHandler) testAlert(ctx *fasthttp.RequestCtx) {
	var in TestAlertRequest
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON {url_env, secret_env, format}")
		return
	}
	c := alerts.Config{URLEnv: in.URLEnv, SecretEnv: in.SecretEnv, Format: in.Format, TimeoutS: 5}
	if err := c.Validate(); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	tctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sender := NewAlertSender(tctx, h.env.Services, ScheduleStateDir(h.root()), true)
	p := alerts.Payload{Event: alerts.EventScheduleFailed, Schedule: "test",
		Message: "Test alert from Heimdall: your webhook is wired. No schedule failed."}
	resp := TestAlertResponse{Delivered: true}
	if err := sender.Send(tctx, []alerts.Config{c}, p); err != nil {
		resp.Delivered, resp.Error = false, err.Error()
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, resp)
}

// NewAlertSender returns a webhook sender whose url_env / secret_env names
// resolve to org secrets (by label) first and, on a local server only, to
// environment variables. A hosted server only sends https to a public host.
// fast makes retries immediate (the test button).
func NewAlertSender(ctx context.Context, s Services, stateDir string, fast bool) *alerts.Sender {
	_ = os.MkdirAll(stateDir, 0o755)
	opts := []alerts.SenderOption{alerts.WithGetenv(AlertGetenv(ctx, s))}
	if fast {
		opts = append(opts, alerts.WithSleep(func(ctx context.Context, _ time.Duration) error { return ctx.Err() }))
	}
	return alerts.NewSender(filepath.Join(stateDir, schedule.DeliveriesFile), opts...)
}

// AlertGetenv resolves a variable name for alerts (see NewAlertSender).
func AlertGetenv(ctx context.Context, s Services) func(string) string {
	return func(name string) string {
		v := ""
		if s.Keys != nil {
			if keys, err := s.Keys.List(ctx, s.OrgID, keystore.KindSecret); err == nil {
				for _, k := range keys {
					if k.Label == name {
						v, _ = s.Keys.Reveal(ctx, s.OrgID, k.ID)
						break
					}
				}
			}
		}
		if v == "" && !s.Hosted {
			v = os.Getenv(name)
		}
		if v != "" && s.Hosted && strings.Contains(v, "://") && !publicHTTPS(v) {
			return "" // treated as unset: hosted alerts go to public https hosts only
		}
		return v
	}
}

// publicHTTPS reports whether raw is an https URL to a host that is not a
// loopback, private or link-local address literal or localhost.
func publicHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsLinkLocalMulticast())
	}
	return true
}
