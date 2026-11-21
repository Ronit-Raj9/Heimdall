package ci

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Hosted CI workflows: GitHub Actions and GitLab CI files that run
// `bifrost-bench ci evaluate` against a hosted Heimdall on every pull (merge)
// request. Neither file holds a secret: the org API token is the
// HEIMDALL_TOKEN secret (GitHub) or masked variable (GitLab), and the server
// URL the HEIMDALL_URL variable unless one is written in.

// ModeHosted is the workflow mode that calls a hosted Heimdall.
const ModeHosted = "hosted"

// CI providers.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
)

// Workflow file paths in the user's repository.
const (
	GitHubHostedPath = ".github/workflows/heimdall-evaluate.yml"
	GitLabHostedPath = ".gitlab/heimdall-evaluate.gitlab-ci.yml"
)

// HostedOptions configures the hosted workflows.
type HostedOptions struct {
	Agent      string   // agent id on the hosted Heimdall (required)
	Config     string   // agent config file in the repository; "" registers nothing (use Image or Endpoint)
	Image      bool     // the workflow passes the image built earlier in the pipeline (HEIMDALL_IMAGE)
	Endpoint   bool     // the workflow passes a preview deployment URL (HEIMDALL_ENDPOINT)
	URL        string   // hosted Heimdall URL; "" reads the HEIMDALL_URL variable
	Source     string   // owner/repo Heimdall is built from
	SourceRef  string   // branch, tag or commit of Heimdall
	Pack       string   // benchmark pack
	Benchmarks []string // benchmark ids (with or instead of Pack)
	Faults     []string // faults besides clean
	FailOn     string   // outcomes that fail the gate
	Trials     int      // 0: the server default
	Tasks      int      // tasks per benchmark; 0: the server default
	BudgetUSD  float64  // 0: the server default
}

var (
	hostedIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	hostedSuite  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$`)
	hostedPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,256}$`)
	hostedRepoRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	hostedRefRe  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)
)

