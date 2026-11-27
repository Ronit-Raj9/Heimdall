# Real-time voice calls: LiveKit, Pipecat, SIP and caller personas

Heimdall's voice track (docs/voice-agents.md) calls an agent over a clean
16 kHz WebSocket. Production voice agents run over WebRTC or a phone line.
The real-time harness adds both:

- **A LiveKit room.** The agent is a LiveKit Agents worker, a Pipecat bot with
  its LiveKit transport, or any bot that joins LiveKit rooms. It can run on
  LiveKit Cloud, on your own LiveKit server, or on Heimdall's.
- **A phone number.** Heimdall dials it through LiveKit SIP and your SIP trunk.
  This works for any agent with a number: Vapi, Retell, ElevenLabs, or a
  contact centre.

The simulated caller is the same one the WebSocket track uses
(`voice/voicecaller.py`). It now plays **personas**: accent, pace, reaction
time, interruptions, background noise, a narrowband phone line and
hesitations. Every metric still comes from the caller's own timeline of what
it heard and when, so latency, barge-in, talk-over and WER are measured the
same way whatever carries the call.

Vapi assistants can also be called over Vapi's WebSocket transport, and
Twilio-style call sockets reached directly. Both use the `voice_platform`
agents in docs/voice-platforms.md.

## How a call runs

```
run network                         | outside (compose profile voice)
                                    |
voice caller  <-- heimdall-voice/1 --> rtcbridge.py == egress forwarder ==> voice-rtc gateway
(the bench's   (16 kHz pcm, JSON)   |   (the run's agent    heimdall-rtc/1     (rtcgateway.py)
 witness)                           |    container)                              |  WebRTC
                                    |                                     LiveKit room  <-- your agent
                                    |                                        |  (worker / bot)
                                    |                                     LiveKit SIP --> your trunk --> phone number
```

1. The `voice_rtc` adapter plans a run container that runs `rtcbridge.py`.
   This is bench code: your agent never runs inside Heimdall. The container
   stays on the run's internal network. Its only route out is one egress
   forwarder to the gateway (`HEIMDALL_VOICE_RTC_URL`).
2. The bridge dials the gateway with a per-run token and sends the dial
   message. The token is an HMAC of the run id and an expiry, signed with
   `HEIMDALL_VOICE_RTC_SECRET`, and the gateway takes each run id once at a
   time. Your LiveKit keys and trunk credentials are names of Settings >
   Secrets entries. The engine expands them into the bridge's environment,
   and `plan.json` redacts them.
3. The gateway creates a fresh room, `<room_prefix>-<run>-<random>`, joins it
   as `heimdall-caller` and brings your agent in:
   - `dispatch: agent`: LiveKit explicit dispatch to your worker's
     `agent_name`.
   - `dispatch: webhook`: a POST of `{"livekit_url", "room", "token",
     "session"}` to your `start_url`, with your bearer token. This is the
     usual way to start a Pipecat bot.
   - `dispatch: auto`: your worker joins every new room by itself.
   - mode `sip`: `CreateSIPParticipant` dials the number, waits until it
     answers, and the phone leg joins the room.
4. Once the agent publishes audio, the gateway sends `ready`, with its setup
   time. From then on it carries audio both ways at 16 kHz. It paces the
   caller's audio in 20 ms frames, with silence between lines as a phone line
   would, so your agent's own voice activity detection sees the caller stop.
   Your agent's `lk.transcription` streams come back as agent-reported lines.
   Those are its own speech and what it heard; what it heard feeds `stt_wer`,
   labelled agent_reported.
5. The call ends when the caller hangs up, your agent leaves, or `max_call_s`
   runs out. The gateway then deletes the room.

A failed setup is an infrastructure failure, with the reason in the run log,
not a fail of your agent. Bridge exit 3 covers: the gateway is not configured
or cannot be reached, the agent never joined, the number did not answer, or
the number is not on the allowlist.

The path was run end to end against LiveKit server v1.13.10. A
webhook-started bot answered in tone code over WebRTC (Opus both ways) and
the caller decoded every word. Setup took 618 ms, and turn latency was about
850 ms, which includes the bot's own 600 ms end-of-speech wait. The first
nine seconds are kept as `voice/testdata/call-livekit-webrtc.jsonl`. The
phone path is covered by the gateway's checks and by unit tests. It has not
dialled a real number from this repository.

