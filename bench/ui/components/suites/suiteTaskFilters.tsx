import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { AdmissionState, FilterOptions, TaskFilters } from "./suites.utils";

/** The value the select uses for "no filter": Radix Select needs a non-empty value per item. */
const ALL = "__all__";

export interface SuiteTaskFiltersProps {
	filters: TaskFilters;
	options: FilterOptions;
	onChange: (next: Partial<TaskFilters>) => void;
	onReset: () => void;
}

/** Kind, difficulty, capability and admission filters. Options are the values this suite has. */
export function SuiteTaskFilters({ filters, options, onChange, onReset }: SuiteTaskFiltersProps) {
	const { t } = useTranslation("bench");
	const active = Boolean(filters.kind || filters.difficulty || filters.capability || filters.status);
	return (
		<div className="flex flex-wrap items-end gap-3" role="group" aria-label={t("suites.filters.label")} data-testid="suites-task-filters">
			<FilterSelect
				label={t("suites.filters.kind")}
				value={filters.kind}
				options={options.kinds.map((kind) => ({ value: kind, label: kind }))}
				onChange={(kind) => onChange({ kind })}
				testId="suites-filter-kind"
			/>
			<FilterSelect
				label={t("suites.filters.difficulty")}
				value={filters.difficulty}
				options={options.difficulties.map((d) => ({
					value: d,
					label: d === "easy" || d === "medium" || d === "hard" ? t(`suites.difficulty.${d}`) : d,
				}))}
				onChange={(difficulty) => onChange({ difficulty })}
				testId="suites-filter-difficulty"
			/>
			<FilterSelect
				label={t("suites.filters.capability")}
				value={filters.capability}
				options={options.capabilities.map((capability) => ({ value: capability, label: capability }))}
				onChange={(capability) => onChange({ capability })}
				testId="suites-filter-capability"
			/>
			<FilterSelect
				label={t("suites.filters.status")}
				value={filters.status}
				options={options.statuses.map((status) => ({ value: status, label: t(`suites.status.${status}`) }))}
				onChange={(status) => onChange({ status: status as AdmissionState | null })}
				testId="suites-filter-status"
			/>
			{active && (
				<Button variant="ghost" size="sm" onClick={onReset} dataTestId="suites-filter-reset">
					{t("suites.filters.reset")}
				</Button>
			)}
		</div>
	);
}

interface FilterSelectProps {
	label: string;
	value: string | null;
	options: readonly { value: string; label: string }[];
	onChange: (value: string | null) => void;
	testId: string;
}

function FilterSelect({ label, value, options, onChange, testId }: FilterSelectProps) {
	const { t } = useTranslation("bench");
	const id = useId();
	// A value the suite does not have (an old URL) still shows as selected.
	const all = value && !options.some((o) => o.value === value) ? [{ value, label: value }, ...options] : options;
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={value ?? ALL} onValueChange={(next) => onChange(next === ALL ? null : next)} disabled={all.length === 0}>
				<SelectTrigger id={id} size="sm" className="min-w-32 text-xs" data-testid={testId}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent data-testid={`${testId}-content`}>
					<SelectItem value={ALL} className="text-xs">
						{t("suites.filters.all")}
					</SelectItem>
					{all.map((option) => (
						<SelectItem key={option.value} value={option.value} className="text-xs" data-testid={`${testId}-option-${option.value}`}>
							{option.label}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}