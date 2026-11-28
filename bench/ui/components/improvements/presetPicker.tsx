import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./w45impI18n";
import "./uff6I18n";

export type PresetName = "trial" | "small" | "medium" | "large" | "custom";

/** Trial sizes the server accepts (improve.MinTrialTasks, MaxTrialTasks, MaxTrialRounds, MaxTrialIdeas). */
export const TRIAL_LIMITS = { minTasks: 10, maxTasks: 40, maxRounds: 3, maxIdeas: 2 } as const;

/**
 * The sizes of A3.6 as the form shows them, and the spending limit each prefills. The server fills
 * every other part of a named preset (improve/spec.go); its switches can only be on, so the form
 * offers no way to turn one off. Custom starts from the small values. The trial sends its own task
 * count, rounds and ideas; the named presets use every ready task.
 */
export const PRESETS: Record<PresetName, { rounds: number; ideas: number; pool: number; limit: number }> = {
	trial: { rounds: 1, ideas: 1, pool: 12, limit: 3 },
	small: { rounds: 3, ideas: 2, pool: 40, limit: 15 },
	medium: { rounds: 5, ideas: 4, pool: 60, limit: 60 },
	large: { rounds: 8, ideas: 8, pool: 100, limit: 400 },
	custom: { rounds: 3, ideas: 2, pool: 40, limit: 15 },
};

export const PRESET_NAMES = ["trial", "small", "medium", "large", "custom"] as const satisfies readonly PresetName[];

/** Size cards: Small, Medium, Large and Custom, one picked. */
export function PresetPicker({ value, onChange }: { value: PresetName; onChange(next: PresetName): void }) {
	const { t } = useTranslation("w45imp");
	const { t: t6 } = useTranslation("uff6");
	return (
		<fieldset className="flex flex-col gap-1 text-xs" data-testid="new-improvement-preset">
			<legend className="text-foreground mb-1 text-sm font-medium">{t("form.size")}</legend>
			<div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
				{PRESET_NAMES.map((name) => {
					const p = PRESETS[name];
					return (
						<label
							key={name}
							className={cn(
								"hover:bg-muted/40 flex cursor-pointer flex-col gap-1 rounded-md border p-2",
								value === name && "border-primary ring-primary/30 ring-1",
							)}
						>
							<span className="flex items-center gap-2">
								<input
									type="radio"
									name="improvement-preset"
									value={name}
									checked={value === name}
									onChange={() => onChange(name)}
									data-testid={`new-improvement-preset-${name}`}
								/>
								<span className="text-foreground font-medium">{name === "trial" ? t6("preset.trial") : t(`preset.${name}`)}</span>
							</span>
							<span className="text-muted-foreground">
								{name === "custom"
									? t("preset.customLine")
									: name === "trial"
										? t6("preset.trialLine", { tasks: p.pool, rounds: p.rounds, ideas: p.ideas })
										: t6("preset.line", { rounds: p.rounds, ideas: p.ideas })}
							</span>
						</label>
					);
				})}
			</div>
		</fieldset>
	);
}