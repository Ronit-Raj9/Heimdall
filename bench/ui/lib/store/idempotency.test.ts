import { afterEach, describe, expect, it } from "vitest";
import {
	jsonBodyText,
	KEY_LIFE_MS,
	newIdempotencyKey,
	requestIdentity,
	resetIdempotencyForTests,
	sendIdempotent,
	type SendOutcome,
} from "./idempotency";

type Answer = { status: number | "FETCH_ERROR"; key: string };

function fakeServer() {
	const keys: string[] = [];
	let fail = false;
	let release: (() => void) | null = null;
	const send = (key: string) =>
		new Promise<Answer>((resolve) => {
			keys.push(key);
			const answer = () => resolve({ status: fail ? "FETCH_ERROR" : 202, key });
			release = answer;
		});
	const outcome = (a: Answer): SendOutcome => (a.status === "FETCH_ERROR" ? "network" : "answered");
	return {
		keys,
		send,
		outcome,
		setFail(v: boolean) {
			fail = v;
		},
		answer() {
			release?.();
		},
	};
}

const id = requestIdentity("POST", "/evaluations", JSON.stringify({ name: "x" }));

afterEach(() => resetIdempotencyForTests());

describe("idempotent POST", () => {
	it("a double call sends one request", async () => {
		const s = fakeServer();
		const a = sendIdempotent(id, s.send, s.outcome);
		const b = sendIdempotent(id, s.send, s.outcome);
		expect(b).toBe(a);
		s.answer();
		expect(await a).toEqual(await b);
		expect(s.keys).toHaveLength(1);
		expect(s.keys[0]).toMatch(/^[A-Za-z0-9_-]{16,64}$/);
	});

	it("a retry after a network error sends the same key", async () => {
		const s = fakeServer();
		s.setFail(true);
		const a = sendIdempotent(id, s.send, s.outcome);
		s.answer();
		expect((await a).status).toBe("FETCH_ERROR");
		s.setFail(false);
		const b = sendIdempotent(id, s.send, s.outcome);
		s.answer();
		await b;
		expect(s.keys).toHaveLength(2);
		expect(s.keys[1]).toBe(s.keys[0]);
	});

	it("a new call after a 2xx answer gets a new key", async () => {
		const s = fakeServer();
		const a = sendIdempotent(id, s.send, s.outcome);
		s.answer();
		await a;
		const b = sendIdempotent(id, s.send, s.outcome);
		s.answer();
		await b;
		expect(s.keys).toHaveLength(2);
		expect(s.keys[1]).not.toBe(s.keys[0]);
	});

	it("a kept key expires after 10 minutes", async () => {
		const s = fakeServer();
		s.setFail(true);
		let t = 1_000;
		const a = sendIdempotent(id, s.send, s.outcome, () => t);
		s.answer();
		await a;
		t += KEY_LIFE_MS + 1;
		s.setFail(false);
		const b = sendIdempotent(id, s.send, s.outcome, () => t);
		s.answer();
		await b;
		expect(s.keys[1]).not.toBe(s.keys[0]);
	});

	it("another body is another request", async () => {
		const s = fakeServer();
		const a = sendIdempotent(id, s.send, s.outcome);
		const b = sendIdempotent(requestIdentity("POST", "/evaluations", "{}"), s.send, s.outcome);
		expect(b).not.toBe(a);
		expect(s.keys).toHaveLength(2);
	});

	it("keys are 32 letters and digits; uploads are left alone", () => {
		expect(newIdempotencyKey()).toMatch(/^[a-f0-9]{32}$/);
		expect(jsonBodyText(undefined)).toBe("");
		expect(jsonBodyText({ a: 1 })).toBe('{"a":1}');
		expect(jsonBodyText(new FormData())).toBeUndefined();
		expect(jsonBodyText(new Blob(["x"]))).toBeUndefined();
	});
});