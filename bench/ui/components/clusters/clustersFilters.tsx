import { Input } from "@/components/ui/input";
import type { ClusterSynthetic } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useId } from "react";
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
	experimentOptions?: readonly string[];
	/** A text filter changed; "" clears it. */
	onText: (filter: ClusterTextFilter, value: string) => void;
	/** null clears the minimum (server default). */
	onMin: (min: number | null) => void;
	onSynthetic: (synthetic: ClusterSynthetic) => void;
	disabled?: boolean;
}

/**
 * Suite, agent, experiments and since as text fields, the minimum count, and the synthetic switch.
 * A field applies on Enter or when it loses focus, so typing does not refetch on every key.
 */
export function ClustersFilters(props: ClustersFiltersProps) {
	const { t } = useTranslation("bench");
	const { disabled, onText } = props;
	const hintId = useId();
	return (
		<div className="flex flex-col gap-1.5">
			<div
				className="flex flex-wrap items-end gap-4"
				role="group"
				aria-label={t("clusters.filters.label")}
				aria-describedby={hintId}
				data-testid="clusters-filters"
			>
				<TextField
					label={t("clusters.filters.suite")}
					value={props.suite}
					placeholder={t("clusters.filters.suitePlaceholder")}
					onCommit={(value) => onText("suite", value)}
					disabled={disabled}
					testId="clusters-filter-suite"
				/>
				<TextField
					label={t("clusters.filters.agent")}
					value={props.agent}
					placeholder={t("clusters.filters.agentPlaceholder")}
					options={props.agentOptions}
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
					className="w-56"
				/>
				<TextField
					label={t("clusters.filters.since")}
					value={props.since}
					placeholder={t("clusters.filters.sincePlaceholder")}
					onCommit={(value) => onText("since", value)}
					disabled={disabled}
					testId="clusters-filter-since"
					className="w-36"
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
					className="w-24"
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
			<p id={hintId} className="text-muted-foreground text-[11px]">
				{t("clusters.filters.apply")}
			</p>
		</div>
	);
}

interface TextFieldProps {
	label: string;
	value: string;
	placeholder?: string;
	type?: "text" | "number";
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
			<label htmlFor={id} className="text-muted-foreground text-xs">
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
				onKeyDown={(e) => {
					if (e.key === "Enter") commit(e.currentTarget.value);
				}}
				className={cn("w-44 font-mono text-xs md:text-xs", className)}
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
			<span id={id} className="text-muted-foreground text-xs">
				{label}
			</span>
			<div
				role="group"
				aria-labelledby={id}
				className="bg-muted inline-flex h-9 items-center gap-0.5 rounded-sm border p-0.5"
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
								active ? "bg-background text-foreground shadow-sm dark:bg-zinc-800" : "text-muted-foreground hover:text-foreground",
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