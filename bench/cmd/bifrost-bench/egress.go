package main

import (
	"context"
	"crypto/rand"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Recorded internet (docs/egress.md): `egress serve` is the service a task's
// agent reaches as its HTTP(S) proxy; `egress record` runs each task's
// reference solution once with the proxy in record mode and writes the
// task's cache tar; `egress image` builds the service image from this
// binary; `egress show` prints what a cache holds.

func init() {
	register("egress", "recorded internet: egress serve|record|image|show (package mirrors and web downloads recorded at admission, replayed byte for byte)", runEgress)
}

func runEgress(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: "+progName+" egress serve|record|image|show [flags]")
		return exitUsage
	}
	switch args[0] {
	case "serve":
		return egressServe(args[1:])
	case "record":
		return egressRecord(args[1:])
	case "image":
		return egressImage(args[1:])
	case "show":
		return egressShow(args[1:])
	}
	fmt.Fprintf(os.Stderr, "egress: unknown subcommand %q (serve, record, image, show)\n", args[0])
	return exitUsage
}

func egressServe(args []string) int {
	fs := flag.NewFlagSet("egress serve", flag.ContinueOnError)
	mode := fs.String("mode", "", "offline | replay | live-allowlisted | record (default $"+egress.EnvMode+", else replay)")
	listen := fs.String("listen", fmt.Sprintf(":%d", egress.Port), "listen address")
	cachePath := fs.String("cache", egress.ServiceCachePath, "cache tar (replay, and the default allowlist of live-allowlisted)")
	caPath := fs.String("ca", egress.ServiceCAPath, "CA certificate and key PEM")
	logPath := fs.String("log", egress.ServiceLogPath, "request log (JSON lines); empty for none")
	allow := fs.String("allow", "", "hosts allowed in live-allowlisted and record (default $"+egress.EnvAllow+"; record: every host)")
	recDir := fs.String("record-dir", "/egress/rec", "record mode: folder for blobs/ and manifest.jsonl")
	maxBytes := fs.Int64("max-bytes", 8<<30, "record mode: cap on recorded bytes")
	rewrite := fs.String("rewrite", "", "record and live: comma list of from-prefix=to-prefix rewrites of the upstream URL (the recording keeps the asked URL)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	m := *mode
	if m == "" {
		m = os.Getenv(egress.EnvMode)
	}
	if m == "" {
		m = egress.ModeReplay
	}
	if !egress.ValidMode(m) {
		return fail("egress serve: mode %q is not offline, replay, live-allowlisted or record", m)
	}
	al := *allow
	if al == "" {
		al = os.Getenv(egress.EnvAllow)
	}
	srv := &egress.Server{Mode: m, Allow: egress.SplitList(al)}
	rws, err := egress.ParseRewrites(egress.SplitList(*rewrite))
	if err != nil {
		return fail("egress serve: %v", err)
	}
	srv.Rewrite = rws
	if b, err := os.ReadFile(*caPath); err == nil {
		if srv.CA, err = egress.LoadCA(b); err != nil {
			return fail("egress serve: %v", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "egress: no CA at %s (%v): HTTPS requests are refused\n", *caPath, err)
	}
	if m == egress.ModeReplay || m == egress.ModeLive {
		if c, err := egress.OpenCache(*cachePath); err == nil {
			srv.Cache = c
			defer c.Close()
		} else if m == egress.ModeReplay {
			fmt.Fprintf(os.Stderr, "egress: no cache at %s (%v): every request is a miss\n", *cachePath, err)
		}
	}
	if m == egress.ModeRecord {
		rec, err := egress.NewRecorder(*recDir, *maxBytes)
		if err != nil {
			return fail("egress serve: %v", err)
		}
		defer rec.Close()
		srv.Recorder = rec
	}
	if *logPath != "" {
		if err := os.MkdirAll(filepath.Dir(*logPath), 0o755); err != nil {
			return fail("egress serve: %v", err)
		}
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fail("egress serve: %v", err)
		}
		defer f.Close()
		srv.Log = f
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fail("egress serve: %v", err)
	}
	entries := 0
	if srv.Cache != nil {
		entries = len(srv.Cache.Entries())
	}
	fmt.Fprintf(os.Stderr, "egress: listening on %s, mode %s, %d recorded responses\n", ln.Addr(), m, entries)
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 60 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail("egress serve: %v", err)
	}
	c, _ := json.Marshal(srv.Counts())
	fmt.Fprintf(os.Stderr, "egress: stopped; %s\n", c)
	return exitOK
}

