package main

// `registry publisher` and `registry platform-images` (wave 4, F1.1): the
// image publisher service of the shared worker and the list of platform
// images it accepts. The publisher is the only holder of the registry's
// write identity and has no Docker socket; only the shared runner can reach
// it (network imagepublish).

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/imagepub"
	"github.com/maximhq/bifrost/bench/suite"
)

// platformSetTTL is how long a computed platform image set is reused.
const platformSetTTL = 60 * time.Second

// cachedPlatformImages returns a function giving imagepub.PlatformImages of
// the two roots, recomputed at most once per platformSetTTL.
func cachedPlatformImages(checkout, library string) func() (map[string]string, error) {
	var mu sync.Mutex
	var set map[string]string
	var at time.Time
	return func() (map[string]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if set != nil && time.Since(at) < platformSetTTL {
			return set, nil
		}
		s, err := imagepub.PlatformImages(checkout, library)
		if err != nil {
			return nil, err
		}
		set, at = s, time.Now()
		return set, nil
	}
}

func runRegistryPublisher(args []string) int {
	fs := flag.NewFlagSet("registry publisher", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "0.0.0.0:8097", "address to serve on")
	writer := fs.String("writer", "registry-writer:5000", "the registry's write side (host:port)")
	authFile := fs.String("auth-file", "", "file holding user:password of the sync identity")
	manifest := fs.String("manifest", "", "registry manifest (digest pins)")
	tokenFile := fs.String("token-file", "", "file holding the bearer token the shared runner sends")
	crane := fs.String("crane", "crane", "crane binary")
	checkout := fs.String("checkout", os.Getenv("HEIMDALL_BENCH_DIR"), "the platform checkout (read only)")
	library := fs.String("library", os.Getenv(suite.LibraryEnv), "the benchmark library (read only)")
	temp := fs.String("temp", "/var/tmp/heimdall-publish", "folder for uploaded image files")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "registry publisher: %v\n%s\n", err, registryUsage)
		return exitUsage
	}
	if *authFile == "" || *manifest == "" || *tokenFile == "" {
		fmt.Fprintf(os.Stderr, "registry publisher: -auth-file, -manifest and -token-file are required\n%s\n", registryUsage)
		return exitUsage
	}
	raw, err := os.ReadFile(*tokenFile)
	token := strings.TrimSpace(string(raw))
	if err != nil || len(token) < 32 {
		fmt.Fprintf(os.Stderr, "registry publisher: token file %s: missing or too short\n", *tokenFile)
		return exitUsage
	}
	if err := os.MkdirAll(*temp, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "registry publisher: %v\n", err)
		return 1
	}
	s := &imagepub.Server{Writer: *writer, AuthFile: *authFile, Manifest: *manifest, Token: token, Crane: *crane,
		Set: cachedPlatformImages(*checkout, *library), TempDir: *temp}
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 30 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		_ = srv.Close()
	}()
	fmt.Fprintf(os.Stderr, "image publisher listening on %s\n", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "registry publisher: %v\n", err)
		return 1
	}
	return exitOK
}

// runRegistryPlatformImages prints the platform images, one per line, sorted.
func runRegistryPlatformImages(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, registryUsage)
		return exitUsage
	}
	set, err := imagepub.PlatformImages(os.Getenv("HEIMDALL_BENCH_DIR"), suite.LibraryDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry platform-images: %v\n", err)
		return 1
	}
	refs := make([]string, 0, len(set))
	for r := range set {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, r := range refs {
		fmt.Println(r)
	}
	return exitOK
}

// platformPinFuncs holds one cached PlatformPin per checkout and library.
var platformPinFuncs sync.Map // checkout + "\x00" + library -> func(string) (string, bool)

// platformPinOf is the PlatformPin of the Docker policies of a shared
// runner (utils.DockerPolicy, SEC-C): whether a plain name is one of the
// platform's own images and the pin the platform's own lock gives it, from
// imagepub.PlatformPins of the read-only checkout and library, recomputed
// at most once per platformSetTTL. A failed read keeps nothing plain.
func platformPinOf(checkout, library string) func(string) (string, bool) {
	key := checkout + "\x00" + library
	if f, ok := platformPinFuncs.Load(key); ok {
		return f.(func(string) (string, bool))
	}
	var mu sync.Mutex
	var pins map[string]string
	var at time.Time
	f := func(ref string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if pins == nil || time.Since(at) >= platformSetTTL {
			p, err := imagepub.PlatformPins(checkout, library)
			if err != nil {
				return "", false
			}
			pins, at = p, time.Now()
		}
		pin, ok := pins[ref]
		return pin, ok
	}
	actual, _ := platformPinFuncs.LoadOrStore(key, f)
	return actual.(func(string) (string, bool))
}
