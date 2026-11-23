import { describe, expect, it } from "vitest";
import { runFaultLabel } from "./runsTable";

// N291: the run list names every fault from the one fault catalog, never a raw id.
describe("run list fault labels", () => {
	it("uses the fault catalog names", () => {
		expect(runFaultLabel("model-down")).toBe("Model goes down");
		expect(runFaultLabel("heimdall-model-brownout")).toBe("Model partly down");
		expect(runFaultLabel("broken-tool")).toBe("Broken tool");
	});
	it("reads an unknown fault in sentence case, without the product prefix", () => {
		expect(runFaultLabel("heimdall-primary-outage")).toBe("Primary outage");
		expect(runFaultLabel("p_outage")).toBe("P outage");
	});
});