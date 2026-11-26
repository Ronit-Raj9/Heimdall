import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { LeftOutNote, listsState } from "./step3Benchmarks";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("step 3 notes (w4m2)", () => {
	it("names each benchmark left out and why", () => {
		const html = render(<LeftOutNote items={[{ id: "voicebench", reason: "needs a voice agent" }]} />);
		expect(html).toContain("voicebench: needs a voice agent");
		expect(html).toContain("Left out: this agent cannot run one of the benchmarks you picked.");
	});
	it("renders nothing when nothing was left out", () => {
		expect(render(<LeftOutNote items={[]} />)).toBe("");
	});
	it("reads an errored list as an error, not as empty", () => {
		expect(listsState({ isError: false, isLoading: false }, { isError: true, isLoading: false })).toBe("error");
		expect(listsState({ isError: false, isLoading: true }, { isError: false, isLoading: false })).toBe("loading");
		expect(listsState({ isError: false, isLoading: false }, { isError: false, isLoading: false })).toBe("ready");
	});
});