import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { didNotStart, evaluationStatusWord, failureCause, statusTone } from "./evaluation.utils";

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
	});

	it("shows cancelled as neutral, not a warning", () => {
		expect(statusTone("cancelled")).toBe("neutral");
		expect(statusTone("notStarted")).toBe("bad");
	});
});
