import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { LeaderboardScope, LeaderboardSynthetic } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useId } from "react";
import { useTranslation } from "react-i18next";

export interface LeaderboardFiltersProps {
	/** The suite the board covers and every suite with sealed runs, newest first. */
	suite: string;
	suites: readonly string[];
	condition: string;
	conditions: readonly string[];
	scope: LeaderboardScope;
	synthetic: LeaderboardSynthetic;
	onSuite: (suite: string) => void;
	onCondition: (condition: string) => void;
	onScope: (scope: LeaderboardScope) => void;
	onSynthetic: (synthetic: LeaderboardSynthetic) => void;
	disabled?: boolean;
}

/** Suite and condition pickers from the response, and the scope and synthetic switches. Values are what the board covers. */
export function LeaderboardFilters(props: LeaderboardFiltersProps) {
	const { t } = useTranslation("bench");
	const { suite, suites, condition, conditions, scope, synthetic, disabled } = props;
	// A value the lists do not carry (an old URL) still shows as selected.
	const suiteOptions = suite && !suites.includes(suite) ? [suite, ...suites] : suites;
	const conditionOptions = condition && !conditions.includes(condition) ? [condition, ...conditions] : conditions;

	return (
		<div
			className="flex flex-wrap items-end gap-4"
			role="group"
			aria-label={t("leaderboard.filters.label")}
			data-testid="leaderboard-filters"
		>
			<Picker
				label={t("leaderboard.filters.suite")}
				value={suite}
				options={suiteOptions}
				placeholder={t("leaderboard.filters.noSuites")}
				onChange={props.onSuite}
				disabled={disabled || suiteOptions.length === 0}
				testId="leaderboard-suite-picker"
				className="min-w-52"
			/>
			<Picker
				label={t("leaderboard.filters.condition")}
				value={condition}
				options={conditionOptions}
				onChange={props.onCondition}
				disabled={disabled || conditionOptions.length === 0}
				testId="leaderboard-condition-picker"
				className="min-w-40"
			/>
			<Segmented
				label={t("leaderboard.filters.scope")}
				value={scope}
				options={[
					{ value: "all", label: t("leaderboard.filters.scopeAll"), hint: t("leaderboard.filters.scopeAllHint") },
					{ value: "common", label: t("leaderboard.filters.scopeCommon"), hint: t("leaderboard.filters.scopeCommonHint") },
				]}
				onChange={props.onScope}
				disabled={disabled}
				testId="leaderboard-scope"
			/>
			<Segmented
				label={t("leaderboard.filters.synthetic")}
				value={synthetic}
				options={[
					{ value: "exclude", label: t("leaderboard.filters.syntheticExclude"), hint: t("leaderboard.filters.syntheticExcludeHint") },
					{ value: "only", label: t("leaderboard.filters.syntheticOnly"), hint: t("leaderboard.filters.syntheticOnlyHint") },
				]}
				onChange={props.onSynthetic}
				disabled={disabled}
				testId="leaderboard-synthetic"
			/>
		</div>
	);
}

interface PickerProps {
	label: string;
	value: string;
	options: readonly string[];
	placeholder?: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	testId: string;
	className?: string;
}

function Picker({ label, value, options, placeholder, onChange, disabled, testId, className }: PickerProps) {
	const id = useId();
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={value || undefined} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} className={cn("font-mono text-xs", className)} data-testid={testId}>
					<SelectValue placeholder={placeholder} />
				</SelectTrigger>
				<SelectContent data-testid={`${testId}-content`}>
					{options.map((option) => (
						<SelectItem key={option} value={option} className="font-mono text-xs" data-testid={`${testId}-option-${option}`}>
							{option}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
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

/** A two-way switch: toggle buttons in a labelled group, the pressed one is the board's value. */
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