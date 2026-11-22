import { useEffect, useRef, useState } from "react";

/** A trailing debouncer: only the last value pushed within `ms` is emitted, once. */
export interface Debouncer<T> {
	push(key: string, value: T): void;
	cancel(): void;
}

export function createDebouncer<T>(ms: number, emit: (key: string, value: T) => void): Debouncer<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	const cancel = () => {
		if (timer !== undefined) clearTimeout(timer);
		timer = undefined;
	};
	return {
		push(key, value) {
			cancel();
			timer = setTimeout(() => {
				timer = undefined;
				emit(key, value);
			}, ms);
		},
		cancel,
	};
}

/**
 * The value, held back until it has not changed for `ms`. Values are compared by their JSON, so a
 * new object with the same content (a spec rebuilt on every render) does not restart the wait.
 * The first value is returned at once.
 */
export function useDebounced<T>(value: T, ms: number): T {
	const key = JSON.stringify(value);
	const [settled, setSettled] = useState<{ key: string; value: T }>(() => ({ key, value }));
	const debouncer = useRef<Debouncer<T> | null>(null);
	if (debouncer.current === null) debouncer.current = createDebouncer<T>(ms, (k, v) => setSettled({ key: k, value: v }));
	const latest = useRef(value);
	latest.current = value;
	useEffect(() => {
		if (key === settled.key) debouncer.current?.cancel();
		else debouncer.current?.push(key, latest.current);
	}, [key, settled.key]);
	useEffect(() => () => debouncer.current?.cancel(), []);
	return key === settled.key ? value : settled.value;
}