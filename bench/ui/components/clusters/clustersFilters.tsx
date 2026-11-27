import { Input } from "@/components/ui/input";
import type { ClusterSynthetic } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useId, useState } from "react";
import "@/app/inbox/views/uf7aI18n";
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

/**
 * Suite and experiments as text fields (applied on Enter or when they lose focus, so typing does
 * not refetch on every key), the agent as a select of the agents in the list, since as a date, the
 * minimum count, and the synthetic switch. Below sm they fold behind "Filters (n)" (M7-37, M7-38).
 */
export function ClustersFilters(props: ClustersFiltersProps) {
	const { t } = useTranslation("bench");
	const { t: f } = useTranslation("uf7a");
	const { disabled, onText } = props;
	const hintId = useId();
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
				aria-describedby={hintId}
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
					label={t("clusters.filters.synthetic")}
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
			<p id={hintId} className="sr-only">
				{t("clusters.filters.apply")}
			</p>
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
 * An uncontrolled field keyed on its URL value: a new value from the URL resets it, and what the
 * user typed is committed on Enter or blur only when it differs.
 */
function TextField({ label, value, placeholder, type = "text", options, onCommit, disabled, testId, className }: TextFieldProps) {
	const id = useId();
	const listId = `${id}-options`;
	const commit = (next: string) => {
		if (next.trim() !== value) onCommit(next.trim());
	};
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-sm">
				{label}
			</label>
			<Input
				key={value}
				id={id}
				type={type}
				min={type === "number" ? 1 : undefined}
				defaultValue={value}
				placeholder={placeholder}
				disabled={disabled}
				list={options && options.length > 0 ? listId : undefined}
				onBlur={(e) => commit(e.currentTarget.value)}
				onChange={type === "date" ? (e) => commit(e.currentTarget.value) : undefined}
				onKeyDown={(e) => {
					if (e.key === "Enter") commit(e.currentTarget.value);
				}}
				className={cn("w-full font-mono text-xs sm:w-44 md:text-xs", className)}
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
				className="border-input bg-card dark:bg-input/20 h-9 w-full rounded-md border px-2 font-mono text-xs shadow-xs outline-none sm:w-44"
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