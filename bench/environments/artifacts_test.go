package environments

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// artifactStream builds a helper stream: the manifest, then members named
// by manifest index with the given bytes.
func artifactStream(t *testing.T, entries []artifactEntry, members map[int][]byte, order ...int) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	add(artifactManifest, raw)
	for _, i := range order {
		add(strconv.Itoa(i), members[i])
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// binaryBytes holds every byte value, NUL and newlines included.
func binaryBytes() []byte {
	b := make([]byte, 0, 512)
	for i := range 512 {
		b = append(b, byte(i))
	}
	return b
}

func artifactTask(wp string, specs ...schemas.ArtifactSpec) *schemas.Task {
	t := &schemas.Task{ID: "t1", Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: specs}}
	if wp != "" {
		t.Environment = &schemas.TaskEnvironment{Image: "x", WorkspacePath: wp}
	}
	return t
}

func TestExportArtifacts(t *testing.T) {
	specs := []schemas.ArtifactSpec{{ID: "report", Path: "out/*.csv", Required: true}, {ID: "mesh", Path: "mesh.obj"}}
	bin := binaryBytes()
	entries := []artifactEntry{
		{ID: "report", Name: "a.csv", Source: "out/a.csv", Bytes: 4},
		{ID: "report", Name: "leak.csv", Source: "out/leak.csv", Error: "outside the workspace"},
		{ID: "mesh", Name: "mesh.obj", Source: "mesh.obj", Bytes: int64(len(bin))},
	}
	stream := artifactStream(t, entries, map[int][]byte{0: []byte("a,b\n"), 2: bin}, 0, 2)
	rc := func(wp string) *schemas.RunContext {
		return &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Suite: &schemas.Suite{}, Task: artifactTask(wp, specs...),
			Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1"}, Dir: t.TempDir()}
	}
	t.Run("copies files and runs a locked-down helper", func(t *testing.T) {
		r := rc("/app")
		d := fakeAnswerDocker(t, string(stream), 0)
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d, HelperImage: "helper:1"})
		files, err := e.ExportArtifacts(t.Context(), r)
		if err != nil || len(files) != 3 {
			t.Fatalf("files %+v %v", files, err)
		}
		if f := files[0]; f.Path != "artifacts/report/a.csv" || f.SHA256 != utils.SHA256Bytes([]byte("a,b\n")) || f.Bytes != 4 || f.Error != "" {
			t.Fatalf("report %+v", f)
		}
		if f := files[1]; f.Path != "" || f.Error != "outside the workspace" || f.Source != "out/leak.csv" {
			t.Fatalf("leak %+v", f)
		}
		if got := readOut(t, filepath.Join(r.Dir, "artifacts", "mesh", "mesh.obj")); !bytes.Equal(got, bin) || files[2].SHA256 != utils.SHA256Bytes(bin) {
			t.Fatalf("binary artifact changed: %d bytes, %+v", len(got), files[2])
		}
		if _, err := os.Stat(filepath.Join(r.Dir, "artifacts", "report", "leak.csv")); !os.IsNotExist(err) {
			t.Fatalf("refused file stored: %v", err)
		}
		args := " " + string(readOut(t, filepath.Join(filepath.Dir(d.Binary), "args")))
		for _, want := range []string{" --network none ", " --cap-drop ALL ", " --cap-add DAC_OVERRIDE ", " --cap-add DAC_READ_SEARCH ",
			" -v bench-ws-r_1:/workspace:ro ", " -v bench-ws-r_1:/app:ro ", " --entrypoint python3 helper:1 -I -c ",
			" -e BENCH_MATCHES=64 ", " -e BENCH_TOTAL=134217728 ", `"path":"out/*.csv"`, " /workspace /app\n"} {
			if !strings.Contains(args, want) {
				t.Fatalf("helper args lack %q:\n%s", want, args)
			}
		}
		if strings.Contains(args, "/bin/sh") || strings.Contains(args, ":rw") {
			t.Fatalf("helper args %s", args)
		}
	})
	t.Run("no artifacts still makes the folder", func(t *testing.T) {
		r := rc("")
		r.Task.Output.Artifacts = nil
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, "", 9)})
		files, err := e.ExportArtifacts(t.Context(), r)
		if err != nil || files != nil {
			t.Fatalf("files %+v %v", files, err)
		}
		if st, err := os.Stat(filepath.Join(r.Dir, "artifacts")); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
			t.Fatalf("artifacts folder %v %v", st, err)
		}
	})
	t.Run("helper failure is an error and leaves nothing", func(t *testing.T) {
		r := rc("")
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, string(stream[:700]), 1)})
		if _, err := e.ExportArtifacts(t.Context(), r); err == nil {
			t.Fatal("helper failure not an error")
		}
		left, err := os.ReadDir(filepath.Join(r.Dir, "artifacts"))
		if err != nil || len(left) != 0 {
			t.Fatalf("artifacts left %v %v", left, err)
		}
	})
	t.Run("clean exit with a broken stream is an error", func(t *testing.T) {
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, "not a tar", 0)})
		if _, err := e.ExportArtifacts(t.Context(), rc("")); err == nil {
			t.Fatal("broken stream not an error")
		}
	})
	t.Run("bad spec is refused before docker runs", func(t *testing.T) {
		r := rc("")
		r.Task.Output.Artifacts = []schemas.ArtifactSpec{{ID: "x", Path: "../etc/passwd"}}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, string(stream), 0)})
		if _, err := e.ExportArtifacts(t.Context(), r); err == nil {
			t.Fatal("bad spec accepted")
		}
	})
	t.Run("needs a handle", func(t *testing.T) {
		r := rc("")
		r.Handle = nil
		if _, err := newDockerWorkspace(DockerWorkspaceConfig{}).ExportArtifacts(t.Context(), r); err == nil {
			t.Fatal("no handle accepted")
		}
	})
}

