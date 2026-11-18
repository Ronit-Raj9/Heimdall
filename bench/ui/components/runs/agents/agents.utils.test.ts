import type { AgentHandoff, AgentSpan } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	MAX_DIFF_CELLS,
	MAX_TREE_DEPTH,
	coveragePercent,
	coverageTone,
	exitTone,
	factState,
	formatBytes,
	formatDurationMs,
	formatOneDecimal,
	formatPercent,
	formatUsd4,
	handoffMarkers,
	isBlamedCandidate,
	kindColor,
	laneLayout,
	lineDiff,
	orderedCounts,
	parallelism,
	presentKinds,
	safePayload,
	shareOfWall,
	sourceStrength,
	strengthTone,
	tierNamesAgent,
	timeOfDay,
	treeOrder,
} from "./agents.utils";

type Node = { id: string; parent?: string };
const order = (nodes: Node[]) =>
	treeOrder(
		nodes,
		(n) => n.id,
		(n) => n.parent,
	).map((e) => `${e.node.id}:${e.depth}${e.orphan ? ":orphan" : ""}`);

describe("treeOrder", () => {
	it("orders depth first with children in input order", () => {
		const nodes: Node[] = [
			{ id: "b", parent: "lead" },
			{ id: "lead" },
			{ id: "a", parent: "lead" },
			{ id: "a1", parent: "a" },
			{ id: "solo" },
		];
		expect(order(nodes)).toEqual(["lead:0", "b:1", "a:1", "a1:2", "solo:0"]);
	});

	it("draws a node whose parent is missing as an orphan root", () => {
		expect(order([{ id: "x", parent: "ghost" }, { id: "root" }])).toEqual(["x:0:orphan", "root:0"]);
	});

	it("never loops on a parent cycle and places every node once", () => {
		const out = order([
			{ id: "a", parent: "b" },
			{ id: "b", parent: "a" },
			{ id: "self", parent: "self" },
		]);
		expect(out).toHaveLength(3);
		expect(out).toContain("self:0:orphan");
		expect(out.filter((s) => s.startsWith("a:"))).toHaveLength(1);
	});

	it("caps the drawn depth", () => {
		const chain: Node[] = Array.from({ length: 12 }, (_, i) => ({ id: `n${i}`, parent: i === 0 ? undefined : `n${i - 1}` }));
		const depths = treeOrder(
			chain,
			(n) => n.id,
			(n) => n.parent,
		).map((e) => e.depth);
		expect(Math.max(...depths)).toBe(MAX_TREE_DEPTH);
	});
});

const span = (over: Partial<AgentSpan> & Pick<AgentSpan, "agent_id" | "seq">): AgentSpan => ({
	kind: "model_turn",
	start_ms: 0,
	end_ms: 100,
	label: "",
	...over,
});

describe("laneLayout", () => {
	it("follows the agent order, adds unknown agents after, and places blocks in percent", () => {
		const lanes = laneLayout(
			[
				span({ agent_id: "b", seq: 3, start_ms: 500, end_ms: 1000, critical: true }),
				span({ agent_id: "a", seq: 1, start_ms: 0, end_ms: 250 }),
				span({ agent_id: "ghost", seq: 2, start_ms: 250, end_ms: 500 }),
			],
			["a", "b", "idle"],
			1000,
		);
		expect(lanes.map((l) => l.agentId)).toEqual(["a", "b", "idle", "ghost"]);
		expect(lanes[0].blocks[0]).toMatchObject({ left: 0, width: 25 });
		expect(lanes[1].blocks[0]).toMatchObject({ left: 50, width: 50 });
		expect(lanes[1].critical).toBe(true);
		expect(lanes[0].critical).toBe(false);
		expect(lanes[2].blocks).toEqual([]);
	});

	it("sorts blocks by start and clamps them to the run", () => {
		const [lane] = laneLayout(
			[
				span({ agent_id: "a", seq: 2, start_ms: 900, end_ms: 5000 }),
				span({ agent_id: "a", seq: 1, start_ms: -50, end_ms: 100 }),
				span({ agent_id: "a", seq: 3, start_ms: 400, end_ms: 300 }),
			],
			["a"],
			1000,
		);
		expect(lane.blocks.map((b) => b.span.seq)).toEqual([1, 3, 2]);
		expect(lane.blocks[0]).toMatchObject({ left: 0, width: 10 });
		expect(lane.blocks[1]).toMatchObject({ left: 40, width: 0 });
		expect(lane.blocks[2].left).toBe(90);
		expect(lane.blocks[2].width).toBeCloseTo(10);
	});

	it("draws everything at the start when the wall time is zero", () => {
		const [lane] = laneLayout([span({ agent_id: "a", seq: 1, start_ms: 10, end_ms: 20 })], ["a"], 0);
		expect(lane.blocks[0]).toMatchObject({ left: 0, width: 0 });
	});
});

