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
	Agent    string // agent id on the hosted Heimdall (required)
	Config   string // agent config file in the repository; "" registers nothing (use Image or Endpoint)
	Image    bool   // the workflow passes the image built earlier in the pipeline (HEIMDALL_IMAGE)
	Endpoint bool   // the workflow passes a preview deployment URL (HEIMDALL_ENDPOINT)
	URL      string // hosted Heimdall URL; "" reads the HEIMDALL_URL variable
	// Source and SourceRef named the GitHub repository the workflows built
	// Heimdall from. The hosted workflows now download the tool from the
	// Heimdall server (DownloadScript); both are still validated for old
	// callers and otherwise unused.
	Source     string
	SourceRef  string
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

// DownloadScript is the shell both hosted workflows run to fetch the
// command line tool from the Heimdall server itself (GET
// /downloads/bifrost-bench-<os>-<arch>, built at the server's commit) and
// check it against the server's SHA256SUMS before it runs. It reads
// HEIMDALL_URL and installs the tool as $1/heimdall (the workflows put
// their folder in place of $1). Nothing comes from
// GitHub or a package registry.
const DownloadScript = `set -eu
base="${HEIMDALL_URL%/}"
[ -n "$base" ] || { echo "set HEIMDALL_URL to the address of your Heimdall" >&2; exit 1; }
dir="$1"
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64 | Linux-amd64) asset=bifrost-bench-linux-amd64 ;;
  Linux-aarch64 | Linux-arm64) asset=bifrost-bench-linux-arm64 ;;
  Darwin-x86_64) asset=bifrost-bench-darwin-amd64 ;;
  Darwin-arm64) asset=bifrost-bench-darwin-arm64 ;;
  *) echo "Heimdall has no build for $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac
tmp=$(mktemp -d)
curl -fsSL --retry 3 --proto '=https,http' "$base/downloads/SHA256SUMS" -o "$tmp/SHA256SUMS"
curl -fsSL --retry 3 --proto '=https,http' "$base/downloads/$asset" -o "$tmp/$asset"
grep "  $asset\$" "$tmp/SHA256SUMS" > "$tmp/$asset.sha256" || { echo "$asset is not in $base/downloads/SHA256SUMS" >&2; exit 1; }
(cd "$tmp" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c "$asset.sha256"; else shasum -a 256 -c "$asset.sha256"; fi)
mkdir -p "$dir"
mv "$tmp/$asset" "$dir/heimdall"
chmod 0755 "$dir/heimdall"
rm -rf "$tmp"
"$dir/heimdall" version`

// GitHubHostedWorkflow renders .github/workflows/heimdall-evaluate.yml: on
// every pull request it downloads the command line tool from your Heimdall
// (checksum checked), runs `heimdall ci evaluate`, which registers the pull
// request's config as a version, evaluates it against the live version,
// posts the report card summary as one comment and sets a commit status,
// and keeps the report as an artifact. No action from GitHub's marketplace
// besides checkout and upload-artifact.
func GitHubHostedWorkflow(o HostedOptions) (string, error) {
	if err := o.validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	urlValue := "${{ vars.HEIMDALL_URL }}"
	if o.URL != "" {
		urlValue = strconv.Quote(o.URL)
	}
	w("# Written by Heimdall: test agent %s on every pull request.", o.Agent)
	w("#")
	w("# Each pull request registers its agent config as a new version on your")
	w("# Heimdall, evaluates it against the live version, posts the report card")
	w("# summary as one comment (updated in place) and sets the commit status.")
	w("# The heimdall command is downloaded from your Heimdall server, built at")
	w("# the commit it runs, and checked against its SHA256SUMS.")
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
	w("      - name: Download heimdall from your Heimdall server")
	w("        shell: bash")
	w("        env:")
	w("          HEIMDALL_URL: %s", urlValue)
	w("        run: |")
	for _, line := range strings.Split(DownloadScript, "\n") {
		line = strings.ReplaceAll(line, `dir="$1"`, `dir="$RUNNER_TEMP/heimdall-bin"`)
		w("          %s", line)
	}
	w(`          echo "$dir" >> "$GITHUB_PATH"`)
	w("")
	if o.Image || o.Endpoint {
		w("      # Build and push your agent image, or deploy a preview, before this")
		w("      # step and pass its digest or URL below.")
	}
	w("      - name: Evaluate this pull request")
	w("        shell: bash")
	w("        env:")
	w("          HEIMDALL_URL: %s", urlValue)
	w("          HEIMDALL_TOKEN: ${{ secrets.HEIMDALL_TOKEN }}")
	for _, kv := range o.hostedEnv() {
		w("          %s: %s", kv[0], strconv.Quote(kv[1]))
	}
	if o.Image {
		w("          HEIMDALL_IMAGE: ${{ steps.image.outputs.digest }} # e.g. ghcr.io/you/agent@sha256:...")
	}
	if o.Endpoint {
		w("          HEIMDALL_ENDPOINT: ${{ steps.preview.outputs.url }} # your preview deployment")
	}
	w("          HEIMDALL_OUT: ${{ runner.temp }}/heimdall-report")
	w("          GITHUB_TOKEN: ${{ github.token }} # the comment and the commit status")
	w("        run: heimdall ci evaluate")
	w("")
	w("      - name: Keep the report")
	w("        if: always()")
	w("        uses: %s", uploadAction)
	w("        with:")
	w("          name: heimdall-report")
	w("          path: ${{ runner.temp }}/heimdall-report/")
	w("          if-no-files-found: warn")
	return b.String(), nil
}

// GitLabHostedCI renders a GitLab CI file to include from .gitlab-ci.yml: a
// job on every merge request that downloads the command line tool from your
// Heimdall (checksum checked) and runs `ci evaluate`. GitLab's CI job token
// cannot write notes or statuses, so the comment and status need
// GITLAB_TOKEN, a project access token with the api scope.
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
	w("# The heimdall command is downloaded from your Heimdall server, built at")
	w("# the commit it runs, and checked against its SHA256SUMS.")
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
	w("  image: %s", gitlabImage)
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
	if o.Image {
		w("    # HEIMDALL_IMAGE: the image an earlier job pushed, e.g. registry.example.com/agent@sha256:...")
	}
	if o.Endpoint {
		w("    # HEIMDALL_ENDPOINT: the URL of the preview deployment an earlier job made")
	}
	w("  before_script:")
	w("    - apk add --no-cache curl ca-certificates >/dev/null")
	w("    - |")
	for _, line := range strings.Split(DownloadScript, "\n") {
		w("      %s", strings.ReplaceAll(line, `dir="$1"`, `dir=/usr/local/bin`))
	}
	w("  script:")
	w("    - heimdall ci evaluate -out heimdall-report")
	w("  artifacts:")
	w("    when: always")
	w("    paths:")
	w("      - heimdall-report/")
	return b.String(), nil
}

// gitlabImage is the job image of the GitLab workflows: a small Linux with
// a shell; curl is added by the job.
const gitlabImage = "alpine:3.23"

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
