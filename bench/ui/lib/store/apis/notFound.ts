/**
 * True when a failed RTK Query request answered 404 (fix wave 2, N128). The same test as the
 * private 404 check in benchApi.ts (isWorkersOff), kept here as a plain helper so screens can tell
 * "this does not exist" from "this failed" without importing from benchApi.ts.
 */
export function isNotFoundError(error: unknown): boolean {
	return Boolean(error && typeof error === "object" && "status" in error && (error as { status: unknown }).status === 404);
}