describe("handoffMarkers", () => {
	const h = (over: Partial<AgentHandoff> & Pick<AgentHandoff, "index">): AgentHandoff => ({
		kind: "message",
		from: "a",
		to: "b",
		at_ms: 500,
		bytes: 1,
		source: "gateway_key",
		...over,
	});

	it("places handoffs with a time between two lanes, clamped to the run", () => {
		const out = handoffMarkers(
			[h({ index: 1 }), h({ index: 2, from: "b", to: "a", at_ms: 5000 }), h({ index: 3, to: "a" })],
			["a", "b"],
			1000,
		);
		expect(out.map((m) => [m.handoff.index, m.left, m.fromLane, m.toLane])).toEqual([
			[1, 50, 0, 1],
			[2, 100, 1, 0],
			[3, 50, 0, 0],
		]);
	});

	it("skips a handoff with no time, no recipient or an end with no lane", () => {
		const out = handoffMarkers(
			[h({ index: 1, at_ms: null }), h({ index: 2, to: undefined }), h({ index: 3, to: "ghost" }), h({ index: 4, from: "ghost" })],
			["a", "b"],
			1000,
		);
		expect(out).toEqual([]);
	});

	it("puts every marker at the start without a wall time", () => {
		expect(handoffMarkers([h({ index: 1 })], ["a", "b"], 0)[0].left).toBe(0);
	});
});

describe("sourceStrength", () => {
	it("maps sources to strengths like schemas.AgentSourceStrength", () => {
		expect(sourceStrength("gateway_key")).toBe("proven");
		expect(sourceStrength("header")).toBe("declared");
		expect(sourceStrength("self_report")).toBe("reported");
		expect(sourceStrength("mailbox")).toBe("reported");
		expect(sourceStrength(undefined)).toBe("reported");
	});
});

describe("parallelism and shares", () => {
	it("divides busy by wall", () => {
		expect(parallelism(293500, 184000)).toBeCloseTo(1.595, 3);
		expect(formatOneDecimal(parallelism(293500, 184000)!, "en")).toBe("1.6");
	});

	it("is unknown without a wall time", () => {
		expect(parallelism(100, 0)).toBeNull();
		expect(parallelism(Number.NaN, 10)).toBeNull();
		expect(shareOfWall(50, 0)).toBeNull();
		expect(shareOfWall(50, 200)).toBe(25);
	});
});

describe("coverage", () => {
	const cases = [
		{ steps: 100, attributed: 100, want: "good" },
		{ steps: 100, attributed: 95, want: "good" },
		{ steps: 100, attributed: 94, want: "warn" },
		{ steps: 100, attributed: 60, want: "warn" },
		{ steps: 100, attributed: 59, want: "bad" },
		{ steps: 0, attributed: 0, want: "neutral" },
	] as const;
	for (const c of cases) {
		it(`${c.attributed}/${c.steps} is ${c.want}`, () => {
			expect(coverageTone(coveragePercent(c))).toBe(c.want);
		});
	}

	it("orders counts strongest first, unknown keys after, zeros dropped", () => {
		expect(orderedCounts({ reported: 2, zz: 1, proven: 20, declared: 0 }, ["proven", "declared", "reported"])).toEqual([
			["proven", 20],
			["reported", 2],
			["zz", 1],
		]);
		expect(orderedCounts(undefined, [])).toEqual([]);
	});
});

describe("lineDiff", () => {
	const ops = (a: string, b: string) => lineDiff(a, b).map((l) => `${l.op === "same" ? " " : l.op === "del" ? "-" : "+"}${l.text}`);

	it("keeps identical text as context", () => {
		expect(ops("a\nb", "a\nb\n")).toEqual([" a", " b"]);
	});

	it("marks a dropped line as removed", () => {
		expect(ops("keep\nNever edit migrations/\nend", "keep\nend")).toEqual([" keep", "-Never edit migrations/", " end"]);
	});

	it("marks an inserted line as added and a changed line as removed then added", () => {
		expect(ops("a\nc", "a\nb\nc")).toEqual([" a", "+b", " c"]);
		expect(ops("a\nold\nz", "a\nnew\nz")).toEqual([" a", "-old", "+new", " z"]);
	});

	it("finds the longest common run inside a reordered middle", () => {
		expect(ops("x\n1\n2\n3\ny", "x\n2\n3\n1\ny")).toEqual([" x", "-1", " 2", " 3", "+1", " y"]);
	});

	it("handles empty sides and CRLF", () => {
		expect(ops("", "a")).toEqual(["+a"]);
		expect(ops("a\r\nb", "")).toEqual(["-a", "-b"]);
		expect(ops("a\r\nb", "a\nb")).toEqual([" a", " b"]);
	});

	it("falls back to removed then added above the cell cap", () => {
		const n = Math.ceil(Math.sqrt(MAX_DIFF_CELLS)) + 1;
		const a = Array.from({ length: n }, (_, i) => `a${i}`).join("\n");
		const b = Array.from({ length: n }, (_, i) => `b${i}`).join("\n");
		const out = lineDiff(a, b);
		expect(out).toHaveLength(2 * n);
		expect(out[0].op).toBe("del");
		expect(out[n].op).toBe("add");
	});
});