func TestReadArtifactStream(t *testing.T) {
	specs := []schemas.ArtifactSpec{{ID: "a", Path: "*.txt", MaxBytes: 8}, {ID: "b", Path: "b/*"}}
	ok := artifactEntry{ID: "a", Name: "x.txt", Source: "x.txt", Bytes: 3}
	t.Run("rules hold again on the bench side", func(t *testing.T) {
		entries := []artifactEntry{
			ok,
			{ID: "a", Name: "big.txt", Source: "big.txt", Bytes: 9},
			{ID: "a", Name: "x.txt", Source: "sub/x.txt", Bytes: 2},
			{ID: "b", Name: "../evil", Source: "b/../evil", Bytes: 1},
			{ID: "b", Source: "b/*", Error: "too many matches: 70, cap 64"},
		}
		dir := t.TempDir()
		r := bytes.NewReader(artifactStream(t, entries, map[int][]byte{0: []byte("abc"), 1: []byte("123456789"), 2: []byte("yz"), 3: []byte("e")}, 0, 1, 2, 3))
		files, err := readArtifactStream(r, dir, specs)
		if err != nil {
			t.Fatal(err)
		}
		wantErr := []string{"", "over the per-file cap of 8 bytes", "name collides with x.txt", "file name not allowed", "too many matches: 70, cap 64"}
		for i, f := range files {
			if f.Error != wantErr[i] || (f.Error == "") != (f.Path != "") {
				t.Fatalf("file %d %+v, want error %q", i, f, wantErr[i])
			}
		}
		var stored []string
		if err := filepath.Walk(filepath.Dir(dir), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				stored = append(stored, p)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(stored) != 1 || stored[0] != filepath.Join(dir, "a", "x.txt") {
			t.Fatalf("stored %v", stored)
		}
	})
	for name, s := range map[string][]byte{
		"empty":           nil,
		"no manifest":     func() []byte { b := artifactStream(t, nil, nil); return b[512:] }(),
		"unknown id":      artifactStream(t, []artifactEntry{{ID: "zz", Name: "x.txt", Source: "x.txt"}}, nil),
		"missing member":  artifactStream(t, []artifactEntry{ok}, nil),
		"size mismatch":   artifactStream(t, []artifactEntry{ok}, map[int][]byte{0: []byte("abcd")}, 0),
		"error member":    artifactStream(t, []artifactEntry{{ID: "a", Name: "x.txt", Source: "x.txt", Error: "not a regular file"}}, map[int][]byte{0: nil}, 0),
		"index too large": artifactStream(t, []artifactEntry{ok}, map[int][]byte{0: []byte("abc"), 1: nil}, 0, 1),
		"repeated member": artifactStream(t, []artifactEntry{ok, ok}, map[int][]byte{0: []byte("abc")}, 0, 0),
		"too many entries": artifactStream(t, func() []artifactEntry {
			out := make([]artifactEntry, 2*(utils.MaxArtifactMatches+1)+1)
			for i := range out {
				out[i] = artifactEntry{ID: "a", Source: "x", Error: "missing"}
			}
			return out
		}(), nil),
		"too many matches": artifactStream(t, func() []artifactEntry {
			out := make([]artifactEntry, utils.MaxArtifactMatches+1)
			for i := range out {
				out[i] = artifactEntry{ID: "a", Name: "n" + strconv.Itoa(i), Source: "x", Error: "not a regular file"}
			}
			return out
		}(), nil),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readArtifactStream(bytes.NewReader(s), t.TempDir(), specs); err == nil {
				t.Fatal("malformed stream accepted")
			}
		})
	}
}

