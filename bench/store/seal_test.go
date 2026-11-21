package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// sealedRun creates a run with a few files, including a nested one, and
// seals it.
func sealedRun(t *testing.T, s *FSStore, m *schemas.Manifest, slotID, runID string) (*schemas.Run, *schemas.SealRecord) {
	t.Helper()
	r := mustNewRun(t, s, m, slotID, runID, 1, schemas.OutcomePass)
	mustWrite(t, s, r, "events.jsonl", `{"seq":1}`+"\n")
	mustWrite(t, s, r, "workspace.diff", "--- a\n+++ b\n")
	mustWrite(t, s, r, "evidence/shim.log", "shim\n")
	if err := s.AppendTransition(context.Background(), r, tr(schemas.StageCleanup, schemas.TransitionCompleted)); err != nil {
		t.Fatal(err)
	}
	return r, mustSeal(t, s, r)
}

func TestSealAndVerify(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r, rec := sealedRun(t, s, m, "s_a1", "r_1")
	dir := s.runDir(r)

	b, err := os.ReadFile(filepath.Join(dir, "seal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Hash != utils.SHA256Bytes(b) {
		t.Fatalf("seal hash %s is not sha256 of seal.json", rec.Hash)
	}
	wantFiles := []string{"evidence/shim.log", "events.jsonl", "run.json", "transitions.jsonl", "workspace.diff"}
	if len(rec.Files) != len(wantFiles) {
		t.Fatalf("files = %v, want %v", rec.Files, wantFiles)
	}
	for _, f := range wantFiles {
		want, err := utils.SHA256File(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Files[f] != want {
			t.Fatalf("file %s hash %s, want %s", f, rec.Files[f], want)
		}
	}
	if _, ok := rec.Files["seal.json"]; ok {
		t.Fatal("seal.json lists itself")
	}
	if rec.RunID != "r_1" || rec.ManifestHash != m.Hash || rec.PrevSeal != "" || rec.SealedAt.IsZero() {
		t.Fatalf("record = %+v", rec)
	}
	perms := []struct {
		path string
		want os.FileMode
	}{
		{dir, 0o555},
		{filepath.Join(dir, "evidence"), 0o555},
		{filepath.Join(dir, "events.jsonl"), 0o444},
		{filepath.Join(dir, "evidence", "shim.log"), 0o444},
		{filepath.Join(dir, "seal.json"), 0o444},
	}
	for _, p := range perms {
		info, err := os.Stat(p.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != p.want {
			t.Fatalf("%s mode %v, want %v", p.path, info.Mode().Perm(), p.want)
		}
	}
	if err := s.Verify(ctx, "e_test", "r_1"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := s.Seal(ctx, r); !errors.Is(err, schemas.ErrSealed) {
		t.Fatalf("second seal err = %v, want ErrSealed", err)
	}
	unsealed := mustNewRun(t, s, m, "s_b1", "r_open", 1, schemas.OutcomePass)
	if err := s.Verify(ctx, "e_test", unsealed.RunID); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("verify unsealed err = %v, want ErrNotFound", err)
	}
}

func TestVerifyCanonicalFile(t *testing.T) {
	dir := t.TempDir()
	canonical := []byte(`{"canonical_version":"1.0"}`)
	if err := os.WriteFile(filepath.Join(dir, "canonical.json"), canonical, 0o644); err != nil {
		t.Fatal(err)
	}
	run := &schemas.Run{RunID: "r_1", CanonicalHash: "sha256:" + utils.SHA256Bytes(canonical)}
	if err := verifyCanonicalFile(dir, run); err != nil {
		t.Fatalf("verify canonical: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "canonical.json"), []byte(`{"canonical_version":"2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyCanonicalFile(dir, run); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("tampered canonical err = %v, want ErrSealMismatch", err)
	}
}

func TestSealRejectsUnredactedSecret(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_secret")
	r := mustNewRun(t, s, m, "s_a1", "r_secret", 1, schemas.OutcomePass)
	mustWrite(t, s, r, "events.jsonl", `{"authorization":"Bearer live-secret"}`+"\n")
	if _, err := s.Seal(context.Background(), r); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("seal err = %v, want ErrSealMismatch", err)
	}
}

// The agent's own output is sealed byte for byte: code that only names a
// password or token seals, a real credential format in it does not, and the
// bench's own files keep the strict key-value check.
func TestSealScansAgentOutputForCredentialsOnly(t *testing.T) {
	tests := []struct {
		name, file, body string
		sealed           bool
	}{
		{name: "diff with password assignment", file: "workspace.diff", body: "+password = \"x\"\n+token = get_token()\n", sealed: true},
		{name: "artifact with key-value form", file: "artifacts/report.json", body: `{"api_key":"[redacted]"}`, sealed: true},
		{name: "state with token field", file: "state/desk/tickets.json", body: `{"reset_token":"abc"}`, sealed: true},
		{name: "diff with sk- key", file: "workspace.diff", body: "+OPENAI_API_KEY = \"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789\"\n", sealed: false},
		{name: "state with a leaked canary key", file: "state/desk/notes.txt", body: "ACME_DEPLOY_CREDENTIAL=sk-team-canary-0123456\n", sealed: true},
		// S1-fix: the canaries as probes plant them, with a real nonce, are as
		// long as a real key and must still seal.
		{name: "diff with a leaked team canary", file: "workspace.diff", body: "+ACME_DEPLOY_CREDENTIAL=sk-team-canary-" + utils.NewNonce() + "\n", sealed: true},
		{name: "state with a leaked bench canary", file: "state/mailbox/messages.jsonl", body: `{"body":"key sk-bench-canary-` + utils.NewNonce() + `"}` + "\n", sealed: true},
		{name: "diff with a canary and a real key", file: "workspace.diff", body: "+K=sk-team-canary-" + utils.NewNonce() + "\n+OPENAI_API_KEY = \"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789\"\n", sealed: false},
		{name: "diff with a canary-looking key of the wrong shape", file: "workspace.diff", body: "+K=sk-team-canary-" + utils.NewNonce() + "x\n", sealed: false},
		{name: "artifact with private key", file: "artifacts/id_rsa", body: "-----BEGIN RSA PRIVATE KEY-----\n", sealed: false},
		{name: "bench file with password assignment", file: "events.jsonl", body: "password = \"x\"\n", sealed: false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t, nil)
			m := mustCreateExp(t, s, "e_agentout")
			r := mustNewRun(t, s, m, "s_a1", fmt.Sprintf("r_agentout%d", i), 1, schemas.OutcomePass)
			mustWrite(t, s, r, tt.file, tt.body)
			_, err := s.Seal(context.Background(), r)
			if tt.sealed && err != nil {
				t.Fatalf("seal err = %v, want sealed", err)
			}
			if !tt.sealed && !errors.Is(err, schemas.ErrSealMismatch) {
				t.Fatalf("seal err = %v, want ErrSealMismatch", err)
			}
		})
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		tamper func(t *testing.T, s *FSStore, dir string)
		want   string
	}{
		{name: "one byte changed", want: "file events.jsonl changed", tamper: func(t *testing.T, s *FSStore, dir string) {
			p := filepath.Join(dir, "events.jsonl")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			b[0] ^= 1
			writeTampered(t, p, b)
		}},
		{name: "file added", want: "file extra.txt added", tamper: func(t *testing.T, s *FSStore, dir string) {
			writeTampered(t, filepath.Join(dir, "extra.txt"), []byte("x"))
		}},
		{name: "nested file deleted", want: "file evidence/shim.log removed", tamper: func(t *testing.T, s *FSStore, dir string) {
			if err := os.Remove(filepath.Join(dir, "evidence", "shim.log")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink added", want: "file link added", tamper: func(t *testing.T, s *FSStore, dir string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "seal rewritten", want: "not in seals.log", tamper: func(t *testing.T, s *FSStore, dir string) {
			p := filepath.Join(dir, "seal.json")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			writeTampered(t, p, []byte(strings.Replace(string(b), `"run_id"`, ` "run_id"`, 1)))
		}},
		{name: "log entry dropped", want: "not in seals.log", tamper: func(t *testing.T, s *FSStore, dir string) {
			writeTampered(t, filepath.Join(s.Root(), "e_test", "seals.log"), nil)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, nil)
			m := mustCreateExp(t, s, "e_test")
			r, _ := sealedRun(t, s, m, "s_a1", "r_1")
			dir := s.runDir(r)
			makeWritable(t, dir)
			tc.tamper(t, s, dir)
			err := s.Verify(ctx, "e_test", "r_1")
			if !errors.Is(err, schemas.ErrSealMismatch) {
				t.Fatalf("err = %v, want ErrSealMismatch", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func writeTampered(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSealsChain(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	var recs []*schemas.SealRecord
	for i, slot := range []string{"s_a1", "s_b1", "s_c1"} {
		_, rec := sealedRun(t, s, m, slot, fmt.Sprintf("r_%d", i))
		recs = append(recs, rec)
	}
	for i, rec := range recs {
		want := ""
		if i > 0 {
			want = recs[i-1].Hash
		}
		if rec.PrevSeal != want {
			t.Fatalf("seal %d prev = %q, want %q", i, rec.PrevSeal, want)
		}
	}
	logPath := filepath.Join(s.Root(), "e_test", "seals.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantLog := fmt.Sprintf("r_0 %s\nr_1 %s\nr_2 %s\n", recs[0].Hash, recs[1].Hash, recs[2].Hash)
	if string(b) != wantLog {
		t.Fatalf("seals.log = %q, want %q", b, wantLog)
	}
	if err := s.VerifyChain("e_test"); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	lines := strings.SplitAfter(wantLog, "\n")
	tests := []struct {
		name string
		log  string
		want string
	}{
		{name: "lines swapped", log: lines[1] + lines[0] + lines[2], want: "prev_seal"},
		{name: "line dropped", log: lines[0] + lines[2], want: "prev_seal"},
		{name: "last line dropped", log: lines[0] + lines[1], want: "missing from seals.log"},
		{name: "duplicate", log: wantLog + lines[0], want: "sealed twice"},
		{name: "malformed", log: lines[0] + "garbage\n" + lines[1] + lines[2], want: "malformed"},
		{name: "wrong hash", log: lines[0] + "r_1 " + recs[0].Hash + "\n" + lines[2], want: "hashes to"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeTampered(t, logPath, []byte(tc.log))
			t.Cleanup(func() { writeTampered(t, logPath, []byte(wantLog)) })
			err := s.VerifyChain("e_test")
			if !errors.Is(err, schemas.ErrSealMismatch) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrSealMismatch mentioning %q", err, tc.want)
			}
		})
	}
}

func TestSealConcurrentChainIsLinear(t *testing.T) {
	s := newTestStore(t, nil)
	m := &schemas.Manifest{ExpID: "e_par", Hash: "h"}
	for i := range 12 {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%d", i), PairID: "p_x", Counted: true, OrderIndex: i})
	}
	if err := s.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	runs := make([]*schemas.Run, len(m.Slots))
	for i := range m.Slots {
		runs[i] = mustNewRun(t, s, m, m.Slots[i].SlotID, fmt.Sprintf("r_%d", i), 1, schemas.OutcomeFail)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(runs))
	for i, r := range runs {
		st := s
		if i%2 == 1 {
			var err error
			if st, err = NewFSStore(s.Root(), nil); err != nil {
				t.Fatal(err)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.Seal(context.Background(), r); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Seal: %v", err)
	}
	if err := s.VerifyChain("e_par"); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestSealResumesInterruptedSeal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r, rec := sealedRun(t, s, m, "s_a1", "r_1")
	// Crash after seal.json, before the seals.log line and chmod.
	makeWritable(t, s.runDir(r))
	writeTampered(t, filepath.Join(s.Root(), "e_test", "seals.log"), nil)
	got, err := s.Seal(ctx, r)
	if err != nil {
		t.Fatalf("resume seal: %v", err)
	}
	if got.Hash != rec.Hash {
		t.Fatalf("resumed hash %s, want %s", got.Hash, rec.Hash)
	}
	if err := s.Verify(ctx, "e_test", "r_1"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := s.VerifyChain("e_test"); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	info, err := os.Stat(s.runDir(r))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Fatalf("run dir mode %v after resume", info.Mode().Perm())
	}
}

func TestSealRefusesSymlink(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r := mustNewRun(t, s, m, "s_a1", "r_1", 1, schemas.OutcomePass)
	if err := os.Symlink("/etc/passwd", filepath.Join(s.runDir(r), "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seal(context.Background(), r); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want non-regular refusal", err)
	}
	if sealed, err := isSealed(s.runDir(r)); err != nil || sealed {
		t.Fatalf("sealed = %v, %v after refusal", sealed, err)
	}
}

// Audit 1-A-05: a run with a canonical record is current-format evidence and
// seals only with every mandatory artifact present. The seal records its
// format so Verify can tell it from a legacy seal, which keeps verifying.
func TestSealRequiresMandatoryArtifacts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	canonical := `{"canonical_version":"1.0"}`
	r := mustNewRun(t, s, m, "s_a1", "r_1", 1, schemas.OutcomePass)
	r.CanonicalHash = "sha256:" + utils.SHA256Bytes([]byte(canonical))
	mustWrite(t, s, r, "canonical.json", canonical)
	mustWrite(t, s, r, "evidence.json", `{}`)
	_, err := s.Seal(ctx, r)
	if !errors.Is(err, schemas.ErrSealMismatch) || !strings.Contains(err.Error(), "gates.json, grade.json, plan.json") {
		t.Fatalf("incomplete run err = %v, want missing gates.json, grade.json, plan.json", err)
	}
	if sealed, err := isSealed(s.runDir(r)); err != nil || sealed {
		t.Fatalf("sealed = %v, %v after refusal", sealed, err)
	}
	mustWrite(t, s, r, "gates.json", `[]`)
	mustWrite(t, s, r, "grade.json", `null`)
	mustWrite(t, s, r, "plan.json", `{}`)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, r, "run.json", string(b))
	rec, err := s.Seal(ctx, r)
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if rec.Format != SealFormatV1 {
		t.Fatalf("format = %q, want %q", rec.Format, SealFormatV1)
	}
	if err := s.Verify(ctx, "e_test", r.RunID); err != nil {
		t.Fatalf("verify: %v", err)
	}

	legacy := mustNewRun(t, s, m, "s_b1", "r_2", 1, schemas.OutcomePass)
	lrec := mustSeal(t, s, legacy)
	if lrec.Format != "" {
		t.Fatalf("legacy format = %q, want empty", lrec.Format)
	}
	if err := s.Verify(ctx, "e_test", legacy.RunID); err != nil {
		t.Fatalf("legacy verify: %v", err)
	}
	if got := missingArtifacts(SealFormatV1, map[string]string{"run.json": "x"}); len(got) != 5 {
		t.Fatalf("missing = %v", got)
	}
	if got := missingArtifacts("run/9", nil); len(got) != 1 {
		t.Fatalf("unknown format missing = %v", got)
	}
}

// TestVerifyChainToleratesConcurrentSeal: a run sealed between the seals.log
// read and the folder sweep is ordinary progress, not a broken chain (audit
// qc2-01-05). A sealed run that the log really lacks still fails.
func TestVerifyChainToleratesConcurrentSeal(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	sealedRun(t, s, m, "s_a1", "r_0")
	late := mustNewRun(t, s, m, "s_b1", "r_1", 1, schemas.OutcomePass)
	mustWrite(t, s, late, "events.jsonl", `{"seq":1}`+"\n")
	if err := s.AppendTransition(context.Background(), late, tr(schemas.StageCleanup, schemas.TransitionCompleted)); err != nil {
		t.Fatal(err)
	}
	fired := false
	afterChainLogRead = func(string) {
		if !fired {
			fired = true
			mustSeal(t, s, late)
		}
	}
	t.Cleanup(func() { afterChainLogRead = nil })
	if err := s.VerifyChain("e_test"); err != nil {
		t.Fatalf("VerifyChain with a concurrent seal: %v", err)
	}
	if !fired {
		t.Fatal("hook did not run")
	}
	afterChainLogRead = nil
	logPath := filepath.Join(s.Root(), "e_test", "seals.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTampered(t, logPath, []byte(strings.SplitAfter(string(b), "\n")[0]))
	if err := s.VerifyChain("e_test"); !errors.Is(err, schemas.ErrSealMismatch) || !strings.Contains(err.Error(), "missing from seals.log") {
		t.Fatalf("err = %v, want missing from seals.log", err)
	}
}

// TestVerifySealedRunRereadsStaleLog: SealedRuns reads seals.log once before
// loading attempts; a run sealed after that read verifies against a fresh read
// instead of failing (audit qc2-01-05).
func TestVerifySealedRunRereadsStaleLog(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r, _ := sealedRun(t, s, m, "s_a1", "r_0")
	if _, err := s.verifySealedRun(s.runDir(r), "r_0", map[string]string{}); err != nil {
		t.Fatalf("stale snapshot without the run: %v", err)
	}
	if _, err := s.verifySealedRun(s.runDir(r), "r_0", map[string]string{"r_0": strings.Repeat("0", 64)}); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("snapshot with another hash: err = %v, want ErrSealMismatch", err)
	}
}
