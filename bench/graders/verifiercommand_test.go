package graders

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/spec"
)

func ptr(v float64) *float64 { return &v }

func TestParseReward(t *testing.T) {
	manyDims := map[string]float64{}
	for i := range maxDimensions + 1 {
		manyDims[fmt.Sprintf("d%d", i)] = 1
	}
	many, err := json.Marshal(map[string]any{"reward": 1, "dimensions": manyDims})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		data    string
		reward  float64
		dims    map[string]float64
		wantErr string
	}{
		{name: "bare integer", data: "1", reward: 1},
		{name: "bare number with newline", data: "0.75\n", reward: 0.75},
		{name: "harbor reward.txt with spaces", data: "  0  \r\n", reward: 0},
		{name: "leading dot and exponent", data: ".5e1", reward: 5},
		{name: "negative", data: "-2", reward: -2},
		{name: "byte order mark", data: "\xef\xbb\xbf1", reward: 1},
		{name: "json reward only", data: `{"reward": 0.5}`, reward: 0.5},
		{name: "json with dimensions", data: `{"reward": 1, "dimensions": {"correctness": 1, "style_2": 0.25}}`, reward: 1,
			dims: map[string]float64{"correctness": 1, "style_2": 0.25}},
		{name: "json empty dimensions", data: `{"reward": 1, "dimensions": {}}`, reward: 1},
		{name: "json trailing whitespace", data: "{\"reward\": 1}\n\n", reward: 1},
		{name: "empty", data: "", wantErr: "empty reward file"},
		{name: "whitespace only", data: " \n\t", wantErr: "empty reward file"},
		{name: "nan", data: "NaN", wantErr: "not a decimal number"},
		{name: "inf", data: "inf", wantErr: "not a decimal number"},
		{name: "infinity", data: "-Infinity", wantErr: "not a decimal number"},
		{name: "hex float", data: "0x1p-2", wantErr: "not a decimal number"},
		{name: "out of range", data: "1e400", wantErr: "out of range"},
		{name: "two numbers", data: "1 0", wantErr: "not a decimal number"},
		{name: "text", data: "PASSED", wantErr: "not a decimal number"},
		{name: "json missing reward", data: `{"dimensions": {"a": 1}}`, wantErr: "has no reward"},
		{name: "json null reward", data: `{"reward": null}`, wantErr: "has no reward"},
		{name: "json string reward", data: `{"reward": "1"}`, wantErr: "parse reward json"},
		{name: "json reward out of range", data: `{"reward": 1e400}`, wantErr: "parse reward json"},
		{name: "json unknown key", data: `{"reward": 1, "rewards": 1}`, wantErr: "unknown field"},
		{name: "json trailing data", data: `{"reward": 1} {"reward": 0}`, wantErr: "data after the object"},
		{name: "json truncated", data: `{"reward": 1`, wantErr: "parse reward json"},
		{name: "json null dimension", data: `{"reward": 1, "dimensions": {"a": null}}`, wantErr: "dimension a is null"},
		{name: "json bad dimension name", data: `{"reward": 1, "dimensions": {"Bad-Name": 1}}`, wantErr: "not [a-z0-9_]{1,40}"},
		{name: "json long dimension name", data: `{"reward": 1, "dimensions": {"` + strings.Repeat("a", 41) + `": 1}}`, wantErr: "not [a-z0-9_]{1,40}"},
		{name: "json too many dimensions", data: string(many), wantErr: "over the limit of 32"},
		{name: "json array", data: `[1]`, wantErr: "not a decimal number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reward, dims, err := parseReward([]byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseReward: %v", err)
			}
			if reward != tt.reward || !reflect.DeepEqual(dims, tt.dims) {
				t.Fatalf("got %v %v, want %v %v", reward, dims, tt.reward, tt.dims)
			}
		})
	}
}

