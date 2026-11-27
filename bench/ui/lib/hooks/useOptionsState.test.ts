import { describe, expect, it } from "vitest";
import { optionsState } from "@/lib/hooks/useOptionsState";

describe("optionsState (X-107)", () => {
	it("reports a failed query as error, not empty", () => {
		expect(optionsState({ isError: true, data: undefined })).toBe("error");
		expect(optionsState({ isError: true, data: [] })).toBe("error");
	});
	it("orders loading, empty and ready", () => {
		expect(optionsState({ isLoading: true })).toBe("loading");
		expect(optionsState({ isFetching: true })).toBe("loading");
		expect(optionsState({ data: [] })).toBe("empty");
		expect(optionsState({ data: { items: [1] } })).toBe("ready");
		expect(optionsState({ data: {} }, 3)).toBe("ready");
	});
});
