import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./w45impI18n";

export type PresetName = "small" | "medium" | "large" | "custom";

/**
 * The sizes of A3.6 as the form shows them, and the spending limit each prefills. The server fills
 * every other part of a named preset (improve/spec.go); its switches can only be on, so the form
 * offers no way to turn one off. Custom starts from the small values.
 */
export const PRESETS: Record<PresetName, { rounds: number; ideas: number; pool: number; limit: number }> = {
	small: { rounds: 3, ideas: 2, pool: 40, limit: 15 },
	medium: { rounds: 5, ideas: 4, pool: 60, limit: 60 },
	large: { rounds: 8, ideas: 8, pool: 100, limit: 400 },
	custom: { rounds: 3, ideas: 2, pool: 40, limit: 15 },
};

export const PRESET_NAMES = ["small", "medium", "large", "custom"] as const satisfies readonly PresetName[];

/** Size cards: Small, Medium, Large and Custom, one picked. */
export function PresetPicker({ value, onChange }: { value: PresetName; onChange(next: PresetName): void }) {
	const { t } = useTranslation("w45imp");
	return (
		<fieldset className="flex flex-col gap-1 text-xs" data-testid="new-improvement-preset">
			<legend className="text-foreground mb-1 text-sm font-medium">{t("form.size")}</legend>
			<div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
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
								<span className="text-foreground font-medium">{t(`preset.${name}`)}</span>
							</span>
							<span className="text-muted-foreground">
								{name === "custom" ? t("preset.customLine") : t("preset.line", { rounds: p.rounds, ideas: p.ideas, pool: p.pool })}
							</span>
						</label>
					);
				})}
			</div>
		</fieldset>
	);
}