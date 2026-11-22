// Command heimdall-dist packages the files a Heimdall server serves at
// /downloads (package dist). The server image build runs it
// (deploy/hosted/Dockerfile.server):
//
//	heimdall-dist python   -src clients/python -out DIR    wheel and sdist of the Python SDK
//	heimdall-dist source   -root . -out DIR -commit SHA    bench-<SHA>.tar.gz of the bench module
//	heimdall-dist manifest -dir DIR -commit SHA -version V SHA256SUMS and manifest.json
//
// It publishes nothing: the folder is copied into the image and served by
// the server itself.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/dist"
)

func main() { os.Exit(run(os.Args[1:])) }

const usage = "usage: heimdall-dist python -src DIR -out DIR | source -root DIR -out DIR -commit SHA | manifest -dir DIR [-commit SHA] [-version V]"

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 64
	}
	fs := flag.NewFlagSet("heimdall-dist "+args[0], flag.ContinueOnError)
	src := fs.String("src", "clients/python", "python: the SDK folder holding pyproject.toml")
	root := fs.String("root", ".", "source: the bench module folder")
	out := fs.String("out", "downloads", "python, source: the output folder")
	dir := fs.String("dir", "downloads", "manifest: the folder to list")
	commit := fs.String("commit", "", "source, manifest: the git commit the files were built at")
	version := fs.String("version", "", "manifest: the server version")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 64
	}
	switch args[0] {
	case "python":
		w, err := dist.BuildWheel(*src, *out)
		if err != nil {
			return fail(err)
		}
		s, err := dist.BuildSdist(*src, *out)
		if err != nil {
			return fail(err)
		}
		fmt.Println(w)
		fmt.Println(s)
	case "source":
		p, err := dist.BuildSource(*root, *out, *commit)
		if err != nil {
			return fail(err)
		}
		fmt.Println(p)
	case "manifest":
		m, err := dist.WriteManifest(*dir, *commit, *version, time.Now())
		if err != nil {
			return fail(err)
		}
		for _, f := range m.Files {
			fmt.Printf("%s  %s (%d bytes)\n", f.SHA256, f.Name, f.Size)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 64
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "heimdall-dist:", err)
	return 1
}
