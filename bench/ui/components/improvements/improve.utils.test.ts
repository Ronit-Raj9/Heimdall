import { describe, expect, it } from "vitest";
import { curveModel, lineageRows, pct, points, usd, versionLabel } from "./improve.utils";
import type { LineageNode } from "@/lib/types/improve";

const node = (seq: number, ref: string, parent?: string, status: LineageNode["status"] = "committed"): LineageNode => ({
	seq,
	round: seq - 1,
	ref,
	version: seq,
	parent,
	author: parent ? "agent" : "human",
	bundle_hash: "b",
	config_hash: "c",
	status,
	reason: "",
	at: "2026-11-22T00:00:00Z",
});

describe("improve utils", () => {
	it("formats", () => {
		expect(pct(0.425)).toBe("43%");
		expect(pct(undefined)).toBe("-");
		expect(points(0.05)).toBe("+5.0 pts");
		expect(points(-0.1)).toBe("-10.0 pts");
		expect(usd(0.01234)).toBe("$0.0123");
		expect(usd(3)).toBe("$3.00");
		expect(versionLabel("agents/x.yaml@v12")).toBe("v12");
	});

	it("places the curve, band and markers", () => {
		const m = curveModel(
			[
				{ round: 0, dev: 0.2, sealed: 0.1, event: "base" },
				{ round: 1, dev: 0.5, audit: 0.4, sealed: 0.45, event: "committed" },
				{ round: 2, dev: 0.5, audit: 0.4, event: "rejected" },
			],
			{ low: 0.1, high: 0.3, mean: 0.2, best_of_k: 0.3, trials: 4, loop_usd: 1, null_mean: 0.15 },
			3,
		);
		const dev = m.lines.find((l) => l.key === "dev")!;
		const sealed = m.lines.find((l) => l.key === "sealed")!;
		expect(dev.points).toHaveLength(3);
		expect(sealed.points).toHaveLength(2);
		expect(dev.path.startsWith("M")).toBe(true);
		expect(m.markers.map((k) => k.event)).toEqual(["committed", "rejected"]);
		expect(m.band!.y1).toBeLessThan(m.band!.y2);
		expect(m.nullY).toBeDefined();
		expect(m.rounds).toEqual([0, 1, 2, 3]);
	});

	it("orders lineage as a tree with rejected siblings", () => {
		const rows = lineageRows([node(1, "v1"), node(2, "v2", "v1"), node(3, "v3", "v1", "rejected"), node(4, "v4", "v2")]);
		expect(rows.map((r) => `${r.node.ref}:${r.depth}`)).toEqual(["v1:0", "v2:1", "v4:2", "v3:1"]);
	});
});
