import "@/lib/i18n";

import { runVoicePath } from "@/lib/store/apis/voiceApi";
import type { VoiceAnalysis, VoiceCall } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { VoicePanel, formatMs, pct, voiceBars } from "./runVoicePanel";

// The analysis of bench/voice/testdata/call-barge-ignored.jsonl, abridged.
const analysis: VoiceAnalysis = {
	protocol: "heimdall-voice/1",
	wall_start_ms: 1794968301000,
	call_ms: 23302.8,
	ended_by: "caller_done",
	first_audio_ms: 75.4,
	turns: [
		{ kind: "greeting", floor_ms: 0, agent_start_ms: 75.4, gap_ms: 75.4, dead_air: false },
		{ kind: "reply", caller_turn: 1, floor_ms: 5154.5, agent_start_ms: 5478.5, gap_ms: 324, latency_ms: 324, dead_air: false },
		{ kind: "reprompt", caller_turn: 3, floor_ms: 19676.9, gap_ms: 3623.1, dead_air: true },
	],
	barge_ins: [{ utt: 2, caller_start_ms: 5983.7, agent_stop_ms: 14618.5, stopped_after_ms: 8634.8, by: "audio_ended", handled: false }],
	dead_air_ms: 3623.1,
	lines: [
		{ turn: 1, kind: "reply", text: "Hi, my grinder arrived cracked.", start_ms: 2013.1, end_ms: 5154.5, tts: "tone_code" },
		{ turn: 2, kind: "interrupt", text: "Sorry, could you say that again?", start_ms: 5984.8, end_ms: 7811.5, tts: "tone_code" },
		{ turn: 4, kind: "reply", text: "Great, thank you!", start_ms: 22155.3, end_ms: 23101.6, tts: "tone_code", final: true },
	],
	utterances: [
		{ utt: 1, start_ms: 75.4, end_ms: 1154, text: "Hello, how can I help?", stt: "tone_code" },
		{ utt: 2, start_ms: 5478.5, end_ms: 14618.5, text: "<b>I have looked</b>", stt: "tone_code", interrupted: true },
		{ utt: 3, start_ms: 20052.1, end_ms: 21496.1, text: null, stt: "none" },
	],
	caller_frames: 380,
	agent_frames: 582,
	clears: 0,
	protocol_errors: 0,
	barge_in_bound_ms: 1000,
	dead_air_bound_ms: 2000,
	complete: true,
};

const call: VoiceCall = {
	run_id: "r_v1",
	caller: "customer",
	called: true,
	analysis,
	audio: [{ track: "agent", sha256: "c3d7", bytes: 689836, url: "/api/bench/runs/r_v1/voice/audio/agent" }],
	witness: "voice caller timeline (bench-owned service; boundary evidence)",
	bounds_source: "task",
};

describe("voice panel", () => {
	it("draws both sides, the latency bars, the barge-in and the audio", () => {
		const html = renderToStaticMarkup(<VoicePanel data={call} />);
		expect(html).toContain('data-testid="voice-panel-r_v1"');
		expect(html.match(/data-testid="voice-bar-caller"/g)?.length).toBe(3);
		expect(html.match(/data-testid="voice-bar-agent"/g)?.length).toBe(3);
		expect(html.match(/data-testid="voice-turn-row"/g)?.length).toBe(3);
		expect(html).toContain('data-testid="voice-dead-air"');
		expect(html).toContain("kept talking");
		expect(html).toContain('src="/api/bench/runs/r_v1/voice/audio/agent"');
		expect(html).toContain("no answer");
		expect(html).toContain("(not transcribed)");
		// Transcripts are text, never markup.
		expect(html).not.toContain("<b>I have looked</b>");
		expect(html).toContain("&lt;b&gt;I have looked&lt;/b&gt;");
	});

	it("warns when the call was classified with the default bounds (6-C-11)", () => {
		expect(renderToStaticMarkup(<VoicePanel data={call} />)).not.toContain("voice-default-bounds");
		expect(renderToStaticMarkup(<VoicePanel data={{ ...call, bounds_source: "default" }} />)).toContain('data-testid="voice-default-bounds"');
	});

	it("says why there is no call", () => {
		const html = renderToStaticMarkup(
			<VoicePanel data={{ run_id: "r_n", caller: "customer", called: false, reason: "the agent never called the voice caller", audio: [], witness: "w", bounds_source: "default" }} />,
		);
		expect(html).toContain('data-testid="voice-no-call"');
		expect(html).toContain("the agent never called the voice caller");
		expect(html).not.toContain("voice-no-audio");
	});

	it("orders bars and formats times", () => {
		const bars = voiceBars(analysis);
		expect(bars.map((b) => b.who)).toEqual(["agent", "caller", "agent", "caller", "agent", "caller"]);
		expect(bars[2].tone).toBe("warn");
		expect(bars[4].tone).toBe("bad");
		expect(pct(50, 200)).toBe("25.00%");
		expect(pct(500, 200)).toBe("100.00%");
		expect(pct(5, 0)).toBe("0%");
		expect(formatMs(340.4)).toBe("340 ms");
		expect(formatMs(1250)).toBe("1.3 s");
		expect(formatMs(undefined)).toBe("-");
		expect(runVoicePath("r/1")).toBe("/runs/r%2F1/voice");
	});
});
