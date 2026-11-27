import { cn } from "@/lib/utils";
import { ChevronDown, Search, X } from "lucide-react";
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
							"focus-visible:ring-(--focus) inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-3 text-xs font-medium whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:outline-none [&_svg]:size-3.5",
							on
								? "border-primary/40 bg-primary/10 text-primary"
								: "border-border/80 bg-card text-muted-foreground hover:bg-accent hover:text-foreground",
						)}
					>
						{o.icon}
						{o.label}
						{o.count !== undefined && (
							<span className={cn("tabular-nums", on ? "text-primary" : "text-muted-foreground")}>{o.count}</span>
						)}
					</button>
				);
			})}
			{clearable && value.length > 0 && (
				<button
					type="button"
					onClick={() => onChange([])}
					data-testid={testId ? `${testId}-clear` : undefined}
					className="text-muted-foreground hover:text-foreground focus-visible:ring-(--focus) inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-full px-2 text-xs transition-colors focus-visible:ring-2 focus-visible:outline-none"
				>
					<X className="size-3.5" aria-hidden="true" />
					{t("clearFilters")}
				</button>
			)}
		</div>
	);
}
export interface FilterSelectOption {
	value: string;
	label: string;
}

export interface FilterSelectProps {
	/** Shown before the value, for example "Status". Also the accessible name. */
	label: string;
	options: readonly FilterSelectOption[];
	/** Selected value; null means no filter. */
	value: string | null;
	onChange: (next: string | null) => void;
	/** Text of the "no filter" option. Default "All". */
	allLabel?: string;
	className?: string;
	testId?: string;
	disabled?: boolean;
}

/**
 * Small native dropdown for one toolbar filter. A value missing from the options (an old URL)
 * is kept as an extra option so the select never lies about the filter in force.
 */
export function FilterSelect({ label, options, value, onChange, allLabel = "All", className, testId, disabled }: FilterSelectProps) {
	const choices = value && !options.some((o) => o.value === value) ? [{ value, label: value }, ...options] : options;
	return (
		<label
			data-slot="filter-select"
			className={cn(
				"border-input bg-card hover:border-ring/50 focus-within:ring-(--focus) relative inline-flex h-8 shrink-0 items-center rounded-md border text-sm shadow-xs transition-colors focus-within:ring-2",
				disabled && "opacity-50",
				className,
			)}
		>
			<span className="text-muted-foreground pointer-events-none pl-2.5 text-xs">{label}</span>
			<select
				aria-label={label}
				value={value ?? ""}
				disabled={disabled}
				onChange={(e) => onChange(e.target.value || null)}
				data-testid={testId}
				className="text-foreground h-full max-w-48 cursor-pointer appearance-none truncate bg-transparent pr-7 pl-1.5 text-sm font-medium outline-none disabled:cursor-not-allowed"
			>
				<option value="">{allLabel}</option>
				{choices.map((o) => (
					<option key={o.value} value={o.value}>
						{o.label}
					</option>
				))}
			</select>
			<ChevronDown className="text-muted-foreground pointer-events-none absolute right-2 size-3.5" aria-hidden="true" />
		</label>
	);
}

export interface FilterToken {
	key: string;
	/** For example "Agent: codex". */
	label: React.ReactNode;
	onRemove: () => void;
}

