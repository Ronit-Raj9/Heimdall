// Rendering and hook-state helpers for component tests, which render to static markup in Node.
import { LOCAL_SESSION, SessionContext, type SessionInfo } from "@/lib/session/sessionContext";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";

/** Static markup of node inside a session (local mode by default). */
export function render(node: ReactNode, session: SessionInfo = LOCAL_SESSION): string {
	return renderToStaticMarkup(<SessionContext.Provider value={session}>{node}</SessionContext.Provider>);
}

/** A settled RTK Query hook result with data. */
export function okQuery<T>(data: T) {
	return { data, currentData: data, error: undefined, isLoading: false, isFetching: false, isError: false, isSuccess: true, refetch: () => undefined };
}

/** An RTK Query hook result still loading. */
export function loadingQuery() {
	return {
		data: undefined,
		error: undefined,
		isLoading: true,
		isFetching: true,
		isError: false,
		isSuccess: false,
		refetch: () => undefined,
	};
}

/** A failed RTK Query hook result with the server's error body. */
export function errorQuery(status: number, message = "refused") {
	const error = { status, data: { error: { message } } };
	return { data: undefined, error, isLoading: false, isFetching: false, isError: true, isSuccess: false, refetch: () => undefined };
}

/** An RTK Query mutation hook result: the trigger and its state. */
export function mutation(state: Record<string, unknown> = {}) {
	const trigger = Object.assign(() => ({ unwrap: () => Promise.resolve(undefined) }), {});
	return [trigger, { isLoading: false, error: undefined, data: undefined, originalArgs: undefined, ...state }] as const;
}

/** The opening tag of the element carrying data-testid, for attribute checks. */
export function tagOf(html: string, testId: string): string | null {
	const match = html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`));
	return match ? match[0] : null;
}