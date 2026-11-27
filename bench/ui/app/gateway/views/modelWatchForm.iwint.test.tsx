import "@/lib/i18n";

import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ModelWatchView, startNow, type WatchScheduleRow } from "./modelWatchCard";
import { EMPTY_WATCH_FORM, WatchFormFields, setupLabels, watchName, watchSchedule, withWatch, type WatchForm } from "./modelWatchForm";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
	useNavigate: () => () => undefined,
}));

// D3 6.8: the form that creates a new model watch, and "Start now" on a skipped model.
const ev = { id: "ev_core", spec: { name: "Core check", arms: [{ label: "sonnet", agent: "a", model: "m" }] } } as unknown as Evaluation;
const filled: WatchForm = {
	...EMPTY_WATCH_FORM,
	providers: "Anthropic, openai anthropic",
	evaluation: "ev_core",
	current: "sonnet",
	usdPerModel: "4",
	maxPerDay: "2",
	maxUsdPerDay: "8",
	match: " claude ",
};

describe("new model watch form", () => {
	it("builds the evaluation schedule with its watch", () => {
		expect(watchSchedule(filled, [])).toEqual({
			schedule: {
				name: "new-models-ev_core",
				evaluation: "ev_core",
				cron: "0 6 * * *",
				max_usd_per_day: 8,
				new_models: { providers: ["anthropic", "openai"], current: "sonnet", usd_per_model: 4, max_per_day: 2, match: "claude" },
			},
		});
		expect(watchSchedule({ ...filled, check: "hourly", match: "" }, []).schedule?.cron).toBe("0 * * * *");
		expect(watchSchedule({ ...filled, match: "" }, []).schedule?.new_models.match).toBeUndefined();
	});

	it("says what is missing before it saves", () => {
		expect(watchSchedule(EMPTY_WATCH_FORM, []).problem).toBe("needProviders");
		expect(watchSchedule({ ...filled, providers: "a b c d e f" }, []).problem).toBe("tooManyProviders");
		expect(watchSchedule({ ...filled, evaluation: "" }, []).problem).toBe("needEvaluation");
		expect(watchSchedule({ ...filled, current: "" }, []).problem).toBe("needCurrent");
		expect(watchSchedule({ ...filled, usdPerModel: "0" }, []).problem).toBe("needBudget");
		expect(watchSchedule({ ...filled, maxUsdPerDay: "" }, []).problem).toBe("needDaily");
		expect(watchSchedule({ ...filled, maxUsdPerDay: "7.99" }, []).problem).toBe("dailyTooLow");
	});

	it("picks a free schedule name in the server's name rule", () => {
		expect(watchName("ev_Core", [])).toBe("new-models-ev_core");
		expect(watchName("ev_core", ["new-models-ev_core", "new-models-ev_core-2"])).toBe("new-models-ev_core-3");
		expect(watchName("x".repeat(80), []).length).toBeLessThanOrEqual(64);
	});

	it("adds the watch last and keeps the rest of the schedules file", () => {
		const file = { alerts: [{ name: "ops" }], schedules: [{ name: "nightly", cron: "0 1 * * *" }] } as never;
		const s = watchSchedule(filled, []).schedule!;
		expect(withWatch(file, s)).toEqual({ alerts: [{ name: "ops" }], schedules: [{ name: "nightly", cron: "0 1 * * *" }, s] });
	});

	it("shows the fields of D3 6.8, with the setups of the picked evaluation", () => {
		expect(setupLabels(ev)).toEqual(["sonnet"]);
		const html = render(<WatchFormFields form={filled} onChange={() => undefined} evaluations={[ev]} />);
		for (const label of [
			"Providers to watch",
			"Evaluation to run",
			"Which setup in that evaluation is your current model",
			"Budget for each new model (USD)",
			"At most per day",
			"Only models whose name contains (optional)",
			"Check",
			"Spending limit per day (USD)",
		]) {
			expect(html).toContain(label);
		}
		expect(html).toContain('<option value="ev_core" selected="">Core check, m</option>');
		expect(html).toContain('<option value="sonnet" selected="">sonnet: m</option>');
	});
});

describe("Start now on a skipped model", () => {
	it("clears the model, then runs the schedule", async () => {
		const calls: string[] = [];
		await startNow(
			async (a) => void calls.push(`retry ${a.schedule} ${a.model}`),
			async (s) => void calls.push(`run ${s}`),
			"watch-a",
			"openai/gpt-new",
		);
		expect(calls).toEqual(["retry watch-a openai/gpt-new", "run watch-a"]);
	});

	it("offers Start now on a skipped model and Try again on a failed one", () => {
		const row = {
			name: "watch-a",
			cron: "0 6 * * *",
			evaluation: "ev_core",
			kind: "evaluation",
			source: "web",
			spent_today_usd: 0,
			new_models: { providers: ["openai"], current: "sonnet", usd_per_model: 4 },
		} as WatchScheduleRow;
		const item = (model: string, state: string) => ({
			provider: "openai",
			model,
			first_seen: "2026-11-27T06:00:00Z",
			state,
			reason: "r",
			claimed_at: "",
		});
		const html = render(
			<ModelWatchView
				row={row}
				state={
					{
						schedule: "watch-a",
						baseline: {},
						checked_at: "",
						seen: { "openai/a": item("a", "skipped"), "openai/b": item("b", "failed") },
					} as never
				}
				onRetry={() => undefined}
				onStartNow={() => undefined}
			/>,
		);
		expect(html).toMatch(/data-testid="model-watch-retry-openai\/a"[^>]*>Start now</);
		expect(html).toMatch(/data-testid="model-watch-retry-openai\/b"[^>]*>Try again</);
	});
});