// egressImage builds the service image: this binary (static) and the
// host's root certificates on scratch.
func egressImage(args []string) int {
	fs := flag.NewFlagSet("egress image", flag.ContinueOnError)
	tag := fs.String("tag", egress.DefaultImage, "image tag")
	bin := fs.String("binary", "", "static bifrost-bench binary to put in the image (default this one)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	exe := *bin
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return fail("egress image: %v", err)
		}
	}
	if f, err := elf.Open(exe); err == nil {
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				f.Close()
				return fail("egress image: %s is dynamically linked; build it with CGO_ENABLED=0", exe)
			}
		}
		f.Close()
	}
	var roots string
	for _, p := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/cert.pem"} {
		if _, err := os.Stat(p); err == nil {
			roots = p
			break
		}
	}
	if roots == "" {
		return fail("egress image: no system root certificates found on this host")
	}
	dir, err := os.MkdirTemp("", "egress-image-")
	if err != nil {
		return fail("egress image: %v", err)
	}
	defer os.RemoveAll(dir)
	if err := copyFile(exe, filepath.Join(dir, "heimdall-egress"), 0o755); err != nil {
		return fail("egress image: %v", err)
	}
	if err := copyFile(roots, filepath.Join(dir, "ca-certificates.crt"), 0o644); err != nil {
		return fail("egress image: %v", err)
	}
	df := "FROM scratch\nCOPY heimdall-egress /heimdall-egress\nCOPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt\n" +
		fmt.Sprintf("EXPOSE %d\nENTRYPOINT [\"/heimdall-egress\", \"egress\", \"serve\"]\n", egress.Port)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0o644); err != nil {
		return fail("egress image: %v", err)
	}
	d := utils.Docker{Binary: "docker"}
	if err := d.Stream(context.Background(), []string{"build", "--network", "none", "--label", utils.LabelBench + "=1", "-t", *tag, dir}, os.Stderr); err != nil {
		return fail("egress image: %v", err)
	}
	fmt.Println(*tag)
	return exitOK
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// egressShow prints a cache tar's rows and hosts.
func egressShow(args []string) int {
	fs := flag.NewFlagSet("egress show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: "+progName+" egress show [-json] <cache.tar | task folder>")
		return exitUsage
	}
	p := fs.Arg(0)
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		p = filepath.Join(p, filepath.FromSlash(egress.TaskCacheFile))
	}
	c, err := egress.OpenCache(p)
	if err != nil {
		return fail("egress show: %v", err)
	}
	defer c.Close()
	sum, _ := utils.SHA256File(p)
	var total int64
	for _, e := range c.Entries() {
		total += e.Size
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"cache": p, "sha256": sum, "responses": len(c.Entries()), "bytes": total, "hosts": egress.Hosts(c.Entries())})
		return exitOK
	}
	fmt.Printf("%s sha256 %s: %d responses, %d bytes\nhosts: %s\n", p, sum, len(c.Entries()), total, strings.Join(egress.Hosts(c.Entries()), ", "))
	for _, e := range c.Entries() {
		fmt.Printf("  %d %s %s (%d bytes)\n", e.Status, e.Method, e.URL, e.Size)
	}
	return exitOK
}

