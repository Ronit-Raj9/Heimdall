import "@/lib/i18n";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { RunningCardBody } from "./runningExperiments";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fixtures = path.resolve(__dirname, "../../public/fixtures");
const { status } = JSON.parse(fs.readFileSync(path.join(fixtures, "experiments.events.e_synthetic-nightly.json"), "utf8")) as {
	status: ExperimentStatus;
};

describe("RunningCardBody image stage", () => {
	it("shows the image stage in plain words", () => {
		const event = { kind: "stage", stage: "image", ts: "2026-11-27T10:00:00Z" } as unknown as LiveEvent;
		const html = render(<RunningCardBody status={status} events={[event]} />);
		expect(html).toContain("Preparing the container image");
		expect(html).not.toContain(">image<");
	});
});