package main

// `images`: the library registry tool for the operator and for runners
// (Z13, INFRA_I4 item 4, bench/imagepin, docs/library-registry.md).
//
//	images sync <suite>      push the suite's images, record their digests, re-lock the suite
//	images prefetch <suite>  pull the images the suite lock pins by digest that this machine lacks
//	images layers <suite>    how much the pinned images share, from the registry's manifests
//	images token-key         the key the server signs pull tokens with, and the registry's certificate
//	images login             docker login to the library registry with this runner's token

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("images", "library registry: push suite images by digest, prefetch, layer report, pull token key (images sync|prefetch|layers|token-key|login)", runImages)
}

const imagesUsage = `usage:
  bifrost-bench images sync <suite> -registry <host[:port]> -auth-file <user:password file> [-namespace library] [-plain-http] [-lock=true] [-root dir] [-config file]
  bifrost-bench images prefetch <suite>... [-concurrency 2] [-json] [-root dir] [-config file]
  bifrost-bench images layers <suite> -auth-file <file> [-plain-http] [-json] [-root dir] [-config file]
  bifrost-bench images token-key -out <dir> [-name text]
  bifrost-bench images login -registry <host[:port]> -token-file <file>`

func runImages(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "sync":
		return runImagesSync(rest)
	case "prefetch":
		return runImagesPrefetch(rest)
	case "layers":
		return runImagesLayers(rest)
	case "token-key":
		return runImagesTokenKey(rest)
	case "login":
		return runImagesLogin(rest)
	}
	fmt.Fprintln(os.Stderr, imagesUsage)
	return exitUsage
}

type imagesSuiteFlags struct {
	root, config string
	fs           *flag.FlagSet
}

