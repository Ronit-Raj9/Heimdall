import { Input } from "@/components/ui/input";
import type { ClusterSynthetic } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useEffect, useId, useRef, useState } from "react";
import "@/app/inbox/views/uf7aI18n";
import "@/app/failures/views/ufrhI18n";
import { useTranslation } from "react-i18next";

/** A text filter's URL param. */
export type ClusterTextFilter = "suite" | "agent" | "exp" | "since";

export interface ClustersFiltersProps {
	/** Values as the URL holds them; "" when absent. exp is a comma list. */
	suite: string;
	agent: string;
	exp: string;
	since: string;
	/** The minimum the list was computed with. */
	min: number;
	synthetic: ClusterSynthetic;
	/** Values to suggest while typing, from the current list. */
	agentOptions?: readonly string[];
	/** The org's benchmarks ("id" or "id@version"); when given, the benchmark is a select of them (M7-38). */
	suiteOptions?: readonly string[];
	experimentOptions?: readonly string[];
	/** A text filter changed; "" clears it. */
	onText: (filter: ClusterTextFilter, value: string) => void;
	/** null clears the minimum (server default). */
	onMin: (min: number | null) => void;
	onSynthetic: (synthetic: ClusterSynthetic) => void;
	disabled?: boolean;
}

/** How many filters differ from their defaults, for the folded "Filters (n)" button on phones. Exported for tests. */
export function activeClusterFilters(p: Pick<ClustersFiltersProps, "suite" | "agent" | "exp" | "since" | "synthetic">): number {
	return [p.suite, p.agent, p.exp, p.since].filter((v) => v.trim() !== "").length + (p.synthetic !== "exclude" ? 1 : 0);
}

/** How long typing pauses before a text filter applies (R3-8). */
export const FILTER_DEBOUNCE_MS = 300;

/**
 * Suite and agent as selects (suite as text when the org's benchmarks are unknown), the comparisons
 * as a pick list (M7-38), since as a date, the minimum count, and the synthetic switch. Text applies
 * live, 300 ms after typing stops, or at once on Enter or blur. Below sm they fold behind "Filters (n)" (M7-37, M7-38).
 */
export function ClustersFilters(props: ClustersFiltersProps) {
	const { t } = useTranslation("bench");
	const { t: f } = useTranslation("uf7a");
	const { t: r } = useTranslation("ufrh");
	const { disabled, onText } = props;
	const [open, setOpen] = useState(false);
	const count = activeClusterFilters(props);
	return (
		<div className="flex flex-col gap-1.5">
			<button
				type="button"
				className="bg-card hover:bg-accent inline-flex h-9 w-fit items-center rounded-md border px-3 text-sm font-medium sm:hidden"
				aria-expanded={open}
				onClick={() => setOpen((o) => !o)}
				data-testid="clusters-filters-toggle"
			>
				{f("clusters.filtersButton", { count })}
			</button>
			<div
				className={cn("flex-wrap items-end gap-4 sm:flex", open ? "flex" : "hidden")}
				role="group"
				aria-label={t("clusters.filters.label")}
				data-testid="clusters-filters"
			>
				{props.suiteOptions && props.suiteOptions.length > 0 ? (
					<SelectField
						label={t("clusters.filters.suite")}
						value={props.suite}
						allLabel={t("clusters.filters.suitePlaceholder")}
						options={props.suiteOptions}
						onCommit={(value) => onText("suite", value)}
						disabled={disabled}
						testId="clusters-filter-suite"
					/>
				) : (
					<TextField
						label={t("clusters.filters.suite")}
						value={props.suite}
						placeholder={t("clusters.filters.suitePlaceholder")}
						onCommit={(value) => onText("suite", value)}
						disabled={disabled}
						testId="clusters-filter-suite"
					/>
				)}
				<SelectField
					label={t("clusters.filters.agent")}
					value={props.agent}
					allLabel={t("clusters.filters.agentPlaceholder")}
					options={props.agentOptions ?? []}
					onCommit={(value) => onText("agent", value)}
					disabled={disabled}
					testId="clusters-filter-agent"
				/>
				{props.experimentOptions && props.experimentOptions.length > 0 ? (
					<ExpPicker
						label={t("clusters.filters.exp")}
						value={props.exp}
						options={props.experimentOptions}
						allLabel={r("clusters.expAll")}
						pickedLabel={(count) => r("clusters.expPicked", { count })}
						clearLabel={r("clusters.expClear")}
						onCommit={(value) => onText("exp", value)}
						disabled={disabled}
						testId="clusters-filter-exp"
					/>
				) : (
					<TextField
						label={t("clusters.filters.exp")}
						value={props.exp}
						placeholder={t("clusters.filters.expPlaceholder")}
						options={props.experimentOptions}
						onCommit={(value) => onText("exp", value)}
						disabled={disabled}
						testId="clusters-filter-exp"
						className="sm:w-56"
					/>
				)}
				<TextField
					label={t("clusters.filters.since")}
					value={props.since}
					type="date"
					onCommit={(value) => onText("since", value)}
					disabled={disabled}
					testId="clusters-filter-since"
					className="sm:w-40"
				/>
				<TextField
					label={t("clusters.filters.min")}
					value={String(props.min)}
					type="number"
					onCommit={(value) => {
						const n = Number.parseInt(value, 10);
						props.onMin(Number.isInteger(n) && n >= 1 ? n : null);
					}}
					disabled={disabled}
					testId="clusters-filter-min"
					className="sm:w-24"
				/>
				<Segmented
					label={r("clusters.whichRuns")}
					value={props.synthetic}
					options={[
						{ value: "exclude", label: t("clusters.filters.syntheticExclude"), hint: t("clusters.filters.syntheticExcludeHint") },
						{ value: "include", label: t("clusters.filters.syntheticInclude"), hint: t("clusters.filters.syntheticIncludeHint") },
						{ value: "only", label: t("clusters.filters.syntheticOnly"), hint: t("clusters.filters.syntheticOnlyHint") },
					]}
					onChange={props.onSynthetic}
					disabled={disabled}
					testId="clusters-synthetic"
				/>
			</div>
		</div>
	);
}

