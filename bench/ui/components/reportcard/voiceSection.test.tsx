import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport } from "./reportCard.fixtures";
import { visibleReport } from "./reportCard.utils";
import { msText } from "./voiceSection";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

describe("voice section", () => {
	it("is hidden without voice calls", () => {
		const html = renderToStaticMarkup(<ReportCard report={scoreReport} />);
		expect(html).not.toContain("report-voice");
	});

	it("shows the measured call quality and only the measured columns", () => {
		const report = {
			...scoreReport,
			voice: [
				{
					arm: "A",
					time_to_first_audio_ms: { value: 640.4, runs: 8 },
					turn_latency_p50_ms: { value: 910, runs: 8 },
					turn_latency_p95_ms: { value: 12500, runs: 8 },
					barge_in_handled: { value: 0.75, runs: 4 },
					talk_overs: { value: 0.5, runs: 8 },
					dead_air_s: { value: 1.25, runs: 8 },
				},
			],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain('data-testid="report-voice"');
		expect(html).toContain("Measured by a simulated caller on the audio");
		expect(html).toContain("640 ms");
		expect(html).toContain("12.5 s");
		expect(html).toContain("75%");
		expect(html).toContain("Talk-overs per call");
		expect(html).toContain("1.3 s");
		expect(html).not.toContain("Speech recognition errors");
		expect(html).not.toContain("Cost per minute");
		expect(html).toContain("Your agent");
		expect(html).not.toMatch(/—|–/);
	});

	it("keeps the shown arm's row on a one-agent card", () => {
		const two = [
			{ arm: "A", talk_overs: { value: 1, runs: 2 } },
			{ arm: "B", talk_overs: { value: 0, runs: 2 } },
		];
		expect(visibleReport({ ...scoreReport, mode: "score", voice: two }).voice).toEqual([two[0]]);
		expect(visibleReport({ ...abReport, voice: two }).voice).toEqual(two);
		expect(msText(undefined)).toBe("-");
	});
});
