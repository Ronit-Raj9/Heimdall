import { CALIBRATION_LABELS_FIXTURE, CALIBRATION_STATUS_FIXTURE } from "@/components/calibration/calibration.fixtures";
import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi, saveWriteToken } from "./benchApi";
import { calibrationApi, calibrationLabelRequest, calibrationPath } from "./calibrationApi";

// W8 judge calibration: the calibration endpoints on the wire.

describe("calibration requests", () => {
	it("escapes the suite id and trims the label body", () => {
		expect(calibrationPath("longmem-s")).toBe("/suites/longmem-s/calibration");
		expect(calibrationPath("../x?y")).toBe("/suites/..%2Fx%3Fy/calibration");
		expect(calibrationLabelRequest("qa", { task: " q1 ", answer: "Max", label: "correct", labeller: " ana ", note: " " })).toEqual({
			url: "/suites/qa/calibration/labels",
			method: "POST",
			body: { task: "q1", answer: "Max", label: "correct", labeller: "ana" },
		});
	});
});

function memoryStorage(): Storage {
	const items = new Map<string, string>();
	return {
		get length() {
			return items.size;
		},
		clear: () => items.clear(),
		getItem: (key) => items.get(key) ?? null,
		key: (i) => [...items.keys()][i] ?? null,
		removeItem: (key) => void items.delete(key),
		setItem: (key, value) => void items.set(key, value),
	};
}

describe("calibration endpoints on the wire", () => {
	let requests: Request[];

	beforeEach(() => {
		requests = [];
		vi.stubGlobal("window", { location: { search: "" }, sessionStorage: memoryStorage() });
		const NodeRequest = globalThis.Request;
		vi.stubGlobal(
			"Request",
			class extends NodeRequest {
				constructor(input: RequestInfo | URL, init?: RequestInit) {
					super(typeof input === "string" ? new URL(input, "http://bench.test") : input, init);
				}
			},
		);
		vi.stubGlobal(
			"fetch",
			vi.fn(async (request: Request) => {
				requests.push(request);
				const url = new URL(request.url);
				const body = url.pathname.endsWith("/labels")
					? request.method === "POST"
						? { ...CALIBRATION_LABELS_FIXTURE.items[1], mode: "live" }
						: { ...CALIBRATION_LABELS_FIXTURE, mode: "live" }
					: { ...CALIBRATION_STATUS_FIXTURE, mode: "live" };
				return new Response(JSON.stringify(body), {
					status: request.method === "POST" ? 201 : 200,
					headers: { "Content-Type": "application/json" },
				});
			}),
		);
	});

	afterEach(() => {
		saveWriteToken("");
		vi.unstubAllGlobals();
	});

	const makeStore = () =>
		configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});

	it("reads without a token, posts a label with the write token, and refetches the card", async () => {
		saveWriteToken("s3cret");
		const store = makeStore();
		const card = store.dispatch(calibrationApi.endpoints.getCalibration.initiate("longmem-s"));
		const status = await card.unwrap();
		expect(status.state).toBe("calibrated");
		expect(new URL(requests[0].url).pathname).toBe("/api/bench/suites/longmem-s/calibration");
		expect(requests[0].headers.has("Authorization")).toBe(false);

		const item = await store
			.dispatch(
				calibrationApi.endpoints.addCalibrationLabel.initiate({
					suiteId: "longmem-s",
					body: { task: "q-017", answer: "Rex", label: "incorrect", labeller: "ana" },
				}),
			)
			.unwrap();
		expect(item.label).toBe("incorrect");
		const post = requests[1];
		expect(post.method).toBe("POST");
		expect(new URL(post.url).pathname).toBe("/api/bench/suites/longmem-s/calibration/labels");
		expect(post.headers.get("Authorization")).toBe("Bearer s3cret");
		expect(JSON.parse(await post.text())).toEqual({ task: "q-017", answer: "Rex", label: "incorrect", labeller: "ana" });
		await vi.waitFor(() => expect(requests.length).toBe(3));
		expect(new URL(requests[2].url).pathname).toBe("/api/bench/suites/longmem-s/calibration");
		card.unsubscribe();
	});
});