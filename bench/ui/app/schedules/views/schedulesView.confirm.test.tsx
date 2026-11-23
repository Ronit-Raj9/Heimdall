import "@/lib/i18n";

import i18n from "i18next";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { benchApi } from "@/lib/store/apis/benchApi";
import type { SchedulesResponse } from "@/lib/types/schedules";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { describe, expect, it, vi } from "vitest";
import { confirmRunNow, evaluationOptions, SchedulesBody } from "./schedulesView";

// Fix wave 3, N231: Run now and Remove ask first; the evaluation picker tells evaluations with one
// name apart.

const asked = vi.hoisted(() => ({ buttons: [] as { testId: string; question: string; confirmLabel: string; onConfirm: () => void }[] }));
vi.mock("@/components/platform/badges", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/platform/badges")>()),
	ConfirmButton: (p: { testId: string; question: string; confirmLabel: string; onConfirm: () => void; label: string }) => {
		asked.buttons.push(p);
		return <button data-testid={p.testId}>{p.label}</button>;
	},
}));

const t = i18n.getFixedT("en", "schedules");

const store = configureStore({
	reducer: { [benchApi.reducerPath]: benchApi.reducer },
	middleware: (g) => g().concat(benchApi.middleware),
});

const DATA: SchedulesResponse = {
	file: { schedules: [{ name: "nightly", evaluation: "ev_1", cron: "0 6 * * *", max_usd_per_day: 5 }] },
	yaml: "",
	rows: [
		{ name: "nightly", evaluation: "ev_1", cron: "0 6 * * *", max_usd_per_day: 5, kind: "evaluation", source: "web", spent_today_usd: 1.5 },
	],
	events: [],
	secrets: [],
	hosted: false,
	ticker: true,
};

describe("Run now", () => {
	it("asks with the schedule's money left and runs nothing when the person says no", async () => {
		const confirm = vi.fn(async (_message: string, _opts?: unknown) => false);
		const ok = await confirmRunNow(confirm, t, DATA.rows[0]);
		expect(ok).toBe(false);
		const [message, opts] = confirm.mock.calls[0] as unknown as [string, { confirmLabel: string }];
		expect(message).toBe("Run nightly now? It starts ev_1 again and can spend up to $3.50 of this schedule's $5.00 limit.");
		expect(opts.confirmLabel).toBe("Run now");
	});

	it("says there is no limit when the schedule has none", async () => {
		const confirm = vi.fn(async (_message: string, _opts?: unknown) => true);
		expect(await confirmRunNow(confirm, t, { ...DATA.rows[0], max_usd_per_day: undefined })).toBe(true);
		expect(confirm.mock.calls[0][0]).toContain("no schedule limit");
	});
});

describe("Remove", () => {
	it("is a confirm button: nothing is saved until the person confirms", () => {
		asked.buttons = [];
		const onSave = vi.fn();
		const html = renderToStaticMarkup(
			<Provider store={store}>
				<SchedulesBody data={DATA} busy={false} onSave={onSave} />
			</Provider>,
		);
		expect(html).toContain('data-testid="schedule-remove-nightly"');
		const remove = asked.buttons.find((b) => b.testId === "schedule-remove-nightly");
		expect(remove?.question).toBe("Remove schedule nightly? It stops running. Past runs stay.");
		expect(remove?.confirmLabel).toBe("Remove schedule");
		expect(onSave).not.toHaveBeenCalled();
		remove?.onConfirm();
		expect(onSave).toHaveBeenCalledTimes(1);
		expect(JSON.stringify(onSave.mock.calls[0])).not.toContain("nightly");
	});
});

describe("evaluationOptions", () => {
	const ev = (id: string, name: string, created: string, status: Evaluation["status"]): Evaluation =>
		({ id, spec: { name, benchmarks: ["bfcl", "coding-core"] }, status, children: [], created_at: created }) as unknown as Evaluation;

	it("gives evaluations with one name different labels, newest first, stopped ones last", () => {
		const opts = evaluationOptions(
			[
				ev("ev_a", "codex score", "2026-11-20T10:05:00Z", "done"),
				ev("ev_b", "codex score", "2026-11-22T20:07:00Z", "done"),
				ev("ev_c", "codex score", "2026-11-23T08:00:00Z", "cancelled"),
			],
			t,
		);
		expect(opts.active.map((o) => o.id)).toEqual(["ev_b", "ev_a"]);
		expect(opts.stopped.map((o) => o.id)).toEqual(["ev_c"]);
		expect(opts.active[0].label).not.toBe(opts.active[1].label);
		expect(opts.active[0].label).toMatch(/^codex score · bfcl, coding-core · \d+ Nov \d\d:\d\d$/);
		expect(opts.stopped[0].label).toContain("(cancelled)");
	});
});