func TestScoreReward(t *testing.T) {
	tests := []struct {
		name      string
		exit      int
		data      []byte
		threshold float64
		want      schemas.Grade
	}{
		{name: "reward at threshold passes", data: []byte("1"), threshold: 1,
			want: schemas.Grade{Passed: true, Reward: ptr(1), Reason: "passed (reward 1)"}},
		{name: "reward above a lower threshold passes", data: []byte(`{"reward": 0.75, "dimensions": {"correctness": 0.75}}`), threshold: 0.5,
			want: schemas.Grade{Passed: true, Reward: ptr(0.75), Dimensions: map[string]float64{"correctness": 0.75}, Reason: "passed (reward 0.75)"}},
		{name: "reward below threshold fails", data: []byte(`{"reward": 0.25, "dimensions": {"correctness": 0.25}}`), threshold: 1,
			want: schemas.Grade{Reward: ptr(0.25), Dimensions: map[string]float64{"correctness": 0.25}, Reason: "reward 0.25 below pass threshold 1"}},
		{name: "nonzero exit with full reward still passes", exit: 1, data: []byte("1"), threshold: 1,
			want: schemas.Grade{Passed: true, ExitCode: 1, Reward: ptr(1), Reason: "passed (reward 1)"}},
		{name: "no file is a broken run", exit: 0, data: nil, threshold: 1,
			want: schemas.Grade{Reason: "test run broken by the patch: no reward file (exit 0)"}},
		{name: "empty file is a broken run", exit: 3, data: []byte{}, threshold: 1,
			want: schemas.Grade{ExitCode: 3, Reason: "test run broken by the patch: empty reward file (exit 3)"}},
		{name: "nan is a broken run", data: []byte("nan"), threshold: 1,
			want: schemas.Grade{Reason: `test run broken by the patch: reward "nan" is not a decimal number (exit 0)`}},
		{name: "long garbage is quoted short", data: bytes.Repeat([]byte("x"), 5000), threshold: 1,
			want: schemas.Grade{Reason: `test run broken by the patch: reward "` + strings.Repeat("x", 40) + `..." is not a decimal number (exit 0)`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schemas.Grade{}
			scoreReward(&got, tt.exit, tt.data, tt.threshold)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
			if got.InvalidEvaluator {
				t.Fatal("a reward grade is never an invalid evaluator")
			}
		})
	}
}

