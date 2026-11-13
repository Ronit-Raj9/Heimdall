import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ExperimentStatus } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

export interface ExperimentPickerProps {
	/** Newest first. */
	experiments: ExperimentStatus[];
	value: string | undefined;
	onChange: (expId: string) => void;
	disabled?: boolean;
}

/** Picks the experiment whose verdict is shown. The first entry is the newest. */
export function ExperimentPicker({ experiments, value, onChange, disabled }: ExperimentPickerProps) {
	const { t } = useTranslation("bench");
	// An id from the URL that the list does not carry still shows as selected.
	const known = value === undefined || experiments.some((experiment) => experiment.exp_id === value);

	return (
		<div className="flex flex-col gap-1">
			<label htmlFor="verdict-experiment-picker" className="text-muted-foreground text-xs">
				{t("verdict.picker.label")}
			</label>
			<Select value={value} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id="verdict-experiment-picker" className="min-w-72" data-testid="verdict-experiment-picker">
					<SelectValue placeholder={t("verdict.picker.placeholder")} />
				</SelectTrigger>
				<SelectContent data-testid="verdict-experiment-picker-content">
					{!known && value && (
						<SelectItem value={value} data-testid={`verdict-experiment-option-${value}`}>
							<span className="font-mono text-xs">{value}</span>
						</SelectItem>
					)}
					{experiments.map((experiment, index) => (
						<SelectItem key={experiment.exp_id} value={experiment.exp_id} data-testid={`verdict-experiment-option-${experiment.exp_id}`}>
							<span>{t("verdict.picker.option", { name: experiment.name || experiment.exp_id, id: experiment.exp_id })}</span>
							<span className="text-muted-foreground text-xs">{t(`verdict.picker.state.${experiment.state}`)}</span>
							{index === 0 && <span className="text-muted-foreground text-xs italic">{t("verdict.picker.newest")}</span>}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}