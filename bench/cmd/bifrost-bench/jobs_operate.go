package main

// Web parity lane L20 (Operate): the /operate page's job kinds. Doctor, GPU
// and voice checks, visual capture and compare, simulator spread, the
// evidence verbs and the Kubernetes status. Every kind is local only except
// operate.doctor, whose hosted variant reports the org-safe subset (the
// organization's suites and agents, no server internals).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Operate job kinds.
const (
	kindOperateDoctor   = "operate.doctor"
	kindGPUCheck        = "gpu.check"
	kindVoiceCheck      = "voice.check"
	kindVisualCapture   = "visual.capture"
	kindVisualCompare   = "visual.compare"
	kindSimulatorSpread = "simulator.spread"
	kindEvidenceVerify  = "evidence.verify"
	kindEvidenceMirror  = "evidence.mirror"
	kindEvidenceArchive = "evidence.archive"
	kindEvidenceBackup  = "evidence.backup"
	kindEvidenceRestore = "evidence.restore"
	kindK8sStatus       = "k8s.status"
)

// operateID is the shape of a suite, task, experiment or container name an
// operate job accepts: no paths, no flags, no shell.
var operateID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$`)

// operateParams is the union of the operate kinds' params; each kind reads
// the fields it needs.
type operateParams struct {
	Suite     string `json:"suite,omitempty"`
	Task      string `json:"task,omitempty"`
	Image     string `json:"image,omitempty"` // visual.compare: project-relative png/jpg
	A         string `json:"a,omitempty"`     // simulator.spread
	B         string `json:"b,omitempty"`
	Condition string `json:"condition,omitempty"`
	Exp       string `json:"exp,omitempty"` // evidence verbs
	Deep      bool   `json:"deep,omitempty"`
	Repair    bool   `json:"repair,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Days      *int   `json:"days,omitempty"`
	NoDB      bool   `json:"no_db,omitempty"`
	Desktop   string `json:"desktop,omitempty"` // visual.capture
	Browser   string `json:"browser,omitempty"`
	URL       string `json:"url,omitempty"`
	Out       string `json:"out,omitempty"` // visual.capture: file name under uploads/visual/
}

func parseOperateParams(raw json.RawMessage) (operateParams, error) {
	var p operateParams
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("params: %v", err)
	}
	return p, nil
}

func operateCheckID(field, v string, required bool) error {
	if v == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if !operateID.MatchString(v) {
		return fmt.Errorf("%s %q is not a valid name (letters, digits, . _ @ -)", field, v)
	}
	return nil
}

// projectRel checks a project-relative file path: no absolute path, no "..".
func projectRel(field, v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if filepath.IsAbs(v) || strings.HasPrefix(v, "-") {
		return "", fmt.Errorf("%s must be a path inside the project", field)
	}
	clean := filepath.Clean(v)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("%s must be a path inside the project", field)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" || part == ".heimdall" {
			return "", fmt.Errorf("%s may not point into %s", field, part)
		}
	}
	return clean, nil
}

func operateInvalid(err error) error { return fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err) }

