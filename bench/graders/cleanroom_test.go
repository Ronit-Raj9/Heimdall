package graders

import (
	"archive/tar"
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReadTarFileTooLarge(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "reward.json", Mode: 0o644, Size: 10, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		limit int64
		want  string
	}{
		{name: "over the limit is typed", limit: 9, want: "10 bytes, over the 9 byte limit"},
		{name: "at the limit reads", limit: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := readTarFile(bytes.NewReader(buf.Bytes()), tt.limit)
			if tt.want == "" {
				if err != nil || string(data) != "0123456789" {
					t.Fatalf("got %q, %v", data, err)
				}
				return
			}
			var tl *tooLargeError
			if !errors.As(err, &tl) || err.Error() != tt.want || data != nil {
				t.Fatalf("got %q, %v", data, err)
			}
		})
	}
}

func TestGatherInputsImages(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Suite: &schemas.Suite{BaseImage: schemas.ImageSpec{Tag: "base:1"}, Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}, TimeoutS: 7}},
	}
	diff := &schemas.Diff{Path: filepath.Join("testdata", "patches", "pass.diff")}
	tests := []struct {
		name, image, want string
	}{
		{name: "base image for both", want: "base:1"},
		{name: "override for both", image: "helper:1", want: "helper:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := gatherInputs(rc, diff, tt.image)
			if err != nil {
				t.Fatalf("gatherInputs: %v", err)
			}
			if in.image != tt.want || in.prepImage != tt.want || in.workdir != "" || in.timeout != 7*time.Second {
				t.Fatalf("inputs = %+v", in)
			}
		})
	}
}

func TestNewGradeJob(t *testing.T) {
	tests := []struct {
		name, exp, run string
	}{
		{name: "names carry the run id", exp: "e_1", run: "r_abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := &gradeInputs{}
			a, b := newGradeJob(tt.exp, tt.run, in), newGradeJob(tt.exp, tt.run, in)
			if a.expID != tt.exp || a.runID != tt.run || a.in != in {
				t.Fatalf("job = %+v", a)
			}
			for prefix, name := range map[string]string{"bench-grade-" + tt.run + "-": a.workspace, "bench-gout-" + tt.run + "-": a.out, "bench-" + tt.run + "-verify-": a.container} {
				if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
					t.Errorf("name %q does not start with %q plus a suffix", name, prefix)
				}
			}
			if a.workspace == b.workspace || a.out == b.out || a.container == b.container {
				t.Errorf("two jobs share names: %+v %+v", a, b)
			}
		})
	}
}

func TestTimedOutReason(t *testing.T) {
	tests := []struct {
		timeout time.Duration
		want    string
	}{
		{3 * time.Second, "test run broken by the patch: verifier timed out after 3s"},
		{5 * time.Minute, "test run broken by the patch: verifier timed out after 5m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := timedOutReason(tt.timeout); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}
