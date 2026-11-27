import { describe, expect, it } from "vitest";
import type { InboxItem } from "@/lib/types/bench";
import { narrowInboxItems, openingInboxFilter } from "./inboxDefaults";

describe("openingInboxFilter", () => {
	it("opens on New while something is new", () => {
		expect(openingInboxFilter({ new: 2, accepted: 5 })).toBe("new");
	});
	it("opens on All when nothing is new but items exist", () => {
		expect(openingInboxFilter({ new: 0, accepted: 5, rejected: 2 })).toBe("all");
	});
	it("keeps New on an empty inbox", () => {
		expect(openingInboxFilter({})).toBe("new");
		expect(openingInboxFilter(undefined)).toBe("new");
	});
});

describe("narrowInboxItems", () => {
	const now = Date.parse("2026-11-27T12:00:00Z");
	const items = [
		{
			id: "a",
			title: "Refund failed",
			trace_id: "t1",
			source: "gw",
			signature: "s",
			flagged: ["tool error"],
			last_seen: "2026-11-27T10:00:00Z",
		},
		{
			id: "b",
			title: "Order lookup",
			trace_id: "t2",
			source: "gw",
			signature: "s",
			flagged: ["timeout"],
			model: "m-large",
			last_seen: "2026-11-01T10:00:00Z",
		},
	] as unknown as InboxItem[];
	it("matches title, flagged reasons and model", () => {
		expect(narrowInboxItems(items, "refund", "", now).map((i) => i.id)).toEqual(["a"]);
		expect(narrowInboxItems(items, "TIMEOUT", "", now).map((i) => i.id)).toEqual(["b"]);
		expect(narrowInboxItems(items, "m-large", "", now).map((i) => i.id)).toEqual(["b"]);
	});
	it("keeps only items seen in the window", () => {
		expect(narrowInboxItems(items, "", "7d", now).map((i) => i.id)).toEqual(["a"]);
		expect(narrowInboxItems(items, "", "", now)).toHaveLength(2);
	});
});