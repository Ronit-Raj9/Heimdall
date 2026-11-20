# Visual grading: what the agent's work looks like

Some work is only right if it looks right: a restyled page, a chart, a
desktop set up a certain way. A state check cannot see that a button is
blue, that a bar has the right height, or that an editor shows line
numbers. Visual grading checks the picture, from evidence the bench took
itself, with cheap deterministic checks first and a calibrated vision judge
only where a rule cannot be written down.

Status, 18 Nov 2026 (X1): built and tested at $0. The checks, the captures,
the grader, the vision judge, its calibration path, the task spec, lint,
scaffolding, the run page panel and the example `examples/acme-visual/` are
in place. Every number on this page comes from a command in "What was run".
No real vision model has been called: the judge path was proven against
judgescript, a scripted model, through a real Bifrost v2.5.1 on a throwaway
stack with no provider key.

## How it works

```
agent exits
   |
   v
capture (by the bench, never the agent)
   artifact png/jpg  -> read from the sealed artifacts/<id>/
   artifact svg/html -> rendered in a clean-room Chromium (no network)
   browser           -> a fresh Chromium inside the run's browser service opens capture.url
   desktop           -> the desktop service's own screenshot tool, pointer parked first
   |
   v
decode, re-encode as PNG, write visual/capture.png (sealed with the run)
   |
   v
verifier command (state, structure)   +   checks of output.visual against the hidden reference
   |                                         dimensions, pixel, ssim, phash, colors, region
   |                                         diff heatmap -> visual/diff.png
   v
optional vision judge (rubric, pinned model, N votes) -> judged, calibrated or capped
   |
   v
grade: passed only when the verifier, every check and the judge (when there is one) pass
```

- **The grader.** A suite selects it with `grader: visual`. It runs the
  suite's verifier command first, exactly as `verifier_command` does (a
  state or structure check beside the looks), then the visual part of the
  task's output. A task without `output.visual` in such a suite is graded by
  the verifier command alone, so one suite can mix visual and non-visual
  tasks. Pure looks tasks can use a verifier that always writes a reward of
  1; the example's verifiers each check something real.
