import { describe, expect, it } from "vitest";
import { hiddenRunTabs, type RunFacts } from "./runTabs";

const plain: RunFacts = { output: "diff", agents: false, visual: false, voice: false, gpu: false, services: false };

describe("hiddenRunTabs", () => {
	it("keeps overview, timeline, output, security and evidence for a plain diff run", () => {
		expect([...hiddenRunTabs(plain)].sort()).toEqual(["agents", "gpu", "services", "visual", "voice"]);
	});
	it("hides security for a run that produced no diff", () => {
		for (const output of ["answer", "state", "artifact"] as const) expect(hiddenRunTabs({ ...plain, output }).has("security")).toBe(true);
	});
	it("shows each evidence tab when the run recorded it", () => {
		expect([...hiddenRunTabs({ output: "diff", agents: true, visual: true, voice: true, gpu: true, services: true })]).toEqual([]);
	});
});