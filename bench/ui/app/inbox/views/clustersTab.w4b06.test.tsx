import { describe, expect, it } from "vitest";
import { clusterExperimentOptions } from "./clustersTab";

describe("clusterExperimentOptions", () => {
	const ids = ["e_mine_ab12", "e_admit_zz_test_qa_4kha", "e_replay_r_abc123_9xyz"];
	it("options hide e_admit_ and e_replay_", () => {
		expect(clusterExperimentOptions(ids, false)).toEqual({ options: ["e_mine_ab12"], hiddenCount: 2 });
	});
	it("lists them all when the box is ticked", () => {
		expect(clusterExperimentOptions(ids, true)).toEqual({ options: ids, hiddenCount: 2 });
	});
});