package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/utils"
)

// Build sandbox of an issuebench.work job (lane W04): a per-job internal
// network with no route out, and the recorded-internet proxy in
// live-allowlisted mode as its only way out (package registries only). The
// proxy is an L7 allowlist, so it confines runc and gVisor containers the
// same way.

// ibLabelRunner names the runner on everything a work job starts, so the
// runner's start sweep finds what a crash left behind.
const ibLabelRunner = "heimdall.runner"

// ibHostRoots is the runner's root certificate bundle, put in front of the
// proxy CA in the sandbox's bundle file.
var ibHostRoots = "/etc/ssl/certs/ca-certificates.crt"

type ibEgressOptions struct {
	Docker  utils.Docker // the org's policy on the shared worker; plain elsewhere
	Public  string       // network the proxy reaches the internet on: HEIMDALL_BUILD_NETWORK on the shared worker, "bridge" elsewhere
	Image   string       // egress.DefaultImage
	Runtime string
	Org     string
	Runner  string
}

// ibJob12 is the job id as a docker name part: at most 12 characters of
// [a-z0-9_.-].
func ibJob12(job string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(job) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() == 12 {
			break
		}
	}
	return b.String()
}

// ibEgressSandbox starts the job's network and proxy and returns the build
// sandbox factory and the cleanup (safe to call more than once, and after
// ctx ends).
func ibEgressSandbox(ctx context.Context, o ibEgressOptions, req issuebench.WorkRequest) (builder.SandboxFactory, func(), error) {
	return ibEgressSandboxWith(ctx, o, req, issuebench.ExecDocker(o.Docker.Command))
}

func ibEgressSandboxWith(ctx context.Context, o ibEgressOptions, req issuebench.WorkRequest, run issuebench.DockerFunc) (builder.SandboxFactory, func(), error) {
	if req.JobID == "" || req.OrgID == "" {
		return nil, func() {}, errors.New("egress sandbox: the request needs org_id and job_id")
	}
	if o.Image == "" {
		o.Image = egress.DefaultImage
	}
	if o.Public == "" {
		o.Public = "bridge"
	}
	if o.Org == "" {
		o.Org = req.OrgID
	}
	j := ibJob12(req.JobID)
	if j == "" {
		return nil, func() {}, errors.New("egress sandbox: job id has no usable characters")
	}
	netName := "heimdall-ib-" + j
	proxy := netName + "-egress"
	labels := map[string]string{
		"heimdall":           "1",
		issuebench.LabelOrg:  o.Org,
		issuebench.LabelWork: "egress",
		issuebench.LabelJob:  req.JobID,
		ibLabelRunner:        o.Runner,
	}
	call := func(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, err := run(cctx, nil, args...)
		return strings.TrimSpace(string(out)), err
	}
	cleanup := func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// Build containers of this job first (a crashed builder leaves them
		// on the network), then the proxy, then the network.
		if ids, err := call(cctx, 10*time.Second, "ps", "-aq", "--filter", "label="+issuebench.LabelJob+"="+req.JobID); err == nil && ids != "" {
			_, _ = call(cctx, 20*time.Second, append([]string{"rm", "-f", "-v"}, strings.Fields(ids)...)...)
		}
		_, _ = call(cctx, 20*time.Second, "rm", "-f", "-v", proxy)
		_, _ = call(cctx, 20*time.Second, "network", "rm", netName)
	}
	fail := func(what string, err error) (builder.SandboxFactory, func(), error) {
		cleanup()
		return nil, func() {}, fmt.Errorf("egress sandbox: %s: %w", what, err)
	}

	netArgs := []string{"network", "create", "--internal"}
	netArgs = append(netArgs, ibLabelArgs(labels)...)
	if _, err := call(ctx, time.Minute, append(netArgs, netName)...); err != nil {
		return fail("create the job network", err)
	}
	caPEM, err := egress.NewCA("heimdall-issuebench-" + req.JobID)
	if err != nil {
		return fail("make the proxy CA", err)
	}
	caCert := egress.CertOnly(caPEM)
	pargs := []string{"create", "--name", proxy, "--network", netName, "--network-alias", egress.ServiceID,
		"--read-only", "--tmpfs", "/tmp", "-v", "/egress", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--memory", "256m", "--pids-limit", "128",
		"-e", egress.EnvMode + "=" + egress.ModeLive,
		"-e", egress.EnvAllow + "=" + strings.Join(req.Hosts(), ",")}
	pargs = append(pargs, ibLabelArgs(labels)...)
	if o.Runtime != "" {
		pargs = append(pargs, "--runtime", o.Runtime)
	}
	// The image's entrypoint is already `heimdall-egress egress serve`.
	pargs = append(pargs, o.Image, "-ca", egress.ServiceCAPath, "-log", "")
	if _, err := call(ctx, 5*time.Minute, pargs...); err != nil {
		return fail("create the proxy", err)
	}
	tmp, err := os.MkdirTemp("", "heimdall-ib-ca-")
	if err != nil {
		return fail("temp folder", err)
	}
	defer os.RemoveAll(tmp)
	caFile := tmp + "/ca.pem"
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		return fail("write the proxy CA", err)
	}
	// docker cp keeps the file's mode and owner: the shared worker runs with
	// umask 077 as uid 10001, and the proxy (root with no capabilities)
	// could not read a 0600 file it does not own.
	if err := os.Chmod(caFile, 0o644); err != nil {
		return fail("write the proxy CA", err)
	}
	if _, err := call(ctx, time.Minute, "cp", caFile, proxy+":"+egress.ServiceCAPath); err != nil {
		return fail("copy the CA into the proxy", err)
	}
	_ = os.Remove(caFile)
	if _, err := call(ctx, time.Minute, "network", "connect", o.Public, proxy); err != nil {
		return fail("connect the proxy to "+o.Public, err)
	}
	if _, err := call(ctx, time.Minute, "start", proxy); err != nil {
		return fail("start the proxy", err)
	}

	roots, _ := os.ReadFile(ibHostRoots)
	bundle := append(append([]byte(nil), roots...), '\n')
	bundle = append(bundle, caCert...)
	buildLabels := map[string]string{}
	for k, v := range labels {
		buildLabels[k] = v
	}
	buildLabels[issuebench.LabelWork] = "build"
	cenv := egress.ClientEnv(egress.ProxyURL(egress.ServiceID), []string{"localhost", "127.0.0.1"})
	extra := make([]string, 0, len(cenv))
	for k, v := range cenv {
		extra = append(extra, k+"="+v)
	}
	sort.Strings(extra)
	f := builder.DockerFactory(builder.DockerConfig{
		Docker:   o.Docker.Binary,
		Run:      run,
		Network:  netName,
		Runtime:  o.Runtime,
		Labels:   buildLabels,
		ExtraEnv: extra,
		Files:    map[string][]byte{egress.CAPath: caCert, egress.BundlePath: bundle},
	})
	return f, cleanup, nil
}

