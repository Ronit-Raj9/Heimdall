import type { OnboardDraftSummary } from "@/lib/types/onboard";
import { describe, expect, it } from "vitest";
import { draftSourceName, keepableSource, readKeptSource, stepReached, visibleDrafts } from "./addBenchmarkState";

const d = (id: string, tasks: number, updated: string) => ({ id, tasks, updated }) as OnboardDraftSummary;

describe("add benchmark helpers (M6-51, M6-53, X-83)", () => {
	it("knows the step reached", () => {
		expect(stepReached({ found: false, draft: false })).toBe(0);
		expect(stepReached({ found: true, draft: false })).toBe(1);
		expect(stepReached({ found: true, draft: true })).toBe(4);
	});

	it("hides empty drafts older than a day, never the current one", () => {
		const now = Date.parse("2026-11-27T12:00:00Z");
		const list = [d("old", 0, "2026-11-20T00:00:00Z"), d("new", 0, "2026-11-27T10:00:00Z"), d("full", 3, "2026-01-01T00:00:00Z")];
		expect(visibleDrafts(list, now, null).map((x) => x.id)).toEqual(["new", "full"]);
		expect(visibleDrafts(list, now, "old").map((x) => x.id)).toEqual(["old", "new", "full"]);
	});

	it("shows the file name, not the server path", () => {
		expect(draftSourceName("/data/uploads/ab12/orders.csv")).toBe("orders.csv");
		expect(draftSourceName("https://huggingface.co/datasets/openai/gsm8k")).toBe("huggingface.co/openai/gsm8k");
	});

	it("keeps only links and pasted rows across a reload", () => {
		expect(keepableSource({ upload: "u1", name: "a.csv" })).toBeNull();
		expect(keepableSource({ url: "https://x" })).toEqual({ url: "https://x" });
		expect(readKeptSource('{"data":"a,b"}')).toEqual({ data: "a,b" });
		expect(readKeptSource("not json")).toBeNull();
	});
});
