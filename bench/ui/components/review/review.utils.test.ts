import type { Review, ReviewQueueItem } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	EMPTY_REVIEW_FORM,
	canWriteReviews,
	filterQueue,
	isReviewReason,
	reviewFormErrors,
	reviewRequestFrom,
	reviewState,
	shortReviewHash,
	tallyVotes,
} from "./review.utils";

const review = (id: string, decision: Review["decision"]): Review => ({
	review_id: id,
	exp_id: "e_x",
	run_id: "r_1",
	parent_seal: "sha256:seal",
	decision,
	reason: "because",
	reviewer: "maya",
	created_at: "2026-11-14T10:00:00Z",
	sealed_outcome: "fail",
	previous_outcome: "fail",
	prev_hash: "",
	hash: "abc",
});

const item = (runId: string, reasons: string[]): ReviewQueueItem => ({
	run_id: runId,
	exp_id: "e_x",
	pair_id: "p_1",
	task: "t01",
	arm: "A",
	condition: "clean",
	trial: 1,
	reasons,
	sealed_outcome: "fail",
	outcome: "fail",
	reviews: 0,
});

describe("reviewFormErrors", () => {
	const cases = [
		{ name: "empty form", values: EMPTY_REVIEW_FORM, want: ["decision", "reason", "reviewer"] },
		{
			name: "blank reason and reviewer",
			values: { decision: "pass", reason: "  ", reviewer: "\t", evidence: "" },
			want: ["reason", "reviewer"],
		},
		{ name: "complete", values: { decision: "flag_only", reason: "look", reviewer: "lee", evidence: "" }, want: [] },
	] as const;
	for (const c of cases) {
		it(c.name, () => {
			expect(reviewFormErrors({ ...c.values })).toEqual(c.want);
		});
	}
});

describe("reviewRequestFrom", () => {
	it("trims text and keeps non-empty evidence lines", () => {
		const req = reviewRequestFrom("r_1", "e_x", {
			decision: "fail",
			reason: " wrong ",
			reviewer: " maya ",
			evidence: "grade.json\n\n  workspace.diff \n",
		});
		expect(req).toEqual({
			exp_id: "e_x",
			run_id: "r_1",
			decision: "fail",
			reason: "wrong",
			reviewer: "maya",
			evidence: ["grade.json", "workspace.diff"],
		});
	});
	it("leaves out an empty evidence list and a missing experiment", () => {
		const req = reviewRequestFrom("r_1", undefined, { decision: "pass", reason: "ok", reviewer: "lee", evidence: " " });
		expect(req).toEqual({ run_id: "r_1", decision: "pass", reason: "ok", reviewer: "lee" });
	});
	it("refuses a form with no decision", () => {
		expect(() => reviewRequestFrom("r_1", "e_x", { ...EMPTY_REVIEW_FORM, reason: "x", reviewer: "y" })).toThrow();
	});
});

describe("canWriteReviews", () => {
	const cases = [
		{ mode: "live", want: true },
		{ mode: "recorded", want: false },
		{ mode: "synthetic", want: false },
		{ mode: undefined, want: false },
	] as const;
	for (const c of cases) {
		it(String(c.mode), () => {
			expect(canWriteReviews(c.mode)).toBe(c.want);
		});
	}
});

describe("filterQueue", () => {
	const items = [item("r_1", ["veto"]), item("r_2", ["split_votes", "flagged"])];
	it("keeps every item without a reason", () => {
		expect(filterQueue(items, null).map((i) => i.run_id)).toEqual(["r_1", "r_2"]);
	});
	it("keeps items with the reason", () => {
		expect(filterQueue(items, "flagged").map((i) => i.run_id)).toEqual(["r_2"]);
	});
});

describe("reviewState", () => {
	const current = review("rv_2", "pass");
	it("marks the review in effect", () => expect(reviewState(current, current)).toBe("current"));
	it("marks an older decision superseded", () => expect(reviewState(review("rv_1", "fail"), current)).toBe("superseded"));
	it("marks a flag", () => expect(reviewState(review("rv_3", "flag_only"), current)).toBe("flag"));
});

describe("small helpers", () => {
	it("tallies judge votes", () => {
		expect(tallyVotes(["correct", "incorrect", "correct", "unparsable", "garbled"])).toEqual({ correct: 2, incorrect: 1, unparsable: 2 });
		expect(tallyVotes(undefined)).toEqual({ correct: 0, incorrect: 0, unparsable: 0 });
	});
	it("knows the queue reasons", () => {
		expect(isReviewReason("veto")).toBe(true);
		expect(isReviewReason("something_new")).toBe(false);
	});
	it("shortens hashes", () => {
		expect(shortReviewHash("sha256:0123456789abcdef")).toBe("0123456789ab");
		expect(shortReviewHash("fedcba9876543210")).toBe("fedcba987654");
	});
});