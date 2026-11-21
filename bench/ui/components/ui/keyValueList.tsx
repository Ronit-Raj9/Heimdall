import { cn } from "@/lib/utils";
import * as React from "react";

export interface KeyValueItem {
	label: React.ReactNode;
	value: React.ReactNode;
	/** Monospace value (ids, hashes, paths). */
	mono?: boolean;
	/** Stable key; defaults to index. */
	key?: string;
}

export interface KeyValueListProps {
	items: KeyValueItem[];
	/** Columns on wide screens. Default 2. Always 1 on phones. */
	columns?: 1 | 2 | 3;
	className?: string;
	testId?: string;
}

const COLS = { 1: "", 2: "sm:grid-cols-2", 3: "sm:grid-cols-2 lg:grid-cols-3" } as const;

/** Dense facts grid: muted label above value. Good inside DetailsDisclosure. */
export function KeyValueList({ items, columns = 2, className, testId }: KeyValueListProps) {
	return (
		<dl data-slot="key-value-list" data-testid={testId} className={cn("grid grid-cols-1 gap-x-6 gap-y-3", COLS[columns], className)}>
			{items.map((item, i) => (
				<div key={item.key ?? i} className="flex min-w-0 flex-col gap-0.5">
					<dt className="text-muted-foreground text-xs">{item.label}</dt>
					<dd className={cn("text-foreground min-w-0 text-sm break-words", item.mono && "font-mono text-xs")}>{item.value}</dd>
				</div>
			))}
		</dl>
	);
}