import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { roundUrl, studyPath } from "@/lib/store/apis/improveStudyApi";
import type { StudyView } from "@/lib/types/improvementStudy";
import { describe, expect, it } from "vitest";
import {
	type ImprovementForm,
	NewImprovementForm,
	createBlock,
	improvementConfirmText,
	improvementFormProblems,
	improvementSpec,
	newForm,
	presetPatch,
	studyLimitText,
} from "./newImprovementDialog";
import { studyVerdictLine } from "./studiesTab";
import i18n from "@/lib/i18n";

// Round 5 lane L7: studies from the create form, the Bifrost prompt source, the chain check.
const filled: ImprovementForm = { ...newForm(), name: "s-study", agent: "a", suite: "hd-harbor-bfcl@1.0.0" };
const props = {
	onChange: () => undefined,
	onSubmit: () => undefined,
	onCancel: () => undefined,
	agents: ["a"],
	suites: ["hd-harbor-bfcl@1.0.0"],
};

/** The exact body the form posts to /improvement-studies for Small with 3 loops and $12 (handlers test parses the same JSON). */
export const WEB_STUDY_BODY =
	'{"kind":"improvement","name":"s-study","agent":"agents/a.yaml","suite":"hd-harbor-bfcl@1.0.0","preset":"small",' +
	'"editable":{"instructions":true,"files":[]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit"},"budget":{"total_usd":12},' +
	'"replicate":{"loops":3}}';

describe("study from the create form", () => {
	const study = { ...filled, ...presetPatch(filled, "small"), limit: "12", loops: "3" };

	it("sends replicate.loops only for 2 or more loops", () => {
		expect(JSON.stringify(improvementSpec(study))).toBe(WEB_STUDY_BODY);
		expect(improvementSpec({ ...study, loops: "1" }).replicate).toBeUndefined();
	});

	it("keeps a trial to one loop and refuses 6", () => {
		expect(presetPatch(study, "trial").loops).toBe("1");
		expect(improvementFormProblems({ ...study, loops: "6" })).toContain("loops");
		expect(createBlock({ ...study, loops: "6" }, undefined, false)).toBe("Loops must be 1 to 5. A trial runs one loop.");
		// A draft saved before the field existed still works as one loop.
		const { loops: _drop, ...old } = study;
		expect(improvementFormProblems(old as ImprovementForm)).not.toContain("loops");
	});

	it("confirms with the shared limit and each loop's share", () => {
		expect(studyLimitText(12, 3)).toBe("Total for 3 loops: at most $12.00 ($4.00 for each loop).");
		expect(improvementConfirmText(study, undefined, "")).toContain("Total for 3 loops: at most $12.00 ($4.00 for each loop).");
		const html = render(<NewImprovementForm {...props} form={study} />);
		expect(html).toContain("Start study");
		expect(html).toContain('data-testid="new-improvement-study-limit"');
	});

	it("offers the Bifrost prompt source disabled, with what is missing", () => {
		const html = render(<NewImprovementForm {...props} form={filled} />);
		expect(html).toMatch(/disabled=""[^>]*data-testid="new-improvement-source-prompt"/);
		expect(html).toContain("Not built on this server yet");
		expect(html).not.toContain('data-testid="new-improvement-loops"');
	});
});

describe("study routes and result", () => {
	it("builds the paths the handlers read", () => {
		expect(studyPath("s-1")).toBe("/improvement-studies/s-1");
		expect(roundUrl("imp-1", 2)).toContain("/api/bench/improvements/imp-1/rounds/2");
	});

	it("says the verdict only when the study has one", () => {
		const t = (k: string, o?: Record<string, unknown>): string => String(i18n.t(k as never, { ns: "ufl7", ...o } as never));
		const base = {
			id: "s",
			name: "s",
			agent: "a",
			suite: "x",
			status: "running",
			reason: "",
			loops: [],
			loops_max: 3,
			held_out: 20,
			created_at: "",
			updated_at: "",
			created_by: "",
			cost_usd: 0,
			running: true,
		} as StudyView;
		expect(studyVerdictLine(t, base)).toBeUndefined();
		const done = {
			...base,
			final: {
				state: "claimable",
				headline: "Better.",
				n: 20,
				n_nonzero: 5,
				gain: 0.05,
				ci95: [0.01, 0.09] as [number, number],
				p_upper: 0.01,
				loop_gains: [],
				better: 2,
				loops: 3,
			},
		};
		expect(studyVerdictLine(t, done)).toBe("Better. Gain +5.0 pts (95% range +1.0 pts to +9.0 pts), 2 of 3 loops better");
	});
});