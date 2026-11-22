import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
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
	it("says where runs go with one choice, its runs at once and how to add a worker (N93)", () => {
		q.target = okQuery({
			target: "shared",
			choices: [{ ...view.choices[0], current: true, per_evaluation: true, note: undefined, slots: 2 }],
		});
		const member = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("member"));
		expect(member).toContain('data-testid="eval-worker-only"');
		expect(member).toContain("Runs go to: Shared worker. It runs 2 at a time.");
		expect(member).toContain("An admin can add a worker on the Workers page.");
		expect(member).not.toContain("<select");
		const admin = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("admin"));
		expect(admin).toContain('href="/settings?tab=runners"');
		expect(admin).toContain("Add a worker");
		expect(admin).not.toContain("An admin can add a worker");
		q.target = okQuery({ target: "shared", choices: [{ ...view.choices[0], current: true, per_evaluation: true }] });
		expect(render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("member"))).toContain(
			"Runs go to: Shared worker.</",
		);
	});

	it("lists every choice when there are several", () => {
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