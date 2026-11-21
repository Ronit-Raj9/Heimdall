import { cn } from "@/lib/utils";
import { X } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

/** Row above a list or table: filters on the left, actions on the right. Wraps on phones. */
export function Toolbar({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="toolbar" role="toolbar" className={cn("flex flex-wrap items-center gap-2", className)} {...props} />;
}

/** Pushes following toolbar items to the right edge. */
export function ToolbarSpacer() {
	return <div data-slot="toolbar-spacer" className="flex-1" />;
}

export interface FilterChipOption<T extends string = string> {
	value: T;
	label: React.ReactNode;
	count?: number;
	icon?: React.ReactNode;
}

export interface FilterChipsProps<T extends string = string> {
	options: FilterChipOption<T>[];
	/** Selected values. Single-select callers pass one value or none. */
	value: T[];
	onChange: (next: T[]) => void;
	/** Allow several chips at once. Default false (a click replaces the selection). */
	multiple?: boolean;
	/** Show a "Clear" chip when something is selected. Default true. */
	clearable?: boolean;
	/** Accessible group name. */
	label?: string;
	className?: string;
	testId?: string;
}

/** Pill toggles for quick filters (category, status). Horizontal scroll on narrow screens. */
export function FilterChips<T extends string = string>({
	options,
	value,
	onChange,
	multiple = false,
	clearable = true,
	label,
	className,
	testId,
}: FilterChipsProps<T>) {
	const { t } = useTranslation("ui");
	const toggle = (v: T) => {
		const on = value.includes(v);
		if (multiple) onChange(on ? value.filter((x) => x !== v) : [...value, v]);
		else onChange(on ? [] : [v]);
	};
	return (
		<div
			data-slot="filter-chips"
			data-testid={testId}
			role="group"
			aria-label={label}
			className={cn("flex max-w-full min-w-0 items-center gap-1.5 overflow-x-auto pb-0.5", className)}
		>
			{options.map((o) => {
				const on = value.includes(o.value);
				return (
					<button
						key={o.value}
						type="button"
						aria-pressed={on}
						data-testid={testId ? `${testId}-${o.value}` : undefined}
						onClick={() => toggle(o.value)}
						className={cn(
							"focus-visible:ring-ring/50 inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-3 text-xs font-medium whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:outline-none [&_svg]:size-3.5",
							on
								? "border-primary/40 bg-primary/10 text-primary"
								: "border-border/80 bg-card text-muted-foreground hover:bg-accent hover:text-foreground",
						)}
					>
						{o.icon}
						{o.label}
						{o.count !== undefined && (
							<span className={cn("tabular-nums", on ? "text-primary/80" : "text-muted-foreground/70")}>{o.count}</span>
						)}
					</button>
				);
			})}
			{clearable && value.length > 0 && (
				<button
					type="button"
					onClick={() => onChange([])}
					data-testid={testId ? `${testId}-clear` : undefined}
					className="text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-full px-2 text-xs transition-colors focus-visible:ring-2 focus-visible:outline-none"
				>
					<X className="size-3.5" aria-hidden="true" />
					{t("clearFilters")}
				</button>
			)}
		</div>
	);
}