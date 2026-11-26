/**
 * Wraps an invalidatesTags function so a failed request invalidates nothing. A refused save (409)
 * must not refetch the file: editors keyed on the file hash would remount and drop the message and
 * the typed edit. The Reload button stays the way to load the other version.
 */
export function tagsOnSuccess<R, A, T>(tags: (result: R, arg: A) => T[]): (result: R | undefined, error: unknown, arg: A) => T[] {
	return (result, error, arg) => (error || result === undefined ? [] : tags(result, arg));
}