func ibLabelArgs(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, "--label", k+"="+labels[k])
	}
	return out
}

// ensureIssuebenchEgressImage builds the proxy image from this binary when
// the runner's daemon lacks it. d is the runner's own docker, never an
// org's policy.
func ensureIssuebenchEgressImage(ctx context.Context, d utils.Docker) error {
	if _, ok, err := d.ImageID(ctx, egress.DefaultImage); err == nil && ok {
		return nil
	}
	if code := egressImage([]string{"-tag", egress.DefaultImage}); code != exitOK {
		return fmt.Errorf("build the egress image %s failed (exit %d)", egress.DefaultImage, code)
	}
	return nil
}

// sweepIssuebench removes what work jobs of this runner left behind
// (containers with their volumes, then networks, then snapshot images) and
// returns how many objects it removed.
func sweepIssuebench(ctx context.Context, d utils.Docker, runner string) (int, error) {
	return sweepIssuebenchWith(ctx, issuebench.ExecDocker(d.Command), runner)
}

func sweepIssuebenchWith(ctx context.Context, run issuebench.DockerFunc, runner string) (int, error) {
	if runner == "" {
		return 0, errors.New("sweep: no runner name")
	}
	filters := []string{"--filter", "label=" + issuebench.LabelWork, "--filter", "label=" + ibLabelRunner + "=" + runner}
	list := func(args ...string) ([]string, error) {
		out, err := run(ctx, nil, append(args, filters...)...)
		if err != nil {
			return nil, err
		}
		return strings.Fields(string(out)), nil
	}
	n := 0
	var errs []error
	steps := []struct {
		ls []string
		rm []string
	}{
		{[]string{"ps", "-aq"}, []string{"rm", "-f", "-v"}},
		{[]string{"network", "ls", "-q"}, []string{"network", "rm"}},
		{[]string{"image", "ls", "-q"}, []string{"rmi", "-f"}},
	}
	for _, st := range steps {
		ids, err := list(st.ls...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, id := range ids {
			if _, err := run(ctx, nil, append(append([]string(nil), st.rm...), id)...); err != nil {
				errs = append(errs, err)
				continue
			}
			n++
		}
	}
	return n, errors.Join(errs...)
}
