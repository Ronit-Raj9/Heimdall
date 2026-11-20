package environments

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	// wrapPrefix is reserved for the variables the file wrapper decodes.
	wrapPrefix = "BENCH_WRAP_"
	// envChunk keeps every env file line under the docker CLI's 64 KiB
	// line limit (bufio.Scanner).
	envChunk = 32 << 10
	// maxWrapBytes bounds the decoded size of everything delivered through
	// the wrapper, so the container's environment stays well under ARG_MAX.
	maxWrapBytes = 512 << 10
	// wrapShell is absolute so a shim on PATH can never stand in for it.
	wrapShell = "/bin/sh"
	wrapName  = "bench-wrap"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// launch is how the agent container starts: the entrypoint, the arguments
// after the image and the environment lines written to the env file.
type launch struct {
	Entrypoint string   // empty keeps the image's own entrypoint
	Args       []string // arguments after the image name
	Env        []string // KEY=VALUE lines for --env-file
}

// link is a symlink the wrapper makes before the agent starts: dst, in a
// folder the agent user owns, points at src, where a volume is mounted.
type link struct {
	dst string
	src string
}

// wrapEnv is a variable the wrapper exports after decoding it, because its
// value cannot travel in an env file line.
type wrapEnv struct {
	name   string
	chunks int
}

// buildLaunch turns a plan into a launch. Plan files and env values that an
// env file cannot carry (newlines, very long values) are base64-encoded into
// BENCH_WRAP_* variables, and the command is wrapped in `sh -c` that decodes
// them and then execs the argv. No plan content is ever spliced into the
// script: the script names only the generated variables, and the argv is
// passed as positional parameters. pathValue replaces PATH; home is the
// default HOME. links are made first, as the agent user, so a plan file
// under a link's parent lands in a folder the agent owns. It is pure.
func buildLaunch(plan *schemas.RunPlan, pathValue, home string, links []link) (*launch, error) {
	return buildLaunchView(plan, pathValue, home, links, standardView)
}

// buildLaunchView is buildLaunch with the wrapper's variable prefix and $0
// taken from the run's view (realism.go): a neutral run's wrapper names
// nothing after the bench. The standard view gives exactly buildLaunch.
func buildLaunchView(plan *schemas.RunPlan, pathValue, home string, links []link, v agentView) (*launch, error) { // Eval integrity area (S2)
	wrapPrefix, wrapName := v.wrapPrefix, v.wrapName
	l := &launch{}
	var budget int
	for i, k := range links {
		if !path.IsAbs(k.dst) || path.Clean(k.dst) != k.dst || !path.IsAbs(k.src) || strings.ContainsAny(k.dst+k.src, "\x00\n") {
			return nil, fmt.Errorf("link %q -> %q must join absolute clean paths", k.dst, k.src)
		}
		l.Env = append(l.Env, fmt.Sprintf("%sL%d_DST=%s", wrapPrefix, i, k.dst), fmt.Sprintf("%sL%d_SRC=%s", wrapPrefix, i, k.src))
	}
	fileChunks, err := encodeFiles(plan.Files, &l.Env, &budget, wrapPrefix)
	if err != nil {
		return nil, err
	}
	env := map[string]string{"HOME": home}
	for k, v := range plan.Env {
		env[k] = v
	}
	env["PATH"] = pathValue
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var wrapped []wrapEnv
	for _, k := range keys {
		v := env[k]
		if !envKeyRe.MatchString(k) {
			return nil, fmt.Errorf("plan env: invalid variable name %q", k)
		}
		if strings.HasPrefix(k, wrapPrefix) {
			return nil, fmt.Errorf("plan env: variable %s uses the reserved prefix %s", k, wrapPrefix)
		}
		if strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("plan env: variable %s contains a NUL byte", k)
		}
		if !strings.ContainsAny(v, "\r\n") && len(k)+1+len(v) <= envChunk {
			l.Env = append(l.Env, k+"="+v)
			continue
		}
		budget += len(v)
		prefix := fmt.Sprintf("%sE%d_", wrapPrefix, len(wrapped))
		wrapped = append(wrapped, wrapEnv{name: k, chunks: appendChunks(&l.Env, prefix, []byte(v))})
	}
	if budget > maxWrapBytes {
		return nil, fmt.Errorf("plan files and long env values total %d bytes, over the %d byte limit", budget, maxWrapBytes)
	}
	if len(fileChunks) == 0 && len(wrapped) == 0 && len(links) == 0 {
		if len(plan.Argv) > 0 {
			l.Entrypoint, l.Args = plan.Argv[0], append([]string(nil), plan.Argv[1:]...)
		}
		return l, nil
	}
	if len(plan.Argv) == 0 {
		return nil, fmt.Errorf("plan has files, folders, memory paths or multi-line env but no argv to exec")
	}
	l.Entrypoint = wrapShell
	l.Args = append([]string{"-c", wrapScriptPrefix(len(links), fileChunks, wrapped, wrapPrefix), wrapName}, plan.Argv...)
	return l, nil
}

