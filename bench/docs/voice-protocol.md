# Voice protocol: heimdall-voice/1

This is the protocol a voice agent speaks to Heimdall's voice caller
(`bench/voice/voicecaller.py`), the simulated customer of a voice task
(`docs/voice-agents.md`). It is deliberately small: one WebSocket, raw PCM
in binary messages, five JSON control messages. A team with a working voice
agent should be able to adapt it in an hour. Agents built on the OpenAI
Realtime event protocol can use the bench's bridge instead (last section).

## Connecting

The agent dials the caller. The caller is a service on the run's own
network:

```
ws://customer:8000/call          (or the URL in $BENCH_VOICE_URL when it is set)
```

`customer` is the task's caller service id (`voice.caller` in task.yaml,
default `customer`). The caller accepts one call per run. A second
connection gets `409 Conflict`; a connection after the call ended gets
`410 Gone`; a connection before the caller's speech engines have loaded
gets `503` (retry). The caller is reached directly, not through the tool
proxy: the proxy does not carry WebSocket audio. TLS is not used inside the
run network.

Right after the upgrade the caller sends:

```json
{"type": "start", "protocol": "heimdall-voice/1", "call_id": "<task id>",
 "format": {"encoding": "pcm_s16le", "sample_rate": 16000, "channels": 1, "frame_ms": 20}}
```

## Audio

Both directions carry the same format: 16-bit signed little-endian PCM,
mono, 16 kHz, in **binary** WebSocket messages.

- The caller sends 20 ms frames (640 bytes) at real-time pace while it
  speaks, and nothing while it is silent. There are no speech start or end
  hints: like a phone line, the agent must detect speech itself.
- The agent may send any whole number of samples per message, up to one
  second (32,000 bytes); longer messages are truncated and logged as a
  protocol error. 20 ms frames are recommended.
- The agent may send faster than real time (a whole TTS clip at once). The
  caller plays the agent's audio on a real-time clock, as a telephone would:
  audio that arrives early queues and plays in order. What the caller
  "hears" is the played timeline, and every latency is measured on it.
- Silence counts: the caller decides that an agent utterance ended when it
  has played 800 ms of silence or nothing after voiced audio
  (`BENCH_VOICE_END_SILENCE_MS`).

## Control messages (JSON text messages)

From the agent:

| Message | Meaning |
|---|---|
| `{"type": "clear"}` | Drop every queued audio frame that has not played yet. Send it when the caller starts talking over you (barge-in): playback stops at once, and `barge_in_handled` measures how soon that happened |
| `{"type": "transcript", "role": "caller", "text": "...", "turn": 3}` | Optional: what your speech to text heard. Kept as agent-reported evidence (labelled `agent_reported`); used for `stt_wer` only when the gateway has no transcription rows for the run. `role: "agent"` lines are logged too |
| `{"type": "mark", "name": "..."}`, `{"type": "event", ...}` | Optional markers, logged as agent-reported |
| `{"type": "hangup", "reason": "..."}` | The agent ends the call; the caller closes after the queued audio has played |

From the caller:

| Message | Meaning |
|---|---|
| `start` | Above |
| `{"type": "hangup", "reason": "caller_done"}` | The caller ended the call. Reasons: `caller_done` (the customer is done), `max_turns`, `max_call` (the call reached `BENCH_VOICE_MAX_CALL_S`, default 240 s), `agent_silent` (two reprompts went unanswered) |

Unknown message types are logged as protocol errors and ignored. Closing the
WebSocket without a hangup ends the call as `agent_disconnected`.

## End of call

The call ends when either side hangs up, when the socket closes, or at the
caller's limits. After the agent process exits, the bench exports the
caller's `transcript.json`, `timeline.jsonl`, `caller.wav` and `agent.wav`.
An agent that never dials still gets graded: the exports exist from the
caller's start, say `no_call`, and the helpdesk state is untouched.

## What the caller does

1. If the agent says nothing for 3 s after connecting
   (`BENCH_VOICE_FIRST_WAIT_MS`), the caller speaks first.
2. After each agent utterance it transcribes what it heard (tone code
   exactly; speech with Whisper tiny.en), waits 300 ms, and answers with the
   task's script (or a pinned model playing the persona).
3. If the agent leaves the caller waiting 7 s (`BENCH_VOICE_REPROMPT_MS`), it
   asks "Hello? Are you still there?", twice at most, then hangs up.
4. If the agent starts speaking again before the caller answered, the
   caller holds its answer and treats it as one longer agent turn.

## A minimal agent

With the helpers the caller serves at `http://customer:8000/kit/`
(`voiceclient.py`, `tonecode.py`, `wsproto.py`, standard library only):

```python
import voiceclient

call = voiceclient.VoiceCall()                     # dials $BENCH_VOICE_URL or ws://customer:8000/call
call.send_audio(my_tts("Hello, how can I help?"), realtime=True, barge_in=True)
while True:
    try:
        pcm = call.listen(end_silence_ms=700)        # the caller's next utterance, or None after 30 s of silence
    except voiceclient.Hangup:
        break
    text = my_stt(pcm)
    call.report("caller", text)                      # optional, agent-reported
    interrupted = call.send_audio(my_tts(answer(text)), realtime=True, barge_in=True)
```

`send_audio(..., barge_in=True)` stops sending and sends `clear` as soon as
the caller has spoken for 200 ms while the agent talks. Without the helpers,
any WebSocket library works: the whole protocol is above.

## Agents that speak the OpenAI Realtime event protocol

`bench/voice/realtimebridge.py` (standard library only) connects the caller
to any WebSocket endpoint that serves the OpenAI Realtime event protocol: by
default the gateway's `/v1/realtime?model=$VOICE_REALTIME_MODEL` on the
run's key, or `$VOICE_REALTIME_URL` for a team's own Realtime-compatible
server. It translates:

| Caller side | Realtime side |
|---|---|
| caller audio frames, 16 kHz | `input_audio_buffer.append`, 24 kHz base64, every 100 ms; once the caller has sent nothing for 300 ms the line is filled with silence, because server-side voice activity detection needs a continuous stream to see the end of an utterance |
| audio frames to the caller, 16 kHz | `response.output_audio.delta` (and the older `response.audio.delta`), 24 kHz |
| `clear` to the caller | `input_audio_buffer.speech_started` while the agent speaks (server VAD), with `response.cancel` |
| `transcript` (agent-reported) | `conversation.item.input_audio_transcription.completed` |
| helpdesk tool over MCP | `response.function_call_arguments.done`, answered with `conversation.item.create` (`function_call_output`) and `response.create` |

It opens the session with `session.update` (instructions, the helpdesk's
tools, PCM 24 kHz both ways, `server_vad`, input transcription) and asks for
the greeting with `response.create`. Run it as the agent's command
(`examples/acme-voice-desk/agents/voice-realtime.yaml`).

## Versioning

The protocol id is in the `start` message. Any incompatible change gets a new
id; `voice check -suite` reports a caller image that speaks another one.