// operateCLI is a cliJob whose args come from operateParams.
func operateCLI(kind string, build func(p operateParams) ([]string, error)) jobKindSpec {
	return cliJob(kind, true, false, func(raw json.RawMessage) ([]string, error) {
		p, err := parseOperateParams(raw)
		if err != nil {
			return nil, err
		}
		return build(p)
	})
}

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindOperateDoctor, Local: true, Hosted: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			if _, err := parseOperateParams(raw); err != nil {
				return nil, "", operateInvalid(err)
			}
			return map[string]any{}, "doctor", nil
		},
		Run: runOperateDoctor,
	})
	registerJobKind(jobKindSpec{
		Kind: kindGPUCheck, Local: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			p, err := parseOperateParams(raw)
			if err == nil {
				err = operateCheckID("suite", p.Suite, false)
			}
			if err != nil {
				return nil, "", operateInvalid(err)
			}
			return p, "gpu", nil
		},
		Run: runGPUCheckJob,
	})
	registerJobKind(operateCLI(kindVoiceCheck, func(p operateParams) ([]string, error) {
		if err := operateCheckID("suite", p.Suite, true); err != nil {
			return nil, err
		}
		return []string{"voice", "check", "-suite", p.Suite, "-json"}, nil
	}))
	registerJobKind(operateCLI(kindVisualCompare, func(p operateParams) ([]string, error) {
		if err := operateCheckID("suite", p.Suite, true); err != nil {
			return nil, err
		}
		if err := operateCheckID("task", p.Task, true); err != nil {
			return nil, err
		}
		img, err := projectRel("image", p.Image)
		if err != nil {
			return nil, err
		}
		if ext := strings.ToLower(filepath.Ext(img)); ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			return nil, errors.New("image must be a .png or .jpg file")
		}
		return []string{"visual", "compare", "-image", img, "-json", p.Suite + "/" + p.Task}, nil
	}))
	registerJobKind(operateCLI(kindVisualCapture, func(p operateParams) ([]string, error) {
		if (p.Desktop == "") == (p.Browser == "") {
			return nil, errors.New("set exactly one of desktop or browser (a running container name)")
		}
		if err := operateCheckID("desktop", p.Desktop, false); err != nil {
			return nil, err
		}
		if err := operateCheckID("browser", p.Browser, false); err != nil {
			return nil, err
		}
		name := p.Out
		if name == "" {
			name = "capture.png"
		}
		if err := operateCheckID("out", name, true); err != nil || !strings.HasSuffix(strings.ToLower(name), ".png") {
			return nil, errors.New("out must be a plain .png file name, for example capture.png")
		}
		args := []string{"visual", "capture", "-out", filepath.Join("uploads", "visual", name)}
		if p.Desktop != "" {
			args = append(args, "-desktop", p.Desktop)
		} else {
			args = append(args, "-browser", p.Browser)
			if p.URL != "" {
				if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
					return nil, errors.New("url must start with http:// or https://")
				}
				args = append(args, "-url", p.URL)
			}
		}
		return args, nil
	}))
	registerJobKind(operateCLI(kindSimulatorSpread, func(p operateParams) ([]string, error) {
		if err := operateCheckID("a", p.A, true); err != nil {
			return nil, err
		}
		if err := operateCheckID("b", p.B, true); err != nil {
			return nil, err
		}
		if err := operateCheckID("condition", p.Condition, false); err != nil {
			return nil, err
		}
		args := []string{"simulator", "spread", "-a", p.A, "-b", p.B, "-json"}
		if p.Condition != "" {
			args = append(args, "-condition", p.Condition)
		}
		return args, nil
	}))
	registerJobKind(operateCLI(kindK8sStatus, func(p operateParams) ([]string, error) {
		return []string{"k8s", "check"}, nil
	}))
	for _, sub := range []string{"verify", "mirror", "archive", "backup", "restore"} {
		registerJobKind(evidenceJob(sub))
	}
}

// runOperateDoctor runs the preflight checks. Hosted: only the org's suites
// and agents, never docker, networks, images or the gateway.
func runOperateDoctor(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	if env.App == nil {
		return nil, errors.New("no app for this organization")
	}
	var checks []schemas.DoctorCheck
	if env.Hosted {
		_, suiteRows := env.App.doctorSuites(ctx)
		_, agentRows := loadDoctorAgents(env.App.cfg.Root)
		checks = append(append(checks, suiteRows...), agentRows...)
	} else {
		checks = env.App.doctor(ctx)
	}
	return doctorResult(checks, log), nil
}

// doctorResult logs each check and counts the statuses.
func doctorResult(checks []schemas.DoctorCheck, log jobs.Logger) map[string]any {
	counts := map[string]int{}
	for _, c := range checks {
		counts[string(c.Status)]++
		line := fmt.Sprintf("%-5s %-22s %s", strings.ToUpper(string(c.Status)), c.ID, c.Detail)
		if c.Status == schemas.DoctorFail {
			log.Warnf("%s", line)
		} else {
			log.Printf("%s", line)
		}
	}
	return map[string]any{"checks": checks, "counts": counts, "ok": counts[string(schemas.DoctorFail)] == 0}
}