// encodeFiles appends the path and content chunks of every plan file, in path
// order, and returns the chunk count of each.
func encodeFiles(files map[string]string, env *[]string, budget *int, wrapPrefix string) ([]int, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		if !path.IsAbs(p) || path.Clean(p) != p || p == "/" || strings.ContainsAny(p, "\x00\n") {
			return nil, fmt.Errorf("plan file path %q must be absolute and clean", p)
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	counts := make([]int, 0, len(paths))
	for i, p := range paths {
		*budget += len(files[p])
		prefix := fmt.Sprintf("%sF%d_", wrapPrefix, i)
		*env = append(*env, prefix+"PATH="+p)
		counts = append(counts, appendChunks(env, prefix, []byte(files[p])))
	}
	return counts, nil
}

// appendChunks base64-encodes data into prefix0, prefix1, ... and returns how
// many variables it used. Empty data still uses one (empty) variable.
func appendChunks(env *[]string, prefix string, data []byte) int {
	enc := base64.StdEncoding.EncodeToString(data)
	n := 0
	for {
		end := min(envChunk, len(enc))
		*env = append(*env, fmt.Sprintf("%s%d=%s", prefix, n, enc[:end]))
		enc = enc[end:]
		n++
		if enc == "" {
			return n
		}
	}
}

// wrapScript is the `sh -c` program that makes the links, writes the plan
// files, exports the wrapped variables, removes every BENCH_WRAP_* variable
// and execs "$@".
func wrapScript(links int, fileChunks []int, envs []wrapEnv) string {
	return wrapScriptPrefix(links, fileChunks, envs, wrapPrefix)
}

// wrapScriptPrefix is wrapScript for the variables named with wrapPrefix.
func wrapScriptPrefix(links int, fileChunks []int, envs []wrapEnv, wrapPrefix string) string { // Eval integrity area (S2)
	var b strings.Builder
	var unset []string
	b.WriteString("set -e\n")
	for i := 0; i < links; i++ {
		prefix := fmt.Sprintf("%sL%d_", wrapPrefix, i)
		fmt.Fprintf(&b, "mkdir -p \"$(dirname \"$%sDST\")\"\n", prefix)
		fmt.Fprintf(&b, "ln -sfn \"$%sSRC\" \"$%sDST\"\n", prefix, prefix)
		unset = append(unset, prefix+"DST", prefix+"SRC")
	}
	for i, n := range fileChunks {
		prefix := fmt.Sprintf("%sF%d_", wrapPrefix, i)
		fmt.Fprintf(&b, "%sP=\"$%sPATH\"\n", wrapPrefix, prefix)
		fmt.Fprintf(&b, "mkdir -p \"$(dirname \"$%sP\")\"\n", wrapPrefix)
		fmt.Fprintf(&b, "printf '%%s'%s | base64 -d > \"$%sP\"\n", chunkRefs(prefix, n), wrapPrefix)
		unset = append(unset, prefix+"PATH")
		unset = append(unset, chunkNames(prefix, n)...)
	}
	for i, e := range envs {
		prefix := fmt.Sprintf("%sE%d_", wrapPrefix, i)
		fmt.Fprintf(&b, "%sV=$(printf '%%s'%s | base64 -d; printf x)\n", wrapPrefix, chunkRefs(prefix, e.chunks))
		fmt.Fprintf(&b, "export %s=\"${%sV%%x}\"\n", e.name, wrapPrefix)
		unset = append(unset, chunkNames(prefix, e.chunks)...)
	}
	unset = append(unset, wrapPrefix+"P", wrapPrefix+"V")
	fmt.Fprintf(&b, "unset %s\n", strings.Join(unset, " "))
	b.WriteString("exec \"$@\"\n")
	return b.String()
}

func chunkNames(prefix string, n int) []string {
	out := make([]string, n)
	for j := range out {
		out[j] = fmt.Sprintf("%s%d", prefix, j)
	}
	return out
}

func chunkRefs(prefix string, n int) string {
	var b strings.Builder
	for _, name := range chunkNames(prefix, n) {
		fmt.Fprintf(&b, " \"$%s\"", name)
	}
	return b.String()
}

// envFileHook, when set (tests only), sees every env file's lines.
var envFileHook func(lines []string)

// envStageOnce sweeps the env file staging folder once per process.
var envStageOnce sync.Once

// envStageDir is the private folder env files are staged in: one per user
// under the temp folder, mode 0700. Each file is named after the process
// that wrote it, so a later process can find and remove the files of one
// that died before it could (sweepEnvFiles).
func envStageDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "bench-env-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("env file folder: %w", err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("env file folder: %w", err)
	}
	if !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("env file folder %s is not a private folder", dir)
	}
	return dir, nil
}

