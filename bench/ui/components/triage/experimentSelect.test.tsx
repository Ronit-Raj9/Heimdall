import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { render, tagOf } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ExperimentSelect, defaultPickerExperiment, useExperimentPicker } from "./experimentSelect";

function exp(exp_id: string, name: string, at: string, extra: Partial<ExperimentStatus> = {}): ExperimentStatus {
	return {
		exp_id,
		name,
		state: "done",
		manifest_hash: "h",
		frozen_at: at,
		slots: { total: 2, done: 2, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "swe-lite@1.0.0",
		arms: ["A", "B"],
		...extra,
	};
}

const list = [
	exp("e_admit_zz_test_rows_ab12", "admit-zz-test-rows", "2026-11-23T10:00:00Z", { suite: "zz-test-rows@1.0.0" }),
	exp("e_replay_r_n697kiewjt_x4qq", "replay r_n697kiewjt", "2026-11-23T09:00:00Z", { arms: ["A"] }),
	exp("e_real", "Prompt v2 vs v1", "2026-11-20T09:00:00Z"),
];

function Probe({ showHidden, value }: { showHidden: boolean; value?: string }) {
	const picker = useExperimentPicker(list, showHidden, new Map());
	return (
		<ExperimentSelect
			picker={picker}
			value={value ?? defaultPickerExperiment(picker.options, null)}
			onChange={() => undefined}
			showHidden={showHidden}
			onShowHidden={() => undefined}
			testId="review-experiment-picker"
		/>
	);
}

describe("ExperimentSelect (N5)", () => {
	it("defaults to the newest real comparison, not a newer check or debug run", () => {
		const html = render(<Probe showHidden={false} />);
		expect(html).toContain("Prompt v2 vs v1");
		expect(html).not.toContain("admit-zz-test-rows");
		expect(tagOf(html, "review-experiment-picker-show-hidden")).not.toContain("checked");
		expect(html).toContain("Also show checks, practice runs and debug runs (2)");
	});
	it("keeps a URL choice and lists hidden ones when the box is ticked", () => {
		const html = render(<Probe showHidden value="e_admit_zz_test_rows_ab12" />);
		expect(html).toContain("Readiness check for zz-test-rows");
		expect(tagOf(html, "review-experiment-picker-show-hidden")).toContain("checked");
	});
	it("picks the URL value first, else the first listed option", () => {
		const options = [{ id: "e_real", label: "x", secondary: "e_real" }];
		expect(defaultPickerExperiment(options, "e_other")).toBe("e_other");
		expect(defaultPickerExperiment(options, null)).toBe("e_real");
		expect(defaultPickerExperiment([], null)).toBeUndefined();
	});
});