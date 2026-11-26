package traceimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// newSuite writes a scenario suite from its template under a fresh
// benchmarks folder and returns the folder.
func newSuite(t *testing.T, id, tmpl string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "benchmarks")
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: id, Template: tmpl})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(root, id), files, false); err != nil {
		t.Fatal(err)
	}
	return root
}

// redactedSession parses a fixture, picks a session and redacts it, as the
// command does.
func redactedSession(t *testing.T, data []byte, id string) (*Session, Source) {
	t.Helper()
	tr, err := Parse(data, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := tr.Pick(id)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRedactor()
	r.Session(s)
	sum := sha256.Sum256(data)
	return s, Source{File: "trace.json", SHA256: hex.EncodeToString(sum[:]), Format: tr.Format, Redacted: r.Report()}
}

const memoryChat = `{"id":"m-1","messages":[
 {"role":"user","content":"Our launch moved to 2026-05-04, note it. Mail ops@example.com if it slips."},
 {"role":"assistant","content":"Noted."},
 {"role":"user","content":"When is our launch?"},
 {"role":"assistant","content":"The launch is on 2026-04-09."}],"status":"failed"}`

func TestWriteDraftEveryKind(t *testing.T) {
	tests := []struct {
		kind, data, session string
		replay              bool
		wantTools           int
		wantWrong           bool // the recorded answer is listed as a wrong answer
	}{
		{KindQA, string(fixture(t, "otlp-support.json")), "5b8efff798038103d269b633813fc60c", false, 0, true},
		{KindQA, string(fixture(t, "langfuse-trace.json")), "", true, 0, false},
		{KindToolUse, string(fixture(t, "chat-transcript.json")), "", false, 2, false},
		{KindToolUse, string(fixture(t, "chat-transcript.json")), "", true, 2, false},
		{KindMCP, string(fixture(t, "otlp-support.json")), "5b8efff798038103d269b633813fc60c", false, 2, false},
		{KindMCP, string(fixture(t, "otlp-support.json")), "5b8efff798038103d269b633813fc60c", true, 2, false},
		{KindMemory, memoryChat, "", false, 0, true},
	}
	for i, tc := range tests {
		name := tc.kind
		if tc.replay {
			name += "-replay"
		}
		t.Run(name, func(t *testing.T) {
			root := newSuite(t, "prod", tc.kind)
			s, src := redactedSession(t, []byte(tc.data), tc.session)
			id := "from-trace-" + string(rune('a'+i))
			res, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "prod"), ID: id, Session: s, Source: src, Replay: tc.replay})
			if err != nil {
				t.Fatal(err)
			}
			wantReplayed := tc.replay && (tc.kind == KindToolUse || tc.kind == KindMCP)
			if !res.SuiteEdited || len(res.EditMe) == 0 || res.Replayed != wantReplayed {
				t.Errorf("result = %+v", res)
			}
			st, task, err := suite.LoadTask(root, "prod", id)
			if err != nil {
				t.Fatalf("the draft does not load: %v", err)
			}
			if task.Provenance == nil || task.Provenance.CreatedBy != "task from-trace" || !strings.Contains(task.Provenance.Source, "sha256:"+src.SHA256) ||
				!strings.Contains(task.Provenance.Source, s.ID) {
				t.Errorf("provenance = %+v", task.Provenance)
			}
			if !hasEditMeError(suite.ValidateTask(context.Background(), st, task)) {
				t.Fatal("task validate passed a draft that still holds EDIT ME markers")
			}
			checkNoLeak(t, res.Dir)
			checkKind(t, res.Dir, tc.kind, s, tc.wantTools, tc.wantWrong)
			if wantReplayed {
				checkReplay(t, res.Dir, task)
			}
			removeEditMe(t, res.Dir)
			st, task, err = suite.LoadTask(root, "prod", id)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range suite.ValidateTask(context.Background(), st, task) {
				if p.Severity == suite.SeverityError {
					t.Errorf("after the EDIT ME lines are gone: %s: %s", p.Path, p.Message)
				}
			}
		})
	}
}

