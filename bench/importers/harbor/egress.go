package harbor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Recorded internet (docs/egress.md). With -set egress=record a task whose
// reference solution downloads at run time is imported instead of skipped:
// it gets an egress service (the forward proxy the agent reaches as
// HTTP(S)_PROXY), a certificate authority of its own whose certificate is
// baked into the image, and an empty cache tar that `bifrost-bench egress
// record` fills by running the reference once with internet. Admission then
// runs in replay mode: every download is answered from the cache, byte for
// byte, and nothing leaves the bench.

// SetEgress is the setting: record imports download-at-run-time tasks with
// an egress service; off (the default) skips them.
const SetEgress = "egress"

// EgressTag marks a task that runs with a recorded internet.
const EgressTag = "egress:recorded"

// planEgress marks p for an egress service: the reference's download line,
// and whether test.sh downloads too (then the verifier reaches the proxy).
// It rewrites the test script notes, which assumed no network. The reason
// is empty when the task can be imported.
func planEgress(p *taskPlan, t *upstreamTask, fetch string, keepNetworked bool) string {
	if t.Compose {
		return "the task downloads at run time and has a compose file; -set egress=record does not combine with compose services yet"
	}
	p.egressFetch = fetch
	if len(p.tests.Networked) > 0 {
		p.egressVerifier = true
		if p.egressFetch == "" {
			p.egressFetch = "test.sh: " + p.tests.Networked[0]
		}
		for i, c := range p.tests.Changes {
			p.tests.Changes[i] = strings.Replace(c, "the verifier has none, so admission fails", "the verifier reaches the recorded internet, replay only", 1)
		}
	}
	return ""
}

// egressService is the task.yaml entry of the proxy. verifier lets the
// verifier reach it too, for a test.sh that downloads.
func egressService(verifier bool) schemas.ServiceSpec {
	return schemas.ServiceSpec{
		Verifier: verifier,
		ID:       egress.ServiceID,
		Role:     schemas.ServiceRoleEgress,
		Image:    egress.DefaultImage,
		Env:      map[string]string{egress.EnvMode: egress.ModeReplay},
		Files: map[string]string{
			egress.ServiceCachePath: egress.TaskCacheFile,
			egress.ServiceCAPath:    egress.TaskCAFile,
		},
		Port:   egress.Port,
		Ready:  &schemas.ReadyCheck{HTTP: "/healthz", TimeoutS: 60},
		Export: []schemas.StateExport{{ID: "requests", Path: egress.ServiceLogPath, MaxBytes: 4 << 20}},
	}
}

// egressDockerfile is appended to the task's Dockerfile, before the bake
// section switches users: the CA certificate goes to CAPath, BundlePath
// holds the image's roots plus it, and the system bundles that exist get it
// appended for clients that read only those (apt over https, wget).
func egressDockerfile() string {
	return fmt.Sprintf(`
# Added by bifrost-bench import harbor -set egress=record (docs/egress.md).
# The agent's downloads go through the task's recorded-internet proxy; this
# is the certificate it signs HTTPS with. Only the proxy holds the key.
USER root
COPY %[1]s %[2]s
RUN set -e; b=%[3]s; : > "$b"; for f in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do if [ -f "$f" ]; then cat "$f" >> "$b"; echo >> "$b"; fi; done; cat %[2]s >> "$b"; for f in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do if [ -f "$f" ] && [ ! -L "$f" ]; then cat %[2]s >> "$f"; fi; done; if [ -d /usr/local/share/ca-certificates ]; then cp %[2]s /usr/local/share/ca-certificates/heimdall-egress.crt; fi; chmod 0644 "$b" %[2]s
# A replayed apt index keeps the Valid-Until of the day it was recorded.
RUN if [ -d /etc/apt/apt.conf.d ]; then echo 'Acquire::Check-Valid-Until "false";' > /etc/apt/apt.conf.d/99heimdall-egress; fi
`, egress.ImageCAFile, egress.CAPath, egress.BundlePath)
}

// writeEgress writes the task's CA, the certificate in the build context and
// an empty cache.
func writeEgress(dir string) error {
	ca, err := egress.NewCA(filepath.Base(dir))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "egress"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(egress.TaskCAFile)), ca, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, envDir, egress.ImageCAFile), egress.CertOnly(ca), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filepath.FromSlash(egress.TaskCacheFile)), egress.EmptyCache(), 0o644)
}

// egressNotes are the IMPORT.md lines of an egress task.
func egressNotes(fetch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- Recorded internet (`-set egress=record`, docs/egress.md): the reference downloads at run time (%s), so the task has an `egress` service, a forward proxy the agent and the controls reach through HTTP_PROXY and HTTPS_PROXY. ", fetch)
	b.WriteString("`bifrost-bench egress record` runs the reference once with internet and writes `egress/cache.tar`; every run after that is answered from it byte for byte (mode replay), and a request it does not hold gets a 404 and a `miss` row in the sealed request log (state/egress/requests). ")
	b.WriteString("The cache tar is a service file, so its sha256 is in the task's fixtures hash in the suite lock.\n")
	b.WriteString("- apt is set not to check Valid-Until (`/etc/apt/apt.conf.d/99heimdall-egress`), because a replayed index keeps the date it was recorded on.\n")
	fmt.Fprintf(&b, "- The task's CA certificate is baked into the image at %s (and appended to the system bundles); its key is only in the egress service. An agent can see that its HTTPS goes through a proxy.\n", egress.CAPath)
	b.WriteString("- An agent that downloads something the reference did not gets a 404 for it. Experiments can set `egress: live-allowlisted` to reach the recorded hosts live (not repeatable, labelled) or `egress: offline`.\n")
	return b.String()
}
