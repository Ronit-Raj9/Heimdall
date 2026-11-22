# Voice agents

Heimdall benchmarks voice agents (a cascade of speech to text, a chat model
and text to speech, or one speech-to-speech Realtime model) on spoken
support calls. Every model call goes through Bifrost; a simulated caller
speaks to the agent, listens to it on a real-time clock and writes a
timeline of the call; the grade is the helpdesk's final state, exactly as in
the text support track (`docs/customer-support.md`). Everything in this
guide was built and checked at $0: scripted controls, the fault proxy's
scripted model, a local fake for audio and chat, and local speech engines.

The wire protocol an agent speaks is in [`docs/voice-protocol.md`](voice-protocol.md).
An agent hosted on Vapi, Retell, ElevenLabs Agents or your own call
WebSocket (Twilio Media Streams format) connects without a container: see
[`docs/voice-platforms.md`](voice-platforms.md).

## Start

```sh
bifrost-bench suite init calls -template voice     # one sample task: refund-in-policy, spoken
docker build -t bench/calls-voicedesk:1 benchmarks/calls/services/helpdesk   # about 1.1 GB; suite admit builds it when missing
bifrost-bench voice check -suite calls             # every voice task and the caller image, no model
bifrost-bench suite admit calls                    # reference passes 3 times, every wrong control fails
```

or the example (`examples/acme-voice-desk/README.md`): the six acme-helpdesk
chats, spoken.

## What a voice task is

A support task (`track: voice`, `output: state`) with a `voice:` block and
two services from one image:

- `helpdesk`: the MCP server of the support track (orders, policy, refunds,
  credits, escalation), reached through the per-run tool proxy, so every
  tool call is in `tools.jsonl`;
- `customer`: the voice caller (`bench/voice/voicecaller.py`), a role `user`
  service the agent dials at `ws://customer:8000/call`. It is not proxied:
  the tool proxy does not carry WebSocket audio, and the caller itself is the
  bench's witness of the call.

```yaml
voice:
  caller: customer                              # the caller service (default customer)
  transport: websocket                          # heimdall-voice/1; the only transport
  probes: [noise, speed, interrupt, silent]     # voice probes this script can take (empty: all)
  barge_in_bound_ms: 1000                       # barge_in_handled bound (default 1000)
  dead_air_ms: 2000                             # dead_air_s bound (default 2000)
```

The planner refuses, with the reason, a voice task whose caller is missing,
not role `user`, hidden, proxied, or without the `timeline.jsonl` and
`transcript.json` exports; any transport but `websocket` (telephony, SIP and
PSTN are not supported); a voice probe the task does not allow; a voice
probe on a task with no call; and an agent's `gateway.models` the run key
cannot serve (below).

### The caller

The caller decides what to say with the text user simulator of the support
track (`bench/simuser/simuser.py`, loaded as a module): the task's
`user.json` script by default, deterministic and free, or a pinned model
playing the persona (`simulator: {mode: llm, ...}`), through its own gateway
key. It then:

1. speaks each line with local text to speech: `tone` (the tone code below)
   or `piper` (Piper, real synthesized speech; the example tasks use it);
2. plays the agent's audio on a real-time clock, so audio that arrives
   faster than real time queues, and a `clear` from the agent drops what has
   not played;
3. finds where the agent's speech starts and ends on the played audio
   (energy, 800 ms of silence ends an utterance), transcribes each utterance
   (`auto`: the tone code exactly, else Whisper tiny.en on CPU) and answers
   it after 300 ms;
4. speaks first after 3 s of agent silence, asks "Hello? Are you still
   there?" after 7 s of silence on the agent's floor (twice at most), then
   hangs up; hangs up after its last scripted line, at `max_turns`, or at
   240 s.

Settings, all from the caller service's `env` (the example sets the first
two):

