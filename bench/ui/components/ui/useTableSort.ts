import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useMemo } from "react";

export const TABLE_SORT_DIRS = ["asc", "desc"] as const;
export type TableSortDir = (typeof TABLE_SORT_DIRS)[number];

/** The column a table is sorted by and its direction; key null keeps the rows in the order given. */
export interface TableSort<K extends string> {
	key: K | null;
	dir: TableSortDir;
}

/** What a column sorts on for one row; null (not known) sorts last in either direction. */
export type SortAccessors<Row, K extends string> = Record<K, (row: Row) => number | string | null | undefined>;

/** The props a SortableHead needs for one column. */
export interface SortHeadProps {
	active: boolean;
	dir: TableSortDir;
	onSort: () => void;
}

/**
 * A sorted copy. Rows without a value for the column go last whatever the direction, so a value
 * that is not known never reads as the best or the worst. Ties keep the order given.
 */
export function sortRows<Row, K extends string>(rows: readonly Row[], sort: TableSort<K>, accessors: SortAccessors<Row, K>): Row[] {
	const key = sort.key;
	if (key === null || !accessors[key]) return [...rows];
	const get = accessors[key];
	const sign = sort.dir === "asc" ? 1 : -1;
	return rows
		.map((row, index) => ({ row, index, v: get(row) }))
		.sort((a, b) => {
			const va = a.v ?? null;
			const vb = b.v ?? null;
			if (va === null || vb === null) {
				if (va !== vb) return va === null ? 1 : -1;
				return a.index - b.index;
			}
			const cmp = typeof va === "number" && typeof vb === "number" ? va - vb : String(va).localeCompare(String(vb));
			return cmp === 0 ? a.index - b.index : sign * cmp;
		})
		.map((x) => x.row);
}

/** A click on a column header: the same column flips direction, a new column starts at its first direction. */
export function nextTableSort<K extends string>(
	current: TableSort<K>,
	key: K,
	firstDir: (key: K) => TableSortDir = () => "asc",
): TableSort<K> {
	if (current.key === key) return { key, dir: current.dir === "asc" ? "desc" : "asc" };
	return { key, dir: firstDir(key) };
}

/** The sort named by URL values, or initial when the key is not one of the columns. */
export function parseTableSort<K extends string>(
	key: string | null,
	dir: string | null,
	columns: readonly K[],
	initial: TableSort<K>,
	firstDir: (key: K) => TableSortDir = () => "asc",
): TableSort<K> {
	if (!key || !(columns as readonly string[]).includes(key)) return initial;
	const k = key as K;
	return { key: k, dir: dir === "asc" || dir === "desc" ? dir : firstDir(k) };
}

export interface UseTableSortOptions<K extends string> {
	/** Direction of a column's first click; ascending when not given. */
	firstDir?: (key: K) => TableSortDir;
	/** URL parameter names; "sort" and "dir" when not given. */
	keyParam?: string;
	dirParam?: string;
}

/**
 * Client-side column sort with the column and direction in the URL (?sort=&dir=). Returns the
 * sorted rows, the current sort and head(key), the props of that column's SortableHead.
 */
export function useTableSort<Row, K extends string>(
	rows: readonly Row[],
	initial: TableSort<K>,
	accessors: SortAccessors<Row, K>,
	options: UseTableSortOptions<K> = {},
) {
	const { firstDir, keyParam = "sort", dirParam = "dir" } = options;
	const [sortKey, setSortKey] = useQueryState(keyParam, parseAsString);
	const [sortDir, setSortDir] = useQueryState(dirParam, parseAsStringLiteral(TABLE_SORT_DIRS));
	const columns = useMemo(() => Object.keys(accessors) as K[], [accessors]);
	const sort = useMemo(() => parseTableSort(sortKey, sortDir, columns, initial, firstDir), [sortKey, sortDir, columns, initial, firstDir]);
	const sorted = useMemo(() => sortRows(rows, sort, accessors), [rows, sort, accessors]);
	const head = (key: K): SortHeadProps => ({
		active: sort.key === key,
		dir: sort.dir,
		onSort: () => {
			const next = nextTableSort(sort, key, firstDir);
			const isInitial = next.key === initial.key && next.dir === initial.dir;
			void setSortKey(isInitial ? null : next.key);
			void setSortDir(isInitial ? null : next.dir);
		},
	});
	return { rows: sorted, sort, head };
}
