# Recordings, live screen and taking control

Browser and desktop runs can keep a video of what the agent did, show the
screen while the agent works, and, in a debug run, let a person take control.

- **Recordings.** A service declares the files it writes for people to watch:
  a browser's video (one webm per page), its HAR and its console messages, or
  a desktop's screen video (mp4). After the agent exits and the state is
  exported, Heimdall copies them into the run at `recordings/<service>/`,
  seals them with the run and shows them on the run page next to the trace.
- **Live screen.** While the agent runs, the run page shows the page or
  desktop the agent is using, refreshed every 2 seconds.
- **Taking control.** In a debug run only, a member of the organization may
  click and type on that screen. Every input is written to the run and the
  run is marked `human_assisted`, which keeps it out of every verdict, report
  card and leaderboard.

Recordings are evidence for people, never input to the grade. The verifier
reads only `state/` and the answer (`graders/cleanroom.go` mounts
`/grader/state` and nothing else), and recordings live outside it.

## Declaring recordings

`recordings:` sits on a service in `task.yaml` or `suite.yaml`, beside
`export:`.

```yaml
services:
  - id: browser
    role: mcp
    image: bench/my-suite-browser:1
    port: 8931
    recordings:
      stop: ["sh", "-c", "kill -TERM 1; sleep 60"]
      wait_s: 20
      files:
        - {id: video, path: /tmp/rec/video, kind: video}
        - {id: har, path: /tmp/rec/har, kind: har}
        - {id: console, path: /tmp/rec/console.jsonl, kind: console}
```

| Field | Meaning |
|---|---|
| `stop` | Runs inside the service after the state exports, so the service can finish its files. When it exits 0 the files are copied at once. A stop command may stop its own container instead (its exec then fails or is cut off); Heimdall then waits for the container to stop and copies the files from the stopped container. |
| `wait_s` | How long to wait for that stop, default 20, at most 120. At the deadline the files are copied as they are. |
| `files[].id` | A short id, unique in the service. |
| `files[].path` | An absolute path in the container: a file, or a folder whose files of the kind's types are copied. |
| `files[].kind` | `video` (`.webm`, `.mp4`), `har` (`.har`, `.json`), `console` or `log` (`.jsonl`, `.json`, `.txt`, `.log`). Other files are skipped. |
| `files[].max_bytes` | The cap of this recording, default 128 MiB. |

A file `<name>.start` next to a recording holds the time its first frame was
taken (epoch milliseconds or RFC 3339). The run page uses it to put each tool
call at the right place in the video. Without it the video is aligned to the
service's first tool call.

### Caps and secrets

- One recording is at most its `max_bytes`; all recordings of a run together
  at most 256 MiB. A recording over a cap is left out and the reason is
  listed on the run page.
- Text recordings (HAR, console, log) are cleaned before they are written:
  `Authorization`, `Cookie`, `Set-Cookie` and API key headers lose their
  values, and key-value secrets and known token formats (`sk-` keys, AWS key
  ids, GitHub and Slack tokens) are masked. The run folder is sealed only
  when it holds no secret, and a HAR is where one would show up.
