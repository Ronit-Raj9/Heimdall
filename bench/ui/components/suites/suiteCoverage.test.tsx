import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CapabilityCoverage, LicenseSummary, ProbeList } from "./suiteCoverage";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;

describe("CapabilityCoverage", () => {
	it("draws one labelled bar per capability, scaled to the most covered", () => {
		const html = renderToStaticMarkup(<CapabilityCoverage tasks={suite("coding-core").tasks} />);
		expect(html).toContain('role="img" aria-label="test_discipline: 2 tasks"');
		expect(html).toMatch(/data-testid="suites-coverage-test_discipline"[\s\S]*?width:100%/);
		expect(html).toMatch(/data-testid="suites-coverage-efficiency"[\s\S]*?width:50%/);
	});

	it("says so when no task names a capability", () => {
		const tasks = suite("coding-core").tasks.map((task) => ({ ...task, capabilities: [] }));
		expect(renderToStaticMarkup(<CapabilityCoverage tasks={tasks} />)).toContain("No task in this suite names a capability.");
	});
});

describe("LicenseSummary", () => {
	it("counts licenses and flags the tasks without one", () => {
		const html = renderToStaticMarkup(<LicenseSummary tasks={suite("harbor-shell").tasks} />);
		expect(html).toContain('data-testid="suites-license-Apache-2.0"');
		expect(html).toContain("1 task has no license: h02");
		expect(renderToStaticMarkup(<LicenseSummary tasks={suite("acme-payments").tasks} />)).not.toContain("suites-license-missing");
	});
});

describe("ProbeList", () => {
	it("groups probes by PS2 family and never shows how they work", () => {
		const html = renderToStaticMarkup(<ProbeList probes={suite("coding-core").probes} />);
		const families = [...html.matchAll(/data-testid="suites-probe-family-(\w+)"/g)].map((m) => m[1]);
		expect(families).toEqual(["unavailable_model", "broken_tool", "misleading_instruction"]);
		expect(html).toMatch(/data-testid="suites-probe-family-broken_tool"[\s\S]*?test-crash[\s\S]*?tool shim/);
		expect(html).toContain("goal-preserving");
	});
});