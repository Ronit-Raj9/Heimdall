import "@/lib/i18n";

import { benchApi } from "@/lib/store/apis/benchApi";
import { errorMessage, errorProblems } from "@/lib/store/apis/schedulesApi";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { describe, expect, it, vi } from "vitest";
import { CANARY_DRIFTED_FIXTURE } from "@/components/canaries/canary.fixtures";
import {
	buildCanaryEdit,
	buildCanaryRequest,
	canaryFormFields,
	canaryFormProblems,
	canaryWhenOf,
	newCanaryFields,
	NewCanaryForm,
	suiteTaskIds,
} from "./newCanaryForm";
import type { SuiteSummary } from "@/lib/types/bench";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

function newStore() {
	return configureStore({
		reducer: { [benchApi.reducerPath]: benchApi.reducer },
		middleware: (g) => g().concat(benchApi.middleware),
	});
}

/** The Create button's opening tag. */
const submitTag = (html: string) => html.match(/<button[^>]*data-testid="canary-new-submit"[^>]*>/)?.[0] ?? "";

const filled = () => ({
	...newCanaryFields(),
	name: "nightly",
	agent: "agents/codex.yaml",
	suite: "coding-core@1.0.0",
	targets: [{ provider: "openai", model: "gpt-4o-mini" }],
});

describe("how often (N80)", () => {
	it("starts on every day at 06:00 UTC and sends that schedule in UTC", () => {
		expect(newCanaryFields().when).toBe("daily");
		expect(buildCanaryRequest(filled()).schedule).toEqual({ cron: "0 6 * * *", timezone: "UTC" });
	});

	it("sends no schedule for Only when I press Check now", () => {
		const req = buildCanaryRequest({ ...filled(), when: "manual", cron: "0 6 * * *" });
		expect(req.schedule).toBeUndefined();
		expect("schedule" in req).toBe(false);
	});

	it("blocks Create while Custom has no cron line", () => {
		const f = { ...filled(), when: "custom" as const, cron: " " };
		expect(canaryFormProblems(f).map((p) => p.field)).toEqual(["schedule"]);
		const html = renderToStaticMarkup(
			<Provider store={newStore()}>
				<NewCanaryForm initial={{ ...CANARY_DRIFTED_FIXTURE.definition, schedule: { cron: "" } }} />
			</Provider>,
		);
		expect(submitTag(html)).toContain('disabled=""');
		expect(html).toContain('data-testid="canary-new-cron"');
		expect(buildCanaryRequest({ ...filled(), when: "custom", cron: "*/30 * * * *" }).schedule).toEqual({
			cron: "*/30 * * * *",
			timezone: "UTC",
		});
	});

	it("maps a saved cron to its choice, else Custom with the line", () => {
		expect(canaryWhenOf({ cron: "0 6 * * *" })).toBe("daily");
		expect(canaryWhenOf({ cron: "0 6 * * *", timezone: "UTC" })).toBe("daily");
		expect(canaryWhenOf({ cron: "0 * * * *" })).toBe("hourly");
		expect(canaryWhenOf({ cron: "0 6 * * 1" })).toBe("weekly");
		expect(canaryWhenOf({ cron: "0 6 * * *", timezone: "Europe/Berlin" })).toBe("custom");
		expect(canaryWhenOf(undefined)).toBe("manual");
		const def = CANARY_DRIFTED_FIXTURE.definition;
		expect(canaryFormFields({ ...def, schedule: { cron: "0 6 * * *" } }).when).toBe("daily");
		const custom = canaryFormFields({ ...def, schedule: { cron: "15 3 * * *" } });
		expect([custom.when, custom.cron]).toEqual(["custom", "15 3 * * *"]);
		expect(canaryFormFields({ ...def, schedule: undefined }).when).toBe("manual");
	});

	it("an edit to Only when I press Check now drops the saved schedule", () => {
		const def = { ...CANARY_DRIFTED_FIXTURE.definition, schedule: { cron: "0 6 * * *" } };
		expect(buildCanaryEdit(def, { ...canaryFormFields(def), when: "manual" }).schedule).toBeUndefined();
	});
});

