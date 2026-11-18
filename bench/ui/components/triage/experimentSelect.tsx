import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ExperimentStatus } from "@/lib/types/bench";
import { useId } from "react";
import { useTranslation } from "react-i18next";

/** Experiments newest first, by start time or, before a start, freeze time. */
export function experimentsNewestFirst(experiments: readonly ExperimentStatus[]): ExperimentStatus[] {
	const at = (e: ExperimentStatus) => {
		const ms = Date.parse(e.started_at ?? e.frozen_at);
		return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
	};
	return [...experiments].sort((x, y) => at(y) - at(x));
}

export interface ExperimentSelectProps {
	/** Newest first. */
	experiments: readonly ExperimentStatus[];
	value: string | null | undefined;
	onChange: (expId: string) => void;
	disabled?: boolean;
	testId: string;
}

/**
 * Picks the experiment a triage or review view reads, newest first, each option named and with
 * its id. An id from the URL that the list does not carry still shows as selected.
 */
export function ExperimentSelect({ experiments, value, onChange, disabled, testId }: ExperimentSelectProps) {
	const { t } = useTranslation("bench");
	const id = useId();
	const known = !value || experiments.some((experiment) => experiment.exp_id === value);
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("triage.picker.label")}
			</label>
			<Select value={value ?? undefined} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} className="w-full min-w-0 sm:w-auto sm:min-w-80" data-testid={testId}>
					<SelectValue placeholder={t("triage.picker.placeholder")} />
				</SelectTrigger>
				<SelectContent>
					{!known && value && (
						<SelectItem value={value}>
							<span className="font-mono text-xs">{value}</span>
						</SelectItem>
					)}
					{experiments.map((experiment) => (
						<SelectItem key={experiment.exp_id} value={experiment.exp_id} data-testid={`${testId}-option-${experiment.exp_id}`}>
							<span>{experiment.name || experiment.exp_id}</span>
							<span className="text-muted-foreground font-mono text-xs">{experiment.exp_id}</span>
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}