## Connect an agent

From Agents > New, pick one of:

- **LiveKit or Pipecat voice**: how it joins (agent name,
  start URL, or every room) and, for your own server, its URL and key
  secrets.
- **Call your voice agent's phone number**: the number, your trunk host,
  caller id and credential secrets.

Or write the block yourself:

```yaml
id: acme-support-voice
adapter: voice_rtc
image: bench/coding-core-base:1      # runs the bridge (python3 only)
model: hosted
budgets: {wall_s: 600, cpus: 0.5, mem_gb: 0.5}
voice_rtc:
  mode: livekit
  livekit:
    url: wss://acme.livekit.cloud      # empty: Heimdall's LiveKit server
    api_key_secret: LIVEKIT_API_KEY     # Settings > Secrets names
    api_secret_secret: LIVEKIT_API_SECRET
    dispatch: agent                     # agent | auto | webhook
    agent_name: support-agent           # WorkerOptions(agent_name=...)
  timeout_s: 30                         # call setup
```

```yaml
voice_rtc:
  mode: sip
  sip:
    number: "+14155550100"              # on the operator's allowlist
    trunk_address: acme.pstn.twilio.com # or trunk_id: ST_... with sip.livekit
    from_number: "+14155550199"
    trunk_user_secret: SIP_TRUNK_USER
    trunk_password_secret: SIP_TRUNK_PASSWORD
    transport: udp                      # udp | tcp | tls
```

A `voice_rtc` agent runs voice benchmarks only. A text task is refused with
the reason. `spec/agent.schema.json` describes every field.

### Pipecat

Run your bot with `LiveKitTransport` and start it from the webhook:

```python
@app.post("/start")
async def start(body: dict):
    transport = LiveKitTransport(url=body["livekit_url"], token=body["token"], room_name=body["room"], params=...)
    asyncio.create_task(run_bot(transport))
    return {}
```

A Pipecat bot behind a Twilio Media Streams websocket can be reached
directly instead, with no room: use the `voice_platform` websocket agent in
docs/voice-platforms.md.

## Caller personas

`BENCH_VOICE_PERSONA` on the caller service picks a persona from
`voice/personas.json`, or takes an inline JSON object with the same fields.
Set it in the task's caller service env for one persona per task. To compare
personas, set it per condition with `apply.service_env.<caller>`, the same
way as the voice probes. The report card then shows each one as a row of
"Voice calls by caller".

| id | Sounds like | Behaviour |
|---|---|---|
| `calm-us` | US English (LJ Speech) | baseline |
| `indian-english` | Indian English (VCTK p248) | |
| `scottish-english` | Scottish, Fife (p237) | |
| `irish-english` | Irish, Dublin (p245) | |
| `australian-english` | Australian, Sydney (p326) | |
| `south-african-english` | South African, Cape Town (p314) | |
| `southern-us` | Tennessee (p310) | slower, longer pauses |
| `hurried` | New York (p297) | fast, talks over half the long answers, hangs up after one reprompt |
| `elderly-slow` | US English | slow, 1.2 s reaction time, "um" half the time, patient |
| `noisy-street` | San Francisco (p294) | 8 dB SNR noise, phone line |
| `phone-line` | Canadian, Montreal (p302) | phone line, 20 dB noise |
| `interrupter` | New Jersey (p360) | talks over every long answer after 0.7 s |

