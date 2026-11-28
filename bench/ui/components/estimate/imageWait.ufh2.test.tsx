import "@/lib/i18n";

import { PreparedView } from "@/app/gateway/views/featureTestDialog";
import { estimateLines } from "@/app/evaluate/views/step5Launch";
import i18n from "@/lib/i18n";
import { render } from "@/lib/testing/render";
import type { Experiment, ExperimentPlan } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";

const t = (k: string, o?: Record<string, unknown>) => String(i18n.t(k, o));
const wait = { images: 5, minutes: 2, total_minutes: 7, up_to: true };
const plan = { errors: [], warnings: [], est_usd: { state: "observed", v: 0.4 }, est_wall_s: { state: "observed", v: 600 } } as unknown as ExperimentPlan;

describe("Task image wait", () => {
	it("the wizard's step 5 lines carry the wait from the one estimate answer", () => {
		const lines = estimateLines(t, { runs: 5, est_minutes: { low: 2, high: 4 }, image_wait: wait });
		expect(lines).toContain("First run in about 2 minutes: its task image is built first. Up to 5 task images, about 7 minutes in all.");
		expect(estimateLines(t, { runs: 5, est_minutes: { low: 2, high: 4 } }).join(" ")).not.toContain("task images");
	});

	it("the feature test form shows the wait under its estimate", () => {
		const draft = { experiment: {} as Experiment, notes: [], image_wait: wait };
		const html = render(<PreparedView prepared={{ draft, plan }} budget={1} confirming={false} />);
		expect(html).toContain("First run in about 2 minutes: its task image is built first. Up to 5 task images, about 7 minutes in all.");
		expect(render(<PreparedView prepared={{ draft: { ...draft, image_wait: null }, plan }} budget={1} confirming={false} />)).not.toContain("task images");
	});
});

describe("Building task images", () => {
	it("is the newest stage while no run has moved past it", async () => {
		const { imageBuilding } = await import("./imageWait");
		const leased = { kind: "stage", stage: "leased", message: "leased to remote worker w1 (attempt 1)" };
		const img = { kind: "stage", stage: "image", message: "Building agent image 1 of 5: task t01" };
		expect(imageBuilding([leased, img])).toEqual({ message: img.message });
		expect(imageBuilding([leased])).toBeNull();
		expect(imageBuilding([leased, img, { kind: "stage", stage: "run" }])).toBeNull();
	});
});
