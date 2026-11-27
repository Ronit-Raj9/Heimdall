import { describe, expect, it, vi } from "vitest";
import { MAX_TOASTS, addUnseenDone, clearUnseenDone, createToastQueue, getUnseenDone, titleWithDone } from "@/lib/notify";

describe("toast queue (M1-45)", () => {
	it("keeps at most three, dropping the oldest", () => {
		const q = createToastQueue();
		for (let i = 0; i < 5; i++) q.push({ text: `t${i}` });
		expect(q.current().map((t) => t.text)).toEqual(["t2", "t3", "t4"]);
		expect(q.current()).toHaveLength(MAX_TOASTS);
	});
	it("stays 5 s, 8 s with an action or Undo", () => {
		const q = createToastQueue();
		q.push({ text: "a" });
		q.push({ text: "b", action: { label: "View", to: "/runs" } });
		q.push({ text: "c", undo: () => Promise.resolve() });
		expect(q.current().map((t) => t.ttl)).toEqual([5000, 8000, 8000]);
		expect(q.current()[0].kind).toBe("success");
	});
	it("tells listeners and dismisses by id", () => {
		const q = createToastQueue();
		const l = vi.fn();
		const off = q.subscribe(l);
		const id = q.push({ text: "x", kind: "info" });
		q.dismiss(id);
		expect(l).toHaveBeenCalledTimes(2);
		expect(q.current()).toEqual([]);
		off();
		q.push({ text: "y" });
		expect(l).toHaveBeenCalledTimes(2);
	});
});

describe("finished count in the tab title", () => {
	it("prefixes only when something finished", () => {
		expect(titleWithDone("Runs", 0)).toBe("Runs");
		expect(titleWithDone("Runs", 2)).toBe("(2 done) Runs");
	});
	it("counts and clears", () => {
		clearUnseenDone();
		addUnseenDone();
		addUnseenDone(2);
		expect(getUnseenDone()).toBe(3);
		clearUnseenDone();
		expect(getUnseenDone()).toBe(0);
	});
});