interface TextFieldProps {
	label: string;
	value: string;
	placeholder?: string;
	type?: "text" | "number" | "date";
	/** Suggestions shown under the field while typing. */
	options?: readonly string[];
	onCommit: (value: string) => void;
	disabled?: boolean;
	testId: string;
	className?: string;
}

/**
 * A field that follows its URL value and applies what was typed 300 ms after typing stops, or at
 * once on Enter or blur, only when it differs (R3-8). It stays mounted, so focus is kept while the
 * list refetches.
 */
function TextField({ label, value, placeholder, type = "text", options, onCommit, disabled, testId, className }: TextFieldProps) {
	const id = useId();
	const listId = `${id}-options`;
	const [draft, setDraft] = useState(value);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	// A new value from the URL (a reset, a shared link) replaces the draft unless it is what was typed.
	useEffect(() => {
		setDraft((cur) => (cur.trim() === value ? cur : value));
	}, [value]);
	useEffect(() => () => {
		if (timer.current) clearTimeout(timer.current);
	}, []);
	const commit = (next: string) => {
		if (timer.current) clearTimeout(timer.current);
		timer.current = null;
		if (next.trim() !== value) onCommit(next.trim());
	};
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-sm">
				{label}
			</label>
			<Input
				id={id}
				type={type}
				min={type === "number" ? 1 : undefined}
				value={draft}
				placeholder={placeholder}
				disabled={disabled}
				list={options && options.length > 0 ? listId : undefined}
				onBlur={(e) => commit(e.currentTarget.value)}
				onChange={(e) => {
					const next = e.currentTarget.value;
					setDraft(next);
					if (type === "date") return commit(next);
					if (timer.current) clearTimeout(timer.current);
					timer.current = setTimeout(() => commit(next), FILTER_DEBOUNCE_MS);
				}}
				onKeyDown={(e) => {
					if (e.key === "Enter") commit(e.currentTarget.value);
				}}
				className={cn("w-full font-mono text-sm sm:w-44 md:text-sm", className)}
				data-testid={testId}
			/>
			{options && options.length > 0 && (
				<datalist id={listId}>
					{options.map((option) => (
						<option key={option} value={option} />
					))}
				</datalist>
			)}
		</div>
	);
}

/** The comparisons ticked in a comma list, toggling one; the order of the list is kept. Exported for tests. */
export function toggleExp(value: string, id: string, options: readonly string[]): string {
	const picked = new Set(value.split(",").map((v) => v.trim()).filter(Boolean));
	if (picked.has(id)) picked.delete(id);
	else picked.add(id);
	// Known ids in list order, then any id from the URL the list no longer has.
	return [...options.filter((o) => picked.has(o)), ...[...picked].filter((p) => !options.includes(p))].join(",");
}