describe("labels and tones", () => {
	it("neutralises control and bidi characters in payloads but keeps line breaks", () => {
		expect(safePayload("a‮b\u0007\nc\td")).toBe("a�b�\nc\td");
		expect(safePayload(undefined)).toBe("");
	});

	it("names what became of each fact", () => {
		expect(factState({ fact: "f", sent: true, delivered: true })).toBe("kept");
		expect(factState({ fact: "f", sent: true, delivered: false })).toBe("lost");
		expect(factState({ fact: "f", sent: false, delivered: true })).toBe("added");
		expect(factState({ fact: "f", sent: false, delivered: false })).toBe("absent");
	});

	it("tones exits and strengths", () => {
		expect(exitTone("completed")).toBe("good");
		expect(exitTone("timeout")).toBe("warn");
		expect(exitTone("exit code 0")).toBe("good");
		expect(exitTone("exit code 137")).toBe("bad");
		expect(exitTone("weird")).toBe("neutral");
		expect(strengthTone("proven")).toBe("good");
		expect(strengthTone("declared")).toBe("neutral");
		expect(strengthTone("reported")).toBe("warn");
		expect(strengthTone("made-up")).toBe("warn");
	});

	it("knows which tiers name an agent and which candidate was picked", () => {
		expect(tierNamesAgent("replay_validated")).toBe(true);
		expect(tierNamesAgent("gate_proven")).toBe(true);
		expect(tierNamesAgent("unattributed")).toBe(false);
		expect(tierNamesAgent("single_agent")).toBe(false);
		const blame = { agent_id: "worker-b", gate: "g", step_seq: 14 };
		expect(isBlamedCandidate({ agent_id: "worker-b", gate: "g", effect: "veto", ref: "step:14", step_seq: 14 }, blame)).toBe(true);
		expect(isBlamedCandidate({ agent_id: "worker-b", gate: "other", effect: "veto", ref: "x", step_seq: 14 }, blame)).toBe(false);
		expect(isBlamedCandidate({ agent_id: "", gate: "g", effect: "veto", ref: "x" }, { gate: "g" })).toBe(false);
	});

	it("colours known kinds and lists present kinds in order", () => {
		expect(kindColor("model_turn")).toBe("var(--chart-cat-1)");
		expect(kindColor("<script>")).toBe("var(--chart-cat-other)");
		expect(
			presentKinds([
				span({ agent_id: "a", seq: 1, kind: "zz" }),
				span({ agent_id: "a", seq: 2, kind: "command" }),
				span({ agent_id: "a", seq: 3 }),
			]),
		).toEqual(["model_turn", "command", "zz"]);
	});
});

describe("formatters", () => {
	it("formats cost with four decimals", () => {
		expect(formatUsd4(0.0884, "en")).toBe("$0.0884");
		expect(formatUsd4(0, "en")).toBe("$0.0000");
	});

	it("formats durations", () => {
		expect(formatDurationMs(850, "en")).toBe("850 ms");
		expect(formatDurationMs(1234, "en")).toBe("1.2 sec");
		expect(formatDurationMs(184000, "en")).toBe("3 min 4 sec");
		expect(formatDurationMs(120000, "en")).toBe("2 min");
		expect(formatDurationMs(179600, "en")).toBe("3 min");
		expect(formatDurationMs(59960, "en")).toBe("1 min");
		expect(formatDurationMs(59940, "en")).toBe("59.9 sec");
		expect(formatDurationMs(-1500, "en")).toBe("-1.5 sec");
	});

	it("formats bytes and percents", () => {
		expect(formatBytes(512, "en")).toBe("512 B");
		expect(formatBytes(1400, "en")).toBe("1.4 kB");
		expect(formatPercent(91.7, "en")).toBe("92%");
		expect(formatPercent(0.4, "en")).toBe("0.4%");
	});

	it("reads a time of day in UTC and ignores zero or bad times", () => {
		expect(timeOfDay("2026-11-14T18:42:11.000Z")).toBe("18:42:11.000");
		expect(timeOfDay("0001-01-01T00:00:00Z")).toBeNull();
		expect(timeOfDay("nope")).toBeNull();
		expect(timeOfDay(undefined)).toBeNull();
	});
});