| Variable | Default | |
|---|---|---|
| `BENCH_VOICE_TTS` | `tone` | `tone` or `piper` |
| `BENCH_VOICE_STT` | `auto` | `auto`, `tone` or `whisper` |
| `BENCH_VOICE_END_SILENCE_MS` | 800 | agent silence that ends an utterance |
| `BENCH_VOICE_REPLY_DELAY_MS` | 300 | the caller's reaction time |
| `BENCH_VOICE_FIRST_WAIT_MS` | 3000 | the caller speaks first after this |
| `BENCH_VOICE_REPROMPT_MS`, `BENCH_VOICE_MAX_REPROMPTS` | 7000, 2 | silence on the agent's floor |
| `BENCH_VOICE_MAX_CALL_S`, `BENCH_VOICE_MAX_AUDIO_S` | 240, 240 | call length; audio kept per track |
| `BENCH_VOICE_KEEP_AUDIO` | 1 | 0 keeps only the tracks' sha256 |

### Local speech engines

Built into the caller image from their primary sources, each checked
against a pinned sha256 at build time (checked 18 Nov 2026):

| Engine | Source | Licence | Size |
|---|---|---|---|
| Piper 2023.11.14-2, Linux x86_64 binary | github.com/rhasspy/piper release, sha256 `a50cb45f…25992` | MIT | 26 MB download, 52 MB unpacked |
| Piper voice `en_US-ljspeech-medium` | huggingface.co/rhasspy/piper-voices, tag v1.0.0, LFS sha256 `6f52a751…b87f22` | voice repo MIT; LJ Speech dataset public domain (its MODEL_CARD) | 63.5 MB |
| faster-whisper 1.2.1 with CTranslate2 4.9.0 | PyPI | MIT, MIT | 442 MB of Python packages with numpy, onnxruntime and PyAV |
| Whisper `tiny.en`, CTranslate2 conversion | huggingface.co/Systran/faster-whisper-tiny.en at `0d3d19a3`, model.bin sha256 `1a5afae0…936b2a` | MIT | 75 MB |

The Piper voice was chosen for its licence: other popular voices (for
example `en_US-lessac`) are trained on data with research-only terms.
Piper runs with `noise_scale` and `noise_w` 0, so a line is synthesized the
same way every time. On this machine (8 shared cores) Piper took 1.3 to 2.3 s
per line and Whisper tiny.en 0.8 to 1.1 s per utterance; the caller
synthesizes the script's lines while it starts. Whisper tiny.en is a weak
recogniser: it heard "Hi, my burr grinder from order B-3001 ... I would like
a refund" as "Hi, my bird grinder from order B. 3,001 ... I would like a
reefer". That is the caller's ear, so agents should speak clearly; the
verifier's `agent_says` checks allow digits or words for amounts.

### The tone code

`bench/voice/tonecode.py` (and `tonecode.go`, byte for byte the same): text
carried in audio, one 20 ms sine tone per 4 bits, a start and end marker and
a CRC-8, 25 bytes a second, so a sentence takes about as long to play as to
say. The admission controls speak it, the scripted model and the fake
answer speech requests with it, and the caller decodes it exactly. It is a
test codec, not speech: a real speech-to-text model cannot read it, and a
result measured with tone-coded audio says nothing about how an agent copes
with real voices. It survives white noise at 3 dB SNR; a speed change
breaks it (by design: the `speed` probe then stands for a recogniser that
cannot follow).

### What the caller writes

Exports of the caller service, sealed with the run:

