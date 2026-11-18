# Acme visual example: tasks graded on what the result looks like

This folder is a complete Heimdall (bifrost-bench) project for work that is
only right if it looks right: a restyled page, a chart, a desktop set up a
certain way. The guide is [`docs/visual-grading.md`](../../docs/visual-grading.md).
No Go code is needed.

- **One suite, three capture kinds.** `acme-visual` on `grader: visual`:
  - `primary-button`: make the settings page's primary button blue and
    centred (the agent edits `site/style.css`); the bench renders the page
    in its clean-room Chromium.
  - `revenue-chart`: draw the quarterly revenue chart to the house spec as
    `chart/revenue.svg`; the bench renders the SVG.
  - `review-layout`: turn on the text editor's line numbers and status bar
    on a Linux desktop; the bench calls the desktop's own screenshot tool.
    The editor keeps that setting in memory only, so no saved file shows
    it: only the picture can decide.
- **Two checks per run.** The task's hidden verifier
  (`verifier/tests/verify.py`) checks the structure or state (the page
  still holds its form, the SVG is well formed with four bars, the note was
  not edited); then the visual grader checks the capture against the hidden
  reference `verifier/reference.png` (`output.visual` in each `task.yaml`).
  A run passes only when both pass.
- **Controls per task.** A reference script that must pass three times, an
  empty control, two plausible wrong results (mutants) and an exploit that
  must all fail. Each exploit plants a fake `visual/capture.png` and reward
  files in the workspace: they count for nothing, because the bench takes
  its own capture.
- **No judge.** Every check here is deterministic, so the suite is verified,
  not judged. `docs/visual-grading.md` shows the same chart task with a
  pinned vision judge, run at $0 against a scripted model.

## Layout

```
benchmarks/acme-visual/
  suite.yaml                                  grader visual, verifier command reward
  probes/model-outage.yaml
  tasks/primary-button/   repo/site/{index.html,style.css}, verifier/{reference.png,tests/verify.py}, solution/, controls/
  tasks/revenue-chart/    repo/{data/revenue.csv,chart-spec.md}, verifier/{reference.png,tests/verify.py}, solution/, controls/
  tasks/review-layout/    seed/Documents/note.txt, verifier/{reference.png,tests/verify.py}, solution/, controls/
agents/oracle.yaml, agents/noop.yaml          scripted controls (no model)
experiments/visual-smoke.yaml                 oracle vs noop, $0
deploy/bench.yaml                             the bench's own stack (127.0.0.1:18080)
```

## Run it

Images: the bench helper `bench/coding-core-base:1`, the pinned Playwright
MCP image of the browser tasks (`mcr.microsoft.com/playwright/mcp:v0.0.83`,
about 1.45 GB) for rendering, and the acme-desktop image (about 860 MB):

```sh
docker build -t heimdall-example/acme-desktop:1 examples/acme-desktop/benchmarks/acme-desktop/services/desktop
cd examples/acme-visual
bifrost-bench suite lint acme-visual -config deploy/bench.yaml
bifrost-bench suite lock acme-visual -config deploy/bench.yaml
bifrost-bench suite admit acme-visual -config deploy/bench.yaml
bifrost-bench run experiments/visual-smoke.yaml -config deploy/bench.yaml
```

Try a check on any image, or remake a reference with the grader's own
capture path:

```sh
bifrost-bench visual compare acme-visual/revenue-chart -image my-chart.png -config deploy/bench.yaml
bifrost-bench visual render site/index.html -width 800 -height 480 -out verifier/reference.png
bifrost-bench visual capture -desktop <desktop container> -out verifier/reference.png
```

## Record

`suite admit acme-visual` on 18 Nov 2026 admitted all three tasks
(`benchmarks/acme-visual/suite.lock`): every reference passed 3 of 3, and the
empty control, both mutants, the exploit and the noop and refuse agents
failed on every task; static validation clean, verifier with no network
(11.5 minutes on a shared 8-core machine). The free experiment
`experiments/visual-smoke.yaml` (`e_visual_smoke_9uwe`) then scored oracle 3
of 3 and noop 0 of 3, with `visual_score` 1 on every oracle run.

What each wrong control scored (`bifrost-bench visual compare`, 18 Nov
2026):

| Task | Control | Failing checks |
|---|---|---|
| primary-button | red, centred | colours (nearest #dc2626 at distance 264), region (crop not found); SSIM 0.9998 passes: luma cannot tell red from blue, which is why the colour check is there |
| primary-button | blue, left | SSIM 0.969, region (found at (116,279), not at (326,279)) |
| primary-button | empty | SSIM 0.975, colours, region |
| revenue-chart | Q3 label missing | pixel: 101 of 153,600 pixels differ |
| revenue-chart | Q2 drawn as 160 | pixel: 1,299 pixels differ |
| review-layout | line numbers only | SSIM 0.986, status bar not found |
| review-layout | toolbar instead | SSIM 0.956, gutter found at (0,92) not (0,50), status bar not found |
| review-layout | fake line numbers typed | SSIM 0.973, gutter and status bar not found |
