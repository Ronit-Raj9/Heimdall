import "@/lib/i18n";

import { buildCanaryRequest } from "@/app/canaries/views/newCanaryForm";
import type { Schedule, SchedulesResponse } from "@/lib/types/schedules";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { describe, expect, it } from "vitest";
import { benchApi } from "@/lib/store/apis/benchApi";
import { mergeScheduleEdit, SchedulesBody } from "./schedulesView";

const store = configureStore({
	reducer: { [benchApi.reducerPath]: benchApi.reducer },
	middleware: (g) => g().concat(benchApi.middleware),
});

const DATA: SchedulesResponse = {
	file: {
		alerts: [{ url_env: "SLACK_URL", format: "slack" }],
		schedules: [{ name: "nightly", experiment: "experiments/n.yaml", cron: "0 6 * * *", max_usd_per_day: 5 }],
	},
	yaml: "",
	rows: [
		{
			name: "nightly",
			experiment: "experiments/n.yaml",
			cron: "0 6 * * *",
			max_usd_per_day: 5,
			kind: "experiment",
			source: "web",
			spent_today_usd: 1.5,
			next: "2026-11-22T06:00:00Z",
		},
		{ name: "canary-gpt", experiment: "canaries/gpt.yaml", cron: "0 7 * * *", kind: "canary", source: "canary", spent_today_usd: 0 },
	],
	events: ["verdict_regressed", "schedule_failed"],
	secrets: ["SLACK_URL"],
	hosted: false,
	ticker: true,
};

function render(data: SchedulesResponse) {
	return renderToStaticMarkup(
		<Provider store={store}>
			<SchedulesBody data={data} busy={false} onSave={() => {}} />
		</Provider>,
	);
}

describe("SchedulesBody", () => {
	it("lists schedules with run buttons, canary rows read-only, and webhook receivers", () => {
		const html = render(DATA);
		expect(html).toContain('data-testid="schedule-run-nightly"');
		expect(html).toContain('data-testid="schedule-remove-nightly"');
		expect(html).not.toContain('data-testid="schedule-remove-canary-gpt"');
		expect(html).toContain("1.50 / 5.00");
		expect(html).toContain('data-testid="alert-test-0"');
		expect(html).toContain("SLACK_URL");
		expect(html).not.toContain('data-testid="schedules-no-ticker"');
	});

	it("shows the empty state, the no-secret hint and the no-ticker note", () => {
		const html = render({ ...DATA, file: { schedules: [] }, rows: [], secrets: [], ticker: false });
		expect(html).toContain('data-testid="schedules-empty"');
		expect(html).toContain('data-testid="alerts-no-secrets"');
		expect(html).toContain('data-testid="schedules-no-ticker"');
		expect(html).toContain('href="/operate"');
	});

	it("shows the hosted guardrails with a runners link and real next, last and spend", () => {
		const rows = [
			{
				...DATA.rows[0],
				last: {
					at: "2026-11-21T06:00:00Z",
					started: "2026-11-21T06:00:00Z",
					finished: "2026-11-21T06:10:00Z",
					schedule: "nightly",
					status: "ok",
					spent_today_usd: 1.5,
				},
			},
		];
		const html = render({ ...DATA, rows, hosted: true, ticker: true, limits: { max_schedules: 20, min_interval_min: 15 } });
		expect(html).toContain('data-testid="schedules-hosted-limits"');
		expect(html).toContain("1 of 20 schedules");
		expect(html).toContain("every 15 minutes");
		expect(html).toContain('href="/workers"');
		expect(html).not.toContain('data-testid="schedules-no-ticker"');
		expect(html).toContain("1.50 / 5.00");
		expect(html).toContain(">ok<");
	});
});

describe("buildCanaryRequest", () => {
	it("splits tasks and provider/model targets", () => {
		const r = buildCanaryRequest({
			name: " n ",
			agent: "agents/a.yaml",
			suite: "s@1",
			tasks: "t1, t2",
			targets: "openai/gpt-x, m2",
			cron: "",
			perRun: "1",
			perDay: "2",
			smoke: false,
			force: true,
		});
		expect(r.name).toBe("n");
		expect(r.tasks).toEqual(["t1", "t2"]);
		expect(r.targets).toEqual([{ provider: "openai", model: "gpt-x" }, { model: "m2" }]);
		expect(r.schedule).toBeUndefined();
		expect(r.force).toBe(true);
		expect(
			buildCanaryRequest({
				name: "n",
				agent: "",
				suite: "s",
				tasks: "all",
				targets: "m",
				cron: "0 6 * * *",
				perRun: "",
				perDay: "",
				smoke: true,
				force: false,
			}).tasks,
		).toBe("all");
	});
});

describe("pause and edit", () => {
	it("offers Pause and Edit on a schedule and shows Paused in place of the next run", () => {
		let html = render(DATA);
		expect(html).toContain('data-testid="schedule-pause-nightly"');
		expect(html).toContain('data-testid="schedule-pause-canary-gpt"');
		expect(html).toContain('data-testid="schedule-edit-nightly"');
		expect(html).not.toContain('data-testid="schedule-edit-canary-gpt"');
		const rows = [{ ...DATA.rows[0], paused: true, next: undefined }, DATA.rows[1]] as SchedulesResponse["rows"];
		html = render({ ...DATA, rows });
		expect(html).toContain('data-testid="schedule-paused-nightly"');
		expect(html).toContain('data-testid="schedule-resume-nightly"');
		expect(html).toContain(">Paused<");
	});

	it("keeps the fields the form does not show when a schedule is edited", () => {
		const initial = {
			name: "n",
			experiment: "experiments/n.yaml",
			cron: "0 6 * * *",
			timezone: "UTC",
			fail_on: ["regressed"],
			paused: true,
		} as Schedule;
		const next = mergeScheduleEdit(initial, { name: "n", experiment: "experiments/m.yaml", cron: "0 7 * * *" });
		expect(next).toEqual({ name: "n", experiment: "experiments/m.yaml", cron: "0 7 * * *", fail_on: ["regressed"], paused: true });
	});
});