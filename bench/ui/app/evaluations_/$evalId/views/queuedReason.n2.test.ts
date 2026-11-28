import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { queuedReason } from "./evaluationView";

// The body GET /evaluations/{id} sends while an evaluation waits (handlers/uifix_cg_queue.go).
const ev = (queue: unknown, status = "running") => ({ id: "ev_1", status, spec: {}, children: [], queue }) as unknown as Evaluation;

describe("queuedReason (N2)", () => {
	it("says no worker when the server sends worker_online false", () => {
		expect(queuedReason(ev({ ahead: 0, worker_online: false }))?.key).toBe("noWorker");
	});
	it("says how many are ahead when a worker is online", () => {
		expect(queuedReason(ev({ ahead: 2, worker_online: true }, "queued"))).toEqual({ key: "queue", opts: { n: 2 } });
	});
	it("says nothing without queue facts", () => {
		expect(queuedReason(ev(undefined))).toBeNull();
	});
});