func newImagesFlags(name string) *imagesSuiteFlags {
	f := &imagesSuiteFlags{fs: flag.NewFlagSet("images "+name, flag.ContinueOnError)}
	f.fs.SetOutput(io.Discard)
	f.fs.StringVar(&f.root, "root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	f.fs.StringVar(&f.config, "config", defaultConfig, "bench config")
	return f
}

// suites loads the named suites and the helper image of the config.
func (f *imagesSuiteFlags) suites(ctx context.Context, refs []string) (string, string, []*schemas.Suite, error) {
	benchRoot, cfg, err := benchmarksRoot(f.fs, f.root, f.config)
	if err != nil {
		return "", "", nil, err
	}
	helper := ""
	if cfg != nil {
		helper = cfg.Images.Helper
	}
	var out []*schemas.Suite
	for _, r := range refs {
		s, err := loadSuite(ctx, benchRoot, r)
		if err != nil {
			return "", "", nil, fmt.Errorf("load suite %s: %w", r, err)
		}
		out = append(out, s)
	}
	return benchRoot, helper, out, nil
}

func runImagesSync(args []string) int {
	f := newImagesFlags("sync")
	registry := f.fs.String("registry", "", "library registry host[:port]")
	authFile := f.fs.String("auth-file", "", "file with the sync identity as user:password (mode 0600)")
	namespace := f.fs.String("namespace", imagepin.DefaultNamespace, "registry path the library lives under")
	plain := f.fs.Bool("plain-http", false, "the registry speaks plain HTTP (a local test registry)")
	relock := f.fs.Bool("lock", true, "re-lock the suite afterwards so suite.lock records the digests")
	pos, err := parseArgs(f.fs, args)
	if err != nil || len(pos) != 1 || *registry == "" || *authFile == "" {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	creds, err := imagepin.ReadCredentials(*authFile)
	if err != nil {
		return fail("%v", err)
	}
	ctx := context.Background()
	benchRoot, helper, suites, err := f.suites(ctx, pos)
	if err != nil {
		return fail("%v", err)
	}
	s := suites[0]
	tags := imagepin.SuiteImages(s, helper)
	fmt.Printf("images sync %s: %d image(s) to %s/%s/%s\n", s.ID, len(tags), *registry, *namespace, s.ID)
	res, err := imagepin.Sync(ctx, imagepin.SyncOptions{Registry: *registry, Namespace: *namespace, Suite: s.ID, Tags: tags,
		Push: creds, PlainHTTP: *plain, Docker: utils.Docker{}, Log: os.Stdout})
	if err != nil {
		return fail("%v", err)
	}
	n, err := imagepin.MergePins(imagepin.PinsPath(s.Root), res)
	if err != nil {
		return fail("%v", err)
	}
	failed, missing := 0, 0
	for _, r := range res {
		switch {
		case r.Error != "":
			failed++
		case r.Missing:
			missing++
		}
	}
	fmt.Printf("%d pinned in %s, %d missing on this machine, %d failed\n", n, imagepin.PinsPath(s.Root), missing, failed)
	if *relock && n > 0 {
		_, cfg, _ := benchmarksRoot(f.fs, f.root, f.config)
		if _, err := ops.SuiteLock(ctx, ops.LockOptions{BenchRoot: benchRoot, Ref: s.ID, Config: cfg, Docker: utils.Docker{}}, os.Stdout); err != nil {
			return fail("lock: %v", err)
		}
	}
	if failed > 0 {
		return 1
	}
	return exitOK
}

func runImagesPrefetch(args []string) int {
	f := newImagesFlags("prefetch")
	conc := f.fs.Int("concurrency", 2, "pulls at once")
	asJSON := f.fs.Bool("json", false, "print the result as JSON")
	pos, err := parseArgs(f.fs, args)
	if err != nil || len(pos) == 0 || *conc < 1 || *conc > 16 {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	ctx := context.Background()
	_, _, suites, err := f.suites(ctx, pos)
	if err != nil {
		return fail("%v", err)
	}
	var wants []imagepin.Want
	seen := map[string]bool{}
	for _, s := range suites {
		for _, w := range imagepin.LockedImages(s) {
			if !seen[w.Tag] {
				seen[w.Tag] = true
				wants = append(wants, w)
			}
		}
	}
	if len(wants) == 0 {
		fmt.Println("no image is pinned by registry digest in these suite locks (run images sync first); nothing to prefetch")
		return exitOK
	}
	logw := io.Writer(os.Stdout)
	if *asJSON {
		logw = os.Stderr
	}
	start := time.Now()
	res := imagepin.Prefetch(ctx, utils.Docker{}, nil, wants, *conc, logw)
	counts := map[string]int{}
	for _, r := range res {
		counts[r.State]++
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"images": res, "elapsed_ns": time.Since(start)})
	} else {
		fmt.Printf("%d image(s) in %s: %d warm, %d pulled, %d differ, %d failed\n", len(res), time.Since(start).Round(time.Millisecond),
			counts[imagepin.PrefetchWarm], counts[imagepin.PrefetchPulled], counts[imagepin.PrefetchDiffers], counts[imagepin.PrefetchFailed])
	}
	if counts[imagepin.PrefetchFailed] > 0 {
		return 1
	}
	return exitOK
}

func runImagesLayers(args []string) int {
	f := newImagesFlags("layers")
	authFile := f.fs.String("auth-file", "", "file with a registry identity as user:password")
	plain := f.fs.Bool("plain-http", false, "the registry speaks plain HTTP")
	asJSON := f.fs.Bool("json", false, "print the report as JSON")
	pos, err := parseArgs(f.fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	var creds imagepin.Credentials
	if *authFile != "" {
		if creds, err = imagepin.ReadCredentials(*authFile); err != nil {
			return fail("%v", err)
		}
	}
	ctx := context.Background()
	_, _, suites, err := f.suites(ctx, pos)
	if err != nil {
		return fail("%v", err)
	}
	wants := imagepin.LockedImages(suites[0])
	if len(wants) == 0 {
		return fail("suite %s: no image is pinned by registry digest in its lock (run images sync first)", suites[0].ID)
	}
	host, _, _ := strings.Cut(wants[0].Pinned, "/")
	rep := imagepin.Layers(ctx, &imagepin.Client{Host: host, PlainHTTP: *plain, User: creds.User, Password: creds.Password}, wants)
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(rep)
		return exitOK
	}
	for _, im := range rep.Images {
		line := fmt.Sprintf("  %-60s %8s  %d layers", im.Tag, humanBytes(im.Bytes), len(im.Layers))
		if im.Error != "" {
			line += "  error: " + im.Error
		}
		fmt.Println(line)
	}
	fmt.Printf("%d image(s): %s pulled one by one, %s distinct in the registry, %s in layers shared by more than one image\n",
		len(rep.Images), humanBytes(rep.TotalBytes), humanBytes(rep.UniqueBytes), humanBytes(rep.SharedBytes))
	for i, l := range rep.SharedLayers {
		if i == 5 {
			break
		}
		fmt.Printf("  shared %s  %8s  used by %d images\n", l.Digest[:19], humanBytes(l.Size), l.Images)
	}
	return exitOK
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func runImagesTokenKey(args []string) int {
	fs := flag.NewFlagSet("images token-key", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "folder for token.key (server only, 0600) and token.crt (the registry's rootcertbundle)")
	name := fs.String("name", "", "certificate common name")
	if err := fs.Parse(args); err != nil || *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	if err := imagepin.GenerateTokenKey(*out, *name); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("wrote %s/%s (mount it into the server only) and %s/%s (the library registry's rootcertbundle)\n", *out, imagepin.TokenKeyFile, *out, imagepin.TokenCertFile)
	return exitOK
}

// runImagesLogin logs the machine's docker in to the library registry with
// the runner token, password on stdin, so prefetch and pinned pulls work.
func runImagesLogin(args []string) int {
	fs := flag.NewFlagSet("images login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registry := fs.String("registry", "", "library registry host[:port]")
	tokenFile := fs.String("token-file", "", "file holding the runner token (hmr_...) or an org API token (hm_...)")
	if err := fs.Parse(args); err != nil || *registry == "" || *tokenFile == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, imagesUsage)
		return exitUsage
	}
	token, err := readTokenFile(*tokenFile)
	if err != nil {
		return fail("%v", err)
	}
	cmd := exec.Command("docker", "login", *registry, "-u", "heimdall", "--password-stdin")
	cmd.Stdin = strings.NewReader(token + "\n")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fail("docker login %s: %v", *registry, err)
	}
	return exitOK
}