describe("every missing field at once (N81)", () => {
	it("lists every missing field of an empty form and disables Create", () => {
		const fields = canaryFormProblems({ ...newCanaryFields(), tasksAll: false, targets: [{ provider: "openai", model: "" }] }).map(
			(p) => p.field,
		);
		expect(fields).toEqual(["name", "agent", "suite", "tasks", "targets"]);
		const html = renderToStaticMarkup(
			<Provider store={newStore()}>
				<NewCanaryForm />
			</Provider>,
		);
		expect(submitTag(html)).toContain('disabled=""');
		for (const f of ["name", "agent", "suite", "targets"]) expect(html).toContain(`data-testid="canary-new-missing-${f}"`);
		expect(html).toContain("Pick an agent.");
		expect(html).toContain("Add at least one model to watch.");
		expect(html).toContain("pick an agent, a benchmark, the tasks and at least one model");
	});

	it("asks for both spending limits unless Practice mode is ticked", () => {
		expect(canaryFormProblems({ ...filled(), perRun: "", perDay: "3" }).map((p) => p.key)).toEqual(["canary.problem.budget"]);
		expect(canaryFormProblems({ ...filled(), perRun: "5", perDay: "3" }).map((p) => p.key)).toEqual(["canary.problem.budgetOrder"]);
		expect(canaryFormProblems({ ...filled(), perRun: "", perDay: "", smoke: true })).toEqual([]);
		expect(canaryFormProblems(filled())).toEqual([]);
	});

	it("sends all for All tasks and the picked ids for Only some tasks", () => {
		expect(buildCanaryRequest(filled()).tasks).toBe("all");
		expect(buildCanaryRequest({ ...filled(), tasksAll: false, taskIds: ["t1", "t2"] }).tasks).toEqual(["t1", "t2"]);
		const suites = [
			{ id: "coding-core", version: "1.0.0", tasks: [{ id: "t1" }, { id: "t2" }] },
			{ id: "other", version: "2", tasks: [{ id: "x" }] },
		] as unknown as SuiteSummary[];
		expect(suiteTaskIds(suites, "coding-core@1.0.0")).toEqual(["t1", "t2"]);
		expect(suiteTaskIds(suites, "coding-core@9")).toEqual([]);
		expect(suiteTaskIds(undefined, "x")).toEqual([]);
	});
});

describe("agent and model pickers (N82)", () => {
	it("shows an agent select and a model picker row instead of free text", () => {
		const html = renderToStaticMarkup(
			<Provider store={newStore()}>
				<NewCanaryForm />
			</Provider>,
		);
		expect(html).toMatch(/<select[^>]*data-testid="canary-new-agent"/);
		expect(html).toContain('data-testid="canary-new-model-0');
		expect(html).toContain('data-testid="canary-new-add-model"');
		expect(html).not.toContain("agents/codex.yaml");
		expect(html).not.toContain("provider/model");
	});

	it("turns two model rows into two targets and skips a row not picked yet", () => {
		const req = buildCanaryRequest({
			...filled(),
			targets: [
				{ provider: "openai", model: "gpt-4o-mini" },
				{ provider: "anthropic", model: "claude-x" },
				{ provider: "openai", model: "" },
			],
		});
		expect(req.targets).toEqual([
			{ provider: "openai", model: "gpt-4o-mini" },
			{ provider: "anthropic", model: "claude-x" },
		]);
		const def = CANARY_DRIFTED_FIXTURE.definition;
		expect(canaryFormFields(def).targets).toEqual(def.targets.map((tg) => ({ provider: tg.provider ?? "", model: tg.model })));
	});
});

describe("server problems (N81)", () => {
	it("reads problems[] of a refused drift monitor and keeps error.message working", () => {
		const err = {
			status: 422,
			data: {
				error: { message: "Pick an agent.\nPick a benchmark.", type: "invalid_request" },
				problems: [
					{ field: "agent", message: "Pick an agent." },
					{ field: "suite", message: "Pick a benchmark." },
				],
			},
		};
		expect(errorProblems(err).map((p) => p.field)).toEqual(["agent", "suite"]);
		expect(errorMessage(err, "x")).toBe("Pick an agent.\nPick a benchmark.");
		expect(errorProblems({ status: 409, data: { error: { message: "exists" } } })).toEqual([]);
		expect(errorProblems(undefined)).toEqual([]);
	});
});