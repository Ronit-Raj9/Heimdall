import "@/app/experiments/views/experimentsI18n";

import {
	experimentPickerOptions,
	isCheckPracticeOrDebug,
	type EvaluationNames,
	type ExperimentPickerOption,
	type StatusWithDebug,
} from "@/components/experiments/experiments.utils";
import { useDriftMonitorNames } from "@/components/experiments/useDriftMonitorNames";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ExperimentStatus } from "@/lib/types/bench";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";

/** Experiments newest first, by start time or, before a start, freeze time. */
export function experimentsNewestFirst(experiments: readonly ExperimentStatus[]): ExperimentStatus[] {
	const at = (e: ExperimentStatus) => {
		const ms = Date.parse(e.started_at ?? e.frozen_at);
		return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
	};
	return [...experiments].sort((x, y) => at(y) - at(x));
}

/** What a picker shows: the listed choices, every choice by id (to name a URL value), and how many are hidden. */
export interface ExperimentPicker {
	options: ExperimentPickerOption[];
	all: ExperimentPickerOption[];
	hiddenCount: number;
}

/**
 * The choices of the review and triage pickers: readable names, newest first, checks, practice
 * runs and debug runs left out unless `showHidden`.
 */
export function useExperimentPicker(
	experiments: readonly StatusWithDebug[],
	showHidden: boolean,
	evalNames?: EvaluationNames,
): ExperimentPicker {
	const { t } = useTranslation("experimentsUx");
	const monitors = useDriftMonitorNames();
	return useMemo(() => {
		const tx = (k: string, o?: Record<string, string>) => String(t(k as never, (o ?? {}) as never));
		return {
			options: experimentPickerOptions(experiments, tx, evalNames, { showHidden, monitors }),
			all: experimentPickerOptions(experiments, tx, evalNames, { showHidden: true, monitors }),
			hiddenCount: experiments.filter(isCheckPracticeOrDebug).length,
		};
	}, [experiments, showHidden, evalNames, monitors, t]);
}

/** The experiment a picker page opens on: the URL value, else the first listed choice. Pure. */
export function defaultPickerExperiment(
	options: readonly ExperimentPickerOption[],
	fromUrl: string | null | undefined,
): string | undefined {
	return fromUrl || options[0]?.id;
}

export interface ExperimentSelectProps {
	picker: ExperimentPicker;
	value: string | null | undefined;
	onChange: (expId: string) => void;
	/** The "Also show checks, practice runs and debug runs" box (URL state all=1). */
	showHidden: boolean;
	onShowHidden: (on: boolean) => void;
	disabled?: boolean;
	testId: string;
}

/**
 * Picks the experiment a triage or review view reads, newest first, each option by its readable
 * name with the id as grey text. Checks, practice runs and debug runs are listed only when the
 * box under the select is ticked. An id from the URL that the list does not carry still shows as
 * selected.
 */
export function ExperimentSelect({ picker, value, onChange, showHidden, onShowHidden, disabled, testId }: ExperimentSelectProps) {
	const { t } = useTranslation("bench");
	const { t: tx } = useTranslation("experimentsUx");
	const id = useId();
	const { options } = picker;
	const listed = !value || options.some((o) => o.id === value);
	const current = value ? picker.all.find((o) => o.id === value) : undefined;
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("triage.picker.label")}
			</label>
			<Select value={value ?? undefined} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} className="w-full min-w-0 sm:w-auto sm:min-w-80" data-testid={testId}>
					<SelectValue placeholder={t("triage.picker.placeholder")}>{value ? (current?.label ?? value) : undefined}</SelectValue>
				</SelectTrigger>
				<SelectContent>
					{!listed && value && (
						<SelectItem value={value}>
							<span>{current?.label ?? value}</span>
							<span className="text-muted-foreground font-mono text-xs">{value}</span>
						</SelectItem>
					)}
					{options.map((o) => (
						<SelectItem key={o.id} value={o.id} data-testid={`${testId}-option-${o.id}`}>
							<span>{o.label}</span>
							<span className="text-muted-foreground font-mono text-xs">{o.secondary}</span>
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{picker.hiddenCount > 0 && (
				<label className="text-muted-foreground inline-flex min-h-6 items-center gap-2 text-sm" title={tx("picker.showHiddenHint")}>
					<input
						type="checkbox"
						checked={showHidden}
						onChange={(ev) => onShowHidden(ev.target.checked)}
						className="accent-primary size-4"
						data-testid={`${testId}-show-hidden`}
					/>
					{tx("picker.showHidden", { count: picker.hiddenCount })}
				</label>
			)}
		</div>
	);
}