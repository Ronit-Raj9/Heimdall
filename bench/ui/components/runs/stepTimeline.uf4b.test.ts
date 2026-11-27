import { describe, expect, it } from "vitest";
import { observerReasonKey } from "./stepTimeline";

describe("observer reasons (M4-56)", () => {
	it("maps the container start failure to plain words and leaves others raw", () => {
		expect(observerReasonKey("observer exited before it was ready: docker: Error response from daemon")).toBe("observerStartFailed");
		expect(observerReasonKey("something else")).toBeNull();
	});
});