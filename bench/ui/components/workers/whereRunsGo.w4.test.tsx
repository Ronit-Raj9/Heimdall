import { okQuery, render } from "@/lib/testing/render";
import type { WorkerTargetView } from "@/lib/store/apis/workerTargetApi";
import { describe, expect, it, vi } from "vitest";
import { EvaluationWorkerPicker } from "./whereRunsGo";

const q = vi.hoisted(() => ({ target: {} as Record<string, unknown> }));

vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>()),
	useGetWorkerTargetQuery: () => q.target,
}));

// N208: Modal takes one evaluation while the shared worker is where runs go.
const view: WorkerTargetView = {
	target: "shared",
	choices: [
		{ target: "shared", kind: "shared", name: "shared", title: "Shared worker", current: true, per_evaluation: true },
		{ target: "platform:modal", kind: "platform", name: "modal", title: "Modal", status: "ok", current: false, per_evaluation: true },
	],
};

const line = "Modal takes evaluations. Comparisons and readiness checks run on the shared worker.";

describe("EvaluationWorkerPicker on a per-evaluation platform", () => {
	it("enables the platform option and says what it takes when it is picked", () => {
		q.target = okQuery(view);
		const html = render(<EvaluationWorkerPicker value="platform:modal" onChange={() => {}} />);
		expect(html).toMatch(/<option value="platform:modal" selected="">Modal/);
		expect(html).not.toMatch(/<option value="platform:modal"[^>]*disabled/);
		expect(html).toContain(line);
	});

	it("says nothing while the shared worker is picked", () => {
		q.target = okQuery(view);
		expect(render(<EvaluationWorkerPicker value="" onChange={() => {}} />)).not.toContain(line);
	});
});