import "@/lib/i18n";

import { CANARY_DRIFTED_FIXTURE, CANARY_LIST_FIXTURE } from "@/components/canaries/canary.fixtures";
import { benchApi } from "@/lib/store/apis/benchApi";
import { canaryApi } from "@/lib/store/apis/canaryApi";
import { schedulesApi } from "@/lib/store/apis/schedulesApi";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildCanaryEdit, canaryFormFields, suiteRefs } from "./newCanaryForm";
import { CanariesBody, CanaryActions, pausedCanaries, unknownCanary, UnknownCanaryNotice } from "./canariesView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

function newStore() {
	return configureStore({
		reducer: { [benchApi.reducerPath]: benchApi.reducer },
		middleware: (g) => g().concat(benchApi.middleware),
	});
}

describe("CanariesBody", () => {
	it("lists the canaries, names a broken definition and shows the open panel", () => {
		const html = renderToStaticMarkup(
			<CanariesBody data={CANARY_LIST_FIXTURE} selected="nightly" onSelect={() => {}} panel={<div data-testid="panel" />} />,
		);
		expect(html).toContain('data-testid="canary-table"');
		expect(html).toContain('data-testid="canaries-errors"');
		expect(html).toContain("broken:");
		expect(html).toContain('data-testid="panel"');
	});

	it("explains how to start when there are no canaries", () => {
		const html = renderToStaticMarkup(
			<CanariesBody data={{ ...CANARY_LIST_FIXTURE, canaries: [], errors: [] }} selected={null} onSelect={() => {}} panel={null} />,
		);
		expect(html).toContain('data-testid="canaries-empty"');
		expect(html).toContain("canary init");
		expect(html).toContain("Catch your agent or model quietly getting worse");
	});

	it("marks a paused drift monitor in the list", () => {
		const name = CANARY_LIST_FIXTURE.canaries[0].name;
		const paused = pausedCanaries([
			{ name: `canary-${name}`, kind: "canary", paused: true } as { name: string; kind: string },
			{ name: "nightly-core", kind: "experiment", paused: true } as { name: string; kind: string },
		]);
		expect([...paused]).toEqual([name]);
		const html = renderToStaticMarkup(
			<CanariesBody data={CANARY_LIST_FIXTURE} selected={null} onSelect={() => {}} panel={null} paused={paused} />,
		);
		expect(html).toContain(`data-testid="canary-paused-${name}"`);
		expect(html).toContain(">Paused<");
	});
});

describe("CanaryActions", () => {
	const def = CANARY_DRIFTED_FIXTURE.definition;
	const render = (d: typeof def) =>
		renderToStaticMarkup(
			<Provider store={newStore()}>
				<CanaryActions name={d.name} definition={d} onEdit={() => {}} />
			</Provider>,
		);

	it("offers Pause, Edit and Delete next to the checks", () => {
		const html = render({ ...def, schedule: { cron: "0 6 * * *" } });
		expect(html).toContain('data-testid="canary-pause"');
		expect(html).toContain('data-testid="canary-edit"');
		expect(html).toContain('data-testid="canary-delete"');
		expect(html).not.toContain('data-testid="canary-paused"');
	});

	it("offers no Pause on a drift monitor without a schedule and says why (E4)", () => {
		const html = render({ ...def, schedule: undefined });
		expect(html).not.toContain('data-testid="canary-pause"');
		expect(html).not.toContain('data-testid="canary-resume"');
		expect(html).toContain('data-testid="canary-no-schedule"');
		expect(html).toContain("This drift monitor runs only when you press Check now, so there is nothing to pause.");
		expect(render({ ...def, schedule: { cron: "0 6 * * *" } })).not.toContain('data-testid="canary-no-schedule"');
	});

	it("shows Paused and Resume for a paused drift monitor", () => {
		const html = render({ ...def, schedule: { cron: "0 6 * * *", paused: true } as typeof def.schedule });
		expect(html).toContain('data-testid="canary-paused"');
		expect(html).toContain('data-testid="canary-resume"');
	});
});

