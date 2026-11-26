import { runOutputKind } from "@/components/runs/runOutput";
import type { RunDetail } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { hiddenRunTabs, runHasVoice, runTabFlags } from "./runTabs";

const detail = (extra: Partial<RunDetail> = {}) => ({ slot: { arm: "A" }, run: { run_id: "r_1" }, ...extra }) as unknown as RunDetail;

describe("runHasVoice", () => {
	it("is false for a run without the timeline state file, so the page never asks for /voice", () => {
		expect(runHasVoice(detail())).toBe(false);
		expect(runHasVoice(detail({ state: [{ id: "other.json" } as never] }))).toBe(false);
		expect(runHasVoice(undefined)).toBe(false);
	});
	it("is true when the run exported timeline.jsonl", () => {
		expect(runHasVoice(detail({ state: [{ id: "timeline.jsonl" } as never] }))).toBe(true);
	});
});

describe("runTabFlags", () => {
	it("keeps the agents, replay and recording tabs when their request failed", () => {
		const flags = runTabFlags({ isError: true }, { isError: true }, { isError: true });
		expect(flags).toEqual({ agents: true, replay: true, recording: true });
	});
	it("hides them when the request answered with nothing to show", () => {
		const flags = runTabFlags({ data: { available: false } }, { data: { frames: [] } }, { data: { recordings: [] } });
		expect(flags).toEqual({ agents: false, replay: false, recording: false });
	});
});

describe("the output kind the server sends", () => {
	it("an answer run has no Security tab even when it recorded nothing", () => {
		const d = detail({ output: "answer" });
		const kind = runOutputKind(d, d.output);
		expect(kind).toBe("answer");
		expect(hiddenRunTabs({ output: kind, agents: false, visual: false, voice: false, gpu: false, services: false }).has("security")).toBe(
			true,
		);
	});
});