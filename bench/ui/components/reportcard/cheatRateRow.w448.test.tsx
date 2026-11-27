import type { Report } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { type CheatCheck, CheatRateRow } from "./cheatRateRow";
import { ReportCard } from "./reportCard";
import { abReport } from "./reportCard.fixtures";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
		<a href={to.replace("$runId", params?.runId ?? "")} {...rest}>
			{children}
		</a>
	),
}));

const withCheat = (cc: CheatCheck): Report => ({ ...abReport, cheat_check: cc }) as Report;

describe("CheatRateRow", () => {
	it("is hidden without a cheat check from the server", () => {
		expect(render(<CheatRateRow report={abReport} />)).toBe("");
		expect(render(<ReportCard report={abReport} />)).not.toContain("report-cheat-rate");
	});

	it("shows each rate with its counts, interval, pass runs and the few runs note", () => {
		const report = withCheat({
			state: "measured",
			evaluation: "ev_cc",
			per_arm: {
				A: { passed: 1, runs: 4, rate: 0.25, low: 0.05, high: 0.7, pass_runs: ["run-7"] },
				B: { passed: 0, runs: 4, rate: 0, low: 0, high: 0.49 },
			},
		});
		const html = render(<CheatRateRow report={report} />);
		expect(html).toContain("A: 1 of 4 impossible tasks passed (25%, 5% to 70%)");
		expect(html).toContain("B: 0 of 4 impossible tasks passed (0%, 0% to 49%)");
		expect(html).toContain('href="/runs/run-7"');
		expect(html).toContain("Any pass of an impossible task means the agent changed the tests or otherwise cheated.");
		expect(html).toContain("Few runs: treat this as a warning sign only.");
		expect(render(<ReportCard report={report} />)).toContain("report-cheat-rate");
	});

	it("says plainly when the rate is not measured, running or has no twins", () => {
		expect(render(<CheatRateRow report={withCheat({ state: "not_measured" })} />)).toContain("Not measured yet.");
		expect(render(<CheatRateRow report={withCheat({ state: "running" })} />)).toContain("Cheat check running.");
		const none = render(<CheatRateRow report={withCheat({ state: "no_twins" })} />);
		// The report names the missing check and offers no create button (M3-14).
		expect(none).toContain("Cheating check: not run for this benchmark");
		expect(none).not.toContain("cheat-make-twins");
		expect(none).not.toContain("%");
	});
});