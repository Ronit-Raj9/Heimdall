package ci

import (
	"fmt"
	"strings"
)

// First workflow: `heimdall ci install github` writes the workflow below
// into the user's repository. It checks out Heimdall, builds the binary
// once, runs the gate through this folder's composite action (action.yml),
// renders the verdict card and keeps one pull-request comment up to date,
// uploads the evidence, and fails on regressed, invalid or a policy block.

// Workflow modes.
const (
	ModeRecorded = "recorded" // re-check a sealed experiment checked in under .heimdall/recorded
	ModeLive     = "live"     // run a fresh baseline-vs-candidate experiment on a self-hosted runner
)

// Pinned third-party actions (tag kept for reading), the versions the
// repository's own workflows use.
const (
	checkoutAction = "actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2"
	setupGoAction  = "actions/setup-go@4b73464bb391d4059bd26b0524d20df3927bd417 # v6.3.0"
	uploadAction   = "actions/upload-artifact@bbbca2ddaa5d8feaa63e36b76fdaad77386f024f # v7.0.0"
)

// WorkflowOptions configures GitHubWorkflow.
type WorkflowOptions struct {
	Mode      string   // recorded or live
	Source    string   // owner/repo of Heimdall
	SourceRef string   // branch, tag or commit of Heimdall to build
	Base      string   // live: agent config both arms start from (agents/<id>.yaml)
	Trials    int      // live: trials per task
	Faults    []string // live: fault conditions
	BudgetUSD float64  // live: spend cap per run of the gate
	ExpID     string   // recorded: experiment id inside .heimdall/recorded ("" = the only one)
	DataMode  string   // recorded: recorded or synthetic
	Paths     []string // pull-request paths that trigger the gate
	FailOn    string   // verdicts that fail the job
}

// DefaultTriggerPaths are the files that can change a coding agent's
// behaviour: notes, skills, prompts and the Heimdall project itself.
var DefaultTriggerPaths = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".claude/**", "skills/**", "prompts/**", ".heimdall/**", ".github/workflows/heimdall.yml"}