// recordResult is one task's recording.
type recordResult struct {
	Task      string   `json:"task"`
	Responses int      `json:"responses"`
	Bytes     int64    `json:"bytes"`
	Hosts     []string `json:"hosts,omitempty"`
	RefExit   int      `json:"reference_exit"`
	Reward    string   `json:"reward,omitempty"` // the verifier's result file after the reference, run in the same container
	Seconds   int      `json:"seconds"`
	Error     string   `json:"error,omitempty"`
}

func egressRecord(args []string) int {
	fs := flag.NewFlagSet("egress record", flag.ContinueOnError)
	suiteDir := fs.String("suite", "", "suite folder")
	tasks := fs.String("tasks", "", "comma list of task ids (default every task with an egress service)")
	parallel := fs.Int("parallel", 2, "tasks recorded at once")
	image := fs.String("image", egress.DefaultImage, "egress service image (build it with `egress image`)")
	logs := fs.String("logs", "", "folder for each task's reference output (default <suite>/.egress-record)")
	timeoutS := fs.Int("timeout-s", 0, "reference time limit (default the task's agent_s, at least 900)")
	maxBytes := fs.Int64("max-bytes", 8<<30, "cap on recorded bytes per task")
	keep := fs.Bool("keep-existing", false, "skip tasks whose cache tar already holds responses")
	rewrite := fs.String("rewrite", "", "comma list of from-prefix=to-prefix upstream rewrites for the recording proxy (for example a dated snapshot.debian.org archive)")
	if err := fs.Parse(args); err != nil || *suiteDir == "" {
		fmt.Fprintln(os.Stderr, "usage: "+progName+" egress record -suite <dir> [-tasks a,b] [-parallel n]")
		return exitUsage
	}
	root, err := filepath.Abs(*suiteDir)
	if err != nil {
		return fail("egress record: %v", err)
	}
	head, err := suite.LoadHead(root)
	if err != nil {
		return fail("egress record: %v", err)
	}
	ids := head.TaskIDs
	if *tasks != "" {
		ids = egress.SplitList(*tasks)
	}
	logDir := *logs
	if logDir == "" {
		logDir = filepath.Join(root, ".egress-record")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fail("egress record: %v", err)
	}
	d := utils.Docker{Binary: "docker"}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if _, ok, err := d.ImageID(ctx, *image); err != nil || !ok {
		return fail("egress record: image %s is missing; run `%s egress image` first", *image, progName)
	}
	work := make(chan string)
	var mu sync.Mutex
	var results []recordResult
	var wg sync.WaitGroup
	for i := 0; i < max(1, *parallel); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				r := recordTask(ctx, d, root, id, *image, logDir, *timeoutS, *maxBytes, *keep, *rewrite)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
				line, _ := json.Marshal(r)
				fmt.Println(string(line))
			}
		}()
	}
	for _, id := range ids {
		work <- id
	}
	close(work)
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Task < results[j].Task })
	bad := 0
	for _, r := range results {
		if r.Error != "" {
			bad++
		}
	}
	fmt.Fprintf(os.Stderr, "egress record: %d tasks, %d failed; run `suite lock` next (the cache tars change the fixtures hash)\n", len(results), bad)
	if bad > 0 {
		return exitInfra
	}
	return exitOK
}

func egressService(t *schemas.Task) *schemas.ServiceSpec {
	for i := range t.Services {
		if t.Services[i].Role == egress.ServiceRole {
			return &t.Services[i]
		}
	}
	return nil
}

