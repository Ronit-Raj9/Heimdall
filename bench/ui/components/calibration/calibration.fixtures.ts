import type { CalibrationLabels, CalibrationRate, CalibrationStats, CalibrationStatus, JudgeCalibrationVerdict } from "@/lib/types/bench";

// W8 judge calibration: fixtures shaped exactly like the Go responses (the contract test checks
// their keys against bench/handlers/testdata/contract).

function rate(k: number, n: number, lo: number, hi: number): CalibrationRate {
	return { k, n, value: n === 0 ? null : k / n, lo, hi };
}

const HUMAN: CalibrationStats = {
	items: 40,
	positives: 22,
	negatives: 18,
	tp: 21,
	fp: 1,
	fn: 1,
	tn: 17,
	undecided: 0,
	agreement: rate(38, 40, 0.8308, 0.9939),
	tpr: rate(21, 22, 0.7716, 0.9988),
	tnr: rate(17, 18, 0.7271, 0.9986),
	fpr: rate(1, 18, 0.0014, 0.2729),
	kappa: 0.8989,
};

export const CALIBRATION_STATUS_FIXTURE: CalibrationStatus = {
	suite: "longmem-s",
	judged: true,
	provider: "openai",
	model: "gpt-4o-2024-08-06",
	prompt_version: "longmemeval-v1",
	prompt_sha256: "4d2c3ab1e0f7aa31c0de9bb1f1e2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4",
	state: "calibrated",
	detail:
		"agreement 0.950 [0.831, 0.994] (38/40) (lower bound must be at least 0.75), TNR 0.944 [0.727, 0.999] (17/18) (lower bound must be at least 0.60), false-positive rate at most 27.3%, kappa 0.90",
	set_sha256: "9a1f0c3e5b7d2a4c6e8f0a1b3c5d7e9f1a3b5c7d9e1f3a5b7c9d1e3f5a7b9c1d",
	labels: 46,
	human_labels: 40,
	seed_items: 6,
	disputed: 0,
	latest: {
		record_id: "cr_7k2m9q4w1z",
		suite: "longmem-s",
		suite_version: "1.0.0",
		provider: "openai",
		model: "gpt-4o-2024-08-06",
		prompt_version: "longmemeval-v1",
		prompt_sha256: "4d2c3ab1e0f7aa31c0de9bb1f1e2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4",
		templates: { "longmemeval-v1-standard": "aa11", "longmemeval-v1-temporal": "bb22" },
		votes: 3,
		served_models: ["gpt-4o-2024-08-06"],
		set_sha256: "9a1f0c3e5b7d2a4c6e8f0a1b3c5d7e9f1a3b5c7d9e1f3a5b7c9d1e3f5a7b9c1d",
		set_rows: 46,
		human_labels: 40,
		seed_items: 6,
		disputed: 0,
		labellers: ["ana", "bo"],
		human: HUMAN,
		consistency: { votes: 3, unanimous_items: rate(44, 46, 0.8516, 0.9947), repeat_agreement: rate(45, 46, 0.8847, 0.9994) },
		perturbations: [
			{ name: "whitespace", flips: rate(0, 46, 0, 0.0771) },
			{ name: "preamble", flips: rate(2, 46, 0.0053, 0.1484) },
		],
		items: null,
		calls: 552,
		usd: { state: "observed", v: 0.41, evidence: "gateway" },
		price_ref: "pricing-snapshot@sha256:1f2e",
		created_at: "2026-11-17T10:12:00Z",
		prev_hash: "",
		hash: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00",
	},
	records: 1,
	thresholds: { min_agreement: 0.75, min_tnr: 0.6, min_labels: 30, max_age_days: 90, source: "defaults" },
	reminders: ["labels changed since calibration cr_7k2m9q4w1z: rerun judge calibrate to measure the judge on them"],
	problems: [],
};

export const CALIBRATION_UNCALIBRATED_FIXTURE: CalibrationStatus = {
	suite: "locomo",
	judged: true,
	provider: "openai",
	model: "gpt-4o-mini",
	prompt_version: "qa-v1",
	prompt_sha256: "77aa",
	state: "uncalibrated",
	detail:
		"no calibration record for judge gpt-4o-mini with prompt qa-v1; judged verdicts are capped at INCONCLUSIVE until `judge calibrate locomo` runs",
	labels: 0,
	human_labels: 0,
	seed_items: 0,
	disputed: 0,
	records: 0,
	thresholds: { min_agreement: 0.75, min_tnr: 0.6, min_labels: 30, max_age_days: 90, source: "defaults" },
	reminders: [],
	problems: [],
};

export const CALIBRATION_LABELS_FIXTURE: CalibrationLabels = {
	suite: "longmem-s",
	set_sha256: "9a1f0c3e5b7d2a4c6e8f0a1b3c5d7e9f1a3b5c7d9e1f3a5b7c9d1e3f5a7b9c1d",
	items: [
		{
			id: "0f3a9c1b2d4e6f80",
			task: "q-017",
			answer: "Max",
			label: "correct",
			labeller: "suite admission controls",
			note: "expected answer (admission control, weak seed)",
			source: "admission_control",
			ref: "answer.yaml",
			at: "2026-11-16T09:00:00Z",
		},
		{
			id: "a1b2c3d4e5f60718",
			task: "q-017",
			answer: "Your dog is called Rex.",
			label: "incorrect",
			labeller: "ana",
			source: "human_review",
			ref: "rv_k2m9q4w1z0",
			at: "2026-11-17T08:00:00Z",
		},
	],
};

export const VERDICT_CALIBRATION_FIXTURE: JudgeCalibrationVerdict = {
	state: "uncalibrated",
	detail:
		"judge not calibrated: no calibration record for model gpt-4o-mini with the prompt the runs used (prompt 77aa); 2 of 24 judged runs contested (in the review queue)",
	model: "gpt-4o-mini",
	templates: ["77aa"],
	human_labels: 0,
	thresholds: { min_agreement: 0.75, min_tnr: 0.6, min_labels: 30, max_age_days: 90, source: "defaults" },
	judged_runs: 24,
	contested_runs: 2,
	capped_from: "improved",
	bias_adjusted: {
		A: {
			raw: rate(5, 12, 0.1517, 0.7233),
			estimate: null,
			lo: null,
			hi: null,
			note: "no calibration record with human labels, so the judged rate cannot be corrected",
		},
		B: {
			raw: rate(9, 12, 0.4281, 0.9451),
			estimate: null,
			lo: null,
			hi: null,
			note: "no calibration record with human labels, so the judged rate cannot be corrected",
		},
	},
};