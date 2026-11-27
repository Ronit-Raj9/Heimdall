import { describe, expect, it } from "vitest";
import { MAX_REPLAY_SESSIONS, modelText, replayProblems, type ReplayForm } from "./replaysView";

const ok: ReplayForm = {
	source: "prod",
	window: "7d",
	sessions: "20",
	onlyFailed: false,
	mode: "fixed",
	candidate: "openai/gpt-4o-mini",
	judge: "",
	cap: "2",
	yes: true,
};

describe("shadow replay start (M5-56)", () => {
	it("is ready when every field is set and the switch is on", () => {
		expect(replayProblems(ok, { enabled: true })).toEqual([]);
	});
	it("lists every reason in form order", () => {
		expect(replayProblems({ ...ok, source: "", candidate: "x", sessions: "0", cap: "0", yes: false }, { enabled: false })).toEqual([
			"needOn",
			"needSource",
			"needModel",
			"needSessions",
			"needCap",
			"needConsent",
		]);
	});
	it("checks sessions and cap against the org limits", () => {
		expect(replayProblems({ ...ok, sessions: String(MAX_REPLAY_SESSIONS + 1) }, { enabled: true })).toEqual(["needSessions"]);
		expect(replayProblems({ ...ok, sessions: "51" }, { enabled: true, maxSessions: 50 })).toEqual(["needSessions"]);
		expect(replayProblems({ ...ok, sessions: "2.5" }, { enabled: true })).toEqual(["needSessions"]);
		expect(replayProblems({ ...ok, cap: "11" }, { enabled: true, maxUsd: 10 })).toEqual(["needCap"]);
	});
	it("writes the picked model as provider/model", () => {
		expect(modelText({ provider: "openai", model: "gpt-4o-mini" })).toBe("openai/gpt-4o-mini");
		expect(modelText({ provider: "", model: "m" })).toBe("m");
	});
});
