import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport } from "./reportCard.fixtures";
import { judgeBadge, pairedChangeText } from "./reportCard.utils";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}));

const base = abReport.benchmarks[0];

describe("judge-graded benchmarks", () => {
	it("labels each calibration state", () => {
		expect(judgeBadge(base)).toBeUndefined();
		const j = (state: string, label: string, trusted: boolean) =>
			judgeBadge({ ...base, judge: { state, label, trusted, human_labels: 0, control_items: 24 } });
		expect(j("calibrated", "human-calibrated", true)).toMatchObject({ tone: "success", text: "Judge: human-calibrated" });
		expect(j("calibrated_on_controls", "calibrated on controls", true)).toMatchObject({ tone: "warning", text: "Judge: calibrated on controls" });
		expect(j("uncalibrated", "judge not calibrated", false)?.text).toBe("Judge: not calibrated");
		expect(j("uncalibrated", "judge not calibrated", false)?.title).toContain("not decisions");
		expect(j("unreliable", "judge unreliable", false)?.tone).toBe("destructive");
	});

	it("shows the paired interval and the smallest detectable change", () => {
		expect(pairedChangeText({ winner: "A", delta: -0.1, significant: false, summary: "", tasks: 8, boot_low: -0.3, boot_high: 0.1, mde: 0.35 })).toBe(
			"95% interval -30 pts to +10 pts; 8 paired tasks can detect about 35 points",
		);
		expect(pairedChangeText(undefined)).toBeUndefined();
	});

	it("renders the badge on the row", () => {
		const report = {
			...abReport,
			benchmarks: [{ ...base, suite_version: "1.2.0", judge: { state: "calibrated_on_controls", label: "calibrated on controls", trusted: true, human_labels: 0, control_items: 24 } }],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain(`data-testid="benchmark-judge-${base.benchmark}"`);
		expect(html).toContain("Judge: calibrated on controls");
		expect(html).toContain("Benchmark version 1.2.0.");
	});
});