func TestScoreExitCode(t *testing.T) {
	tests := []struct {
		name string
		exit int
		want schemas.Grade
	}{
		{name: "zero passes", exit: 0, want: schemas.Grade{Passed: true, Reason: "passed (exit 0)"}},
		{name: "one fails", exit: 1, want: schemas.Grade{ExitCode: 1, Reason: "exit 1"}},
		{name: "killed fails", exit: 137, want: schemas.Grade{ExitCode: 137, Reason: "exit 137"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schemas.Grade{}
			scoreExitCode(&got, tt.exit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestScoreResult(t *testing.T) {
	junit := resultSpec{format: schemas.VerifierResultJUnit, path: junitPath}
	reward := resultSpec{format: schemas.VerifierResultReward, path: defaultRewardPath, threshold: 1}
	exit := resultSpec{format: schemas.VerifierResultExitCode}
	required := []string{"test_hidden_calc.py::test_add"}
	tests := []struct {
		name     string
		spec     resultSpec
		exit     int
		out      resultRead
		required []string
		want     schemas.Grade
	}{
		{name: "junit follows hidden_tests rules", spec: junit, out: resultRead{data: []byte(junitPass)}, required: required,
			want: schemas.Grade{Passed: true, TestsRan: 3, Reason: "passed (3 tests)"}},
		{name: "junit zero tests fails", spec: junit, exit: 5, out: resultRead{data: []byte(junitZero)},
			want: schemas.Grade{ExitCode: 5, Reason: "test run broken by the patch: no tests ran (exit 5)"}},
		{name: "junit bad required id is invalid", spec: junit, out: resultRead{data: []byte(junitPass)}, required: []string{"test_add"},
			want: schemas.Grade{TestsRan: 3, InvalidEvaluator: true, Reason: `required test id "test_add" is not file.py::test`}},
		{name: "reward ignores required", spec: reward, out: resultRead{data: []byte("1")}, required: []string{"test_add"},
			want: schemas.Grade{Passed: true, Reward: ptr(1), Reason: "passed (reward 1)"}},
		{name: "exit code ignores the file", spec: exit, exit: 0, out: resultRead{}, required: required,
			want: schemas.Grade{Passed: true, Reason: "passed (exit 0)"}},
		{name: "oversized file is a broken run", spec: reward, exit: 0, out: resultRead{problem: "reward file is 70000 bytes, over the 65536 byte limit"},
			want: schemas.Grade{Reason: "test run broken by the patch: reward file is 70000 bytes, over the 65536 byte limit (exit 0)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schemas.Grade{}
			scoreResult(&got, tt.spec, tt.exit, tt.out, tt.required)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResultSettings(t *testing.T) {
	tests := []struct {
		name    string
		v       schemas.SuiteVerifier
		want    resultSpec
		wantErr string
	}{
		{name: "default is junit", v: schemas.SuiteVerifier{},
			want: resultSpec{format: "junit", path: "/grader/out/junit.xml", limit: maxJUnitBytes, noun: "junit report"}},
		{name: "junit custom path", v: schemas.SuiteVerifier{Result: "junit", ResultPath: "/grader/out/report.xml"},
			want: resultSpec{format: "junit", path: "/grader/out/report.xml", limit: maxJUnitBytes, noun: "junit report"}},
		{name: "reward defaults", v: schemas.SuiteVerifier{Result: "reward"},
			want: resultSpec{format: "reward", path: "/grader/out/reward.json", limit: maxRewardBytes, noun: "reward file", threshold: 1}},
		{name: "reward txt and threshold", v: schemas.SuiteVerifier{Result: "reward", ResultPath: "/grader/out/reward.txt", PassThreshold: 0.5},
			want: resultSpec{format: "reward", path: "/grader/out/reward.txt", limit: maxRewardBytes, noun: "reward file", threshold: 0.5}},
		{name: "reward big threshold", v: schemas.SuiteVerifier{Result: "reward", PassThreshold: 1e9},
			want: resultSpec{format: "reward", path: "/grader/out/reward.json", limit: maxRewardBytes, noun: "reward file", threshold: 1e9}},
		{name: "exit code", v: schemas.SuiteVerifier{Result: "exit_code"}, want: resultSpec{format: "exit_code"}},
		{name: "unknown format", v: schemas.SuiteVerifier{Result: "tap"}, wantErr: "is not junit, reward or exit_code"},
		{name: "exit code with path", v: schemas.SuiteVerifier{Result: "exit_code", ResultPath: "/grader/out/x"}, wantErr: "does not apply"},
		{name: "threshold on junit", v: schemas.SuiteVerifier{PassThreshold: 0.5}, wantErr: "only to result reward"},
		{name: "threshold on exit code", v: schemas.SuiteVerifier{Result: "exit_code", PassThreshold: 1}, wantErr: "only to result reward"},
		{name: "negative threshold", v: schemas.SuiteVerifier{Result: "reward", PassThreshold: -1}, wantErr: "must be between"},
		{name: "huge threshold", v: schemas.SuiteVerifier{Result: "reward", PassThreshold: 2e9}, wantErr: "must be between"},
		{name: "nan threshold", v: schemas.SuiteVerifier{Result: "reward", PassThreshold: math.NaN()}, wantErr: "must be between"},
		{name: "inf threshold", v: schemas.SuiteVerifier{Result: "reward", PassThreshold: math.Inf(1)}, wantErr: "must be between"},
		{name: "path in the workspace", v: schemas.SuiteVerifier{Result: "reward", ResultPath: "/workspace/reward.json"}, wantErr: "directly in /grader/out"},
		{name: "path in the hidden tests", v: schemas.SuiteVerifier{Result: "reward", ResultPath: "/grader/tests/reward.json"}, wantErr: "directly in /grader/out"},
		{name: "path traversal", v: schemas.SuiteVerifier{Result: "reward", ResultPath: "/grader/out/../tests/reward.json"}, wantErr: "directly in /grader/out"},
		{name: "path in a subfolder", v: schemas.SuiteVerifier{Result: "reward", ResultPath: "/grader/out/sub/reward.json"}, wantErr: "directly in /grader/out"},
		{name: "path is the folder", v: schemas.SuiteVerifier{Result: "junit", ResultPath: "/grader/out"}, wantErr: "directly in /grader/out"},
		{name: "relative path", v: schemas.SuiteVerifier{Result: "junit", ResultPath: "grader/out/junit.xml"}, wantErr: "directly in /grader/out"},
		{name: "dot file name", v: schemas.SuiteVerifier{Result: "junit", ResultPath: "/grader/out/.."}, wantErr: "directly in /grader/out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resultSettings(tt.v)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resultSettings: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestVerifierWorkdir(t *testing.T) {
	tests := []struct {
		in, want, wantErr string
	}{
		{in: "", want: "/workspace"},
		{in: "/workspace", want: "/workspace"},
		{in: "/app", want: "/app"},
		{in: "/srv/repo", want: "/srv/repo"},
		{in: "app", wantErr: "clean absolute path"},
		{in: "/", wantErr: "clean absolute path"},
		{in: "/app/", wantErr: "clean absolute path"},
		{in: "/app/../grader", wantErr: "clean absolute path"},
		{in: "/grader", wantErr: "reserves"},
		{in: "/grader/out", wantErr: "reserves"},
		{in: "/tmp/ws", wantErr: "reserves"},
		{in: "/home/agent", wantErr: "reserves"},
		{in: "/proc", wantErr: "reserves"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := verifierWorkdir(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestVerifierCommandInputs(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	diff := &schemas.Diff{Path: filepath.Join("testdata", "patches", "pass.diff")}
	task := &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}}
	suite := func(tag, workdir string, v schemas.SuiteVerifier) *schemas.Suite {
		v.Command = []string{"sh", "/grader/tests/test.sh"}
		return &schemas.Suite{Version: "1.0.0", BaseImage: schemas.ImageSpec{Tag: tag}, Workdir: workdir, Verifier: v}
	}
	tests := []struct {
		name      string
		helper    string
		suite     *schemas.Suite
		prep, img string
		workdir   string
		format    string
		wantErr   string
	}{
		{name: "helper prepares, base image verifies", helper: "helper:1", suite: suite("node:22", "/app", schemas.SuiteVerifier{Result: "exit_code"}),
			prep: "helper:1", img: "node:22", workdir: "/app", format: "exit_code"},
		{name: "no helper uses the base image for both", suite: suite("py:1", "", schemas.SuiteVerifier{Result: "reward"}),
			prep: "py:1", img: "py:1", workdir: "/workspace", format: "reward"},
		{name: "no base image", helper: "helper:1", suite: suite("", "", schemas.SuiteVerifier{}), wantErr: "needs a base image"},
		{name: "bad workdir", suite: suite("py:1", "/grader", schemas.SuiteVerifier{}), wantErr: "reserves"},
		{name: "bad result", suite: suite("py:1", "", schemas.SuiteVerifier{Result: "reward", ResultPath: "/workspace/r.json"}), wantErr: "directly in /grader/out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newVerifierCommand(HiddenTestsConfig{Image: tt.helper})
			in, spec, err := g.inputs(&schemas.RunContext{Task: task, Suite: tt.suite}, diff)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("inputs: %v", err)
			}
			if in.prepImage != tt.prep || in.image != tt.img || in.workdir != tt.workdir || spec.format != tt.format {
				t.Fatalf("inputs = %+v, spec = %+v", in, spec)
			}
		})
	}
}

// TestSuiteSchemaResultFields keeps spec/suite.schema.json and resultSettings
// in agreement: what the schema admits, the grader accepts, and the result
// settings the grader refuses never load.
func TestSuiteSchemaResultFields(t *testing.T) {
	raw, err := spec.FS.ReadFile("suite.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("suite.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	// The suite schema refers to the task schema (services, output) by its $id.
	taskRaw, err := spec.FS.ReadFile("task.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	taskDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(taskRaw))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("https://github.com/maximhq/bifrost/bench/spec/task.schema.json", taskDoc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("suite.schema.json")
	if err != nil {
		t.Fatalf("compile suite schema: %v", err)
	}
	const head = "id: s\nversion: 1.0.0\nenvironment: docker_workspace\nbase_image: {tag: x}\nworkdir: /workspace\ntasks: [t1]\nprobes: []\n"
	tests := []struct {
		name  string
		yaml  string
		valid bool
	}{
		{"hidden_tests as before", "grader: hidden_tests\nverifier: {command: [pytest], timeout_s: 60}\n", true},
		{"hidden_tests with junit", "grader: hidden_tests\nverifier: {command: [pytest], timeout_s: 60, result: junit}\n", true},
		{"hidden_tests refuses reward", "grader: hidden_tests\nverifier: {command: [pytest], timeout_s: 60, result: reward}\n", false},
		{"hidden_tests refuses result_path", "grader: hidden_tests\nverifier: {command: [pytest], timeout_s: 60, result_path: /grader/out/j.xml}\n", false},
		{"unknown grader", "grader: my_grader\nverifier: {command: [pytest], timeout_s: 60}\n", false},
		{"verifier_command junit default", "grader: verifier_command\nverifier: {command: [pytest], timeout_s: 60}\n", true},
		{"verifier_command junit path", "grader: verifier_command\nverifier: {command: [node], timeout_s: 60, result: junit, result_path: /grader/out/junit.xml}\n", true},
		{"verifier_command reward", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, result_path: /grader/out/reward.txt, pass_threshold: 0.5}\n", true},
		{"verifier_command reward max threshold", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, pass_threshold: 1000000000}\n", true},
		{"verifier_command exit_code", "grader: verifier_command\nverifier: {command: [go], timeout_s: 60, result: exit_code}\n", true},
		{"unknown result", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: tap}\n", false},
		{"zero threshold", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, pass_threshold: 0}\n", false},
		{"negative threshold", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, pass_threshold: -1}\n", false},
		{"threshold over max", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, pass_threshold: 1000000001}\n", false},
		{"threshold without reward", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, pass_threshold: 0.5}\n", false},
		{"threshold with exit_code", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: exit_code, pass_threshold: 0.5}\n", false},
		{"path with exit_code", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: exit_code, result_path: /grader/out/x}\n", false},
		{"path in workspace", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, result_path: /workspace/reward.json}\n", false},
		{"path traversal", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, result_path: /grader/out/../x}\n", false},
		{"path subfolder", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, result: reward, result_path: /grader/out/a/b}\n", false},
		{"unknown verifier key", "grader: verifier_command\nverifier: {command: [sh], timeout_s: 60, threshold: 1}\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var y any
			if err := yaml.Unmarshal([]byte(head+tt.yaml), &y); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(y)
			if err != nil {
				t.Fatal(err)
			}
			v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			verr := sch.Validate(v)
			if (verr == nil) != tt.valid {
				t.Fatalf("schema valid = %v, want %v: %v", verr == nil, tt.valid, verr)
			}
			var s schemas.Suite
			if err := yaml.Unmarshal([]byte(head+tt.yaml), &s); err != nil {
				t.Fatal(err)
			}
			if tt.valid && s.Grader == VerifierCommandType {
				if _, err := resultSettings(s.Verifier); err != nil {
					t.Fatalf("schema admits what the grader refuses: %v", err)
				}
			}
		})
	}
}

// TestVerifierCommandArtifactInputs: the run's artifacts folder reaches the
// verifier read-only at /grader/artifacts.
func TestVerifierCommandArtifactInputs(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "artifacts", "report"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifacts", "report", "out.csv"), []byte("a,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"},
		Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "out.csv", Required: true}}}}
	suite := &schemas.Suite{Version: "1.0.0", BaseImage: schemas.ImageSpec{Tag: "py:1"},
		Verifier: schemas.SuiteVerifier{Result: "exit_code", Command: []string{"sh", "/grader/tests/test.sh"}}}
	in, _, err := newVerifierCommand(HiddenTestsConfig{}).inputs(&schemas.RunContext{Task: task, Suite: suite, Dir: dir},
		&schemas.Diff{Path: filepath.Join("testdata", "patches", "pass.diff")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-v", filepath.Join(dir, "artifacts") + ":/grader/artifacts:ro"}
	if got := scenarioMounts(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts %v, want %v", got, want)
	}
}
