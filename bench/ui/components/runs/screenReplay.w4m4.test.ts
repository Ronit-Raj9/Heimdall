import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/store/apis/framesApi", () => ({ frameImageUrl: () => "", useGetRunFramesQuery: () => ({ isLoading: false }) }));

const { playToggle } = await import("./screenReplay");

describe("playToggle", () => {
	it("restarts from the first frame when played on the last frame", () => {
		expect(playToggle(false, 9, 9)).toEqual({ playing: true, index: 0 });
	});
	it("pauses and resumes in place elsewhere", () => {
		expect(playToggle(true, 9, 9)).toEqual({ playing: false, index: 9 });
		expect(playToggle(false, 3, 9)).toEqual({ playing: true, index: 3 });
	});
});