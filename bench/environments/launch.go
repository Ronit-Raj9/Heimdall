package environments

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

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
// default HOME. It is pure.
func buildLaunch(plan *schemas.RunPlan, pathValue, home string) (*launch, error) {
	l := &launch{}
	var budget int
	fileChunks, err := encodeFiles(plan.Files, &l.Env, &budget)
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
	if len(fileChunks) == 0 && len(wrapped) == 0 {
		if len(plan.Argv) > 0 {
			l.Entrypoint, l.Args = plan.Argv[0], append([]string(nil), plan.Argv[1:]...)
		}
		return l, nil
	}
	if len(plan.Argv) == 0 {
		return nil, fmt.Errorf("plan has files or multi-line env but no argv to exec")
	}
	l.Entrypoint = wrapShell
	l.Args = append([]string{"-c", wrapScript(fileChunks, wrapped), wrapName}, plan.Argv...)
	return l, nil
}

// encodeFiles appends the path and content chunks of every plan file, in path
// order, and returns the chunk count of each.
func encodeFiles(files map[string]string, env *[]string, budget *int) ([]int, error) {
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

// wrapScript is the `sh -c` program that writes the plan files, exports the
// wrapped variables, removes every BENCH_WRAP_* variable and execs "$@".
func wrapScript(fileChunks []int, envs []wrapEnv) string {
	var b strings.Builder
	var unset []string
	b.WriteString("set -e\n")
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

// writeEnvFile writes lines to a private temp file outside the run folder, so
// secrets never appear in the docker CLI's argv or in the sealed run. The
// caller removes it once the container is created.
func writeEnvFile(lines []string) (string, error) {
	f, err := os.CreateTemp("", "bench-env-*")
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
