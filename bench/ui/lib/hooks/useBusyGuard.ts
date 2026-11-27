import { useCallback, useRef, useState } from "react";

/**
 * A guard against a double press (M7-73): run(fn) starts fn only when nothing is in flight, and
 * busy stays true until it settles. The ref check holds even before React re-renders, so two clicks
 * in the same frame start one mutation.
 *   const { busy, run } = useBusyGuard();
 *   <Button isLoading={busy} onClick={() => run(() => save(body).unwrap())}>Save</Button>
 */
export function createBusyGuard(onChange: (busy: boolean) => void) {
	let inFlight = false;
	return {
		/** Resolves to fn's result, or undefined when a call was already running. */
		async run<T>(fn: () => T | Promise<T>): Promise<T | undefined> {
			if (inFlight) return undefined;
			inFlight = true;
			onChange(true);
			try {
				return await fn();
			} finally {
				inFlight = false;
				onChange(false);
			}
		},
		isBusy: () => inFlight,
	};
}

export function useBusyGuard(): { busy: boolean; run: <T>(fn: () => T | Promise<T>) => Promise<T | undefined> } {
	const [busy, setBusy] = useState(false);
	const guard = useRef<ReturnType<typeof createBusyGuard> | null>(null);
	if (!guard.current) guard.current = createBusyGuard(setBusy);
	const run = useCallback(<T>(fn: () => T | Promise<T>) => guard.current!.run(fn), []);
	return { busy, run };
}