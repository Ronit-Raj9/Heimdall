import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useAdmissionLabel } from "./suiteBadges";
import "./uf6aI18n";
import type { AdmissionState, FilterOptions, TaskFilters } from "./suites.utils";

/** The value the select uses for "no filter": Radix Select needs a non-empty value per item. */
const ALL = "__all__";

export interface SuiteTaskFiltersProps {
	filters: TaskFilters;
	options: FilterOptions;
	onChange: (next: Partial<TaskFilters>) => void;
	onReset: () => void;
	/** M6-1: the search text (task id or title) and its change handler; no search box when absent. */
	search?: string;
	onSearch?: (next: string) => void;
	/** X-44: controls that sit at the end of the filter row (the columns toggle). */
	trailing?: ReactNode;
}

/** Kind, difficulty, capability and admission filters. Options are the values this suite has. */
export function SuiteTaskFilters({ filters, options, onChange, onReset, search, onSearch, trailing }: SuiteTaskFiltersProps) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf6a");
	const admissionLabel = useAdmissionLabel();
	const searchId = useId();
	const active = Boolean(filters.kind || filters.difficulty || filters.capability || filters.status);
	return (
		<div className="flex flex-wrap items-end gap-3" role="group" aria-label={t("suites.filters.label")} data-testid="suites-task-filters">
			{onSearch && (
				<div className="flex w-full min-w-0 flex-col gap-1.5 sm:w-64">
					<label htmlFor={searchId} className="text-muted-foreground text-xs">
						{tf("tasks.search")}
					</label>
					<Input
						id={searchId}
						type="search"
						value={search ?? ""}
						placeholder={tf("tasks.searchPlaceholder")}
						onChange={(event) => onSearch(event.target.value)}
						className="h-(--control-h)"
						data-testid="suites-task-search"
					/>
				</div>
			)}
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
				label={tf("tasks.statusColumn")}
				value={filters.status}
				options={options.statuses.map((status) => ({ value: status, label: admissionLabel(status) }))}
				onChange={(status) => onChange({ status: status as AdmissionState | null })}
				testId="suites-filter-status"
			/>
			{active && (
				<Button variant="ghost" size="sm" onClick={onReset} dataTestId="suites-filter-reset">
					{t("suites.filters.reset")}
				</Button>
			)}
			{trailing && <div className="ml-auto flex items-end">{trailing}</div>}
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
	// M6-8: a filter with no option to pick is not shown, not shown disabled.
	if (all.length === 0) return null;
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={value ?? ALL} onValueChange={(next) => onChange(next === ALL ? null : next)}>
				<SelectTrigger id={id} className="min-w-32" data-testid={testId}>
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