// runGPUCheckJob is `gpu check` in process.
func runGPUCheckJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	p, err := parseOperateParams(j.Params)
	if err != nil {
		return nil, err
	}
	a := env.App
	if a == nil {
		return nil, errors.New("no app for this organization")
	}
	if a.cfg.GPU.Mode == lib.GPUModeOff {
		log.Printf("gpu.mode is off: GPU tasks are refused at plan time on this machine")
		return map[string]any{"mode": lib.GPUModeOff}, nil
	}
	img := a.cfg.Images.Helper
	if img == "" {
		return nil, errors.New("no image for the container check: set images.helper in the bench config")
	}
	rep := gpu.Check(ctx, a.docker, a.cfg.GPU.SMI, a.cfg.GPU.Mode, a.cfg.GPU.Devices, img)
	if p.Suite != "" {
		s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), p.Suite)
		if err != nil {
			return nil, fmt.Errorf("load suite %s: %v", p.Suite, err)
		}
		for _, t := range s.Tasks {
			if t.GPUCount() == 0 {
				continue
			}
			fit := gpu.RequestFit{Task: t.ID, Need: fmt.Sprintf("%d GPU(s), %d MiB each", t.GPUCount(), t.Resources.GPU.MinVRAMMB), Fits: true}
			if err := gpu.Fits(rep.Selected, t.Resources.GPU); err != nil {
				fit.Fits, fit.Error = false, err.Error()
				rep.Problems = append(rep.Problems, "task "+t.ID+" "+err.Error())
			}
			rep.Requests = append(rep.Requests, fit)
		}
	}
	for _, pr := range rep.Problems {
		log.Warnf("problem: %s", pr)
	}
	if len(rep.Problems) == 0 {
		log.Printf("gpu check passed")
	}
	if len(rep.Problems) > 0 {
		return rep, fmt.Errorf("gpu check found %d problem(s)", len(rep.Problems))
	}
	return rep, nil
}

// evidenceJob runs one `evidence <sub>` verb in process for the local org,
// with the server's own config (the S3 keys stay in this process). The JSON
// report is logged and becomes the job result.
func evidenceJob(sub string) jobKindSpec {
	kind := "evidence." + sub
	validate := func(p operateParams) error {
		if err := operateCheckID("exp", p.Exp, sub == "restore"); err != nil {
			return err
		}
		if p.Days != nil && (*p.Days < 0 || *p.Days > 3650) {
			return errors.New("days must be between 0 and 3650")
		}
		return nil
	}
	return jobKindSpec{
		Kind: kind, Local: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			p, err := parseOperateParams(raw)
			if err == nil {
				err = validate(p)
			}
			if err != nil {
				return nil, "", operateInvalid(err)
			}
			target := "evidence"
			if p.Exp != "" {
				target = p.Exp
			}
			return p, target, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			p, err := parseOperateParams(j.Params)
			if err != nil {
				return nil, err
			}
			if err := validate(p); err != nil {
				return nil, err
			}
			if env.App == nil {
				return nil, errors.New("no app for this organization")
			}
			return runEvidenceVerb(ctx, env.App.cfg, env.App.logger, sub, p, log)
		},
	}
}

// runEvidenceVerb checks what the CLI would fail on first (so the job log
// says why), then runs the verb with -json into a buffer.
func runEvidenceVerb(ctx context.Context, cfg *lib.Config, logger schemas.Logger, sub string, p operateParams, log jobs.Logger) (any, error) {
	m, err := openEvidenceMirror(cfg, "", logger)
	if err != nil {
		return nil, fmt.Errorf("evidence mirror: %v (set storage.mirror_dir or storage.s3 in the bench config)", err)
	}
	org := evidencestore.LocalOrgID
	if sub != "backup" {
		if _, err := evidencestore.FindOrg(evidenceRunsRoot(cfg), org, logger); err != nil {
			return nil, fmt.Errorf("runs folder: %v", err)
		}
	}
	log.Printf("evidence %s: mirror %s", sub, m.Blob.String())
	f := evidenceFlags{org: org, exp: p.Exp, deep: p.Deep, repair: p.Repair, dryRun: p.DryRun, noDB: p.NoDB, json: true, days: -1}
	if p.Days != nil {
		f.days = *p.Days
	}
	var buf bytes.Buffer
	e := &evidenceEnv{cfg: cfg, logger: logger, f: f, out: &buf}
	var code int
	switch sub {
	case "verify":
		code = e.verifyCmd(ctx)
	case "mirror":
		code = e.mirrorCmd(ctx)
	case "archive":
		code = e.archiveCmd(ctx)
	case "backup":
		code = e.backupCmd(ctx)
	case "restore":
		code = e.restoreCmd(ctx)
	default:
		return nil, fmt.Errorf("unknown evidence verb %s", sub)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != "" {
			log.Printf("%s", line)
		}
	}
	var result any
	if b := bytes.TrimSpace(buf.Bytes()); len(b) > 0 {
		if err := json.Unmarshal(b, &result); err != nil {
			result = map[string]any{"output": string(b)}
		}
	}
	if code != exitOK {
		if sub == "verify" && code == 1 {
			return result, errors.New("evidence verify found differences between the mirror and the local runs (see the log)")
		}
		return result, fmt.Errorf("evidence %s failed with exit code %s; the server log has the details", sub, strconv.Itoa(code))
	}
	return result, nil
}
