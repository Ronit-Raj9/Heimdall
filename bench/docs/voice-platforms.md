# Voice agents on Vapi, Retell, ElevenLabs or your own WebSocket

Most voice teams do not run their agent in a container Heimdall builds: the
agent lives on a voice platform (Vapi, Retell, ElevenLabs Agents) or on the
team's own voice server (Pipecat, LiveKit, a custom stack) that answers phone
calls over a WebSocket. This guide connects such an agent and says what each
connection can and cannot be benchmarked on.

Agents that run inside Heimdall's run container and dial the caller
themselves are covered in [`voice-agents.md`](voice-agents.md); the wire
protocol is in [`voice-protocol.md`](voice-protocol.md).

## Connect

Agents > New agent, then one of:

| Form | Platform | Text benchmarks | Voice benchmarks |
|---|---|---|---|
| Connect your Vapi assistant | Vapi | yes, through Vapi's chat API (`POST /chat`) | yes, through Vapi's WebSocket transport |
| Connect your Retell agent | Retell | yes, through Retell's chat API (`/create-chat`, `/create-chat-completion`) | no (Retell calls use its own WebRTC and phone stacks) |
| Connect your ElevenLabs agent | ElevenLabs Agents | yes, conversation WebSocket with text only | yes, conversation WebSocket with audio |
| Connect your voice WebSocket | your own server | no (a call socket has no text mode) | yes, in the audio format you pick |

The API key is never typed into the form. Add it first in Settings > Secrets
(for example `VAPI_PRIVATE_KEY`), then give the form the secret's name.
Heimdall reads it only when a run or a test needs it, and it never appears in
the agent's config, plans or logs.

| Field | Vapi | Retell | ElevenLabs | Voice WebSocket |
|---|---|---|---|---|
| Agent ID | assistant id (required) | chat agent id (required) | agent id (required) | not used |
| API key secret | private key (required) | API key (required) | API key (only for a private agent) | bearer token (optional) |
| URL | API base, default `https://api.vapi.ai` | default `https://api.retellai.com` | default `https://api.elevenlabs.io` | `wss://` endpoint (required) |
| Audio format | Vapi's own | n/a | ElevenLabs' own | `twilio`, `vapi` or `elevenlabs` |
| Timeout | one reply, or the call to connect (default 60 s) | same | same | same |

The form writes an agent config like this (also usable with
`bifrost-bench agent add`):

```yaml
name: support-line
adapter: voice_platform
voice_platform:
  platform: vapi            # vapi | retell | elevenlabs | websocket
  agent_id: 8c1e2c4a-...
  api_key_secret: VAPI_PRIVATE_KEY
  # url: https://api.vapi.ai
  # framing: twilio         # websocket only: twilio | vapi | elevenlabs
  # timeout_s: 60
```

A Retell agent that exists only as a voice agent needs a chat agent with the
same prompt and tools; give its id.

## Test my agent

Test sends one text turn through the platform's chat API, from the server,
exactly as a text benchmark does, and shows the agent's opening line and
reply. A support agent that sticks to its script will not answer `READY`:
the test still passes when a reply comes back. A voice WebSocket endpoint has
no text mode, so Test says so; run a voice benchmark to check it.

## Text benchmarks

Text conversations (support tasks with a simulated customer, tau-style
tasks) run through Heimdall's conversation driver: each customer turn is sent
to the platform's chat API on the run's own session, and the agent's reply
comes back as the next turn. The platform keeps its own conversation state
(a Vapi chat chain, a Retell chat, an ElevenLabs conversation), and Heimdall
ends the session when the run ends.

A 5xx, 429 or broken connection from the platform is reported as the
platform's failure (infrastructure), not as a wrong answer by the agent. A 4xx (wrong key, unknown agent) is reported with the
platform's reason.

Until a server enables the conversation driver for hosted voice agents, the
wizard marks text benchmarks as not available for these agents and leaves
them out, with that reason.

## Voice benchmarks

A voice task's run container holds Heimdall's bridge
(`bench/voice/platformbridge.py`, standard library only). It dials the task's
simulated caller over `heimdall-voice/1`, opens the agent's call socket, and
carries audio both ways:

- **Twilio Media Streams** (`framing: twilio`): the bridge plays Twilio. It
  sends `connected`, `start` (`audio/x-mulaw`, 8000 Hz, mono), a `media`
  event every 20 ms (silence while the caller is quiet, as a phone line
  carries) and `stop`. The agent answers with `media`, `clear` (the caller
  drops the audio still queued: barge-in) and `mark` (echoed back once the
  audio before it played). This is what your server already speaks if it
  answers Twilio `<Stream>`.
- **Vapi** (`platform: vapi`, or `framing: vapi` for your own server):
  binary 16-bit PCM at 16 kHz both ways, `{"type": "hangup"}` to end. For a
  Vapi assistant the bridge first creates the call (`POST /call` with
  transport `vapi.websocket`) and dials the returned socket URL.
- **ElevenLabs** (`platform: elevenlabs`, or `framing: elevenlabs`):
  `user_audio_chunk` up in the format the agent's
  `conversation_initiation_metadata` asks for, `audio` events down,
  `interruption` as barge-in, `ping` answered with `pong`.

The caller speaks with local text to speech, plays the agent's audio on a
real-time clock, transcribes it and writes the call timeline. Everything on
the report card is measured from that timeline, the same way as for an
in-container agent, so numbers are comparable across agents.

The run container may reach exactly one host and port: the platform's API
host (which also serves its call socket) or your WebSocket's. The key is
passed to the bridge through the environment from Settings > Secrets.

## The report card

Every evaluation with a voice call gets a **Voice** section, one row per
agent:

| Column | What |
|---|---|
| First audio | from the call connecting to the agent's first words |
| Reply time (median, slowest 5%) | from the caller finishing to the agent starting to answer |
| Stopped when interrupted | share of the caller's interruptions after which the agent went quiet within the task's bound |
| Talk-overs per call | times the agent started speaking while the caller was still talking (the caller's own interruptions do not count) |
| Dead air | long silences while the caller waited for the agent |
| Speech recognition errors | word error rate of the agent's transcript of what the caller said, when the agent reports one |
| Cost per minute | measured cost divided by the call length |

Each cell is the mean over the calls that measured it; hover for the count.
A column no call measured is left out. Latency includes the network between
the worker and the platform, which is part of what a real caller hears.

## What is not covered

- **Phone numbers and SIP.** Heimdall does not dial a phone number or a SIP
  URI. Connect the WebSocket your telephony provider streams to instead.
- **Retell calls.** Only Retell's chat API is reached, so Retell agents run
  text benchmarks only.
- **The platform's own tools.** Tools the agent calls on its platform (Vapi
  tools, ElevenLabs server tools, Retell functions) run against your
  backends, not Heimdall's task services, so a task that checks helpdesk
  state can only see what the agent said.
- **Model traffic.** The platform calls its own models with its own keys.
  Heimdall does not see or price those calls; cost per minute is what the
  run measured, not your platform bill.
