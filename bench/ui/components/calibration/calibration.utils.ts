import type { CalibrationLabel, CalibrationLabelRequest, CalibrationRate, CalibrationState, Mode } from "@/lib/types/bench";

// W8 judge calibration (docs/judge-calibration.md): pure helpers of the calibration page.

/** "0.900 [0.735, 0.979] (27/30)", or "n/a (0/0)" when nothing was measured. */
export function formatRate(r: CalibrationRate | undefined | null): string {
	if (!r) return "n/a";
	if (r.value === null || r.lo === null || r.hi === null) return `n/a (${r.k}/${r.n})`;
	return `${r.value.toFixed(3)} [${r.lo.toFixed(3)}, ${r.hi.toFixed(3)}] (${r.k}/${r.n})`;
}

/** The upper end of the false-positive rate's interval as a percentage, "n/a" when not measured. */
export function fprBound(r: CalibrationRate | undefined | null): string {
	if (!r || r.hi === null) return "n/a";
	return `${(100 * r.hi).toFixed(1)}%`;
}

/** Badge tone of a calibration state: only a calibrated judge is good. */
export function stateTone(state: CalibrationState): "good" | "warn" | "bad" | "neutral" {
	switch (state) {
		case "calibrated":
			return "good";
		case "calibrated_on_controls":
		case "few_labels":
		case "allowed_uncalibrated":
			return "warn";
		case "unreliable":
		case "uncalibrated":
			return "bad";
		default:
			return "neutral";
	}
}

/** Labels can be written only on a live server, like reviews. */
export function canLabel(mode: Mode | undefined): boolean {
	return mode === "live";
}

export interface LabelFormValues {
	task: string;
	answer: string;
	label: CalibrationLabel | "";
	labeller: string;
	note: string;
}

export const EMPTY_LABEL_FORM: LabelFormValues = { task: "", answer: "", label: "", labeller: "", note: "" };

export type LabelFormErrors = Partial<Record<"task" | "answer" | "label" | "labeller", true>>;

/** What a label still lacks before it can be posted. */
export function labelFormErrors(v: LabelFormValues): LabelFormErrors {
	const errors: LabelFormErrors = {};
	if (v.task.trim() === "") errors.task = true;
	if (v.answer.trim() === "") errors.answer = true;
	if (v.label !== "correct" && v.label !== "incorrect") errors.label = true;
	if (v.labeller.trim() === "") errors.labeller = true;
	return errors;
}

/** The request body of a complete form; null while the form is incomplete. */
export function labelRequestFrom(v: LabelFormValues): CalibrationLabelRequest | null {
	if (Object.keys(labelFormErrors(v)).length > 0) return null;
	const req: CalibrationLabelRequest = {
		task: v.task.trim(),
		answer: v.answer,
		label: v.label as CalibrationLabel,
		labeller: v.labeller.trim(),
	};
	if (v.note.trim() !== "") req.note = v.note.trim();
	return req;
}