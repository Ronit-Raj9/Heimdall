import "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { MatrixBuilder, estimateWithOneRetry } from "./matrixBuilder";
import { defaultBuilderValues } from "./matrixBuilderSchema";

vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ testId }: { testId?: string }) => <div data-testid={testId} />,
}));
vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	usePlanExperimentMutation: () => mutation(),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation({ isLoading: true }),
}));

const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;

describe("MatrixBuilder estimate", () => {
	it("retries once on 503", async () => {
		const waits: number[] = [];
		const wait = (ms: number) => {
			waits.push(ms);
			return Promise.resolve();
		};
		let calls = 0;
		const busy = () => {
			calls++;
			return calls === 1 ? Promise.reject({ status: 503 }) : Promise.resolve("ok");
		};
		await expect(estimateWithOneRetry(busy, wait)).resolves.toBe("ok");
		expect(calls).toBe(2);
		expect(waits).toEqual([5000]);

		calls = 0;
		const always = () => {
			calls++;
			return Promise.reject({ status: 503 });
		};
		await expect(estimateWithOneRetry(always, wait)).rejects.toEqual({ status: 503 });
		expect(calls).toBe(2);

		calls = 0;
		const broken = () => {
			calls++;
			return Promise.reject({ status: 500 });
		};
		await expect(estimateWithOneRetry(broken, wait)).rejects.toEqual({ status: 500 });
		expect(calls).toBe(1);
	});

	it("labels the Estimate button while it waits", () => {
		const html = render(
			<MatrixBuilder catalog={catalog} defaults={defaultBuilderValues(catalog, 1)} onStarted={() => undefined} />,
			hostedSession("member"),
		);
		expect(html).toMatch(/data-testid="builder-estimate"[\s\S]*?Estimating\.\.\./);
	});
});