- `timeline.jsonl`: `call_start` (protocol, wall clock, settings, probe),
  every audio frame both ways (`frame`: direction, sequence, play time,
  arrival time, voiced), `caller_speech_start` and `_end` (turn, kind
  opening, reply, interrupt, reprompt or closing, the exact text, TTS
  engine, probe applied), `agent_speech_start` and `_end`,
  `agent_transcript` (the caller's transcription and its engine),
  `agent_clear`, `barge_in_start` and `barge_in` (when the agent's audio
  stopped and why), `overlap`, `reply_withheld`, `agent_reported` (the
  agent's own transcript messages), `protocol_error`, `hangup`, `call_end`
  (who ended it, length, the sha256 and size of both audio tracks). At most
  200,000 frame lines; more are counted, not written.
- `transcript.json`: the conversation in the text simulator's shape (agent
  lines are what the caller transcribed), so the support verifier reads it
  unchanged.
- `caller.wav` and `agent.wav`: both sides on the call clock, 16 kHz mono,
  capped at 240 s each (7.7 MB, under the 16 MiB export cap). With
  `BENCH_VOICE_KEEP_AUDIO=0` only their sha256 is kept, in `call_end`.

An agent that never dials still gets these exports (`no_call`), so it fails
on the untouched helpdesk state instead of breaking the grade.

## Metrics

Computed from the sealed timeline (its sha256 is checked against the state
export record first), with the task's bounds. All are unknown with a reason,
never zero, when the timeline is missing, tampered with, or the agent never
called.

| Metric | What | Evidence | Better |
|---|---|---|---|
| `time_to_first_audio_ms` | call connected to the agent's first voiced audio, as played | caller timeline (boundary) | lower |
| `turn_latency_p50_ms`, `turn_latency_p95_ms` | end of a caller line to the start of the agent's answer, as played; linear interpolation for the percentile | caller timeline | lower |
| `barge_in_handled` | share of the caller's interruptions after which the agent's played audio stopped within `barge_in_bound_ms`; not applicable without one | caller timeline | higher |
| `dead_air_s` | silence on the agent's floor (after connecting or after a caller line, before the agent spoke), summed over gaps longer than `dead_air_ms` | caller timeline | lower |
| `stt_wer` | word error rate of the agent's transcription of the caller's known lines (each line's text is what the caller said; number words are not normalized) | Bifrost's transcription rows when the agent transcribed through the gateway (`bifrost_log`), else the agent's reported transcripts (`agent_reported`, weaker); unknown with neither | lower |
| `voice_cost_per_minute` | the run's cost ledger divided by the call's minutes | gateway | lower |

A turn opens at the call's start (the greeting) and at the end of every
caller line except an interruption, the caller's last line, and a line the
agent was already talking over. The support metrics (`escalated`,
`escalation_correct`, `policy_violation`) apply as on the text track;
`user_turns` is unknown on a voice task, because the caller is not proxied
(its timeline has the turns).

`voice check -timeline <file>` prints what these metrics read from one
timeline; the run page's Voice call panel draws it (below).

## Probes

| Probe | Layer | What |
|---|---|---|
| `caller-noise` | service | white noise 10 dB below the caller's speech, seeded per turn |
| `caller-speed` | service | the caller's audio 1.2 times faster, pitch up |
| `caller-interrupts` | service | 700 ms into the agent's second utterance the caller talks over it ("Sorry, sorry, could you say that again?") |
| `caller-silent` | service | the caller says nothing for 9 s before its second reply |
| `stt-outage` | gateway | the fault proxy answers every `whisper-1` transcription with a 503 from the first on; chat and speech pass |

A Realtime outage is the same kind of plan with the Realtime model
(`model: gpt-realtime`): the session is refused at the handshake (no probe
file ships for it).

The caller probes set `BENCH_VOICE_PROBE` and its parameters on the caller
service. `stt-outage` is a model-filtered fault plan: the fault proxy counts
audio requests only for a plan that names their model, numbered among
themselves, so a plain `model-outage` never touches audio and the STT
outage never touches chat. It needs a fault proxy at this version or later
(the audio routes below); the live stack's proxy image is older, so run it
on a stack built from this repository.

## Agents

| Config | What |
|---|---|
| `agents/voice-cascaded.yaml` | `bench/voice/cascadedagent.py`: transcribes each caller utterance at `/v1/audio/transcriptions`, runs the conversation and the helpdesk's MCP tools at `/v1/chat/completions`, speaks at `/v1/audio/speech` (WAV), all on the run's key; stops and sends `clear` when the caller barges in; says it cannot hear and never acts when transcription fails, and hangs up after three failures |
| `agents/voice-cascaded-sarvam.yaml` | the same program with Sarvam AI speech: `openai/saaras:v4` transcribes, `openai/bulbul:v3` speaks (speaker `shubh`), chat on the arm's model (below, "Sarvam AI speech"); paid with a Sarvam key |
| `agents/voice-broken.yaml` | the same program with `--mode broken`: talks straight over the caller (one burst, no `clear`), 4.5 s of dead air before every answer, and halves every refund the model decides. For checking the benchmark itself |
| `agents/voice-realtime.yaml` | `bench/voice/realtimebridge.py`: the caller's audio to an OpenAI Realtime event endpoint and back (`docs/voice-protocol.md`), by default the gateway's `/v1/realtime`; barge-in from the server's voice activity detection; tools as Realtime function calls over MCP |

The cascaded configs declare their audio models:

```yaml
gateway:
  provider: openai
  models: [whisper-1, gpt-4o-mini-tts]   # the run key may also call these
```

`gateway.models` (optional, omitted when empty, so older configs hash as
before) adds the models to the key slot and to the run key's
`allowed_models`, after the arm's model; it is part of the config hash in
the manifest. `model_fidelity` treats audio rows for these models as
declared, and fires on an audio row served by another model. The planner
refuses an empty or repeated entry, the arm's own model, a model of another
provider, and the field together with `gateway.routing`.

## Through Bifrost: what was checked

Verified on a throwaway stack (compose project `hd-x8`: stock
`maximhq/bifrost:v2.5.1@sha256:5ec9873a…`, Postgres 18, the bench's fault
proxy built from this repository, the fake below), 18 Nov 2026:

| Route | Works against a custom base URL | Bifrost's log row |
|---|---|---|
| `POST /v1/audio/speech` | yes; the body is passed through, but the response `Content-Type` is rewritten to `audio/mpeg` even when the upstream answers `audio/wav` (clients must look at the bytes) | object `speech`, `speech_input` (the text), `speech_output` (the audio, base64, in the detail row), cost from the datasheet by characters, latency, upstream latency, virtual key |
| `POST /v1/audio/transcriptions` | yes, multipart passed through | object `transcription`, `transcription_input` (the uploaded audio, base64), `transcription_output` and `content_summary` (the text), cost by audio seconds (`audio_cost`), latency, virtual key |
| `GET /v1/realtime?model=openai/gpt-realtime` (WebSocket, `x-bf-vk`) | yes, events relayed both ways | one row per Realtime turn, object `realtime.turn`, written at `response.done` (also on `error` events and on cancelled responses): tokens and cost from the event's usage, `raw_response` (the `response.done` event), the session id as `parent_request_id`, transport `websocket` in `metadata`; no audio and no input transcript |
| `POST /v1/realtime`, `POST /v1/realtime/calls` | present (WebRTC SDP offer; `calls` needs `?model=`) | not tested: WebRTC needs SDP and ICE, out of scope |
| `POST /v1/realtime/client_secrets` | forwarded to the upstream | not tested further |

**Realtime sessions raise Bifrost's dropped-log counter.** During every
Realtime session Bifrost v2.5.1 logged `failed to update realtime
transcript for log <id>: log not found` (it tries to attach the turn's
transcript before the turn's row is written), and `GET /api/logs/dropped`
rose by one per warning: 42 warnings, counter 42, over the session-level
tests and the Realtime experiment on this stack. The turn rows themselves
were written. Heimdall's `dropped_logs` instrument reads any rise as lost
evidence, so **an experiment with a Realtime agent on v2.5.1 is INVALID
(fix_harness)**, as the Realtime pipeline run below was. That is the
instrument doing its job: the gateway says it lost log writes, and the bench
cannot tell which.

So a cascaded agent is fully witnessed: every transcription, chat and
speech call is a row with its cost, and the transcription rows carry the
agent's transcript of the caller (which `stt_wer` reads). A Realtime agent
is witnessed per turn (cost, tokens, latency), but its transcripts come only
from the agent's own reports, so its `stt_wer` is labelled
`agent_reported` and its audio is witnessed only by the caller.

**Realtime through the bench's fault proxy.** The proxy forwarded plain
HTTP and dropped the WebSocket upgrade: checked on the same stack with the
provider's base URL set to the fault proxy, Bifrost accepted the client's
WebSocket and then sent it one `error` event, "failed to dial upstream
websocket ws://faultproxy:9100/v1/realtime?model=...: upstream handshake
failed with 426 Upgrade Required". The proxy now relays the session
(`faultproxy/realtime.go`): `GET /v1/realtime` is an audio route of the
openai provider; the proxy swaps the slot placeholder for the real key,
dials the upstream (TLS for https), passes the `101 Switching Protocols`
back, copies bytes both ways without parsing them, and writes one ledger
line per session (kind `audio`, status 101, the session's length). A
model-filtered plan refuses the session with its status (a Realtime
outage); there is no fault inside a running session. Checked the same way
after the change: the session opened, a response completed, the ledger
line says 101. The scripted model has no Realtime session and answers 501,
so a Realtime agent needs a real or fake upstream. The live stack's proxy
image predates this.

What the bench does with these rows: Bifrost's `speech`, `transcription`
and `realtime.turn` objects are a new row kind, `audio` (they were `other`,
which the cost ledger skipped, so audio cost was silently left out). The
cost ledger prices an audio row at **Bifrost's own row cost** (from the
datasheet the gateway pins, `deploy/bifrost/datasheet/pricing.json`), price
reference `bifrost-row-cost`, because the bench's token snapshot has no
per-second or per-character prices; a row without Bifrost's cost is
unknown, an injected audio fault costs 0. `tokens` adds the tokens of audio
rows that carry them (Realtime turns). Steps and tool calls still count
message rows only. Bifrost v2.5.1 writes a `realtime.turn` row with no cost
for a cancelled response (the agent cancelled it on barge-in, seen in the
pipeline runs below), so a Realtime run with a cancelled turn has its cost
and `voice_cost_per_minute` unknown, not zero.

## Sarvam AI speech

A cascaded agent can use Sarvam AI (docs.sarvam.ai) for speech to text and
text to speech while its chat turns stay on any model of the run's
provider: `agents/voice-cascaded-sarvam.yaml` in the example transcribes
with `saaras:v4` and speaks with `bulbul:v3` (speaker `shubh`). The agent
still calls `/v1/audio/transcriptions` and `/v1/audio/speech` on Bifrost
with its run key, model `openai/saaras:v4` and `openai/bulbul:v3`; Bifrost
logs every call; only the fault proxy holds the Sarvam key.

How: the openai provider entry of the fault proxy carries a `sarvam_audio`
block (`faultproxy/sarvam.go`, on by default in the built-in table). An
audio request whose model matches it (`saaras:*`, `saarika:*`, `bulbul:*`
by default) is translated, not forwarded:

| OpenAI shape (from Bifrost) | Sarvam API |
|---|---|
| `POST /v1/audio/transcriptions`, multipart `file`, `model`, `language` | `POST /speech-to-text`, multipart `file`, `model`, `language_code` (`en` becomes `en-IN`; none: the block's `language`, default `en-IN`); `transcript` comes back as `{"text": ...}` with `language` and the WAV's seconds as `usage` (`response_format: text` gets plain text) |
| `POST /v1/audio/speech`, JSON `model`, `input`, `voice`, `response_format`, `speed`, `language_code` | `POST /text-to-speech`, JSON `text`, `target_language_code`, `model`, `speaker` (an OpenAI voice name such as `alloy` is dropped for the block's `speaker`, else Sarvam's default), `pace`, `output_audio_codec` (wav, mp3, flac, aac; `pcm` is `linear16` and `opus` at 24 kHz); the first base64 entry of `audios` comes back as the audio bytes |
| the slot placeholder in `Authorization` | the real key in `api-subscription-key`, read once from `SARVAM_API_KEY` (the block's `key_env`) |

A Sarvam error keeps its status and message in the OpenAI error envelope,
so Bifrost records it as a provider error; a transport failure is a 502 with
`upstream_error` on the ledger line, as for any forwarded call. Everything
else is the provider's: the slot, the run's fault plan, the audio call
numbers, the ledger line (kind `audio`, upstream `real`). Without the key a
Sarvam model gets a 503 naming `SARVAM_API_KEY` when the provider is real,
and the scripted model's tone code when the provider is scripted (as every
audio model). Other audio models (`whisper-1`) are untouched.

Block fields (all optional): `base_url` (`https://api.sarvam.ai`),
`key_env` (`SARVAM_API_KEY`), `models` (globs on the model, provider prefix
removed), `language`, `speaker`. A provider table file that replaces the
openai entry must restate `audio:` and `sarvam_audio:` to keep them.

**Faults.** An STT outage on Sarvam is the same model-filtered plan with
`model: saaras:v4`: the proxy fails the transcription before calling
Sarvam, and speech and chat pass (`TestSarvamSTTOutage`).

**Cost.** Bifrost's datasheet has no price for Sarvam models under the
openai provider, so Bifrost writes their rows without a cost and the cost
ledger marks them unknown ("audio row without Bifrost's price"), never
zero; `voice_cost_per_minute` is then unknown too.

**Why not Bifrost's own `sarvam` provider.** Bifrost v2.5.1 has one (it
translates the same routes itself), but a run key serves one provider on
one key slot, and the slot is what binds the run's fault plan and ledger
lines. Speech on a second provider would need a second slot per run and
evidence joined across two slots; translating in the proxy keeps one slot,
one plan and one ledger per run.

**Checked against Sarvam**, 21 Nov 2026, from the server, with the exact
bodies the proxy sends: `text-to-speech` with `target_language_code: en-IN`,
`model: bulbul:v3`, `speaker: shubh` answered 200 with one base64 WAV
(22,050 Hz, mono, 16-bit, 3.6 s for a 48-character line, 1.9 s round trip);
`speech-to-text` with that WAV, `model: saaras:v4`, `language_code: en-IN`
answered 200, transcript "Hello, I would like a refund for order B3001."
(1.2 s). Sarvam's current docs name the speech language field
`language_code`; `target_language_code` (Bifrost's own provider sends it
too) is still accepted. Unit tests run against a fake Sarvam
(`faultproxy/sarvam_test.go`).

## $0 fakes

- **The fault proxy's scripted model** (a provider with no key, or a run
  bound to the scripted plan, as `agent check` binds every run; observed
  on this stack: `smoke: true` on an experiment does not, the agent's calls
  went to the provider's upstream) answers `POST /v1/audio/speech` with the
  input as tone code (WAV or PCM at
  24 kHz, like the real API; mp3 and the other formats are refused) and
  `POST /v1/audio/transcriptions` by decoding the tone code in the upload
  (empty text for anything else). Both routes are a new ledger kind, `audio`
  (`Audio` patterns of the openai provider), with their own call numbers;
  the multipart `model` field is read for model-filtered plans, from the body
  the proxy already holds (32 MiB cap), file parts skipped.
- **`bench/voice/fakeopenai.py`**, an OpenAI-compatible server for a test
  stack's provider base URL: speech (tone code, or Piper with
  `FAKE_TTS=piper`), transcription (tone code, a known-audio table by sha256,
  or the local Whisper with `FAKE_STT=whisper`), chat completions from
  conversation scripts (`voice/testdata/chat-script.json`: the script whose
  match text is in the conversation's first user message, the reply at the
  number of assistant messages since, so parallel calls never interleave),
  and a Realtime WebSocket with the minimal event subset (session, input
  buffer with tone-code VAD, responses with audio deltas, transcripts,
  function calls, cancel). It is not a model: it answers what a test
  scripted.

## Evidence

Every number here cost nothing; none is a finding about any agent.

- Unit tests: `GOWORK=off go test ./voice/ ./metrics/ ./faultproxy/
  ./gateways/bifrost/ ./evidence/ ./gates/ ./handlers/ ./scaffold/
  ./cmd/bifrost-bench/`: tone code (Go and Python give the same bytes),
  timeline analysis on recorded calls (`voice/testdata/call-*.jsonl`,
  recorded by `voice_test.py` from real caller sessions), every voice
  metric, voice plan checks, scripted audio and the STT-outage plan in the
  fault proxy, key minting with `gateway.models`, audio cost rows, the
  fidelity gate, the voice routes, the template. `TestVoicePython` runs
  `voice/voice_test.py` (WebSocket framing, a full call, the caller
  speaking first and reprompting, barge-in handled and ignored, the silent,
  noise and speed probes, the no-call exports, the fake's speech,
  transcription, chat scripts and Realtime events).
- Realtime end to end, 18 Nov 2026: `realtimebridge.py` against
  `ws://127.0.0.1:28500/v1/realtime?model=openai/gpt-realtime` (Bifrost
  v2.5.1, `x-bf-vk` virtual key, provider base URL the fake), a local caller
  (tone TTS) and the helpdesk on refund-in-policy: four function calls ran
  over MCP, the refund of 4,500 pence landed, the caller said its last line
  and hung up, the bridge exited 0; Bifrost logged 28 `realtime.turn` rows
  with status success and one cancelled. Timeline: `voice/testdata/call-realtime-fake.jsonl`.
- Through Bifrost and the relaying fault proxy, 18 Nov 2026:
  `TestEndToEndThroughGateway` (gated; `BENCH_VOICE_E2E_GATEWAY`,
  `BENCH_VOICE_E2E_KEY`, `BENCH_VOICE_E2E_AGENT`) on refund-in-policy with
  the provider's base URL the fault proxy and the proxy's upstream the
  fake: the cascaded agent passed (first audio 170 ms, one answered turn at
  1,277 ms, no dead air; Bifrost logged its speech, transcription and chat
  rows) and the Realtime bridge passed (first audio 60 ms, one turn at
  1,027 ms), each with the refund of 4,500 pence made and the caller hanging
  up after its last line. A hand-made virtual key was used for these two;
  the pipeline runs below mint their own.
- Admission and pipeline runs (all $0, details and per-run numbers in
  `examples/acme-voice-desk/README.md`): `suite admit` admitted 6 of 6
  tasks with all 54 controls as expected (three full admissions, the last
  on the final caller build); `voice-smoke` on the live stack, oracle 6 of
  6 and noop 0 of 6 (`e_voice_smoke_whw0`, regressed, hold); the cascaded
  reference agent against the broken one through the real pipeline on the
  throwaway stack, 16 runs over every voice probe and the STT outage
  (`e_voice_cascaded_fake_t8sk`: cascaded resolved 7 of 7 outside the
  outage with first audio 110 to 185 ms, barge-in handled 1, no dead air;
  broken 2 of 7 with first audio about 4.6 s, barge-in handled 0, about 9 s
  of dead air; both hung up without acting under the STT outage); the
  Realtime bridge against the cascaded agent (`e_voice_realtime_fake_sah4`,
  INVALID by `dropped_logs`, above); `agent check` of the cascaded agent on
  the scripted model (14 chat and 27 audio calls answered by the scripted
  model). Nothing was paid.

## What is measured, and what is not

Measured: whether the call ends with the right helpdesk state and policy
(the verdict), what the caller heard the agent say, how fast the agent
answers as heard on a real-time line, whether it stops when talked over,
dead air, how well its speech to text heard known lines (when its
transcripts are witnessed or reported), and what the call cost per minute.

Not measured, and not claimed:

- **Telephony.** No SIP, PSTN, jitter, packet loss or DTMF. The line is a
  clean 16 kHz WebSocket; a hosted agent on a Twilio Media Streams socket
  gets mu-law 8 kHz (`docs/voice-platforms.md`), but no Opus.
- **Audio quality.** No MOS, naturalness or prosody score, no speaker
  similarity. Barge-in is measured as "audio stopped", not as how gracefully.
- **Real callers.** The caller is a script (or a pinned model) speaking with
  one synthetic voice. No accents, no real background noise, no human
  timing. The noise and speed probes are synthetic.
- **The caller's own ear** is Whisper tiny.en: it mishears. A real agent's
  words reach the verifier through it (`agent_says`), so a check may fail on
  a mishearing; the tone code path, used by the controls, has no such
  errors.
- **Faults inside a Realtime session.** The proxy relays the session's
  bytes without parsing them: a session can be refused, not broken half way.
- Apart from the two direct Sarvam calls above, no real provider has been
  called for the numbers here: every speech, transcription, chat and
  Realtime answer came from the scripted model, the fake or the local
  engines.