func (o *HostedOptions) validate() error {
	if !hostedIDRe.MatchString(o.Agent) {
		return fmt.Errorf("agent %q: want an agent id (lowercase letters, digits, '.', '-', '_')", o.Agent)
	}
	if o.Config != "" && (!hostedPathRe.MatchString(o.Config) || strings.Contains(o.Config, "..") || strings.HasPrefix(o.Config, "/")) {
		return fmt.Errorf("config %q: want a path inside the repository", o.Config)
	}
	if o.Config == "" && !o.Image && !o.Endpoint {
		return fmt.Errorf("pick what the pull request changes: an agent config file, an image or an endpoint")
	}
	if o.URL != "" {
		u, err := url.Parse(o.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || strings.ContainsAny(o.URL, "\"'\n ") {
			return fmt.Errorf("url %q: want the http(s) address of your Heimdall", o.URL)
		}
		o.URL = strings.TrimRight(o.URL, "/")
	}
	if o.Source == "" {
		o.Source = "Ronit-Raj9/Heimdall"
	}
	if o.SourceRef == "" {
		o.SourceRef = "main"
	}
	if !hostedRepoRe.MatchString(o.Source) || !hostedRefRe.MatchString(o.SourceRef) {
		return fmt.Errorf("source %s@%s: want owner/repo and a branch, tag or commit", o.Source, o.SourceRef)
	}
	if o.Pack != "" && !hostedIDRe.MatchString(o.Pack) {
		return fmt.Errorf("pack %q is not a pack id", o.Pack)
	}
	for _, b := range o.Benchmarks {
		if !hostedSuite.MatchString(b) {
			return fmt.Errorf("benchmark %q is not a benchmark id", b)
		}
	}
	if o.Pack == "" && len(o.Benchmarks) == 0 {
		return fmt.Errorf("pick a benchmark pack or at least one benchmark")
	}
	for _, f := range o.Faults {
		if !hostedIDRe.MatchString(f) {
			return fmt.Errorf("fault %q is not a fault id", f)
		}
	}
	if o.FailOn == "" {
		o.FailOn = strings.Join(DefaultFailOn, ",")
	}
	if _, err := ParseFailOn(o.FailOn); err != nil {
		return err
	}
	if o.Trials < 0 || o.Tasks < 0 || o.BudgetUSD < 0 {
		return fmt.Errorf("trials, tasks and budget must not be negative")
	}
	return nil
}

// hostedEnv is the HEIMDALL_* settings both workflows hand to
// `ci evaluate`, in a fixed order. Values are already validated.
func (o *HostedOptions) hostedEnv() [][2]string {
	env := [][2]string{{"HEIMDALL_AGENT", o.Agent}}
	if o.Config != "" {
		env = append(env, [2]string{"HEIMDALL_CONFIG", o.Config})
	}
	if o.Pack != "" {
		env = append(env, [2]string{"HEIMDALL_PACK", o.Pack})
	}
	if len(o.Benchmarks) > 0 {
		env = append(env, [2]string{"HEIMDALL_BENCHMARKS", strings.Join(o.Benchmarks, ",")})
	}
	if len(o.Faults) > 0 {
		env = append(env, [2]string{"HEIMDALL_FAULTS", strings.Join(o.Faults, ",")})
	}
	env = append(env, [2]string{"HEIMDALL_FAIL_ON", o.FailOn})
	if o.Trials > 0 {
		env = append(env, [2]string{"HEIMDALL_TRIALS", strconv.Itoa(o.Trials)})
	}
	if o.Tasks > 0 {
		env = append(env, [2]string{"HEIMDALL_TASKS", strconv.Itoa(o.Tasks)})
	}
	if o.BudgetUSD > 0 {
		env = append(env, [2]string{"HEIMDALL_BUDGET_USD", strconv.FormatFloat(o.BudgetUSD, 'f', -1, 64)})
	}
	return env
}

// GitHubHostedWorkflow renders .github/workflows/heimdall-evaluate.yml: on
// every pull request it runs Heimdall's composite action (bench/ci) in
// evaluate mode, which registers the pull request's config as a version,
// evaluates it against the live version, posts the report card summary as
// one comment and sets a commit status.
func GitHubHostedWorkflow(o HostedOptions) (string, error) {
	if err := o.validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Written by Heimdall: test agent %s on every pull request.", o.Agent)
	w("#")
	w("# Each pull request registers its agent config as a new version on your")
	w("# Heimdall, evaluates it against the live version, posts the report card")
	w("# summary as one comment (updated in place) and sets the commit status.")
	w("#")
	w("# Setup (repository Settings > Secrets and variables > Actions):")
	w("#   secret   HEIMDALL_TOKEN  an org API token from Heimdall (Settings > API tokens)")
	if o.URL == "" {
		w("#   variable HEIMDALL_URL    the address of your Heimdall, e.g. https://heimdall.example.com")
	}
	w("# Pull requests from forks are skipped: GitHub gives them no secrets.")
	w("name: Heimdall evaluation (%s)", o.Agent)
	w("")
	w("on:")
	w("  pull_request:")
	if o.Config != "" && !o.Image && !o.Endpoint {
		w("    paths:")
		w("      - %s", strconv.Quote(o.Config))
		w("      - %s", strconv.Quote(GitHubHostedPath))
	}
	w("  workflow_dispatch:")
	w("")
	w("permissions:")
	w("  contents: read")
	w("  pull-requests: write # the report card comment")
	w("  statuses: write # the commit status")
	w("")
	w("concurrency:")
	w("  group: heimdall-%s-${{ github.event.pull_request.number || github.ref }}", o.Agent)
	w("  cancel-in-progress: true # a newer push cancels the older evaluation")
	w("")
	w("jobs:")
	w("  evaluate:")
	w("    name: Heimdall evaluation")
	w("    if: github.event_name == 'workflow_dispatch' || github.event.pull_request.head.repo.full_name == github.repository")
	w("    runs-on: ubuntu-latest")
	w("    timeout-minutes: 240")
	w("    steps:")
	w("      - name: Check out the repository")
	w("        uses: %s", checkoutAction)
	w("        with:")
	w("          persist-credentials: false")
	w("")
	if o.Image || o.Endpoint {
		w("      # Build and push your agent image, or deploy a preview, before this")
		w("      # step and pass its digest or URL below.")
	}
	w("      - name: Evaluate this pull request")
	w("        uses: %s/bench/ci@%s # pin a commit for a reproducible gate", o.Source, o.SourceRef)
	w("        with:")
	w("          mode: evaluate")
	if o.URL != "" {
		w("          url: %s", strconv.Quote(o.URL))
	} else {
		w("          url: ${{ vars.HEIMDALL_URL }}")
	}
	w("          token: ${{ secrets.HEIMDALL_TOKEN }}")
	for _, kv := range o.hostedEnv() {
		w("          %s: %s", actionInput(kv[0]), strconv.Quote(kv[1]))
	}
	if o.Image {
		w("          image: ${{ steps.image.outputs.digest }} # e.g. ghcr.io/you/agent@sha256:...")
	}
	if o.Endpoint {
		w("          endpoint: ${{ steps.preview.outputs.url }} # your preview deployment")
	}
	return b.String(), nil
}

// actionInput maps HEIMDALL_FAIL_ON to the action input fail-on.
func actionInput(env string) string {
	name := strings.ToLower(strings.TrimPrefix(env, "HEIMDALL_"))
	if name == "budget_usd" {
		return "budget-usd"
	}
	return strings.ReplaceAll(name, "_", "-")
}

// GitLabHostedCI renders a GitLab CI file to include from .gitlab-ci.yml: a
// job on every merge request that builds Heimdall and runs `ci evaluate`.
// GitLab's CI job token cannot write notes or statuses, so the comment and
// status need GITLAB_TOKEN, a project access token with the api scope.
func GitLabHostedCI(o HostedOptions) (string, error) {
	if err := o.validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Written by Heimdall: test agent %s on every merge request.", o.Agent)
	w("# Include it from .gitlab-ci.yml:")
	w("#   include:")
	w("#     - local: %s", GitLabHostedPath)
	w("#")
	w("# Setup (project Settings > CI/CD > Variables, masked and protected as you need):")
	w("#   HEIMDALL_TOKEN  an org API token from Heimdall (Settings > API tokens)")
	if o.URL == "" {
		w("#   HEIMDALL_URL    the address of your Heimdall, e.g. https://heimdall.example.com")
	}
	w("#   GITLAB_TOKEN    a project access token with the api scope, for the merge")
	w("#                   request comment and the commit status")
	w("heimdall-evaluate:")
	w("  stage: test")
	w("  image: golang:1.26")
	w("  timeout: 4h")
	w("  interruptible: true # a newer push cancels the older evaluation")
	w("  rules:")
	w("    - if: $CI_PIPELINE_SOURCE == \"merge_request_event\"")
	if o.Config != "" && !o.Image && !o.Endpoint {
		w("      changes:")
		w("        - %s", strconv.Quote(o.Config))
		w("        - %s", strconv.Quote(GitLabHostedPath))
	}
	w("  variables:")
	if o.URL != "" {
		w("    HEIMDALL_URL: %s", strconv.Quote(o.URL))
	}
	for _, kv := range o.hostedEnv() {
		w("    %s: %s", kv[0], strconv.Quote(kv[1]))
	}
	w("    HEIMDALL_SOURCE: %s", strconv.Quote(o.Source))
	w("    HEIMDALL_SOURCE_REF: %s # pin a commit for a reproducible gate", strconv.Quote(o.SourceRef))
	if o.Image {
		w("    # HEIMDALL_IMAGE: the image an earlier job pushed, e.g. registry.example.com/agent@sha256:...")
	}
	if o.Endpoint {
		w("    # HEIMDALL_ENDPOINT: the URL of the preview deployment an earlier job made")
	}
	w("  script:")
	w("    - git init -q /tmp/heimdall")
	w("    - git -C /tmp/heimdall fetch -q --depth 1 \"https://github.com/$HEIMDALL_SOURCE.git\" \"$HEIMDALL_SOURCE_REF\"")
	w("    - git -C /tmp/heimdall checkout -q FETCH_HEAD")
	w("    - (cd /tmp/heimdall/bench && GOWORK=off CGO_ENABLED=0 go build -o /usr/local/bin/heimdall ./cmd/bifrost-bench)")
	w("    - heimdall ci evaluate -out heimdall-report")
	w("  artifacts:")
	w("    when: always")
	w("    paths:")
	w("      - heimdall-report/")
	return b.String(), nil
}

// HostedWorkflow renders the file for provider and returns it with its path
// in the repository.
func HostedWorkflow(provider string, o HostedOptions) (path, file string, err error) {
	switch provider {
	case "", ProviderGitHub:
		file, err = GitHubHostedWorkflow(o)
		return GitHubHostedPath, file, err
	case ProviderGitLab:
		file, err = GitLabHostedCI(o)
		return GitLabHostedPath, file, err
	}
	return "", "", fmt.Errorf("provider %q: want github or gitlab", provider)
}
