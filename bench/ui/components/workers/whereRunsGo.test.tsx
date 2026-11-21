import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import type { WorkerTargetView } from "@/lib/store/apis/workerTargetApi";
import { describe, expect, it, vi } from "vitest";
import { EvaluationWorkerPicker, WhereRunsGo } from "./whereRunsGo";

const q = vi.hoisted(() => ({ target: {} as Record<string, unknown> }));

vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>()),
	useGetWorkerTargetQuery: () => q.target,
	useSetWorkerTargetMutation: () => mutation(),
}));

const view: WorkerTargetView = {
	target: "platform:modal",
	choices: [
		{
			target: "shared",
			kind: "shared",
			name: "shared",
			title: "Shared worker",
			current: false,
			per_evaluation: false,
			note: "while a sandbox platform is where runs go, the shared worker runs every run there",
		},
		{ target: "platform:modal", kind: "platform", name: "modal", title: "Modal", status: "ok", current: true, per_evaluation: true },
		{ target: "platform:e2b", kind: "platform", name: "e2b", title: "E2B", status: "ok", current: false, per_evaluation: false },
		{
			target: "worker:gpu",
			kind: "worker",
			name: "gpu",
			title: "gpu (Fly Machines)",
			status: "ready",
			current: false,
			per_evaluation: true,
		},
	],
};

describe("WhereRunsGo", () => {
	it("lists every choice in one select with the current one chosen", () => {
		q.target = okQuery(view);
		const html = render(<WhereRunsGo />);
		expect(html).toContain('data-testid="where-runs-go-select"');
		expect(html).toContain("Modal (a fresh sandbox per run)");
		expect(html).toContain("gpu (Fly Machines)");
		expect(html).toContain("Shared worker");
		expect(html).toMatch(/<option value="platform:modal" selected="">/);
		expect(html).not.toMatch(/arm|runner|lease/i);
	});

	it("renders nothing off the hosted platform", () => {
		q.target = { isError: true, error: { status: 503 } };
		expect(render(<WhereRunsGo />)).toBe("");
	});
});

describe("EvaluationWorkerPicker", () => {
	it("hides with only the shared worker and lists every choice otherwise", () => {
		q.target = okQuery({ target: "shared", choices: [view.choices[0]] });
		expect(render(<EvaluationWorkerPicker value="" onChange={() => undefined} />)).toBe("");
		q.target = okQuery(view);
		const html = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />);
		expect(html).toContain("Modal (a fresh sandbox per run), where runs go");
		expect(html).toContain('<option value="worker:gpu">gpu (Fly Machines), ready</option>');
		expect(html).toMatch(
			/<option value="platform:e2b" disabled="">E2B \(a fresh sandbox per run\) \(set it as where runs go on the Workers page\)<\/option>/,
		);
		expect(html).toMatch(/<option value="shared" disabled=""/);
	});
});