/** The comparisons as a folded list of check boxes (M7-38): "every comparison" or "N comparisons". */
function ExpPicker({
	label,
	value,
	options,
	allLabel,
	pickedLabel,
	clearLabel,
	onCommit,
	disabled,
	testId,
}: {
	label: string;
	value: string;
	options: readonly string[];
	allLabel: string;
	pickedLabel: (count: number) => string;
	clearLabel: string;
	onCommit: (value: string) => void;
	disabled?: boolean;
	testId: string;
}) {
	const id = useId();
	const picked = value.split(",").map((v) => v.trim()).filter(Boolean);
	const list = [...options, ...picked.filter((p) => !options.includes(p))];
	return (
		<div className="flex flex-col gap-1">
			<span id={id} className="text-muted-foreground text-sm">
				{label}
			</span>
			<details className="relative" data-testid={testId}>
				<summary
					aria-labelledby={id}
					aria-disabled={disabled || undefined}
					className="border-input bg-card dark:bg-input/20 flex h-9 w-full cursor-pointer list-none items-center rounded-md border px-2 font-mono text-sm shadow-xs sm:w-56"
					data-testid={`${testId}-summary`}
				>
					{picked.length === 0 ? allLabel : pickedLabel(picked.length)}
				</summary>
				<fieldset
					aria-labelledby={id}
					disabled={disabled}
					className="bg-popover absolute z-20 mt-1 flex max-h-72 w-full min-w-56 flex-col gap-0.5 overflow-y-auto rounded-md border p-2 shadow-md sm:w-72"
				>
					{list.map((o) => (
						<label key={o} className="hover:bg-accent flex min-h-6 cursor-pointer items-center gap-2 rounded-sm px-1 font-mono text-sm break-all">
							<input
								type="checkbox"
								className="accent-primary size-4 shrink-0"
								checked={picked.includes(o)}
								onChange={() => onCommit(toggleExp(value, o, options))}
								data-testid={`${testId}-option-${o}`}
							/>
							{o}
						</label>
					))}
					{picked.length > 0 && (
						<button type="button" className="text-primary mt-1 self-start text-sm underline-offset-2 hover:underline" onClick={() => onCommit("")}>
							{clearLabel}
						</button>
					)}
				</fieldset>
			</details>
		</div>
	);
}

/** A select of known values with an "every ..." first choice; a URL value not in the list stays selectable. */
function SelectField({
	label,
	value,
	allLabel,
	options,
	onCommit,
	disabled,
	testId,
}: {
	label: string;
	value: string;
	allLabel: string;
	options: readonly string[];
	onCommit: (value: string) => void;
	disabled?: boolean;
	testId: string;
}) {
	const id = useId();
	const list = value && !options.includes(value) ? [...options, value] : options;
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-sm">
				{label}
			</label>
			<select
				id={id}
				value={value}
				disabled={disabled}
				onChange={(e) => onCommit(e.target.value)}
				className="border-input bg-card dark:bg-input/20 h-9 w-full rounded-md border px-2 font-mono text-sm shadow-xs outline-none sm:w-44"
				data-testid={testId}
			>
				<option value="">{allLabel}</option>
				{list.map((o) => (
					<option key={o} value={o}>
						{o}
					</option>
				))}
			</select>
		</div>
	);
}

interface SegmentedProps<T extends string> {
	label: string;
	value: T;
	options: readonly { value: T; label: string; hint: string }[];
	onChange: (value: T) => void;
	disabled?: boolean;
	testId: string;
}

/** Toggle buttons in a labelled group, the pressed one is the list's value. Same look as the leaderboard's switches. */
function Segmented<T extends string>({ label, value, options, onChange, disabled, testId }: SegmentedProps<T>) {
	const id = useId();
	return (
		<div className="flex flex-col gap-1">
			<span id={id} className="text-muted-foreground text-sm">
				{label}
			</span>
			<div
				role="group"
				aria-labelledby={id}
				className="bg-muted inline-flex h-9 w-fit items-center gap-0.5 rounded-md border p-0.5"
				data-testid={testId}
			>
				{options.map((option) => {
					const active = option.value === value;
					return (
						<button
							key={option.value}
							type="button"
							aria-pressed={active}
							title={option.hint}
							disabled={disabled}
							onClick={() => {
								if (!active) onChange(option.value);
							}}
							data-testid={`${testId}-${option.value}`}
							className={cn(
								"focus-visible:ring-ring/50 h-full cursor-pointer rounded-sm px-3 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
								active ? "bg-card text-foreground shadow-sm dark:bg-accent" : "text-muted-foreground hover:text-foreground",
							)}
						>
							{option.label}
						</button>
					);
				})}
			</div>
		</div>
	);
}