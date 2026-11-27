import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { childReasons, didNotStart, evaluationStatusWord, failureCause, statusTone } from "./evaluation.utils";

const ev = (over: Partial<Evaluation>): Evaluation =>
	({ id: "ev_1", status: "failed", spec: { name: "x", mode: "score", arms: [], benchmarks: ["b"] }, children: [], ...over }) as unknown as Evaluation;

describe("lane 3 evaluation words (M3-9, M3-10, M3-25)", () => {
	it("says Did not start for a failure where nothing ran", () => {
		const none = ev({ children: [{ benchmark: "b", experiment_id: "e", status: "failed", spent_usd: 0 }] as never });
		expect(didNotStart(none)).toBe(true);
		expect(evaluationStatusWord(none)).toBe("notStarted");
		const spent = ev({ children: [{ benchmark: "b", experiment_id: "e", status: "failed", spent_usd: 0.2 }] as never });
		expect(didNotStart(spent)).toBe(false);
		expect(didNotStart(ev({ status: "done" }))).toBe(false);
	});

	it("maps known worker messages to a cause", () => {
		expect(failureCause("worker refused plan p1: image hd-x:1 not found in registry; run deploy/hosted/registry-sync.sh")).toBe("imageMissing");
		expect(failureCause("no online worker for org")).toBe("noWorker");
		expect(failureCause("worker refused the runs")).toBe("workerRefused");
		expect(failureCause("something odd")).toBeUndefined();
		// The server's cause code wins over the text (G1); an unknown code falls back to the text.
		expect(failureCause("something odd", "image_missing")).toBe("imageMissing");
		expect(failureCause("something odd", "no_worker")).toBe("noWorker");
		expect(failureCause("worker refused the runs", "new_code")).toBe("workerRefused");
		expect(failureCause(undefined, "worker_refused")).toBe("workerRefused");
	});

	it("carries the child's cause code into its reason", () => {
		const kids = [{ benchmark: "b", experiment_id: "", status: "skipped", skipped: "odd", cause: "no_worker" }] as never;
		expect(childReasons(kids)).toEqual([{ benchmark: "b", reason: "odd", cause: "no_worker" }]);
		expect(childReasons([{ benchmark: "c", experiment_id: "", status: "skipped" }] as never)).toEqual([{ benchmark: "c", reason: "skipped" }]);
	});

	it("shows cancelled as neutral, not a warning", () => {
		expect(statusTone("cancelled")).toBe("neutral");
		expect(statusTone("notStarted")).toBe("bad");
	});
});
