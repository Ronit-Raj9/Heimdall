package emit

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
	_ "github.com/maximhq/bifrost/bench/issuebench/lang/node"
)

// TestLayersOfLanguageSupport: a Go, JavaScript or TypeScript environment
// image copies the result converters (heimdall/) and has a python3 for the
// grader; a Python environment's layers are exactly as before.
func TestLayersOfLanguageSupport(t *testing.T) {
	for _, lang := range []string{"go", "javascript", "typescript"} {
		l, err := LayersOf(issuebench.EnvSpec{Repo: "acme/tool", Language: lang, BaseImage: "node:22-bookworm-slim"}, "acme__tool-1", "abc")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(l.EnvDockerfile, "COPY repo/ /testbed/\nCOPY heimdall/ /heimdall/\n") {
			t.Errorf("%s env layer has no heimdall copy:\n%s", lang, l.EnvDockerfile)
		}
		if !strings.Contains(l.BaseDockerfile, "RUN (command -v python3 >/dev/null || (apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*))\n") {
			t.Errorf("%s base layer installs no python3 for the grader:\n%s", lang, l.BaseDockerfile)
		}
	}
	for _, lang := range []string{"python", ""} {
		l, err := LayersOf(issuebench.EnvSpec{Repo: "acme/tool", Language: lang, BaseImage: "python:3.11-slim"}, "acme__tool-1", "abc")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(l.EnvDockerfile, "COPY heimdall/") || strings.Contains(l.BaseDockerfile, "python3") {
			t.Errorf("python layers changed:\n%s\n%s", l.BaseDockerfile, l.EnvDockerfile)
		}
		wantBase := "FROM python:3.11-slim\n" +
			"RUN (command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*))\n" +
			"WORKDIR /testbed\n"
		if l.BaseDockerfile != wantBase {
			t.Errorf("python base layer = %q", l.BaseDockerfile)
		}
	}
}