// hasEditMeError reports whether validation refused an EDIT ME marker.
func hasEditMeError(problems []suite.Problem) bool {
	for _, p := range problems {
		if p.Severity == suite.SeverityError && strings.Contains(p.Message, "EDIT ME") {
			return true
		}
	}
	return false
}

// checkNoLeak proves the planted secret, the emails, the phone number and the
// card number of the fixtures reached no file, and the admission secret scan
// finds nothing.
func checkNoLeak(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, leaked := range []string{plantedSecret, "jane.doe@example.com", "ops@example.com", "415 555 0134", "4111 1111", "123456789012", "FAKEtoken"} {
			if bytes.Contains(b, []byte(leaked)) {
				t.Errorf("%s holds %q", p, leaked)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := suite.ScanSecrets(dir)
	if err != nil || len(findings) > 0 {
		t.Errorf("secret scan: %v %v", findings, err)
	}
}

// checkKind checks what each kind's draft must hold.
func checkKind(t *testing.T, dir, kind string, s *Session, wantTools int, wantWrong bool) {
	t.Helper()
	instrFile := "instruction.md"
	if kind == KindMemory {
		instrFile = "sessions/recall.md"
	}
	instr := readText(t, filepath.Join(dir, filepath.FromSlash(instrFile)))
	if kind == KindMemory {
		if learn := readText(t, filepath.Join(dir, "sessions", "learn.md")); !strings.Contains(learn, "Our launch moved") {
			t.Errorf("learn.md = %s", learn)
		}
		if conv := readText(t, filepath.Join(dir, "sessions", "learn-files", "conversation.md")); !strings.Contains(conv, "2026-05-04") {
			t.Errorf("conversation.md = %s", conv)
		}
		if _, err := os.Stat(filepath.Join(dir, "sessions", "learn-files", "kickoff-notes.md")); err == nil {
			t.Error("the copied task's session file is still there")
		}
	}
	reqs := s.Requests()
	if !strings.HasPrefix(instr, scaffold.EditMeMarker) || !strings.Contains(instr, strings.TrimSpace(reqs[len(reqs)-1])) && !strings.Contains(instr, reqs[0]) {
		t.Errorf("instruction = %s", instr)
	}
	if !strings.Contains(readText(t, filepath.Join(dir, TranscriptFile)), "## Conversation") {
		t.Error("no transcript")
	}
	switch kind {
	case KindQA, KindMemory:
		var spec schemas.AnswerSpec
		if err := yaml.Unmarshal([]byte(readText(t, filepath.Join(dir, "verifier", "answer.yaml"))), &spec); err != nil {
			t.Fatal(err)
		}
		answer := strings.TrimSpace(s.FinalAnswer)
		if wantWrong && (len(spec.WrongOnes) != 1 || spec.WrongOnes[0] != answer || spec.Expected[0] != placeholderAnswer) {
			t.Errorf("failed session: answer spec = %+v", spec)
		}
		if !wantWrong && (spec.Expected[0] != answer || len(spec.WrongOnes) != 0) {
			t.Errorf("answer spec = %+v", spec)
		}
	case KindToolUse, KindMCP:
		var doc struct {
			CreatedBy string     `json:"created_by"`
			Calls     []ToolCall `json:"calls"`
		}
		if err := json.Unmarshal([]byte(readText(t, filepath.Join(dir, ObservedToolsFile))), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.CreatedBy != "task from-trace" || len(doc.Calls) != wantTools {
			t.Errorf("%s = %+v", ObservedToolsFile, doc)
		}
		ref := readText(t, filepath.Join(dir, "solution", "reference.sh"))
		for _, c := range s.ToolCalls {
			if !strings.Contains(ref, "tool("+quote(c.Name)) {
				t.Errorf("reference.sh lacks the call to %s:\n%s", c.Name, ref)
			}
		}
		checks := readText(t, filepath.Join(dir, "verifier", "tests", "checks.py"))
		if !strings.Contains(checks, `STATE = "inventory/db.json"`) && !strings.Contains(checks, `STATE = "tools/calls.json"`) {
			t.Errorf("checks.py has no STATE line:\n%s", checks)
		}
	}
}

// checkReplay checks a replaying draft: the replay server replaces the
// copied service, seeded with the observed calls, and the copied seed is gone.
func checkReplay(t *testing.T, dir string, task *schemas.Task) {
	t.Helper()
	if len(task.Services) != 1 {
		t.Fatalf("services = %+v", task.Services)
	}
	sv := task.Services[0]
	if sv.ID != ReplayServiceID || sv.Build == nil || sv.Build.Dockerfile != "replay/Dockerfile" || sv.Files["/seed/tools.observed.json"] != ObservedToolsFile ||
		len(sv.Export) != 1 || sv.Export[0].Path != "/state/calls.json" {
		t.Errorf("replay service = %+v", sv)
	}
	for _, rel := range []string{"replay/server.py", "replay/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "seed")); err == nil {
		t.Error("the copied service's seed folder is still there")
	}
	if !strings.Contains(readText(t, filepath.Join(dir, "verifier", "tests", "checks.py")), `"where": {"tool": `) {
		t.Error("no example checks over calls.json")
	}
	if len(task.Controls.Mutants) != 1 || task.Controls.Mutants[0] != ObservedMutant {
		t.Errorf("mutants = %v; a failed session's calls are a mutant", task.Controls.Mutants)
	}
	if m := readText(t, filepath.Join(dir, ObservedMutant)); strings.Contains(m, "EDIT ME") || !strings.Contains(m, "tool(") {
		t.Errorf("mutant = %s", m)
	}
}

// removeEditMe deletes every EDIT ME line of the draft, as a person does
// after confirming each judgement.
func removeEditMe(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var kept []string
		for _, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, scaffold.EditMeMarker) || strings.HasPrefix(l, "# EDIT ME") {
				continue
			}
			kept = append(kept, line)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(p, []byte(strings.Join(kept, "\n")), info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteDraftRefusals(t *testing.T) {
	data := fixture(t, "chat-transcript.json")
	s, src := redactedSession(t, data, "")
	t.Run("wrong suite kind", func(t *testing.T) {
		root := newSuite(t, "web", scaffold.TemplateBrowser)
		if _, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "web"), ID: "x", Session: s, Source: src}); err == nil || !strings.Contains(err.Error(), "drafts qa") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("kind mismatch", func(t *testing.T) {
		root := newSuite(t, "qa1", KindQA)
		if _, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "qa1"), ID: "x", Kind: KindMCP, Session: s, Source: src}); err == nil {
			t.Error("a -kind other than the suite's template was accepted")
		}
		if _, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "qa1"), ID: "x", Kind: "browser", Session: s, Source: src}); err == nil || !strings.Contains(err.Error(), "Choose one of") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no request rolls back", func(t *testing.T) {
		root := newSuite(t, "qa2", KindQA)
		before := readText(t, filepath.Join(root, "qa2", "suite.yaml"))
		empty := &Session{ID: "e", Turns: []Turn{{Role: "assistant", Text: "hello"}}, Outcome: Outcome{Status: OutcomeUnknown}}
		if _, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "qa2"), ID: "x", Session: empty, Source: src}); err == nil || !strings.Contains(err.Error(), "no user request") {
			t.Errorf("err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "qa2", "tasks", "x")); err == nil {
			t.Error("the half-written task folder was left behind")
		}
		if after := readText(t, filepath.Join(root, "qa2", "suite.yaml")); after != before {
			t.Errorf("suite.yaml not restored:\n%s", after)
		}
	})
	t.Run("existing task", func(t *testing.T) {
		root := newSuite(t, "qa3", KindQA)
		if _, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "qa3"), ID: "hotel-cap", Session: s, Source: src}); err == nil || !strings.Contains(err.Error(), "-force") {
			t.Errorf("err = %v", err)
		}
	})
}
