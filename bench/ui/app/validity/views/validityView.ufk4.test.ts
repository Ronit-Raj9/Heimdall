import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { describe, expect, it } from "vitest";
import { scanFailure } from "./validityView";

// K4: the scan's error reaches the page in the server's words, not "Unexpected error".
describe("validity scan error", () => {
	it("keeps a failed job's sentence", () => {
		expect(getBenchErrorMessage(scanFailure("this server has no run index"))).toBe("This server has no run index");
	});
	it("strips the job prefixes of a refused start", () => {
		const refused = "create job: invalid job parameters: This server cannot read runs, so the scan cannot run.";
		expect(getBenchErrorMessage(scanFailure(refused))).toBe("This server cannot read runs, so the scan cannot run.");
	});
});