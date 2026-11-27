import { DRAFT_PREFIX, readDraft, storableDraft } from "@/lib/hooks/useFormDraft";
import { useEffect } from "react";

function session(): Storage | null {
	try {
		return typeof sessionStorage !== "undefined" ? sessionStorage : null;
	} catch {
		return null;
	}
}

/** The form's start values: a stored draft merged over the defaults (Q-150), else the defaults. */
export function draftOr<T>(key: string, defaults: T, storage: Pick<Storage, "getItem"> | null = session()): T {
	return readDraft(storage, key, defaults);
}

/** The keys a stored draft holds, so later defaults do not overwrite what the person typed before a reload. */
export function draftKeys(key: string, storage: Pick<Storage, "getItem"> | null = session()): string[] {
	try {
		const raw = storage?.getItem(DRAFT_PREFIX + key);
		const parsed = raw ? (JSON.parse(raw) as unknown) : null;
		return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? Object.keys(parsed) : [];
	} catch {
		return [];
	}
}

/**
 * Keeps a form's values as a draft in sessionStorage beside the leave guard (Q-150, X-97): written
 * while `dirty`, dropped once the form is done (`done`) or back to its defaults. Secret fields
 * (token, password, key) are never stored, as in useFormDraft.
 */
export function useDraftSync<T>(key: string, value: T, dirty: boolean, done: boolean): void {
	useEffect(() => {
		try {
			if (done || !dirty) session()?.removeItem(DRAFT_PREFIX + key);
			else session()?.setItem(DRAFT_PREFIX + key, JSON.stringify(storableDraft(value)));
		} catch {
			/* storage blocked: the form still works for this page */
		}
	}, [key, value, dirty, done]);
}