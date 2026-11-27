import i18n from "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues, experimentFormSchema } from "./experimentFormSchema";
import { PlanInvalidAlert, invalidFields } from "./planInvalid";
import { suiteOptionText } from "./suiteOptions";

// The token the form reads at mount; tests set it (the node test environment has no sessionStorage).
const storedToken = vi.hoisted(() => ({ value: "" }));
vi.mock("@/components/experiments/experiments.utils", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/experiments/experiments.utils")>()),
	readWriteToken: () => storedToken.value,
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	usePlanExperimentMutation: () => mutation(),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation(),
}));

const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;
const defaults = defaultFormValues(catalog, dateSeed(new Date("2026-11-18T06:00:00Z")));

describe("ExperimentForm write token", () => {
	it("asks for the write token in local mode only", () => {
		expect(render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />)).toContain(
			"experiment-write-token-input",
		);
		const hosted = render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />, hostedSession("member"));
		expect(hosted).not.toContain("experiment-write-token-input");
		expect(hosted).toContain('data-testid="experiment-plan-button"');
	});

	// U2: plan and estimate are writes too, so the token sits above Plan and Plan waits for it (no 401).
	it("asks for the token before Plan and keeps Plan off until one is entered", () => {
		storedToken.value = "";
		const html = render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />);
		expect(html.indexOf("experiment-write-token-input")).toBeLessThan(html.indexOf('data-testid="experiment-plan-button"'));
		expect(html).toMatch(/data-testid="experiment-plan-button"[^>]*disabled=""/);
		expect(html).toContain("Enter the write token to plan and start.");
		storedToken.value = "t0ken";
		const withToken = render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />);
		expect(withToken).not.toMatch(/data-testid="experiment-plan-button"[^>]*disabled=""/);
		storedToken.value = "";
	});
});
// QC2-27-10: a probe id two suites share shows the selected suite's layer, not the first catalog match.
describe("ExperimentForm condition layers", () => {
	it("resolves each condition's layer through the selected suite", () => {
		const shared: Catalog = {
			...catalog,
			probes: [{ ...catalog.probes.find((p) => p.id === "stale-notes")!, layer: "gateway" }, ...catalog.probes],
			suites: catalog.suites.map((s) => ({
				...s,
				probe_layers: { "stale-notes": "fixture", "model-outage": "gateway", "test-crash": "tool_shim", "empty-test-run": "tool_shim" },
			})),
		};
		const html = render(
			<ExperimentForm
				catalog={shared}
				defaults={defaultFormValues(shared, dateSeed(new Date("2026-11-18T06:00:00Z")))}
				onStarted={() => undefined}
			/>,
		);
		const row = html.slice(html.indexOf('data-testid="experiment-condition-stale-notes"'));
		expect(row.slice(0, row.indexOf("</label>"))).toContain("fixture failure test");
		expect(row.slice(0, row.indexOf("</label>"))).not.toContain("gateway failure test");
	});
});

// N43: Plan on an invalid form names every field to fix next to the button, in form order.
describe("ExperimentForm invalid Plan (N43)", () => {
	const issuesOf = (values: unknown) => {
		const parsed = experimentFormSchema.safeParse(values);
		return parsed.success ? {} : Object.fromEntries(parsed.error.issues.map((i) => [String(i.path[0]), true]));
	};
	const filled = { ...defaults, name: "x", armBLabel: "candidate", overridePath: "notes.user_file", overrideValue: "1" };

	it("lists the invalid fields in the order the form shows them", () => {
		expect(invalidFields(issuesOf({ ...filled, name: "" }))).toEqual(["name"]);
		expect(invalidFields(issuesOf(defaults))).toEqual(["name", "armBLabel", "overridePath", "overrideValue"]);
		expect(invalidFields(issuesOf({ ...filled, totalUsd: 0, seed: -1 }))).toEqual(["seed", "totalUsd"]);
		expect(invalidFields(issuesOf(filled))).toEqual([]);
	});

	it("says how many fields to fix, with a button per field", () => {
		const one = render(<PlanInvalidAlert fields={["name"]} onFocus={() => undefined} />);
		expect(one).toContain('role="alert"');
		expect(one).toContain("Fix 1 field above before planning.");
		expect(one).toContain('data-testid="plan-invalid-field-name"');
		expect(one).toContain(">Name<");
		const two = render(<PlanInvalidAlert fields={["name", "totalUsd"]} onFocus={() => undefined} />);
		expect(two).toContain("Fix 2 fields above before planning.");
		expect(two).toContain(">Total spend cap (USD)<");
		expect(render(<PlanInvalidAlert fields={[]} onFocus={() => undefined} />)).toBe("");
	});
});

// N39 and N42: the A/B form lists ready benchmarks with their ready counts and only My agents' agents.
describe("ExperimentForm benchmark and agent menus (N39)", () => {
	it("names ready counts and marks practice benchmarks", () => {
		const t = i18n.getFixedT("en", "builder");
		const s = { id: "acme-desktop", version: "1.0.0", tasks: ["a", "b"], probes: [] };
		expect(suiteOptionText(s, new Map([["acme-desktop@1.0.0", { ids: ["a"], total: 2 }]]), t)).toBe(
			"acme-desktop@1.0.0, 1 of 2 tasks ready to run (practice)",
		);
		expect(suiteOptionText({ ...s, id: "hd-x" }, undefined, t)).toBe("hd-x@1.0.0 (2 tasks)");
	});

	it("counts ready tasks in the all-tasks choice when some are not ready", () => {
		const ref = `${catalog.suites[0].id}@${catalog.suites[0].version}`;
		const ready = new Map([[ref, { ids: catalog.suites[0].tasks.slice(0, 2), total: catalog.suites[0].tasks.length }]]);
		const html = render(<ExperimentForm catalog={catalog} defaults={defaults} ready={ready} onStarted={() => undefined} />);
		expect(html).toContain("All ready tasks (2)");
	});
});