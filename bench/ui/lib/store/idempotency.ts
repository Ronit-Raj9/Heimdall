/**
 * One Idempotency-Key per submit (fix wave 3, N186). The server keeps the first 2xx answer of a
 * POST for 24 hours per caller, org, path and key; this module decides which key a POST carries:
 *
 * - An identical request (method, URL, body text) already in flight gets the same promise, so a
 *   double click sends one request.
 * - Otherwise the request carries the key kept for its identity, or a new one. The key is kept
 *   until an answer arrives that is not a network error, or for 10 minutes; after a network error
 *   it is kept, so a retry of the same request reuses it and the server replays the first answer
 *   instead of starting the work again.
 */

export const IDEMPOTENCY_HEADER = "Idempotency-Key";

/** How long a key waits for a retry after a network error. */
export const KEY_LIFE_MS = 10 * 60 * 1000;

/** What a send ended with: a network error keeps the key for a retry; anything else drops it. */
export type SendOutcome = "network" | "answered";

interface KeptKey {
	key: string;
	at: number;
}

const inFlight = new Map<string, Promise<unknown>>();
const kept = new Map<string, KeptKey>();

/** A new key: 32 hex characters (a UUID without dashes), within the server's 16 to 64 rule. */
export function newIdempotencyKey(): string {
	const c = globalThis.crypto;
	if (c && typeof c.randomUUID === "function") return c.randomUUID().replace(/-/g, "");
	let out = "";
	for (let i = 0; i < 32; i++) out += Math.floor(Math.random() * 16).toString(16);
	return out;
}

/** The identity two submits share when they are the same request. */
export function requestIdentity(method: string, url: string, body: string): string {
	return `${method.toUpperCase()} ${url}\n${body}`;
}

/**
 * Sends one request under its key. send gets the key to attach; outcome tells whether the answer
 * was a network error (the key is kept) or an answer (the key is dropped).
 */
export function sendIdempotent<T>(
	identity: string,
	send: (key: string) => Promise<T>,
	outcome: (result: T) => SendOutcome,
	now: () => number = Date.now,
): Promise<T> {
	const open = inFlight.get(identity);
	if (open) return open as Promise<T>;
	const t = now();
	let entry = kept.get(identity);
	if (!entry || t - entry.at > KEY_LIFE_MS) {
		entry = { key: newIdempotencyKey(), at: t };
		kept.set(identity, entry);
	}
	const key = entry.key;
	const p = (async () => {
		try {
			const result = await send(key);
			if (outcome(result) === "answered") kept.delete(identity);
			return result;
		} finally {
			inFlight.delete(identity);
		}
	})();
	inFlight.set(identity, p);
	return p;
}

/**
 * The body text of a JSON or empty body, or undefined for a body this module leaves alone
 * (multipart uploads, files, binary data, streams).
 */
export function jsonBodyText(body: unknown): string | undefined {
	if (body === undefined || body === null) return "";
	if (typeof body === "string") return body;
	if (typeof FormData !== "undefined" && body instanceof FormData) return undefined;
	if (typeof Blob !== "undefined" && body instanceof Blob) return undefined;
	if (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams) return undefined;
	if (typeof ReadableStream !== "undefined" && body instanceof ReadableStream) return undefined;
	if (body instanceof ArrayBuffer || ArrayBuffer.isView(body)) return undefined;
	if (typeof body === "object") {
		try {
			return JSON.stringify(body);
		} catch {
			return undefined;
		}
	}
	return String(body);
}

/** Test hook: forget every in-flight request and kept key. */
export function resetIdempotencyForTests(): void {
	inFlight.clear();
	kept.clear();
}