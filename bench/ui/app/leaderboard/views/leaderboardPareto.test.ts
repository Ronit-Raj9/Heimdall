import { describe, expect, it } from "vitest";
import type { Pareto } from "@/lib/types/bench";
import { paretoShortIds } from "./leaderboardPareto";

const point = (id: string, agent: string) => ({ id, label: "", agent, model: "m", x: null, y: null, placed: true, frontier: false, dominated_by: [], reason: "" });

describe("paretoShortIds", () => {
	it("shortens hashes to agent plus 6 characters, in points, dominated_by and frontier", () => {
		const p = { x: "cost_per_success", x_label: "", y: "", note: "", frontier: ["aaaaaa111"], points: [point("aaaaaa111", "codex"), { ...point("bbbbbb222", "codex"), dominated_by: ["aaaaaa111"] }] } as unknown as Pareto;
		const out = paretoShortIds(p);
		expect(out.points.map((x) => x.id)).toEqual(["codex aaaaaa", "codex bbbbbb"]);
		expect(out.points[1].dominated_by).toEqual(["codex aaaaaa"]);
		expect(out.frontier).toEqual(["codex aaaaaa"]);
	});
	it("keeps the full id when two short names would collide", () => {
		const p = { frontier: [], points: [point("aaaaaa1", "x"), point("aaaaaa2", "x")] } as unknown as Pareto;
		expect(paretoShortIds(p).points.map((x) => x.id)).toEqual(["aaaaaa1", "aaaaaa2"]);
	});
});
