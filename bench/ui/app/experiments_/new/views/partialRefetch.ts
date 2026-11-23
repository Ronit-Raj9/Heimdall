import "./hw17I18n";

import { useEffect, useRef, useState } from "react";

/** How often a partial suite list entry is read again, and at most how many times (N281). */
export const PARTIAL_REFETCH_MS = 5_000;
export const PARTIAL_REFETCH_MAX = 6;

/**
 * Reads the suite list again every 5 s, at most 6 times, while the entry the page needs is partial (a head entry
 * whose full summary is still loading on the server, so its probes and tasks are not listed yet).
 */
export function usePartialSuiteRefetch(partial: boolean, refetch: () => unknown): void {
	const [tries, setTries] = useState(0);
	const again = useRef(refetch);
	again.current = refetch;
	useEffect(() => {
		if (!partial || tries >= PARTIAL_REFETCH_MAX) return;
		const id = setTimeout(() => {
			setTries((n) => n + 1);
			void again.current();
		}, PARTIAL_REFETCH_MS);
		return () => clearTimeout(id);
	}, [partial, tries]);
}