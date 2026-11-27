import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import type { CalibrationStatus } from "@/lib/types/bench";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CalibrateJudgeForm, CalibrateResultView } from "./calibrateJudgeForm";

const api = vi.hoisted(() => ({ models: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/modelsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/modelsApi")>()),
	useGetModelsQuery: () => api.models,
}));
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>()),
	useCreateJobMutation: () => mutation(),
}));

const status = {
	suite: "qa",
	judged: true,
	provider: "openai",
	model: "gpt-4.1-mini",
	state: "uncalibrated",
	detail: "",
	labels: 12,
	human_labels: 10,
	seed_items: 2,
	disputed: 0,
	records: 0,
	reminders: [],
	problems: [],
} as unknown as CalibrationStatus;

const key = { id: "k1", provider: "openai", label: "mine", last4: "abcd", status: "ok", models: [], budget_usd: 0, month_spent_usd: 0, over_budget: false };

beforeEach(() => {
	api.models = okQuery({ providers: [], keys: [key] });
});

describe("CalibrateJudgeForm", () => {
	it("shows the model picker, sample size, call estimate and run buttons", () => {
		const html = render(<CalibrateJudgeForm status={status} />, hostedSession("member"));
		expect(html).toContain('data-testid="calibrate-judge-sample"');
		expect(html).toContain("About 48 grader call(s)");
		expect(html).toContain("Hosted cap: 400 calls");
		expect(html).toContain('data-testid="job-run-calibrate.run"');
		expect(html).not.toContain('data-testid="calibrate-judge-nokey"');
	});

	it("links to add a model key when the org has none for the judge provider", () => {
		api.models = okQuery({ providers: [], keys: [] });
		const html = render(<CalibrateJudgeForm status={status} />, hostedSession("member"));
		expect(html).toContain('data-testid="key-missing-note"');
		expect(html).toContain("Add a key for openai");
		expect(html).not.toContain('data-testid="job-run-calibrate.run"');
	});

	it("shows agreement and kappa of a finished run", () => {
		const html = render(
			<CalibrateResultView
				result={{
					suite: "qa",
					estimate: false,
					provider: "openai",
					model: "m",
					items: 12,
					calls: 48,
					spent_usd: 0.0123,
					record_id: "cal_1",
					human_labels: 10,
					human: { agreement: { value: 0.9 }, fpr: { value: 0.05 }, kappa: 0.8 },
				}}
			/>,
		);
		expect(html).toContain("90.0%");
		expect(html).toContain("0.800");
		expect(html).toContain("cal_1");
	});
});
