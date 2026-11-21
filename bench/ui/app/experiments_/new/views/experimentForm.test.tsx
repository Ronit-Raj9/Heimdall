import "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues } from "./experimentFormSchema";

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
			suites: catalog.suites.map((s) => ({ ...s, probe_layers: { "stale-notes": "fixture", "model-outage": "gateway", "test-crash": "tool_shim", "empty-test-run": "tool_shim" } })),
		};
		const html = render(<ExperimentForm catalog={shared} defaults={defaultFormValues(shared, dateSeed(new Date("2026-11-18T06:00:00Z")))} onStarted={() => undefined} />);
		const row = html.slice(html.indexOf('data-testid="experiment-condition-stale-notes"'));
		expect(row.slice(0, row.indexOf("</label>"))).toContain("fixture failure test");
		expect(row.slice(0, row.indexOf("</label>"))).not.toContain("gateway failure test");
	});
});