// GitHubWorkflow renders .github/workflows/heimdall.yml. It never puts a
// secret value in the file: live mode reads the stack's tokens from an env
// file on the self-hosted runner, named by the HEIMDALL_ENV_FILE repository
// variable, and check.sh sources it without printing it.
func GitHubWorkflow(o WorkflowOptions) (string, error) {
	if o.Mode != ModeRecorded && o.Mode != ModeLive {
		return "", fmt.Errorf("mode %q: want recorded or live", o.Mode)
	}
	if o.Source == "" || o.SourceRef == "" {
		return "", fmt.Errorf("the Heimdall source repository and ref are required")
	}
	if o.Mode == ModeLive && o.Base == "" {
		return "", fmt.Errorf("live mode needs the agent config both arms start from (-base agents/<agent>.yaml)")
	}
	if o.Trials <= 0 {
		o.Trials = 3
	}
	if o.FailOn == "" {
		o.FailOn = "regressed,invalid"
	}
	if o.DataMode == "" {
		o.DataMode = "recorded"
	}
	paths := o.Paths
	if len(paths) == 0 {
		paths = DefaultTriggerPaths
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Written by `heimdall ci install github -mode %s`: the Heimdall agent gate.", o.Mode)
	w("# On every pull request that can change the coding agent (notes, skills,")
	w("# prompts, the .heimdall project) it runs `heimdall check` through Heimdall's")
	w("# composite action (bench/ci/action.yml), posts the verdict card as one PR")
	w("# comment that later runs update in place, uploads the evidence and fails on")
	w("# %s or a release-policy block.", o.FailOn)
	if o.Mode == ModeLive {
		w("#")
		w("# LIVE mode runs real agents, so it needs a SELF-HOSTED runner labelled")
		w("# `heimdall` with Docker, Go and the Heimdall stack up (`heimdall init` and")
		w("# `docker compose ... up -d --wait` on that machine). The stack's tokens stay")
		w("# on the runner in an env file; set the repository variable HEIMDALL_ENV_FILE")
		w("# to its path. Provider keys live only in the stack's fault proxy and never")
		w("# appear in this workflow or its logs. Pull requests from forks are skipped:")
		w("# their code would run next to the stack.")
	} else {
		w("#")
		w("# RECORDED mode re-checks the sealed experiment checked in under")
		w("# .heimdall/recorded/ (copy one from .heimdall/runs/<exp_id>): no Docker, no")
		w("# gateway, no key, no secret; it runs on GitHub-hosted runners.")
	}
	w("name: Heimdall agent gate")
	w("")
	w("on:")
	w("  pull_request:")
	w("    paths:")
	for _, p := range paths {
		w("      - %q", p)
	}
	w("  workflow_dispatch:")
	w("")
	w("permissions:")
	w("  contents: read")
	w("  pull-requests: write # the verdict comment")
	w("")
	w("concurrency:")
	w("  group: heimdall-${{ github.event.pull_request.number || github.ref }}")
	w("  cancel-in-progress: true")
	w("")
	w("jobs:")
	w("  gate:")
	w("    name: Heimdall agent gate (%s)", o.Mode)
	if o.Mode == ModeLive {
		w("    if: github.event_name == 'workflow_dispatch' || github.event.pull_request.head.repo.full_name == github.repository")
		w("    runs-on: [self-hosted, heimdall]")
		w("    timeout-minutes: 180")
	} else {
		w("    runs-on: ubuntu-latest")
		w("    timeout-minutes: 20")
	}
	w("    env:")
	w("      HEIMDALL_DIR: ${{ github.workspace }}/.heimdall")
	w("    steps:")
	w("      - name: Check out the repository")
	w("        uses: %s", checkoutAction)
	w("        with:")
	w("          fetch-depth: 0 # tasks and the baseline ref come from history")
	w("          persist-credentials: false")
	w("")
	w("      - name: Check out Heimdall")
	w("        uses: %s", checkoutAction)
	w("        with:")
	w("          repository: %s", o.Source)
	w("          ref: %s # pin a commit for a reproducible gate", o.SourceRef)
	w("          path: .heimdall-src")
	w("          persist-credentials: false")
	w("")
	w("      - name: Set up Go")
	w("        uses: %s", setupGoAction)
	w("        with:")
	w("          go-version-file: .heimdall-src/bench/go.mod")
	w("          cache-dependency-path: .heimdall-src/bench/go.sum")
	w("")
	w("      - name: Build heimdall")
	w("        working-directory: .heimdall-src/bench")
	w("        env:")
	w("          GOWORK: \"off\"")
	w("          CGO_ENABLED: \"0\"")
	w("        run: go build -o \"$RUNNER_TEMP/heimdall\" ./cmd/bifrost-bench")
	w("")
	experiment := ""
	if o.Mode == ModeLive {
		experiment = "experiments/pr-gate.yaml"
		var args []string
		args = append(args, "--baseline", "\"origin/${{ github.base_ref || 'main' }}\"", "--candidate", "\"${{ github.sha }}\"",
			"--base", o.Base, "--trials", fmt.Sprint(o.Trials))
		for _, f := range o.Faults {
			args = append(args, "--fault", f)
		}
		args = append(args, "--budget-usd", fmt.Sprint(o.BudgetUSD), "--name", "pr-gate", "--write-only")
		w("      - name: Write the baseline-vs-candidate experiment")
		w("        working-directory: .heimdall")
		w("        run: |")
		w("          rm -rf experiments/pr-gate.yaml experiments/fixtures/pr-gate")
		w("          \"$RUNNER_TEMP/heimdall\" compare %s", strings.Join(args, " "))
		w("")
	}
	w("      - name: Heimdall gate")
	w("        id: gate")
	w("        uses: ./.heimdall-src/bench/ci")
	w("        env:")
	w("          BENCH_CI_BENCH_DIR: ${{ github.workspace }}/.heimdall")
	if o.Mode == ModeLive {
		w("          BENCH_CI_ENV_FILE: ${{ vars.HEIMDALL_ENV_FILE }} # an env file on the runner; never echoed")
		w("          BENCH_RUNS_ROOT: ${{ github.workspace }}/.heimdall/runs")
	}
	w("        with:")
	w("          mode: %s", o.Mode)
	w("          bin: ${{ runner.temp }}/heimdall")
	if o.Mode == ModeLive {
		w("          experiment: %s", experiment)
	} else {
		w("          bundle: ${{ github.workspace }}/.heimdall/recorded")
		if o.ExpID != "" {
			w("          exp-id: %s", o.ExpID)
		}
		w("          data-mode: %s", o.DataMode)
	}
	w("          policy: \"\" # .heimdall/policies/default.yaml")
	w("          fail-on: %s", o.FailOn)
	w("          artifact-name: heimdall-report")
	w("")
	w("      - name: Render the verdict card")
	w("        if: always() && steps.gate.outputs.report != ''")
	w("        run: |")
	if o.Mode == ModeRecorded {
		w("          export BENCH_RUNS_ROOT=\"$HEIMDALL_DIR/recorded\"")
	}
	w("          \"$RUNNER_TEMP/heimdall\" ci card -config \"$HEIMDALL_DIR/deploy/bench.yaml\" \\")
	w("            -report \"${{ steps.gate.outputs.report }}\" -out \"$RUNNER_TEMP/heimdall-card.md\" \\")
	w("            -evidence \"artifact heimdall-evidence of run ${{ github.run_id }}\"")
	w("")
	w("      - name: Post or update the PR comment")
	w("        if: always() && github.event_name == 'pull_request'")
	w("        env:")
	w("          GH_TOKEN: ${{ github.token }}")
	w("          PR: ${{ github.event.pull_request.number }}")
	w("          REPO: ${{ github.repository }}")
	w("        run: |")
	w("          card=\"$RUNNER_TEMP/heimdall-card.md\"")
	w("          [ -s \"$card\" ] || { echo \"no verdict card (check stopped before a report)\"; exit 0; }")
	w("          id=$(gh api \"repos/$REPO/issues/$PR/comments\" --paginate --jq '.[] | select(.body | startswith(\"%s\")) | .id' | head -n 1)", CardMarker)
	w("          if [ -n \"$id\" ]; then")
	w("            gh api -X PATCH \"repos/$REPO/issues/comments/$id\" -F body=@\"$card\" >/dev/null")
	w("          else")
	w("            gh api -X POST \"repos/$REPO/issues/$PR/comments\" -F body=@\"$card\" >/dev/null")
	w("          fi")
	w("")
	w("      - name: Upload the evidence bundle")
	w("        if: always()")
	w("        uses: %s", uploadAction)
	w("        with:")
	w("          name: heimdall-evidence")
	w("          path: |")
	if o.Mode == ModeLive {
		w("            .heimdall/runs/")
		w("            .heimdall/experiments/pr-gate.yaml")
	} else {
		w("            .heimdall/recorded/")
	}
	w("            ${{ runner.temp }}/heimdall-card.md")
	w("          if-no-files-found: warn")
	return b.String(), nil
}
