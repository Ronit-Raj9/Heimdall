import "@/lib/i18n";

import i18n from "i18next";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { addScheduleMissing, afterSave, AlertsEditor } from "./schedulesView";

const asked = vi.hoisted(() => ({ buttons: [] as { testId: string; question: string; confirmLabel: string }[] }));
vi.mock("@/components/platform/badges", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/platform/badges")>()),
	ConfirmButton: (p: { testId: string; question: string; confirmLabel: string; label: string }) => {
		asked.buttons.push(p);
		return <button data-testid={p.testId}>{p.label}</button>;
	},
}));
vi.mock("@/lib/store/apis/schedulesApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/schedulesApi")>()),
	useTestAlertMutation: () => [() => Promise.resolve({}), { isLoading: false }],
}));

const t = i18n.getFixedT("en", "w4m7b");

describe("schedule form", () => {
	it("closes only after a save that worked", () => {
		expect(afterSave(true)).toBe(true);
		expect(afterSave(false)).toBe(false);
	});

	it("names the empty limit in evaluation mode", () => {
		expect(addScheduleMissing({ name: "n", cron: "0 6 * * *", max: "" }, "evaluation", "ev_1", t)).toEqual([
			"a daily spending limit above 0",
		]);
	});

	it("names every missing part and accepts an empty limit for a comparison file", () => {
		expect(addScheduleMissing({ name: "", cron: "", max: "" }, "experiment", "", t)).toEqual([
			"a name",
			"a comparison file",
			"when it runs",
		]);
		expect(addScheduleMissing({ name: "n", cron: "0 6 * * *", max: "" }, "experiment", "practice.yaml", t)).toEqual([]);
	});
});

describe("schedule alerts", () => {
	it("alert Remove asks first", () => {
		asked.buttons = [];
		renderToStaticMarkup(
			<AlertsEditor alerts={[{ url_env: "SLACK_URL" }]} events={[]} secrets={[]} busy={false} onChange={() => undefined} />,
		);
		const b = asked.buttons.find((x) => x.testId === "alert-remove-0");
		expect(b?.question).toBe("Remove the alert to SLACK_URL?");
		expect(b?.confirmLabel).toBe("Remove alert");
	});
});