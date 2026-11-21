package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/utils"
)

// opLog is a jobs.Logger that keeps the lines.
type opLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *opLog) add(s string)                { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }
func (l *opLog) Printf(f string, a ...any)   { l.add(fmt.Sprintf(f, a...)) }
func (l *opLog) Warnf(f string, a ...any)    { l.add("WARN " + fmt.Sprintf(f, a...)) }
func (l *opLog) Errorf(f string, a ...any)   { l.add("ERROR " + fmt.Sprintf(f, a...)) }
func (l *opLog) Write(p []byte) (int, error) { l.add(string(p)); return len(p), nil }
func (l *opLog) text() string                { l.mu.Lock(); defer l.mu.Unlock(); return strings.Join(l.lines, "\n") }

func TestEvidenceVerifyJobPasses(t *testing.T) {
	_, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n")
	cfg, err := loadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	logger := utils.NewStdLogger(&strings.Builder{}, false)
	ctx := context.Background()
	log := &opLog{}
	if _, err := runEvidenceVerb(ctx, cfg, logger, "mirror", operateParams{}, log); err != nil {
		t.Fatalf("mirror job: %v\n%s", err, log.text())
	}
	res, err := runEvidenceVerb(ctx, cfg, logger, "verify", operateParams{Deep: true}, log)
	if err != nil {
		t.Fatalf("verify job: %v\n%s", err, log.text())
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "e_smoke_sdoj") {
		t.Fatalf("verify result does not name the experiment: %s", b)
	}
}

func TestEvidenceJobNoMirrorSaysWhatToDo(t *testing.T) {
	_, config := evidenceSandbox(t, "")
	cfg, err := loadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runEvidenceVerb(context.Background(), cfg, utils.NewStdLogger(&strings.Builder{}, false), "verify", operateParams{}, &opLog{})
	if err == nil || !strings.Contains(err.Error(), "storage.mirror_dir") {
		t.Fatalf("want a hint about storage.mirror_dir, got %v", err)
	}
}

func TestOperateParamsValidation(t *testing.T) {
	specs := map[string]jobKindSpec{}
	jobKindsMu.Lock()
	for _, s := range jobKinds {
		specs[s.Kind] = s
	}
	jobKindsMu.Unlock()
	for _, k := range []string{kindOperateDoctor, kindGPUCheck, kindVoiceCheck, kindVisualCapture, kindVisualCompare,
		kindSimulatorSpread, kindEvidenceVerify, kindEvidenceMirror, kindEvidenceArchive, kindEvidenceBackup, kindEvidenceRestore, kindK8sStatus} {
		s, ok := specs[k]
		if !ok {
			t.Fatalf("kind %s not registered", k)
		}
		if s.Hosted != (k == kindOperateDoctor) {
			t.Errorf("%s: hosted=%v", k, s.Hosted)
		}
	}
	bad := map[string]string{
		kindVoiceCheck:      `{}`,
		kindVisualCompare:   `{"suite":"s","task":"t","image":"../etc/passwd.png"}`,
		kindVisualCapture:   `{"desktop":"a","browser":"b"}`,
		kindSimulatorSpread: `{"a":"x","b":"-config=/etc"}`,
		kindEvidenceRestore: `{}`,
		kindEvidenceVerify:  `{"exp":"../x"}`,
		kindGPUCheck:        `{"suite":"a b"}`,
		kindK8sStatus:       `{"unknown":1}`,
	}
	for k, raw := range bad {
		if _, _, err := specs[k].Validate(context.Background(), jobEnv{}, json.RawMessage(raw)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s %s: want invalid params, got %v", k, raw, err)
		}
	}
	good := map[string]string{
		kindVoiceCheck:      `{"suite":"voice-core"}`,
		kindVisualCompare:   `{"suite":"s","task":"t","image":"uploads/shot.png"}`,
		kindVisualCapture:   `{"browser":"pw","url":"https://example.com"}`,
		kindSimulatorSpread: `{"a":"e_1","b":"e_2"}`,
		kindEvidenceRestore: `{"exp":"e_smoke_sdoj"}`,
		kindEvidenceVerify:  `{}`,
		kindOperateDoctor:   ``,
	}
	for k, raw := range good {
		if _, _, err := specs[k].Validate(context.Background(), jobEnv{}, json.RawMessage(raw)); err != nil {
			t.Errorf("%s %s: %v", k, raw, err)
		}
	}
}