describe("unknown drift monitor name (N76)", () => {
	it("knows a name that is neither a monitor nor a broken definition", () => {
		const known = CANARY_LIST_FIXTURE.canaries[0].name;
		expect(unknownCanary(CANARY_LIST_FIXTURE, "x")).toBe(true);
		expect(unknownCanary(CANARY_LIST_FIXTURE, known)).toBe(false);
		expect(unknownCanary(CANARY_LIST_FIXTURE, "broken")).toBe(false);
		expect(unknownCanary(CANARY_LIST_FIXTURE, null)).toBe(false);
		expect(unknownCanary(undefined, "x")).toBe(false);
		expect(unknownCanary({ ...CANARY_LIST_FIXTURE, canaries: [], errors: [] }, "x")).toBe(true);
	});

	it("says the name is unknown and offers every drift monitor", () => {
		const html = renderToStaticMarkup(<UnknownCanaryNotice name="x" onShowAll={() => {}} />);
		expect(html).toContain('role="status"');
		expect(html).toContain("No drift monitor is called x. It may have been deleted.");
		expect(html).toContain('data-testid="canary-unknown-show-all"');
		expect(html).toContain("Show all drift monitors");
	});
});

describe("drift monitor form", () => {
	it("lists the benchmarks as id@version and keeps hidden fields on edit", () => {
		expect(
			suiteRefs([
				{ id: "hd-harbor-bfcl", version: "1.0.0" },
				{ id: "x", version: "" },
			] as never),
		).toEqual(["hd-harbor-bfcl@1.0.0", "x"]);
		const def = { ...CANARY_DRIFTED_FIXTURE.definition, description: "keep me", schedule: { cron: "0 6 * * *", timezone: "UTC" } };
		const f = { ...canaryFormFields(def), tasksAll: false, taskIds: ["t9"], when: "custom" as const, cron: "0 7 * * *" };
		const body = buildCanaryEdit(def, f);
		expect(body.name).toBe(def.name);
		expect(body.description).toBe("keep me");
		expect(body.tasks).toEqual(["t9"]);
		expect(body.schedule).toEqual({ cron: "0 7 * * *", timezone: "UTC" });
		expect(body.force).toBeUndefined();
		expect(buildCanaryEdit(def, { ...f, when: "manual" }).schedule).toBeUndefined();
	});
});

describe("drift monitor cache", () => {
	afterEach(() => vi.unstubAllGlobals());

	it("refetches the open drift monitor after it is deleted and created again", async () => {
		const gets: string[] = [];
		const NodeRequest = globalThis.Request;
		vi.stubGlobal(
			"Request",
			class extends NodeRequest {
				constructor(input: RequestInfo | URL, init?: RequestInit) {
					super(typeof input === "string" ? new URL(input, "http://bench.test") : input, init);
				}
			},
		);
		vi.stubGlobal(
			"fetch",
			vi.fn(async (request: Request) => {
				const path = new URL(request.url).pathname;
				if (request.method === "GET" && path.endsWith("/canaries/zz")) gets.push(path);
				if (request.method === "DELETE") return new Response(null, { status: 204 });
				return new Response(JSON.stringify({ ...CANARY_DRIFTED_FIXTURE, name: "zz", path: "canaries/zz.yaml", next: "" }), {
					status: 200,
					headers: { "Content-Type": "application/json" },
				});
			}),
		);
		const store = newStore();
		const sub = store.dispatch(canaryApi.endpoints.getCanary.initiate("zz"));
		await sub;
		expect(gets.length).toBe(1);
		await store.dispatch(schedulesApi.endpoints.deleteCanary.initiate("zz"));
		await vi.waitFor(() => expect(gets.length).toBe(2));
		await store.dispatch(
			schedulesApi.endpoints.createCanary.initiate({
				name: "zz",
				agent: "agents/a.yaml",
				suite: "s@1",
				tasks: ["t"],
				trials: 1,
				targets: [{ model: "m" }],
				budget: {},
			}),
		);
		await vi.waitFor(() => expect(gets.length).toBe(3));
		sub.unsubscribe();
	});
});