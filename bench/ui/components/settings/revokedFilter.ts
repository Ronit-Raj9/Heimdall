/**
 * Token lists show live tokens by default; revoked and expired ones sit behind a
 * "Show revoked (n)" toggle so the one token in use is not lost among old ones.
 */
export function splitRevoked<T>(items: readonly T[], isLive: (item: T) => boolean, showRevoked: boolean): { shown: T[]; hidden: number } {
	const live = items.filter(isLive);
	return { shown: showRevoked ? [...items] : live, hidden: items.length - live.length };
}