- A recording that fails never fails the run. It is listed with its reason.
  When a service writes no file at a recording's path, the reason is a
  plain note: the agent never opened a page or screen, or the image cannot
  record (see [Images that record](#images-that-record)).

The run folder gets `recordings/<service>/<id>-<file>` for each file and
`recordings/recordings.jsonl` with one line per recording (service, id,
kind, file, media type, bytes, sha256, start time or the error). Both are
part of the run's seal.

## The browser template

`suite init -template browser` writes a browser image that records by
default. Its entrypoint is `record.js`, a small launcher that starts the
upstream Playwright MCP server (`@playwright/mcp` 0.0.83, pinned by digest)
with `record.json`:

- every browser context records a 1280x720 video and a HAR (`content: omit`,
  so bodies are left out);
- console messages and page errors go to `/tmp/rec/console.jsonl`;
- on SIGTERM it closes every context first, so the video and HAR are
  complete (the upstream server alone kills the browser and leaves a
  truncated video and no HAR), then saves each page's video as
  `/tmp/rec/video/page-<n>.webm` with its `.start` file.

The template task's `stop` is `kill -TERM 1; sleep 60`: tini passes the
signal to the launcher, the service stops once the files are written, and
the files are copied from the stopped container.

## The desktop template

`suite init -template computer-use` installs ffmpeg in the desktop image.
`desktop.py` records the display from the start of the session at 2 frames a
second into `/tmp/desktop/rec/desktop.mp4` (a fragmented mp4, so a recording
cut short still plays), with `desktop.mp4.start`. Set `DESKTOP_RECORD` to
another folder, or to empty to turn recording off. The template task's
`stop` is `desktop.py stop-recording`, which stops ffmpeg so it writes the
end of the file and returns when it has. Expect tens of MB per hour.

## Images that record

Recording depends on the service image, not on Heimdall alone.

- **Browser.** The template's base is `mcr.microsoft.com/playwright/mcp`
  v0.0.83 (Playwright 1.64), pinned by digest. That version reads
  `browser.contextOptions` from `--config` and passes `recordVideo` and
  `recordHar` to every isolated context; checked in the pinned image (one
  navigate, then SIGTERM, leaves `video/page-1.webm`, `har/context-1.har`
  and `console.jsonl`). The image ships its own ffmpeg for Playwright's
  video, so nothing else is installed. An older Playwright MCP, or an image
  started without `record.js`, still runs the task but writes no video or
  HAR; the run page then lists each missing recording with a note.
  `visual/capture.go` uses the same pin for screenshots; bump both together
  (a scaffold test checks they match).
- **Desktop.** The computer-use template installs ffmpeg with apt, which
  makes the image about 1 GB. A desktop image built before recordings (for
  example from an older template) has no ffmpeg: `desktop.py` then skips
  recording, the live screen falls back to a half-size PNG, and the run
  page notes the missing video. To record, rebuild it once (the apt step
  needs the network; runs never get it):

  ```sh
  docker build -t <tag in the task's services: image> benchmarks/<suite>/services/desktop
  bifrost-bench suite admit <suite>   # locks the new image digest
  ```

  Expect a few minutes and about 1 GB of disk for the image.

## The run page

The **Recording** tab of a run shows the video and, beside it, the tool calls
of the recorded service with their times:

- click a step and the video jumps to it;
- while the video plays, the step under the playhead is highlighted and the
  bar under the video marks every step (faulted calls in red);
- the HAR, console and any failed recording are listed for download;
- **Compare two runs** puts another run's recording beside this one (an
  agent's two versions on one task, or a pass next to a fail) with one
  button that plays both from the start.

API: `GET /api/bench/runs/{id}/recordings` lists the recordings and the
steps; `GET /api/bench/runs/{id}/recordings/{n}` serves one file in byte
ranges (the `Range` header), so a video seeks without loading whole.

## Live screen

A service declares how to read its screen with `live:`:

```yaml
    live:
      frame: ["node", "/opt/heimdall/record.js", "frame"]   # prints one JPEG or PNG
      input: ["node", "/opt/heimdall/record.js", "input"]   # reads one action as JSON on stdin
```

The desktop template uses `desktop.py frame` and `desktop.py input`.

While the agent runs, the engine runs `frame` every 2 seconds and publishes
the image (at most 640 KiB) to the run page. Frames are display only: they
are kept in the server's memory for 15 seconds and never written to the run
folder or a database. A run on a worker sends its frames to the server with
the worker's own token (`POST /api/bench/workers/leases/{lease_id}/live-frame`).
Only members of the run's organization see a live screen, never public
readers, because a live screen can show a customer's staging data.

API: `GET /api/bench/runs/{id}/live` says whether the run shows its screen;
`GET /api/bench/runs/{id}/live/frame` is the newest image.

## Taking control in a debug run

A person may take control only of a **debug run** (a replay, fork or
breakpoint run of a recorded run, started from the run page's debug actions), whose results are
never counted anyway, and only when the service has an `input` command.
Whether a run is a debug run comes from the server's own plan, never from the
worker.

On the run page, **Take control** on the live screen turns clicks on the
image into clicks on the page or desktop, and adds a box to type text and
buttons for Enter, Tab, Escape and scrolling.
`POST /api/bench/runs/{id}/takeover` takes one input:

```json
{"action": "click", "x": 480, "y": 300, "frame_w": 960, "frame_h": 600}
{"action": "type", "text": "hello"}
{"action": "key", "combo": "ctrl+s"}
{"action": "scroll", "direction": "down"}
```

`x` and `y` are pixels of the live frame; `frame_w` and `frame_h` are its
size, and the service scales them to its screen. The run takes the inputs
with its next frame, performs each with the service's `input` command (the
desktop uses the agent's own click, type, key and scroll tools), writes it to
`human_actions.jsonl` in the run folder with who sent it, and marks the run
`human_assisted: true` in `run.json`. A human assisted run is left out of
verdicts, report cards and leaderboards like a practice run, and its
Recording tab lists the inputs.

## Limits

- Recording costs CPU: a browser context's video adds an encoder per page,
  and a desktop's ffmpeg about a tenth of a core at 2 frames a second.
- The browser video starts when Playwright opens the page, at the agent's
  first browser call. Pages opened later get their own video.
- Live frames reach the page every 2 seconds and inputs act with the next
  frame, so taking control is for checking and nudging, not for fast work.
- Recordings and the live screen need the Docker workspace. The Kubernetes
  and sandbox drivers do not run services yet.
- Suites admitted before recordings existed keep their locks. Their browser
  and desktop images record once their services gain `recordings:` and the
  suite is admitted again.