// runnerPrefetch is the runner's engine.WorkerOptions.Prefetch: once per
// suite lock, pull the images it pins by digest that this machine lacks.
// docker is the worker's own: a shared runner passes the org's policy
// docker, so the pulls and tags stay inside that org's namespace and an
// image the policy refuses is not retried on every experiment.
func runnerPrefetch(logger schemas.Logger, docker utils.Docker) func(context.Context, *schemas.Suite) {
	var done sync.Map
	return func(ctx context.Context, s *schemas.Suite) {
		wants := imagepin.LockedImages(s)
		if len(wants) == 0 {
			return
		}
		key := s.ID + "@" + s.LockHash
		if _, loaded := done.LoadOrStore(key, true); loaded {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		defer cancel()
		start := time.Now()
		res := imagepin.Prefetch(ctx, docker, nil, wants, 2, nil)
		pulled, failed, refused := 0, 0, 0
		var firstErr error
		for _, r := range res {
			switch r.State {
			case imagepin.PrefetchPulled:
				pulled++
			case imagepin.PrefetchFailed:
				failed++
				if strings.Contains(r.Error, utils.ErrPolicy.Error()) {
					refused++
				}
				if firstErr == nil {
					firstErr = errors.New(r.Error)
				}
			}
		}
		if failed > refused {
			done.Delete(key) // the next experiment of this suite tries again (a policy refusal would only be refused again)
		}
		if failed > 0 {
			logger.Warn("image prefetch: some pinned images could not be pulled", "suite", s.ID, "failed", failed, "error", firstErr.Error())
		}
		if pulled > 0 || failed > 0 {
			logger.Info("image prefetch", "suite", s.ID, "images", len(res), "pulled", pulled, "failed", failed, "elapsed", time.Since(start).Round(time.Second).String())
		}
	}
}