/** Active filters as small removable tokens, plus Clear when more than one is set. Renders nothing when none is set. */
export function FilterTokens({ tokens, onClear, className, testId }: { tokens: readonly FilterToken[]; onClear?: () => void; className?: string; testId?: string }) {
	const { t } = useTranslation("ui");
	if (tokens.length === 0) return null;
	return (
		<div data-slot="filter-tokens" data-testid={testId} className={cn("flex flex-wrap items-center gap-1.5", className)}>
			{tokens.map((tk) => (
				<span
					key={tk.key}
					className="bg-muted text-foreground inline-flex h-6 items-center gap-1 rounded-md pr-1 pl-2 text-xs"
					data-testid={testId ? `${testId}-${tk.key}` : undefined}
				>
					{tk.label}
					<button
						type="button"
						onClick={tk.onRemove}
						aria-label={`${t("clearFilters")} ${typeof tk.label === "string" ? tk.label : tk.key}`}
						className="text-muted-foreground hover:text-foreground focus-visible:ring-(--focus) inline-flex size-4 cursor-pointer items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
					>
						<X className="size-3" aria-hidden="true" />
					</button>
				</span>
			))}
			{onClear && tokens.length > 1 && (
				<button
					type="button"
					onClick={onClear}
					data-testid={testId ? `${testId}-clear` : undefined}
					className="text-muted-foreground hover:text-foreground cursor-pointer px-1 text-xs"
				>
					{t("clearFilters")}
				</button>
			)}
		</div>
	);
}

/** A few mutually exclusive options as one small segmented control. */
export function SegmentedControl<T extends string>({
	options,
	value,
	onChange,
	label,
	testId,
	className,
}: {
	options: readonly { value: T; label: React.ReactNode }[];
	value: T;
	onChange: (next: T) => void;
	label?: string;
	testId?: string;
	className?: string;
}) {
	return (
		<div
			role="radiogroup"
			aria-label={label}
			data-slot="segmented-control"
			data-testid={testId}
			className={cn("bg-muted inline-flex h-8 shrink-0 items-center gap-0.5 rounded-md p-0.5", className)}
		>
			{options.map((o) => {
				const on = o.value === value;
				return (
					<button
						key={o.value}
						type="button"
						role="radio"
						aria-checked={on}
						onClick={() => onChange(o.value)}
						data-testid={testId ? `${testId}-${o.value}` : undefined}
						className={cn(
							"focus-visible:ring-(--focus) h-full cursor-pointer rounded-sm px-2.5 text-xs font-medium whitespace-nowrap transition-colors focus-visible:ring-2 focus-visible:outline-none",
							on ? "bg-background text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground",
						)}
					>
						{o.label}
					</button>
				);
			})}
		</div>
	);
}

/** Search box sized for a toolbar row. */
export function ToolbarSearch({
	value,
	onChange,
	placeholder,
	label,
	testId,
	className,
}: {
	value: string;
	onChange: (next: string) => void;
	placeholder?: string;
	label: string;
	testId?: string;
	className?: string;
}) {
	return (
		<label className={cn("relative min-w-40 flex-1 sm:max-w-xs", className)}>
			<span className="sr-only">{label}</span>
			<Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden="true" />
			<input
				type="search"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				placeholder={placeholder}
				aria-label={label}
				data-testid={testId}
				className="border-input bg-card placeholder:text-muted-foreground focus-visible:ring-(--focus) h-8 w-full rounded-md border pr-2.5 pl-8 text-sm shadow-xs focus-visible:ring-2 focus-visible:outline-none"
			/>
		</label>
	);
}

/**
 * The chip that shows or hides test objects in a list (M1-18). Renders nothing when the list has
 * no test items. Pair it with useShowTests and withoutTests from lib/testObjects.
 */
export function ShowTestsChip({ count, on, onChange, className, testId }: { count: number; on: boolean; onChange: (on: boolean) => void; className?: string; testId?: string }) {
	const { t } = useTranslation("ufs1");
	if (count === 0) return null;
	return (
		<button
			type="button"
			aria-pressed={on}
			onClick={() => onChange(!on)}
			className={cn(
				"inline-flex min-h-7 items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-(--focus) focus-visible:outline-none",
				on ? "border-primary/40 bg-primary-soft text-primary" : "bg-card text-muted-foreground hover:text-foreground hover:bg-muted",
				className,
			)}
			data-testid={testId ?? "show-tests-chip"}
		>
			{on ? t("testObjects.hide", { count }) : t("testObjects.show", { count })}
		</button>
	);
}