Fields: `voice` and `speaker` (a Piper voice and its speaker id), `speed`
(speaking rate, through Piper's length scale), `reply_delay_ms`,
`interruption_rate` (chance per agent answer after the greeting, once the
agent has talked for `interrupt_after_ms`), `noise_snr_db`, `channel`
(`phone`: 300 to 3400 Hz, then G.711 mu-law at 8 kHz), `patience` (reprompts
before hanging up) and `filler_rate` (lines that start with "um,"). A task's
own `BENCH_VOICE_REPLY_DELAY_MS` or `BENCH_VOICE_MAX_REPROMPTS` wins over the
persona. Interruptions are seeded (`BENCH_VOICE_PERSONA_SEED`), so reruns
match. The plan refuses an unknown persona or a bad inline field, and the
caller refuses one again at start.

The persona's voice, accent and pace need Piper (`BENCH_VOICE_TTS=piper`).
In tone-code mode they are recorded as `voice_applied: not_applied` and the
tone code keeps its pace, so controls stay readable. Noise, the phone line,
interruptions and hesitations apply in both modes.

`deploy/voice/caller.Dockerfile` builds a caller image with every persona
voice. It includes Piper with `en_US-ljspeech-medium` (public domain) and
`en_GB-vctk-medium` (CC BY 4.0, 109 speakers, accents from VCTK's
speaker-info.txt), faster-whisper tiny.en and the Silero VAD model. The
`en_US-l2arctic` voice is not used: its data is CC BY-NC.

### Voice activity

The caller finds where the agent's speech starts and ends with an energy
detector. With `BENCH_VOICE_VAD=silero`, and onnxruntime plus the Silero v5
model in the image (`SILERO_VAD_MODEL`), it uses Silero's speech probability
on 512-sample windows, gated by energy. This is steadier on noisy phone audio.

## Metrics

All of these come from the caller's timeline, which is boundary evidence
(docs/voice-agents.md):

- `time_to_first_audio_ms`, `turn_latency_p50_ms`, `turn_latency_p95_ms`,
  `barge_in_handled`, `dead_air_s`, `talk_overs`, `stt_wer`,
  `voice_cost_per_minute`: as for every voice call. Over a real-time leg they
  include the network and codec delay a real caller would hear.
- `audio_jitter_p95_ms` (new): the 95th percentile of how far each agent audio
  frame's arrival strayed from the previous frame's arrival plus its length,
  inside utterances. Pauses of 200 ms or more count as speech.
- Task success is the benchmark's own verifier on the call transcript
  (`transcript.json`): what the caller heard, transcribed.

The report card's **Voice calls by caller** section shows, for each agent and
caller (the plain caller, each persona, each voice probe): calls, task
success, first audio, turn latency p95, how often it stopped when interrupted,
and audio jitter.

## Operator setup

1. In `deploy/hosted/.env`, set `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`
   (32 or more characters), `HEIMDALL_VOICE_RTC_SECRET` (`openssl rand -hex 24`),
   `HEIMDALL_VOICE_RTC_URL` (same host: `ws://172.17.0.1:8790/dial`) and
   `LIVEKIT_PUBLIC_URL` (the wss URL teams' bots join).
2. Run `docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env --profile voice up -d`.
   This starts `livekit` (7880 signalling, 7881/tcp, 50000-50100/udp),
   `voice-redis`, `livekit-sip` (5060, RTP 10000-10100/udp) and `voice-rtc`.
3. Open the firewall for LiveKit's ports (and SIP's, for phone calls). Put TLS
   in front of 7880 for `LIVEKIT_PUBLIC_URL`, for example a Caddy site
   `voice.example.com { reverse_proxy livekit:7880 }` with Caddy on the
   `voice` network.
4. Phone calls: set `HEIMDALL_VOICE_SIP_ALLOW` to the E.164 prefixes of
   numbers your organizations own. With it empty, no phone call is placed.
   Calls cost money and reach real phones.
5. Check: `curl http://172.17.0.1:8790/healthz` shows `own_livekit` and
   `phone`.

The server and the runners read `HEIMDALL_VOICE_RTC_URL`,
`HEIMDALL_VOICE_RTC_SECRET` and `HEIMDALL_VOICE_SIP_ALLOW`. With the URL
empty, every `voice_rtc` run is refused with the reason.

## Full-Duplex-Bench

Full-Duplex-Bench (v1, v1.5, v2 and v3; github.com/DanielLin94144/Full-Duplex-Bench)
is licensed CC BY-NC 4.0 for its code and its own data (LICENSE, checked
22 Nov 2026). Its CANDOR subsets are under the CANDOR Corpus License and may
not be redistributed. A non-commercial licence does not allow it in a hosted
library, so it is not imported. The caller personas and probes cover the same
behaviours it tests (pauses, interruptions, turn-taking, background speech) on
openly licensed voices.

## Not covered

- Load tests (hundreds of concurrent callers): one call per run, and
  `HEIMDALL_VOICE_MAX_CALLS` per gateway.
- Network impairment (loss, jitter injection) on the WebRTC leg.
- DTMF and call transfer.
- Inbound calls (the agent calling Heimdall).
- Explicit dispatch has not been tested from this repository against a real
  LiveKit Agents worker. The webhook path has been tested against a real
  LiveKit server.
