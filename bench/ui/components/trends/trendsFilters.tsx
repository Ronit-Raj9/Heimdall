import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { TrendMetric, TrendSynthetic } from "@/lib/types/bench";
import { cn, controlledSelectValue } from "@/lib/utils";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { ALL_VALUE, TREND_METRICS, TREND_SYNTHETIC } from "./trends.utils";

export interface TrendsFiltersProps {
	/** Suite filter, "" for every suite, and the suites with indexed runs. */
	suite: string;
	suites: readonly string[];
	/** Agent filter, "" for every agent, and the agent ids with indexed runs. */
	agent: string;
	agents: readonly string[];
	metric: TrendMetric;
	metrics: readonly TrendMetric[];
	condition: string;
	conditions: readonly string[];
	synthetic: TrendSynthetic;
	onSuite: (suite: string) => void;
	onAgent: (agent: string) => void;
	onMetric: (metric: TrendMetric) => void;
	onCondition: (condition: string) => void;
	onSynthetic: (synthetic: TrendSynthetic) => void;
	disabled?: boolean;
}

/**
 * Suite, agent, metric and condition pickers and the synthetic switch, in one row above the chart.
 * Suite and agent offer "all"; the callbacks get "" for it.
 */
export function TrendsFilters(props: TrendsFiltersProps) {
	const { t } = useTranslation("bench");
	const { disabled } = props;
	const metrics = props.metrics.length > 0 ? props.metrics : TREND_METRICS;
	const conditions = optionsWithCurrent(props.condition, props.conditions);

	return (
		<div className="flex flex-wrap items-end gap-4" role="group" aria-label={t("trends.filters.label")} data-testid="trends-filters">
			<Picker
				label={t("trends.filters.suite")}
				value={props.suite || ALL_VALUE}
				options={[ALL_VALUE, ...optionsWithCurrent(props.suite, props.suites)]}
				labels={{ [ALL_VALUE]: t("trends.filters.allSuites") }}
				onChange={(next) => props.onSuite(fromPicker(next))}
				disabled={disabled}
				testId="trends-suite-picker"
				className="min-w-52"
			/>
			<Picker
				label={t("trends.filters.agent")}
				value={props.agent || ALL_VALUE}
				options={[ALL_VALUE, ...optionsWithCurrent(props.agent, props.agents)]}
				labels={{ [ALL_VALUE]: t("trends.filters.allAgents") }}
				onChange={(next) => props.onAgent(fromPicker(next))}
				disabled={disabled}
				testId="trends-agent-picker"
				className="min-w-44"
			/>
			<Picker
				label={t("trends.filters.metric")}
				value={props.metric}
				options={metrics}
				labels={Object.fromEntries(metrics.map((m) => [m, t(`trends.metric.${m}`)]))}
				onChange={(next) => props.onMetric(next as TrendMetric)}
				disabled={disabled}
				testId="trends-metric-picker"
				className="min-w-44 font-sans"
			/>
			<Picker
				label={t("trends.filters.condition")}
				value={props.condition}
				options={conditions}
				onChange={props.onCondition}
				disabled={disabled || conditions.length === 0}
				testId="trends-condition-picker"
				className="min-w-40"
			/>
			<SyntheticSwitch value={props.synthetic} onChange={props.onSynthetic} disabled={disabled} />
		</div>
	);
}

/** The options of a picker, with the current value first when the list does not carry it (an old URL). */
export function optionsWithCurrent(value: string, options: readonly string[]): string[] {
	return value && !options.includes(value) ? [value, ...options] : [...options];
}

/** A picker value as a filter: "" for the "all" option. */
export function fromPicker(value: string): string {
	return value === ALL_VALUE ? "" : value;
}

interface PickerProps {
	label: string;
	value: string;
	options: readonly string[];
	labels?: Record<string, string>;
	onChange: (value: string) => void;
	disabled?: boolean;
	testId: string;
	className?: string;
}

function Picker({ label, value, options, labels, onChange, disabled, testId, className }: PickerProps) {
	const id = useId();
	return (
		<div className="flex flex-col gap-1.5">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={controlledSelectValue(value)} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} className={cn("font-mono text-xs", className)} data-testid={testId}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent data-testid={`${testId}-content`}>
					{options.map((option) => (
						<SelectItem key={option} value={option} className="font-mono text-xs" data-testid={`${testId}-option-${option}`}>
							{labels?.[option] ?? option}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}

/** Exclude, include or only synthetic runs: toggle buttons in a labelled group. */
function SyntheticSwitch({
	value,
	onChange,
	disabled,
}: {
	value: TrendSynthetic;
	onChange: (v: TrendSynthetic) => void;
	disabled?: boolean;
}) {
	const { t } = useTranslation("bench");
	const id = useId();
	return (
		<div className="flex flex-col gap-1.5">
			<span id={id} className="text-muted-foreground text-xs">
				{t("trends.filters.synthetic")}
			</span>
			<div
				role="group"
				aria-labelledby={id}
				className="bg-muted inline-flex h-(--control-h) w-fit items-center gap-0.5 rounded-md border p-0.5"
				data-testid="trends-synthetic"
			>
				{TREND_SYNTHETIC.map((option) => {
					const active = option === value;
					return (
						<button
							key={option}
							type="button"
							aria-pressed={active}
							title={t(`trends.filters.syntheticHint.${option}`)}
							disabled={disabled}
							onClick={() => {
								if (!active) onChange(option);
							}}
							data-testid={`trends-synthetic-${option}`}
							className={cn(
								"focus-visible:ring-ring/50 h-full cursor-pointer rounded-sm px-3 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
								active ? "bg-card text-foreground shadow-sm dark:bg-accent" : "text-muted-foreground hover:text-foreground",
							)}
						>
							{t(`trends.filters.syntheticOption.${option}`)}
						</button>
					);
				})}
			</div>
		</div>
	);
}