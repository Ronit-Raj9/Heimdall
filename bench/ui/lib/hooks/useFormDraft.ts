import { useCallback, useEffect, useRef, useState } from "react";

/** Every form draft lives in sessionStorage under this prefix, so it survives a reload, Back and the sign-in after a 401. */
export const DRAFT_PREFIX = "heimdall.draft.";

// Fields never written to storage: secrets stay in memory only.
const SECRET_FIELD = /token|password|secret|api_?key/i;

/** The draft without secret fields, for storage. */
export function storableDraft<T>(value: T): Partial<T> {
	if (!value || typeof value !== "object" || Array.isArray(value)) return value as Partial<T>;
	const out: Record<string, unknown> = {};
	for (const [k, v] of Object.entries(value as Record<string, unknown>)) if (!SECRET_FIELD.test(k)) out[k] = v;
	return out as Partial<T>;
}

/** The stored draft merged over `initial`, or `initial` when nothing usable is stored. */
export function readDraft<T>(storage: Pick<Storage, "getItem"> | null | undefined, key: string, initial: T): T {
	try {
		const raw = storage?.getItem(DRAFT_PREFIX + key);
		if (!raw) return initial;
		const parsed = JSON.parse(raw) as unknown;
		if (initial && typeof initial === "object" && !Array.isArray(initial)) {
			return parsed && typeof parsed === "object" ? { ...initial, ...(parsed as Partial<T>) } : initial;
		}
		return parsed as T;
	} catch {
		/* private window or bad JSON: start fresh */
		return initial;
	}
}

function session(): Storage | null {
	try {
		return typeof sessionStorage !== "undefined" ? sessionStorage : null;
	} catch {
		return null;
	}
}

/**
 * A form's typed input kept as a draft (X-97): `const [draft, update, clear] = useFormDraft("newExperiment", EMPTY)`.
 * update merges fields; clear drops the draft (call it after a successful submit). Fields whose
 * name says token, password, secret or API key are never stored.
 */
export function useFormDraft<T>(key: string, initial: T): [T, (patch: Partial<T>) => void, () => void] {
	const [draft, setDraft] = useState<T>(() => readDraft(session(), key, initial));
	const cleared = useRef(false);
	useEffect(() => {
		if (cleared.current) {
			cleared.current = false;
			return;
		}
		try {
			session()?.setItem(DRAFT_PREFIX + key, JSON.stringify(storableDraft(draft)));
		} catch {
			/* storage blocked: the draft still works for this page */
		}
	}, [key, draft]);
	const update = useCallback(
		(patch: Partial<T>) =>
			setDraft((d) => (d && typeof d === "object" && !Array.isArray(d) ? { ...d, ...patch } : (patch as T))),
		[],
	);
	const initialRef = useRef(initial);
	initialRef.current = initial;
	const clear = useCallback(() => {
		cleared.current = true;
		try {
			session()?.removeItem(DRAFT_PREFIX + key);
		} catch {
			/* nothing stored */
		}
		setDraft(initialRef.current);
	}, [key]);
	return [draft, update, clear];
}