- **Captures are the bench's.** The agent never hands in a screenshot. A
  file the agent leaves called `visual/capture.png` in its workspace counts
  for nothing (the example's exploit controls do exactly that).
- **Sealed evidence.** The capture, the reference and the diff heatmap are
  written to `visual/` in the run folder before the seal, each decoded and
  re-encoded by the grader, so the file the run page shows is never the
  agent's own file. Their sha256, size and dimensions are in the grade. A
  capture is capped at 16 MiB encoded, 8192 x 16384 and 40 million pixels;
  a larger one is refused, never cut.
- **Who is at fault.** An artifact the agent did not produce, or an image
  that does not decode, fails the run. A capture the bench could not take (a
  service missing, the renderer broken, a timeout) makes the grade
  `invalid_evaluator` with the reason, never the agent's fail.

## Captures

| `capture.kind` | Where the image comes from | Settings |
|---|---|---|
| `artifact` | The artifact file `capture.artifact` (and `capture.file` for a glob). png and jpg are read as they are; svg and html are rendered in a fresh container of the renderer image with `--network none`, no capabilities, the artifact folder read-only | `width`, `height` (viewport), `scale` (1 to 3), `full_page` (html), `wait_ms`, `image` |
| `browser` | A new headless Chromium inside the run's browser service container (`capture.service`, default `browser`) opens `capture.url` after the agent exited. Only that URL's origin loads; the agent's own browser session is not touched | `url`, `width`, `height`, `scale`, `full_page`, `wait_ms` |
| `desktop` | The desktop service's own MCP `screenshot` tool (the one the agent used), called from inside its container at 127.0.0.1 after the pointer is parked in the bottom-right corner with its `move` tool | `service` (default `desktop`) |

The renderer and the browser capture run `visual/render.js` with the
Playwright library of the pinned Playwright MCP image
(`mcr.microsoft.com/playwright/mcp:v0.0.83@sha256:690763cb...`, the browser
tasks' image, about 1.45 GB, already on a machine that runs browser tasks).
It launches Chromium with a fixed viewport and device scale factor, light
colour scheme, reduced motion, locale en-US, time zone UTC, service workers
blocked, font hinting off and LCD text off, waits for the fonts to load,
and takes one PNG with animations disabled and the text caret hidden. The
grade records the renderer image and the sha256 of its font list
(`fc-list`), so a changed font set shows up as a changed hash.

A browser capture needs the browser service to be the Playwright MCP image
(it uses `/app/node_modules/playwright-core` inside it). A desktop capture
needs a desktop service whose MCP server has a `screenshot` tool returning
a PNG image block, like `examples/acme-desktop`'s.

## The task spec: `output.visual`

```yaml
output:
  kind: artifact
  artifacts:
    - {id: site, path: "site/*", required: true}
  visual:
    capture: {kind: artifact, artifact: site, file: index.html, width: 800, height: 480}
    reference: verifier/reference.png       # hidden: keep it out of repo_dir
    method: ssim                            # the first check, over the whole image
    threshold: 0.995
    masks:                                  # ignored by every check
      - {x: 176, y: 246, w: 164, h: 24}
    checks:
      - {id: button-blue, method: colors, colors: ["#1d4ed8"], min_share: 0.3, area: {x: 330, y: 283, w: 140, h: 42}}
      - {id: button-centred, method: region, crop: {x: 326, y: 279, w: 148, h: 50}, at: {x: 326, y: 279}, max_offset: 2}
    rubric: verifier/rubric.md              # optional: the vision judge (the suite then declares grading: judged)
    judge: {provider: anthropic, model: claude-sonnet-4-5, prompt_version: visual-v1, votes: 3, aggregate: majority}
```

The types are `schemas.VisualCheck`, `VisualCapture`, `VisualAssert`,
`VisualRect` and `VisualJudgeSpec` (`schemas/visual.go`); the JSON schema is
`$defs/visual` in `spec/task.schema.json`. `output.visual` may also sit in
`suite.yaml` for every task. Every field is optional on the task and on the
grade (`Grade.Visual`), so specs, manifests, locks and seals written before
it keep their hashes. The judge spec and the rubric are part of the task
folder, so the suite lock fingerprints them like every other task file.

## Checks

Every check reports a number, its threshold and a one-line detail in the
grade; `visual_score` is the share that passed. A check that cannot be
measured (no reference, sizes that differ for a pixel comparison) fails
with the reason.

| Method | Value | Passes when | Default threshold | Use it for |
|---|---|---|---|---|
| `dimensions` | 1 or 0 | the capture is `width` x `height` (default the reference's) | 1 | a chart or export of a fixed size |
| `pixel` | share of unmasked pixels whose largest channel difference is over `tolerance` | value <= threshold | 0 | exact layouts rendered by the bench itself (same renderer, same fonts): a chart drawn to a spec, a settings screen |
| `ssim` | mean SSIM of the luma over 8x8 windows (stride 4; a window touching a mask is left out) | value >= threshold | 0.98 | "looks the same" with small rendering noise; structure, not colour (a red button where a blue one was scores 0.9998: add `colors`) |
| `phash` | Hamming distance of 64-bit DCT perceptual hashes (masked pixels count as mid grey) | value <= threshold | 6 | coarse layout identity that survives JPEG and noise |
| `colors` | the largest distance (RGB, 0 to 442) from an expected colour to the nearest dominant colour covering at least `min_share` of the area | value <= threshold | 24 (min share 0.01) | "the button is blue", "the brand colour is used" |
| `region` | mean absolute difference per channel (0 to 1) of a crop of the reference (`crop`) or a file (`crop_file`) at its best match; with `at`, it must be found within `max_offset` pixels of that point | value <= threshold (and near `at`) | 0.02 (offset 2) | "the button is centred", "the gutter is at the left", a logo present |

`area` limits pixel, ssim, phash and colors to a rectangle and limits where
region searches. Region searches a 4x reduced copy first and refines the
best eight candidates at full size, so a 148 x 50 crop is found in an 800 x
480 capture in well under a second. Values are rounded to six decimals so a
grade prints and hashes the same way on every machine.

### Pixel, SSIM, perceptual hash or the judge?

- **The output is specified exactly** (a chart drawn to a house spec, a page
  whose CSS is fixed apart from the change): `pixel` with a small
  `tolerance` and a threshold of a few pixels. It catches a missing label
  (101 pixels in the example) and a bar 20 units short (1,299 pixels).
- **The output may vary slightly** but its structure must not: `ssim` with
  0.98 to 0.995, plus `colors` for what luma cannot see and `region` for
  where something must be.
- **Only coarse identity matters**, or captures are lossy (JPEG): `phash`.
- **No rule can be written down** ("the chart is readable", "the page looks
  professional"): a rubric and the vision judge, calibrated before its
  verdicts count (below). Keep deterministic checks beside it for what can
  be stated.

## Making captures stable

The same state must give the same pixels; X1 checked this with 5 repeated
captures of each kind (see "What was run"): every repeat was byte-identical.

- **Fix the viewport and scale** in `capture`; never rely on defaults when a
  layout depends on width.
- **Fonts come from the pinned image.** Name fonts the image has
  (`Liberation Sans`, `DejaVu Sans`, ...) and never load web fonts: the
  renderer has no network. The grade records the font list's hash.
- **Mask what changes by itself**: times written by a script at load (the
  example's "Last saved" line), counters, a cursor position in a status bar.
  Masks are rectangles in capture pixels, drawn blue on the run page.
- **Animations and the text caret** are off in the renderer, and motion is
  reduced. On a desktop the pointer is parked before the shot; an app's own
  text caret is not controlled by the bench: in the example's editor four
  shots 350 ms apart were identical, but mask the caret's line if your app
  keeps blinking.
- **Make the reference with the same path.** `bifrost-bench visual render`
  and `visual capture` use the exact code a grade uses and write the
  bench-encoded PNG, whose sha256 a grade of the same state records.

## The vision judge (`visual_judge`)

The judge is the `visual_judge` grade source: a judged part of the visual
grade (`Grade.Judged`, `Grade.Visual.Judge`, metric `visual_judge_score`),
beside the deterministic `visual` checks (`Grade.Visual.Checks`, metric
`visual_score`). Its model, prompt version, votes and aggregate are in the
task file, which the suite lock fingerprints, and the sha256 of the prompt
template is in every judged grade and calibration record.

A rubric lets a model decide outcomes, so a suite with one must declare
`grading: judged` (lint and the grader refuse it otherwise, as for the text
judge); the rubric's pinned vision judge stands in for a suite `judge:`, and
a judged suite never shares a comparison with a verified one.

When `output.visual.rubric` is set, the grader asks the pinned vision model
through Bifrost, on its own virtual key (never the run's, revoked after),
at temperature 0: the prompt `visual-v1` (`graders/visualprompts/`) with the
rubric, the capture as the first image and the reference, when there is
one, as the second. The model must answer one JSON object
`{"verdict": "pass"|"fail", "score": 0..1, "rationale": "..."}`; anything
else is an unparsable vote. `votes` samples are aggregated by `majority`
(more than half pass or fail, else undecided) or by `mean` (the mean score
against `pass_score`, default 0.5). An undecided judge makes the grade
`invalid_evaluator`.

A judged visual grade is labelled like the text judge's: `Grade.Judged` is
true and `Grade.Judge` is a JudgeRecord with the model, `prompt_version`
`visual-v1`, `prompt_sha256` (the sha256 of the shipped template text), the
votes as correct, incorrect or unparsable, the served model, the judge's
cost (never the agent's) and the calibration record in force. W8's
`judge_calibration` instrument therefore reads it unchanged:

- **No calibration record** for the exact model and template: the verdict of
  an experiment that rests on judged runs is capped at INCONCLUSIVE
  (`uncalibrated`), unless the experiment sets `allow_uncalibrated_judge`.
  The run page says "uncalibrated: reports, does not decide".
- **With a record**: the grade keeps the record's id and the agreement
  numbers (agreement, TPR, TNR, kappa with intervals) beside the judge's
  verdict; fewer than 30 human labels is `few_labels`, bounds under the
  policy thresholds make the experiment INVALID (`unreliable`).

### Calibrating it

Calibration items can hold images (`CalibrationItem.image`, the sha256 of
`calibration/images/<sha256>.png` beside `labels.jsonl`; checked on every
load for presence, hash and a 16 MiB cap; an item without an image keeps
its id exactly as before):

Calibration needs a judged suite. The stock `examples/acme-visual` suite is
not one: its checks are deterministic, no task has a rubric or a vision
judge, and `visual calibrate acme-visual` stops with `suite acme-visual has
no task with a rubric and a vision judge`. Make a judged copy first (here
`acme-visual-judged`, with the revenue chart task only): add
`rubric: verifier/rubric.md` and a pinned `judge:` (as in the YAML above) to
the task's `output.visual`, and set `grading: judged` in its `suite.yaml`.
`visual label` refuses a task without a rubric and a vision judge, since
calibration would never use that label.

A run's capture sits under its slot folder:
`<runs_root>/<exp>/slots/<slot>/runs/<run>/visual/capture.png`.

```sh
bifrost-bench visual label acme-visual-judged -task revenue-chart \
    -image runs/<exp>/slots/<slot>/runs/<run>/visual/capture.png \
    -label correct -labeller dana -note "matches the spec"
bifrost-bench visual calibrate acme-visual-judged          # forecast only
bifrost-bench visual calibrate acme-visual-judged -yes     # spends: one key, two passes of every item
```

`visual calibrate` runs the pinned vision judge on every labelled image
(first pass and a repeat pass; the text perturbations do not apply to
images) and writes an ordinary hash-chained calibration record whose
templates are `{"visual-v1": <sha256>}`; `judge status` and the verdict's
instrument read it like any other. All tasks of a suite with a rubric must
pin the same judge.

## Authoring a visual task

```sh
bifrost-bench task init -suite shop-admin -id hero-banner -kind browser -visual
# writes output.visual with EDIT ME notes and moves the suite to grader visual
bifrost-bench visual capture -browser <browser container> -url http://webapp:8000/ -out benchmarks/shop-admin/tasks/hero-banner/verifier/reference.png
bifrost-bench visual render site/index.html -width 800 -height 480 -out verifier/reference.png    # artifacts
bifrost-bench visual capture -desktop <desktop container> -out verifier/reference.png             # desktops
bifrost-bench visual compare shop-admin/hero-banner -image wrong.png     # try the checks on any image
bifrost-bench task validate shop-admin/hero-banner && bifrost-bench suite lock shop-admin && bifrost-bench suite admit shop-admin
```

`-visual` applies to browser, computer-use and artifact tasks. Lint
(`suite lint`, `task validate`) refuses: `output.visual` under another
grader, a reference that is missing, does not decode, leaves the task folder
or sits inside `repo_dir` (the agent would see it), masks, areas, crops or
`at` points outside the image, thresholds out of their method's range, a
tolerance outside 0 to 255, an unknown method or capture kind, a capture
artifact that is not declared or is a glob without `file`, a browser or
desktop capture without its service (or a desktop service without a port),
a browser capture without an http url, a rubric without a pinned judge or in
a suite that is not `grading: judged`, and nothing to grade at all.

## Example: `examples/acme-visual`

Three tasks, every one also checked by a hidden verifier:

| Task | Capture | Checks | Controls that must fail |
|---|---|---|---|
| `primary-button`: make the settings page's primary button blue and centred (edit `style.css`) | the page rendered at 800 x 480, the script-written "Last saved" time masked | SSIM 0.995; `#1d4ed8` covers 30% of the button area; the button crop found centred within 2 px | empty (grey, left), red but centred, blue but left, an exploit that replaces the page and plants a fake capture |
| `revenue-chart`: draw the quarterly revenue chart to the house spec as SVG | the SVG rendered at 480 x 320 | at most 0.01% of pixels differ by more than 32; 480 x 320; `#2563eb` covers 10% | empty (no file), Q3 label missing, Q2 drawn as 160, an exploit that saves the spec as the chart |
| `review-layout`: turn on the editor's line numbers and status bar (desktop) | the desktop's own screenshot, the status bar's line and column masked | SSIM 0.99; the line-number gutter at the left edge; the status bar at the bottom | empty, line numbers only, toolbar instead of status bar, an exploit that types fake line numbers into the note |

`review-layout` is the case only vision grading can decide: the editor
keeps the setting in memory, so no saved file shows it, and its state
verifier only checks that the note was not edited.

## Metrics, API and the run page

- `visual_score` (rate, higher is better, evidence outcome): share of a run's
  deterministic checks that passed. `visual_judge_score` (rate, higher is
  better): the vision judge's mean score, judged evidence, unknown when no
  sample parsed. Both are not applicable to runs without `output.visual`.
- `GET /api/bench/runs/{id}` carries the grade with `visual` (keys pinned in
  `handlers/testdata/contract/Visual*.keys`).
  `GET /api/bench/runs/{id}/visual/{capture.png|reference.png|diff.png}`
  serves an image of a sealed run only, only when its bytes hash to both the
  grade's record and the seal, as `image/png` with `nosniff` and a sandbox
  CSP.
- The run page shows a Visual grade panel under each arm's steps: the
  capture with its masks, the reference and the heatmap, every check's
  number, and the judge's verdict, votes, calibration state and rationale
  (the model's words, shown clipped as plain text).

## What was run (18 Nov 2026)

Unit tests (generated images: known shifts, colour changes, noise, JPEG,
alpha), the grader and judge against a scripted vision endpoint and a fake
gateway, lint, scaffold, the handler and the contract keys, and the UI
panel: see the X1 report for the commands. Real-Docker results:

- **Determinism, 5 captures each, byte-identical:** an html page at 800 x
  400 and at 800 x 300 full page with device scale 2 (1600 px wide), an
  SVG at 320 x 200 (clean-room renderer, about 7 s per render), a page
  captured inside a browser container from a web server on an internal
  network, and a desktop (xfe file manager) through its screenshot tool
  (`GOWORK=off go test -tags integration ./visual/ -run TestIntegration -p 1 -v`).
  Two fresh desktop containers driven by the same reference script gave
  the same sha256 (`1b5c08da7d96...`). The same page rendered from a file
  and captured over http from the browser container gave the same sha256
  (`b61e82ad7c05...`), and the chart the oracle drew in the experiment below
  was captured with the sha256 `visual render` gave for it by hand
  (`6576f3ec6d0d...`).
- **Browser capture in the real pipeline:** a scratch copy of
  `examples/acme-web`'s `create-customer` task on `grader: visual` with a
  browser capture of `http://webapp:8000/customers` (the environment's own
  Playwright MCP service, the web application hidden on the run's second
  network). The oracle's capture from a smoke experiment became the
  reference with `method: pixel, threshold: 0`, and `suite admit` passed the
  reference 3 of 3 from three fresh service pairs, so the captures were
  pixel-identical; the wrong-tier mutant differed in 474 of 1,024,000
  pixels and the empty control in 4,210.
- **Admission** (`suite admit acme-visual`): 3 of 3 tasks admitted, every
  reference 3 of 3, every empty control, mutant, exploit, noop and refuse
  failed (11.5 minutes on the shared machine; a first admission before the
  verifiers took their paths from the environment took 26.5).
- **Experiment** `e_visual_smoke_9uwe` through the bench's own stack (scripted
  oracle and noop agents, anthropic provider, no model call): oracle 3 of 3,
  noop 0 of 3, `visual_score` 1 on every oracle run; noop scored 0 on the page
  and the desktop and was failed as "required artifact missing" on the
  chart. Verdict inconclusive (3 tasks cannot decide), as designed for a
  smoke. The oracle's desktop and chart captures had the same sha256 as in
  an earlier run of the same experiment (`2aff9ad75573...`,
  `6576f3ec6d0d...`); the page's differed only inside the masked time.
  Served back with `serve -offline`: `GET /runs/<run>/visual/capture.png`
  returned the PNG with the sha256 the grade records, `image/png`,
  `nosniff`; any other name answered 404.
- **Vision judge through real Bifrost at $0:** a throwaway stack
  (`hd-x1`: Bifrost v2.5.1, the fault proxy, Postgres, judgescript extended
  to hash image blocks) with dummy keys only. `visual calibrate` minted a
  key, sent 18 calls with the capture and the reference as OpenAI
  `image_url` parts; Bifrost delivered them to the proxy's anthropic
  upstream as Anthropic image blocks, and judgescript saw the same sha256 of
  both images the grader sent. The record (3 fixture labels, one deliberate
  false positive scripted): agreement 2/3, TPR 1/1, TNR 1/2, kappa 0.40,
  both passes identical. The "$0.0136" it printed is the price snapshot
  applied to the token counts judgescript reports; no provider was called.
- **Judged pipeline at $0 on the same stack:** the chart task with a rubric
  and a pinned judge (`claude-sonnet-4-5`, 3 votes, majority) in a suite on
  `grading: judged`. `suite admit` admitted it with the judge asked on every
  control that produced a chart: reference 3 of 3 (checks and judge pass);
  the missing-label mutant failed on the pixel check although the scripted
  judge passed it (the deliberate false positive: a deterministic check
  catches what a judge misses); the wrong-value mutant failed on both; the
  exploit failed the verifier, the pixel and the colour check and the
  judge. The experiment `e_visual_judged_smoke_9t6d` (oracle 1 of 1, noop 0
  of 1) recorded the judged grade with the calibration record
  `cr_m65slxvpv6`, and the verdict's `judge_calibration` instrument read it
  as `few_labels` (3 labels, agreement 0.667 [0.094, 0.992]) and left the
  verdict inconclusive.

## Security

- The agent's bytes are never served raw: every image the run page loads is
  re-encoded by the grader from decoded pixels, checked against the grade
  and the seal, and served as `image/png` with `nosniff` and a sandbox CSP.
  The artifact route still serves the agent's files only as downloads.
- svg and html run in a container with no network, no capabilities, a 2 GB
  and 512-process cap, the artifact folder read-only; requests to anything
  but the folder are aborted. A browser capture loads only the capture
  URL's origin.
- The reference image is hidden from the agent (lint refuses one inside
  `repo_dir`), but it is copied into the sealed run folder and shown on the
  run page: anyone who can read runs can see it, as they can see the
  verifier's output. Keep references of held-out tasks off shared servers.
- The judge's rationale is model text: it is clipped and rendered as plain
  text, and it never changes a check.

## Limits

- **No real vision model has run.** The judge path, its JSON parsing,
  aggregation and calibration are proven with a scripted endpoint and
  judgescript only; how a real model scores the example rubric is unknown.
- **No OCR, no video, no score without a reference or a rubric** (out of
  scope). Text is checked as pixels (a missing label changes pixels), not
  read.
- **Pixel checks need the bench's renderer.** An agent-rendered PNG (its own
  matplotlib, say) will not match a reference pixel for pixel; use SSIM,
  colours, regions or the judge for those.
- **One capture per task**, taken after the last session; no capture per
  step and no capture of a page state that only lived in the agent's
  browser tab (the browser capture reopens the URL in a new browser, so
  client-side-only changes are not seen).
- **Desktop captures depend on the desktop image**: its fonts and theme are
  whatever the image installs, and the pointer can only be parked when the
  MCP server has a `move` tool.
- **Calibration items are per image**: two renders that differ in one pixel
  are two items. The vision record covers the `visual-v1` template only.
- **Admission is slow on a shared machine**: about 25 s per control for an
  artifact task (a render, the verifier container, the export).

## See also

- [`browser-tasks.md`](browser-tasks.md), [`computer-use.md`](computer-use.md),
  [`scenarios.md`](scenarios.md): the tracks the captures come from.
- [`judge-calibration.md`](judge-calibration.md): the calibration records,
  thresholds and the instrument.
- [`examples/acme-visual`](../examples/acme-visual/README.md): the example
  project.
