import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ExportsPanel } from "./integrationsView";

function exp(exp_id: string, name: string, at: string, extra: Partial<ExperimentStatus> = {}): ExperimentStatus {
	return {
		exp_id,
		name,
		state: "done",
		manifest_hash: "h",
		frozen_at: at,
		slots: { total: 2, done: 2, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "swe-lite@1.0.0",
		arms: ["A", "B"],
		...extra,
	};
}

const experiments = [
	exp("e_admit_acme_safety_5e8q", "admit-acme-safety", "2026-11-23T10:00:00Z", { suite: "acme-safety@1.0.0", arms: ["oracle", "noop"] }),
	exp("e_eval_x_kaky", "eval-20261122-143334-423797-hd-harbor-bfcl", "2026-11-22T10:00:00Z", {
		suite: "hd-harbor-bfcl@1.0.0",
		arms: ["A"],
		evaluation_id: "ev_1",
		state: "running",
	}),
	exp("e_real", "Prompt v2 vs v1", "2026-11-20T10:00:00Z"),
	exp("e_canary_gone_ab12", "canary-gone", "2026-11-19T10:00:00Z"),
];

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => okQuery({ experiments }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => okQuery({ suites: [] }) }));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useListEvaluationsQuery: () => okQuery([{ id: "ev_1", spec: { name: "codex score" } }]),
}));
vi.mock("@/lib/store/apis/canaryApi", () => ({ useGetCanariesQuery: () => okQuery({ canaries: [{ name: "nightly" }] }) }));
vi.mock("@/lib/store/apis/integrationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/integrationsApi")>()),
	useListExportsQuery: () => okQuery({ exports: [] }),
	useDeleteExportMutation: () => [async () => ({ data: undefined }), {}],
}));

describe("ExportsPanel (N19)", () => {
	it("groups choices with readable names and hides checks by default", () => {
		const html = render(<ExportsPanel />);
		expect(html).toContain('<optgroup label="Evaluations">');
		expect(html).toContain('<optgroup label="Comparisons">');
		expect(html).toContain("codex score · BFCL function calling · Running");
		expect(html).toContain('title="e_eval_x_kaky"');
		expect(html).toContain("Prompt v2 vs v1 · Finished");
		expect(html).not.toContain('value="e_admit_acme_safety_5e8q"');
		expect(html).toContain("Also show checks, practice runs and debug runs");
		expect(html.indexOf("codex score")).toBeLessThan(html.indexOf("Prompt v2 vs v1 · Finished"));
	});
	it("lists checks in their own group when the box is ticked", () => {
		const html = render(<ExportsPanel showHiddenAtStart />);
		expect(html).toContain('<optgroup label="Checks, practice and debug runs">');
		expect(html).toContain('value="e_admit_acme_safety_5e8q"');
		expect(html).toContain("Readiness check for acme-safety · Finished");
	});
	it("labels a deleted drift monitor's runs (deleted) (N257)", () => {
		const html = render(<ExportsPanel showHiddenAtStart />);
		expect(html).toContain("Drift monitor gone (deleted) · Finished");
	});
});