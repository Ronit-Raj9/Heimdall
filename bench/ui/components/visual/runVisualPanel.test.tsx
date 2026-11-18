import "@/lib/i18n";

import { toRunVisual, visualImageUrl, type RunVisual } from "@/lib/store/apis/visualApi";
import type { RunResponse, VisualGrade } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { VisualPanel, formatVisualValue, maskStyle } from "./runVisualPanel";

const img = (path: string) => ({
	path,
	sha256: "a5524653712ff1d6ebc4d6b4aa217bf647b69a29d5fb9beb641f56fe0e2d7f1e",
	bytes: 2000,
	width: 800,
	height: 480,
});

const grade: VisualGrade = {
	capture: img("visual/capture.png"),
	reference: img("visual/reference.png"),
	diff: img("visual/diff.png"),
	source: {
		kind: "artifact",
		artifact: "artifacts/site/index.html",
		renderer: "mcr.microsoft.com/playwright/mcp:v0.0.83",
		width: 800,
		height: 480,
		scale: 1,
		fonts_sha256: "084ceece4237aa",
		format: "png",
	},
	masks: [{ x: 176, y: 246, w: 164, h: 24 }],
	checks: [
		{ id: "ssim", method: "ssim", value: 0.96903, threshold: 0.995, passed: false, detail: "mean SSIM over 23345 windows" },
		{
			id: "button-centred",
			method: "region",
			value: 0.000017,
			threshold: 0.02,
			passed: false,
			detail: "crop 148x50 found at (116,279), not at (326,279) within 2 px",
			found: { x: 116, y: 279 },
		},
		{ id: "button-blue", method: "colors", value: 0, threshold: 24, passed: true, detail: "<img src=x onerror=alert(1)>" },
	],
	passed: 1,
	score: 0.333333,
};

const data: RunVisual = {
	runId: "r_abc",
	sealed: true,
	passed: false,
	invalid: false,
	reason: "visual: 1 of 3 checks passed",
	visual: grade,
};

describe("VisualPanel", () => {
	it("shows the three images, the masks and every check's number", () => {
		const html = renderToStaticMarkup(<VisualPanel data={data} />);
		expect(html).toContain('data-testid="visual-panel-r_abc"');
		expect(html).toContain("1 of 3 checks passed");
		expect(html).toContain("/api/bench/runs/r_abc/visual/capture.png");
		expect(html).toContain("/api/bench/runs/r_abc/visual/reference.png");
		expect(html).toContain("/api/bench/runs/r_abc/visual/diff.png");
		expect(html).toContain('data-testid="visual-mask-capture.png-0"');
		expect(html).toContain("164x24 at (176,246)");
		expect(html).toContain('data-testid="visual-check-button-centred"');
		expect(html).toContain("0.96903");
		expect(html).toContain("not at (326,279)");
		expect(html).toContain("fonts 084ceece4237");
		expect(html).not.toContain('data-testid="visual-judge"');
	});

	it("renders check details inert", () => {
		const html = renderToStaticMarkup(<VisualPanel data={data} />);
		expect(html).not.toContain("<img src=x");
		expect(html).toContain("&lt;img src=x");
	});

	it("serves no image of an unsealed run", () => {
		const html = renderToStaticMarkup(<VisualPanel data={{ ...data, sealed: false }} />);
		expect(html).toContain('data-testid="visual-unsealed"');
		expect(html).not.toContain("/visual/capture.png");
	});

	it("labels an uncalibrated judge as reporting only, and a calibrated one with its record", () => {
		const judge = {
			model: "vision-model",
			prompt_version: "visual-v1",
			prompt_sha256: "x",
			rubric_sha256: "y",
			aggregate: "majority",
			votes: ["pass", "fail", "pass"],
			scores: [1, 0.5, 1],
			score: 0.833333,
			verdict: "pass",
			rationale: "Both bars are blue.",
			usd: { state: "observed" as const, v: 0.01 },
			decides: false,
			reason: "no calibration record for vision-model and visual-v1",
		};
		let html = renderToStaticMarkup(<VisualPanel data={{ ...data, visual: { ...grade, judge } }} />);
		expect(html).toContain('data-testid="visual-judged-badge"');
		expect(html).toContain("uncalibrated: reports, does not decide");
		expect(html).toContain("Both bars are blue.");
		expect(html).toContain("pass, fail, pass");
		html = renderToStaticMarkup(
			<VisualPanel
				data={{
					...data,
					visual: {
						...grade,
						judge: {
							...judge,
							decides: true,
							reason: undefined,
							calibration: "cr_visual1@abc",
							agreement: {
								items: 40,
								positives: 20,
								negatives: 20,
								tp: 18,
								fp: 2,
								fn: 2,
								tn: 18,
								undecided: 0,
								agreement: { k: 36, n: 40, value: 0.9, lo: 0.76, hi: 0.97 },
								tpr: { k: 18, n: 20, value: 0.9, lo: null, hi: null },
								tnr: { k: 18, n: 20, value: 0.9, lo: null, hi: null },
								fpr: { k: 2, n: 20, value: 0.1, lo: null, hi: null },
								kappa: 0.8,
							},
						},
					},
				}}
			/>,
		);
		expect(html).toContain("Calibration record cr_visual1@abc.");
		expect(html).toContain("Agreement with people 36 of 40 (0.9).");
	});

	it("narrows a run detail and escapes the run id", () => {
		const detail = {
			run: { run_id: "r_abc" },
			sealed: true,
			grade: { passed: true, invalid_evaluator: false, reason: "ok", visual: grade },
		} as unknown as RunResponse;
		expect(toRunVisual(detail).visual?.passed).toBe(1);
		expect(toRunVisual({ ...detail, grade: null } as RunResponse).visual).toBeNull();
		expect(visualImageUrl("r/1", "diff.png")).toBe("/api/bench/runs/r%2F1/visual/diff.png");
	});

	it("formats values and mask boxes", () => {
		expect(formatVisualValue(0.0000171)).toBe("0.000017");
		expect(formatVisualValue(24)).toBe("24");
		expect(maskStyle({ x: 80, y: 48, w: 160, h: 24 }, 800, 480)).toEqual({ left: "10%", top: "10%", width: "20%", height: "5%" });
	});
});