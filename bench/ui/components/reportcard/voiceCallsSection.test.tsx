import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { scoreReport } from "./reportCard.fixtures";
import { callerName, hasVoiceCalls, VoiceCallsTable } from "./voiceCallsSection";

const report: Report = {
	...scoreReport,
	voice_calls: [
		{ arm: "A", condition: "clean", runs: 4, successes: 3, success_rate: 0.75, time_to_first_audio_ms: { value: 812.4, runs: 4 }, turn_latency_p95_ms: { value: 1490, runs: 4 } },
		{ arm: "A", condition: "persona-hurried", runs: 4, successes: 1, success_rate: 0.25, barge_in_handled: { value: 0.5, runs: 2 }, audio_jitter_p95_ms: { value: 3.2, runs: 4 } },
	],
};

describe("voice calls section", () => {
	it("shows success and call quality per caller", () => {
		expect(hasVoiceCalls(report)).toBe(true);
		expect(hasVoiceCalls(scoreReport)).toBe(false);
		const html = renderToStaticMarkup(<VoiceCallsTable report={report} />);
		expect(html).toContain("Plain caller");
		expect(html).toContain("Persona hurried");
		expect(html).toContain("75% (3 of 4)");
		expect(html).toContain("812 ms");
		expect(html).toContain("1490 ms");
		expect(html).toContain("50%");
		expect(html).toContain("3 ms");
		expect(html).not.toMatch(/condition|probe|\barm\b/i);
	});

	it("words caller ids", () => {
		expect(callerName("clean")).toBe("Plain caller");
		expect(callerName("noisy-street")).toBe("Noisy street");
	});
});