// TestArtifactScript runs the helper script on the host against a real
// folder and reads its stream the way ExportArtifacts does.
func TestArtifactScript(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	root, second, outside := t.TempDir(), t.TempDir(), t.TempDir()
	bin := binaryBytes()
	write := func(dir, rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, rel string) {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "out/a.csv", []byte("a,b\n"))
	write(root, "out/bad name.csv", []byte("x"))
	write(root, "out/sub.csv/keep", []byte("dir"))
	write(outside, "secret.csv", []byte("secret"))
	link(filepath.Join(outside, "secret.csv"), "out/leak.csv")
	write(second, "real.csv", []byte("second root"))
	link(filepath.Join(second, "real.csv"), "out/via-second.csv")
	write(root, "mesh.obj", bin)
	write(root, "big.dat", bytes.Repeat([]byte("z"), 11))
	write(root, "p/x.txt", []byte("one"))
	write(root, "q/x.txt", []byte("two"))
	for i := range 7 {
		write(root, "many/f"+strconv.Itoa(i)+".log", []byte("l"))
	}
	write(root, "t1.bin", bytes.Repeat([]byte("1"), 6))
	write(root, "t2.bin", bytes.Repeat([]byte("2"), 6))
	specs := []schemas.ArtifactSpec{
		{ID: "report", Path: "out/*.csv"},
		{ID: "mesh", Path: "mesh.obj"},
		{ID: "big", Path: "big.dat", MaxBytes: 10},
		{ID: "dup", Path: "*/x.txt"},
		{ID: "many", Path: "many/*.log"},
		{ID: "none", Path: "nothing/*.pdf", Required: true},
		{ID: "total", Path: "t?.bin"},
	}
	reqs := make([]artifactRequest, len(specs))
	for i, a := range specs {
		reqs[i] = artifactRequest{ID: a.ID, Path: a.Path, MaxBytes: utils.ArtifactMaxBytes(a)}
	}
	payload, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	// A total cap that the first t?.bin fits under and the second does not.
	total := 4 + len64("second root") + int64(len(bin)) + 3 + 5 + 6
	cmd := exec.Command(py, "-I", "-c", artifactScript, root, second)
	cmd.Env = []string{"BENCH_SPECS=" + string(payload), "BENCH_TOTAL=" + strconv.FormatInt(total, 10), "BENCH_MATCHES=5"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, stderr.String())
	}
	dir := t.TempDir()
	files, err := readArtifactStream(bytes.NewReader(out), dir, specs)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ id, source, name, err string }
	var got []row
	for _, f := range files {
		got = append(got, row{f.ID, f.Source, f.Name, f.Error})
		if (f.Error == "") != (f.Path != "" && f.SHA256 != "") {
			t.Fatalf("entry %+v", f)
		}
	}
	want := []row{
		{"report", "out/a.csv", "a.csv", ""},
		{"report", "out/bad name.csv", "bad name.csv", "file name not allowed"},
		{"report", "out/leak.csv", "leak.csv", "outside the workspace"},
		{"report", "out/sub.csv", "sub.csv", "not a regular file"},
		{"report", "out/via-second.csv", "via-second.csv", ""},
		{"mesh", "mesh.obj", "mesh.obj", ""},
		{"big", "big.dat", "big.dat", "over the per-file cap of 10 bytes"},
		{"dup", "p/x.txt", "x.txt", ""},
		{"dup", "q/x.txt", "x.txt", "name collides with p/x.txt"},
		{"many", "many/f0.log", "f0.log", ""},
		{"many", "many/f1.log", "f1.log", ""},
		{"many", "many/f2.log", "f2.log", ""},
		{"many", "many/f3.log", "f3.log", ""},
		{"many", "many/f4.log", "f4.log", ""},
		{"many", "many/*.log", "", "too many matches: 7, cap 5"},
		{"none", "nothing/*.pdf", "", "missing: no file matches nothing/*.pdf"},
		{"total", "t1.bin", "t1.bin", ""},
		{"total", "t2.bin", "t2.bin", "over the run's total cap of " + strconv.FormatInt(total, 10) + " bytes"},
	}
	if len(got) != len(want) {
		t.Fatalf("entries\n%+v\nwant\n%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if b := readOut(t, filepath.Join(dir, "mesh", "mesh.obj")); !bytes.Equal(b, bin) {
		t.Fatalf("binary artifact changed: %d bytes", len(b))
	}
	if b := readOut(t, filepath.Join(dir, "report", "via-second.csv")); string(b) != "second root" {
		t.Fatalf("second root file %q", b)
	}
	if files[6].Bytes != 11 {
		t.Fatalf("oversize entry records %d bytes", files[6].Bytes)
	}
	if missing := utils.MissingArtifacts(specs, files); len(missing) != 1 || !strings.HasPrefix(missing[0], "none (nothing/*.pdf: missing: no file matches") {
		t.Fatalf("missing %v", missing)
	}
}

func len64(s string) int64 { return int64(len(s)) }