// recordTask runs one task's reference with the proxy in record mode on a
// network with internet, then packs the recording into the task's cache
// tar. The containers and the network carry a run label of their own and
// are removed at the end.
func recordTask(ctx context.Context, d utils.Docker, root, id, image, logDir string, timeoutS int, maxBytes int64, keep bool, rewrite string) (res recordResult) {
	res = recordResult{Task: id, RefExit: -1}
	start := time.Now()
	defer func() { res.Seconds = int(time.Since(start).Seconds()) }()
	s, t, err := suite.LoadTask(filepath.Dir(root), root, id)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	svc := egressService(t)
	if svc == nil {
		res.Error = "task has no egress service (import with -set egress=record)"
		return res
	}
	cachePath := filepath.Join(t.Dir, filepath.FromSlash(egress.TaskCacheFile))
	if keep {
		if c, err := egress.OpenCache(cachePath); err == nil {
			n := len(c.Entries())
			c.Close()
			if n > 0 {
				res.Responses, res.Error = n, ""
				return res
			}
		}
	}
	caPath := filepath.Join(t.Dir, filepath.FromSlash(egress.TaskCAFile))
	if _, err := os.Stat(caPath); err != nil {
		res.Error = "no " + egress.TaskCAFile + ": " + err.Error()
		return res
	}
	logf, err := os.Create(filepath.Join(logDir, id+".log"))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer logf.Close()
	tag, _, err := environments.EnsureTaskImage(ctx, d, s, t, logf)
	if err != nil || tag == "" {
		res.Error = fmt.Sprintf("task image: %v", err)
		return res
	}
	sfx := make([]byte, 4)
	rand.Read(sfx)
	runID := "egrec-" + hex.EncodeToString(sfx)
	labels := utils.LabelArgs("egress-record", runID)
	netName := "bench-egrec-" + runID
	proxyName := "bench-" + runID + "-egress"
	taskName := "bench-" + runID + "-task"
	recDir, err := os.MkdirTemp(logDir, id+"-rec-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(recDir)
	os.Chmod(recDir, 0o777)
	defer func() {
		bg := context.Background()
		d.Run(bg, "rm", "-f", taskName, proxyName)
		d.Run(bg, "network", "rm", netName)
	}()
	if _, err := d.MustRun(ctx, append(append([]string{"network", "create"}, labels...), netName)...); err != nil {
		res.Error = "network: " + err.Error()
		return res
	}
	absCA, _ := filepath.Abs(caPath)
	pargs := append([]string{"run", "-d", "--name", proxyName}, labels...)
	pargs = append(pargs, "--network", netName, "--network-alias", egress.ServiceID,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-v", recDir+":/egress/rec", "-v", absCA+":"+egress.ServiceCAPath+":ro",
		"-e", egress.EnvMode+"="+egress.ModeRecord, image,
		"-log", "/egress/rec/requests.jsonl", "-record-dir", "/egress/rec", fmt.Sprintf("-max-bytes=%d", maxBytes))
	if rewrite != "" {
		pargs = append(pargs, "-rewrite", rewrite)
	}
	if _, err := d.MustRun(ctx, pargs...); err != nil {
		res.Error = "egress container: " + err.Error()
		return res
	}
	user := "1000:1000"
	if utils.TaskRunsAsRoot(t) {
		user = "0:0"
	}
	wd := ""
	if t.Environment != nil {
		wd = t.Environment.WorkspacePath
	}
	if wd == "" {
		wd = s.Workdir
	}
	targs := append([]string{"run", "-d", "--name", taskName}, labels...)
	targs = append(targs, "--network", netName, "--user", user, "--cap-drop", "ALL", "--security-opt", "no-new-privileges")
	if user == "0:0" {
		for _, c := range utils.RootCaps {
			targs = append(targs, "--cap-add", c)
		}
	}
	if wd != "" {
		targs = append(targs, "-w", wd)
	}
	env := map[string]string{}
	if t.Environment != nil {
		for k, v := range t.Environment.Env {
			env[k] = v
		}
	}
	for k, v := range egress.ClientEnv(egress.ProxyURL(egress.ServiceID), []string{egress.ServiceID}) {
		env[k] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		targs = append(targs, "-e", k+"="+env[k])
	}
	targs = append(targs, "--entrypoint", "sleep", tag, "infinity")
	if _, err := d.MustRun(ctx, targs...); err != nil {
		res.Error = "task container: " + err.Error()
		return res
	}
	ref := filepath.Join(t.Dir, filepath.FromSlash(t.Controls.Reference))
	if _, err := d.MustRun(ctx, "exec", "-u", "0", taskName, "mkdir", "-p", "/tmp/bench"); err != nil {
		res.Error = "prepare: " + err.Error()
		return res
	}
	if _, err := d.MustRun(ctx, "cp", ref, taskName+":/tmp/bench/reference.sh"); err != nil {
		res.Error = "copy reference: " + err.Error()
		return res
	}
	limit := timeoutS
	if limit <= 0 {
		limit = max(t.Timeouts.AgentS, 900)
	}
	rctx, rcancel := context.WithTimeout(ctx, time.Duration(limit)*time.Second)
	defer rcancel()
	r, err := d.Run(rctx, "exec", taskName, "sh", "/tmp/bench/reference.sh")
	if r != nil {
		logf.Write(r.Stdout)
		logf.Write(r.Stderr)
		res.RefExit = r.ExitCode
	}
	if err != nil {
		res.RefExit = 137
		fmt.Fprintf(logf, "\nreference: %v\n", err)
	}
	res.Reward = recordVerify(ctx, d, s, t, taskName, wd, logf)
	if _, err := d.Run(context.Background(), "stop", "-t", "10", proxyName); err != nil {
		res.Error = "stop egress: " + err.Error()
		return res
	}
	tmp := cachePath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	rows, err := egress.Pack(recDir, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		res.Error = "pack: " + err.Error()
		return res
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		res.Error = err.Error()
		return res
	}
	if b, err := os.ReadFile(filepath.Join(recDir, "requests.jsonl")); err == nil {
		os.WriteFile(filepath.Join(logDir, id+".requests.jsonl"), b, 0o644)
	}
	res.Responses = len(rows)
	for _, e := range rows {
		res.Bytes += e.Size
	}
	res.Hosts = egress.Hosts(rows)
	if res.RefExit != 0 {
		res.Error = fmt.Sprintf("reference exited %d (recording kept; see %s)", res.RefExit, filepath.Join(logDir, id+".log"))
	}
	return res
}

// recordVerify runs the suite's verifier command in the task container after
// the reference, still through the recording proxy, so a test script that
// downloads at verify time is recorded too, and returns the result file. It
// is a preview only: admission grades again, in replay mode.
func recordVerify(ctx context.Context, d utils.Docker, s *schemas.Suite, t *schemas.Task, container, wd string, logf io.Writer) string {
	if len(s.Verifier.Command) == 0 || t.Verifier.Tests == "" {
		return ""
	}
	tests := filepath.Join(t.Dir, filepath.FromSlash(t.Verifier.Tests))
	if _, err := d.MustRun(ctx, "exec", "-u", "0", container, "mkdir", "-p", "/grader/tests", "/grader/out"); err != nil {
		return "error: " + err.Error()
	}
	if _, err := d.MustRun(ctx, "cp", tests+"/.", container+":/grader/tests"); err != nil {
		return "error: " + err.Error()
	}
	limit := s.Verifier.TimeoutS
	if limit <= 0 {
		limit = 900
	}
	vctx, cancel := context.WithTimeout(ctx, time.Duration(limit)*time.Second)
	defer cancel()
	args := []string{"exec", "-u", "0"}
	if wd != "" {
		args = append(args, "-w", wd)
	}
	r, err := d.Run(vctx, append(append(args, container), s.Verifier.Command...)...)
	if r != nil {
		fmt.Fprintf(logf, "\n=== verifier (record preview)\n")
		logf.Write(r.Stdout)
		logf.Write(r.Stderr)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	rp := s.Verifier.ResultPath
	if rp == "" {
		rp = "/grader/out/reward.json"
	}
	out, err := d.Run(ctx, "exec", container, "cat", rp)
	if err != nil || out.ExitCode != 0 {
		return fmt.Sprintf("no result (verifier exit %d)", r.ExitCode)
	}
	return strings.TrimSpace(string(out.Stdout))
}