// sweepEnvFiles removes staged env files whose writing process is gone: a
// hard crash between writing an env file and removing it would otherwise
// leave gateway keys and route tokens on disk.
func sweepEnvFiles(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		pid, ok := envFilePID(ent.Name())
		if !ok || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, ent.Name()))
	}
}

// envFilePID is the pid in an env file name "env-<pid>-<random>". It is pure.
func envFilePID(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "env-")
	if !ok {
		return 0, false
	}
	n, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(n)
	return pid, err == nil && pid > 0
}

// processAlive reports whether pid names a live process (signal 0). A
// process owned by another user (EPERM) counts as alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// writeEnvFile writes lines to a private file outside the run folder, so
// secrets never appear in the docker CLI's argv or in the sealed run. The
// caller removes it once the container is created; a file left by a process
// that died first is removed by the next process that writes one.
func writeEnvFile(lines []string) (string, error) {
	if envFileHook != nil {
		envFileHook(lines)
	}
	dir, err := envStageDir()
	if err != nil {
		return "", err
	}
	envStageOnce.Do(func() { sweepEnvFiles(dir) })
	f, err := os.CreateTemp(dir, "env-"+strconv.Itoa(os.Getpid())+"-*")
	if err != nil {
		return "", fmt.Errorf("create env file: %w", err)
	}
	_, werr := io.WriteString(f, strings.Join(lines, "\n")+"\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		if rerr := os.Remove(f.Name()); rerr != nil && !os.IsNotExist(rerr) {
			return "", fmt.Errorf("write env file: %v; remove: %w", firstErr(werr, cerr), rerr)
		}
		return "", fmt.Errorf("write env file: %w", firstErr(werr, cerr))
	}
	return f.Name(), nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// cappedWriter writes at most limit bytes to w and silently drops the rest,
// so a chatty agent never sees a broken pipe and never fills the disk.
type cappedWriter struct {
	w         io.Writer
	limit     int64
	n         int64
	truncated bool
	err       error // first write error, reported after the process ends
}

// Write implements io.Writer. It always reports the full length as written.
func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return len(p), nil
	}
	room := c.limit - c.n
	chunk := p
	if int64(len(chunk)) > room {
		chunk = chunk[:max(room, 0)]
		c.truncated = true
	}
	if len(chunk) > 0 {
		n, err := c.w.Write(chunk)
		c.n += int64(n)
		if err != nil {
			c.err = err
		}
	}
	return len(p), nil
}
