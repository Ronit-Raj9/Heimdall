import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import type { CalibrationItem, CalibrationStatus } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { CALIBRATION_STATUS_FIXTURE } from "./calibration.fixtures";
import { CalibrateJudgeForm } from "./calibrateJudgeForm";
import { CalibrationCard, LabelForm, LabelsTable } from "./calibrationPanel";

// Fix wave 3, N286: no write-token box on the hosted server, the labeller is the signed-in member,
// and "Run the check" needs a label or the benchmark's own answers.

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/modelsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/modelsApi")>()),
	useGetModelsQuery: () =>
		okQuery({
			providers: [],
			keys: [
				{
					id: "k1",
					provider: "openai",
					label: "mine",
					last4: "abcd",
					status: "ok",
					models: [],
					budget_usd: 0,
					month_spent_usd: 0,
					over_budget: false,
				},
			],
		}),
}));
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>()),
	useCreateJobMutation: () => mutation(),
}));

const noop = () => {};

/** The opening tags of every element with the test id. */
function tags(html: string, testId: string): string[] {
	return html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`, "g")) ?? [];
}

const status = (human: number, seeds: number) =>
	({
		suite: "qa",
		judged: true,
		provider: "openai",
		model: "gpt-4.1-mini",
		state: "uncalibrated",
		detail: "",
		labels: human + seeds,
		human_labels: human,
		seed_items: seeds,
		disputed: 0,
		records: 0,
		reminders: [],
		problems: [],
	}) as unknown as CalibrationStatus;

describe("LabelForm on the hosted server", () => {
	it("has no write-token box and no labeller box, and says who labels", () => {
		const html = render(<LabelForm tasks={["q1"]} mode="live" onSubmit={noop} />, hostedSession("member"));
		expect(html).not.toContain('data-testid="calibration-token-input"');
		expect(html).not.toContain('data-testid="calibration-label-labeller"');
		expect(html).toContain("Labelled as Ada Lovelace");
	});

	it("keeps the token and labeller boxes in local mode", () => {
		const html = render(<LabelForm tasks={["q1"]} mode="live" onSubmit={noop} />);
		expect(html).toContain('data-testid="calibration-token-input"');
		expect(html).toContain('data-testid="calibration-label-labeller"');
	});
});

describe("LabelsTable", () => {
	it("prints the labeller's display name", () => {
		const item = {
			id: "i1",
			task: "q1",
			answer: "Max",
			label: "correct",
			labeller: "usr_2",
			labeller_name: "Bo",
			source: "label",
			at: "2026-11-20T00:00:00Z",
		};
		const html = render(<LabelsTable items={[item as CalibrationItem]} />);
		expect(html).toContain(">Bo<");
		expect(html).not.toContain("usr_2");
	});
});

describe("CalibrateJudgeForm with no labels", () => {
	it("disables Estimate and Run until a label or the benchmark's own answers are there", () => {
		const html = render(<CalibrateJudgeForm status={status(0, 0)} />, hostedSession("member"));
		const buttons = tags(html, "job-run-calibrate.run");
		expect(buttons).toHaveLength(2);
		for (const b of buttons) expect(b).toContain('disabled=""');
		expect(html).toContain("Add a label, or tick");
	});

	it("enables them with a label and shows All instead of 0 in the sample box", () => {
		const html = render(<CalibrateJudgeForm status={status(3, 0)} />, hostedSession("member"));
		for (const b of tags(html, "job-run-calibrate.run")) expect(b).not.toContain('disabled=""');
		const sample = tags(html, "calibrate-judge-sample")[0];
		expect(sample).toContain('placeholder="All 3"');
		expect(sample).toContain('value=""');
	});
});

describe("CalibrationCard", () => {
	it("points at Run the check, not the command line", () => {
		const html = render(<CalibrationCard status={CALIBRATION_STATUS_FIXTURE} />);
		expect(html).toContain("Shows the cost first.");
		expect(html).not.toContain("heimdall judge calibrate");
	});
});