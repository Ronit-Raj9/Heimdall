import "@/lib/i18n";

import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import {
	CALIBRATION_LABELS_FIXTURE,
	CALIBRATION_STATUS_FIXTURE,
	CALIBRATION_UNCALIBRATED_FIXTURE,
	VERDICT_CALIBRATION_FIXTURE,
} from "./calibration.fixtures";
import { EMPTY_LABEL_FORM, canLabel, formatRate, fprBound, labelFormErrors, labelRequestFrom, stateTone } from "./calibration.utils";
import { CalibrationCard, ContestedBadge, LabelForm, LabelsTable, VerdictCalibration } from "./calibrationPanel";

const noop = () => {};

/** The opening tag of the element with the test id. */
function tag(html: string, testId: string): string {
	const match = html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`));
	expect(match, testId).not.toBeNull();
	return match![0];
}

describe("calibration utils", () => {
	it("formats rates with their exact interval and says n/a when nothing was measured", () => {
		expect(formatRate({ k: 27, n: 30, value: 0.9, lo: 0.7347, hi: 0.9789 })).toBe("0.900 [0.735, 0.979] (27/30)");
		expect(formatRate({ k: 0, n: 0, value: null, lo: null, hi: null })).toBe("n/a (0/0)");
		expect(formatRate(undefined)).toBe("n/a");
		expect(fprBound({ k: 1, n: 18, value: 1 / 18, lo: 0.0014, hi: 0.2729 })).toBe("27.3%");
		expect(fprBound(null)).toBe("n/a");
	});

	it("rates only a calibrated judge as good", () => {
		expect(stateTone("calibrated")).toBe("good");
		expect(stateTone("few_labels")).toBe("warn");
		expect(stateTone("allowed_uncalibrated")).toBe("warn");
		expect(stateTone("uncalibrated")).toBe("bad");
		expect(stateTone("unreliable")).toBe("bad");
		expect(stateTone("not_judged")).toBe("neutral");
	});

	it("needs task, answer, label and labeller, and sends a trimmed body", () => {
		expect(labelFormErrors(EMPTY_LABEL_FORM)).toEqual({ task: true, answer: true, label: true, labeller: true });
		expect(labelRequestFrom(EMPTY_LABEL_FORM)).toBeNull();
		expect(labelRequestFrom({ task: " q1 ", answer: "Max", label: "correct", labeller: " ana ", note: "  " })).toEqual({
			task: "q1",
			answer: "Max",
			label: "correct",
			labeller: "ana",
		});
		expect(labelRequestFrom({ task: "q1", answer: "Rex", label: "incorrect", labeller: "ana", note: "wrong dog" })?.note).toBe("wrong dog");
		expect(canLabel("live")).toBe(true);
		expect(canLabel("recorded")).toBe(false);
		expect(canLabel(undefined)).toBe(false);
	});
});

describe("CalibrationCard", () => {
	it("shows state, agreement, kappa, the false-positive bound, set size, date and reminders", () => {
		const html = renderToStaticMarkup(<CalibrationCard status={CALIBRATION_STATUS_FIXTURE} />);
		expect(html).toContain("Calibrated");
		expect(html).toContain("0.950 [0.831, 0.994] (38/40)");
		expect(html).toContain("0.90");
		expect(html).toContain("27.3%");
		expect(html).toContain("40 human-labelled, 6 seeds, 0 disputed (46 rows)");
		expect(html).toContain("2026-11-17 (cr_7k2m9q4w1z)");
		expect(tag(html, "calibration-reminders")).toBeTruthy();
		expect(html).toContain("heimdall judge calibrate longmem-s");
	});

	it("says never and not calibrated without a record", () => {
		const html = renderToStaticMarkup(<CalibrationCard status={CALIBRATION_UNCALIBRATED_FIXTURE} />);
		expect(html).toContain("Not calibrated");
		expect(html).toContain("never");
		expect(html).toContain("n/a");
		expect(html).not.toContain('data-testid="calibration-reminders"');
	});
});

describe("VerdictCalibration and ContestedBadge", () => {
	it("shows the cap, the contested runs and raw beside corrected rates", () => {
		const html = renderToStaticMarkup(<VerdictCalibration jc={VERDICT_CALIBRATION_FIXTURE} />);
		expect(html).toContain("capped from IMPROVED");
		expect(html).toContain("2 contested");
		expect(tag(html, "calibration-bias-A")).toBeTruthy();
		expect(html).toContain("0.750 [0.428, 0.945] (9/12)");
		expect(html).toContain("never replaces the verdict");
	});

	it("renders nothing for no contested run", () => {
		expect(renderToStaticMarkup(<ContestedBadge count={0} />)).toBe("");
		expect(renderToStaticMarkup(<ContestedBadge count={1} />)).toContain("1 contested");
	});
});

describe("LabelForm and LabelsTable", () => {
	it("offers every task and is writable in live mode only", () => {
		const live = renderToStaticMarkup(<LabelForm tasks={["q1", "q2"]} mode="live" onSubmit={noop} />);
		expect(live).toContain('<option value="q1">q1</option>');
		expect(tag(live, "calibration-label-submit")).not.toContain('disabled=""');
		expect(live).not.toContain('data-testid="calibration-label-readonly"');
		const recorded = renderToStaticMarkup(<LabelForm tasks={["q1"]} mode="recorded" onSubmit={noop} />);
		expect(tag(recorded, "calibration-label-submit")).toContain('disabled=""');
		expect(recorded).toContain("Labels are written only on a live server");
	});

	it("lists labels newest first and marks admission-control seeds as weak", () => {
		const html = renderToStaticMarkup(<LabelsTable items={CALIBRATION_LABELS_FIXTURE.items} />);
		expect(html.indexOf("Your dog is called Rex.")).toBeLessThan(html.indexOf(">Max<"));
		expect(html).toContain("seed (weak)");
		expect(html).toContain("Review");
		expect(renderToStaticMarkup(<LabelsTable items={[]} />)).toContain("No labels yet.");
	});
});

// The Go side writes one key list per type (bench/handlers/testdata/contract/<Type>.keys): one key
// a line, "?" for an optional key. The fixtures carry exactly the listed keys.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

function keys(type: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l !== "" && !l.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function problems(where: string, obj: object, type: string): string[] {
	const { required, optional } = keys(type);
	const have = Object.keys(obj);
	return [
		...required.filter((k) => !have.includes(k)).map((k) => `${where}: missing ${k}`),
		...have.filter((k) => !required.includes(k) && !optional.includes(k)).map((k) => `${where}: extra ${k}`),
	];
}

describe("calibration fixtures follow the Go contract", () => {
	it("match the key lists", () => {
		const s = CALIBRATION_STATUS_FIXTURE;
		const out = [
			...problems("status", s, "CalibrationStatus"),
			...problems("uncalibrated", CALIBRATION_UNCALIBRATED_FIXTURE, "CalibrationStatus"),
			...problems("latest", s.latest!, "CalibrationRecord"),
			...problems("latest.human", s.latest!.human, "CalibrationStats"),
			...problems("latest.human.fpr", s.latest!.human.fpr, "CalibrationRate"),
			...problems("thresholds", s.thresholds, "JudgeCalibrationThresholds"),
			...problems("labels", CALIBRATION_LABELS_FIXTURE, "CalibrationLabels"),
			...CALIBRATION_LABELS_FIXTURE.items.flatMap((it, i) => problems(`labels.items[${i}]`, it, "CalibrationItem")),
			...problems("verdict", VERDICT_CALIBRATION_FIXTURE, "JudgeCalibrationVerdict"),
			...problems("bias A", VERDICT_CALIBRATION_FIXTURE.bias_adjusted!.A, "BiasAdjustedRate"),
		];
		expect(out).toEqual([]);
	});
});