import { describe, expect, it } from "vitest";
import { queueSourceLine } from "./queueSource";

describe("queueSourceLine (M6-68)", () => {
	it("names the benchmark when the queue has one", () => {
		expect(queueSourceLine({ kind: "runs", benchmark: "support-v2" })).toEqual({ key: "source.runsOf", benchmark: "support-v2" });
		expect(queueSourceLine({ kind: "judge", benchmark: "support-v2" })).toEqual({ key: "source.judgeOf", benchmark: "support-v2" });
	});
	it("says only what is labelled without a benchmark", () => {
		expect(queueSourceLine({ kind: "runs" })).toEqual({ key: "source.runs" });
		expect(queueSourceLine({ kind: "judge", benchmark: " " })).toEqual({ key: "source.judge" });
	});
});
