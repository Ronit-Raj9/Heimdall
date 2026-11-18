# Acme voice desk example: a voice agent on a support call

This folder is a complete Heimdall (bifrost-bench) project for a company
that wants to test its voice agent on phone support: the six conversations
of `examples/acme-helpdesk`, spoken. It follows the voice suite template
(`bifrost-bench suite init -template voice`); the user guide is
[`docs/voice-agents.md`](../../docs/voice-agents.md) and the protocol the
agent speaks is [`docs/voice-protocol.md`](../../docs/voice-protocol.md).
Acme Brew Club, its customers, orders and policy are fictional (see
`examples/acme-helpdesk/README.md`); everything here is MIT licensed.

- **A call, not a chat.** Each task starts the helpdesk MCP server and the
  voice caller. The agent dials `ws://customer:8000/call`, hears the
  customer's speech (Piper, voice en_US-ljspeech-medium), answers in audio,
  and works the helpdesk through the per-run tool proxy.
- **Graded on state.** The hidden verifier reads the exported helpdesk
  database and the call's transcript, as on the text track: `db`, `policy`
  and `communicate` (the agent's words as the caller transcribed them).
- **Measured on the line.** The caller's timeline gives time to first audio,
  turn latency, barge-in, dead air, the agent's transcription error rate
  and the cost per minute.
- **Controls per task.** A reference that speaks in tone code (the bench's
  test codec, read exactly by the caller), an empty control, an exploit and
  two mutants.

## Folder map

```
acme-voice-desk/
  deploy/bench.yaml                   paths, gateway, networks, helper image; server on 127.0.0.1:28502
  agents/oracle.yaml, noop.yaml       scripted controls for the free smoke run
  agents/voice-cascaded.yaml          the reference cascaded agent (STT, chat, TTS through the gateway)
  agents/voice-broken.yaml            the deliberately broken one (talks over, dead air, half refunds)
  agents/voice-realtime.yaml          the Realtime bridge (speech to speech through the gateway's /v1/realtime)
  agents/images/voice-agent/          their image: copies of bench/voice/*.py on the bench helper image
  experiments/voice-smoke.yaml        oracle against noop, $0
  experiments/voice-cascaded-fake.yaml  cascaded against broken through the pipeline, on a fake provider only
  benchmarks/acme-voice-desk/
    suite.yaml                        track voice, output state, simulator scripted
    services/helpdesk/                one image for the helpdesk and the caller: server.py, simuser.py
                                      and prompts, voicecaller.py, tonecode.py, wsproto.py, voiceclient.py
                                      (copies of bench/simuser and bench/voice), Piper and Whisper tiny.en
    probes/                           caller-noise, caller-speed, caller-interrupts, caller-silent, stt-outage
    tasks/<task>/                     task.yaml (voice: block, the two services), user.json, seed, verifier, controls
```

## Run it

```sh
cd examples/acme-voice-desk
docker build -t heimdall-example/acme-voice-desk:1 benchmarks/acme-voice-desk/services/helpdesk   # 1.1 GB
docker build -t heimdall-example/voice-agent:1 agents/images/voice-agent
bifrost-bench voice check -config deploy/bench.yaml -suite acme-voice-desk
bifrost-bench suite admit -config deploy/bench.yaml acme-voice-desk
BENCH_SERVER_MODE=synthetic bifrost-bench run -config deploy/bench.yaml experiments/voice-smoke.yaml
```

`voice-cascaded-fake.yaml` needs a stack whose openai provider is the fake
(`docs/voice-agents.md`, "$0 fakes"); on a stack with a real OpenAI key its
calls are paid.

## Results

All at $0 on 18 Nov 2026; they prove the plumbing, not any agent's quality.

- **Admission** (`suite admit`, Docker, the bench's scripted controls): 6
  of 6 tasks admitted, every one of the 54 controls as expected (per task:
  the reference passed 3 of 3; the empty control, two mutants, the exploit,
  the null noop and the null refuse failed), 25 min. Two earlier full
  admissions of the previous caller build gave the same 54 results. `suite
  lock` on a copy of the admitted suite reports no drift (6 admitted); lock
  sha256 `0433e304ff5c`.
- **`voice-smoke`** on the live stack (scripted controls, anthropic slots
  only; no model was called): `e_voice_smoke_whw0`, oracle resolved 6 of 6,
  noop 0 of 6 (it never dials, the caller's exports say `no_call`); verdict
  regressed, hold; $0.
- **`voice-cascaded-fake`** through the real pipeline on the throwaway stack
  (stock Bifrost v2.5.1, the bench's fault proxy, the fake as the openai
  upstream; run keys minted by the bench with `gateway.models`):
  `e_voice_cascaded_fake_t8sk`, 16 sealed runs, verdict inconclusive (3
  tasks), nothing paid ($0.034 is what these calls would cost at pinned
  prices). One run per cell; per-run numbers from the caller's timeline and
  Bifrost's rows:

  | Condition | Arm | Resolved | First audio (ms) | Turn latency p50 (ms) | Dead air (s) | Barge-in handled | STT WER (Bifrost rows) |
  |---|---|---|---|---|---|---|---|
  | clean, 3 tasks | cascaded | 3 of 3 | 117 to 143 | 1,306 to 1,741 | 0 | n/a | 0.13 to 0.21 |
  | clean, 3 tasks | broken | 2 of 3 (refund-in-policy: half refund) | 4,622 to 4,681 | 5,886 to 6,096 | 8.9 to 9.1 | n/a | 0.13 to 0.28 |
  | caller-interrupts | cascaded | 1 of 1 | 185 | 1,917 | 0 | 1 | 0.17 |
  | caller-interrupts | broken | 0 of 1 | 4,643 | 5,822 | 8.8 | 0 | 0.22 |
  | caller-noise | cascaded / broken | 1 of 1 / 0 of 1 | 110 / 4,612 | 1,608 / 6,132 | 0 / 9.3 | n/a | 0.39 / 0.41 |
  | caller-speed | cascaded / broken | 1 of 1 / 0 of 1 | 111 / 4,615 | 1,385 / 5,798 | 0 / 8.9 | n/a | 0.25 / 0.28 |
  | caller-silent | cascaded / broken | 1 of 1 / 0 of 1 | 111 / 4,626 | 1,291 / 2,316 | 0 / 7.8 | n/a | 0.21 / 0.21 |
  | stt-outage | cascaded / broken | 0 of 1 / 0 of 1 (every whisper-1 call 503; the agent says it cannot hear and hangs up, nothing refunded) | 123 / 4,619 | 461 / 4,960 | 0 / 17.9 | n/a | unknown (no transcript) |

  The caller spoke Piper speech; the fake transcribed it with Whisper
  tiny.en, so the WER is Whisper's on Piper's voice (noise raised it from
  0.21 to 0.39 on the same task). The fake's chat completions follow
  `voice/testdata/chat-script.json`: the "model" is a script, so
  resolution measures the plumbing and the broken agent's deliberate
  faults, not a model.
- **`agent check agents/voice-cascaded.yaml`** on the same stack (every call
  bound to the fault proxy's scripted model), run `r_pa15tifnz3`: result
  WARN (11 PASS; the WARNs are the optional agent-reported steps and a
  workspace observer that could not start on this host); 14 chat calls and
  27 audio calls answered by the scripted model (ledger upstream
  `scripted`), the
  caller decoded the scripted model's tone-coded speech exactly ("Scripted
  reply from the bench fault proxy."), nothing left the proxy.
- **`voice-realtime-fake`** on the same stack (the Realtime bridge through
  Bifrost's `/v1/realtime` and the fault proxy's WebSocket relay to the
  fake): `e_voice_realtime_fake_sah4`, 6 sealed runs. Cascaded 3 of 3,
  Realtime 2 of 3 (first audio 32 to 35 ms; the fake's 500 ms voice
  activity detection cut the caller's long Piper lines, so the Realtime arm
  answered while the caller was still talking: no answered turn by the
  metric's rule on the two resolved calls, and the third looped to
  `max_turns`; its `stt_wer` 0.39 to 0.44 is agent-reported). Verdict
  **invalid, fix_harness**: Bifrost's dropped-log counter rose 59 to 79
  during the experiment (`docs/voice-agents.md`, "Realtime sessions raise
  Bifrost's dropped-log counter"). The instrument is right to refuse it.
