import "@/app/leaderboard/views/uf4bI18n";

import { Button } from "@/components/ui/button";
import type { PairRun } from "@/lib/types/bench";
import { useId } from "react";
import { useTranslation } from "react-i18next";

/**
 * The setups shown first (M4-60): the first two whose outcome differs, else the first two. A pair
 * with two setups or fewer shows them all.
 */
export function defaultSetupSelection(runs: readonly Pick<PairRun, "arm" | "run">[]): string[] {
	if (runs.length <= 2) return runs.map((r) => r.arm);
	const first = runs[0];
	const other = runs.find((r) => r.run.outcome !== first.run.outcome);
	return [first.arm, (other ?? runs[1]).arm];
}

/** Keeps the picked arms in the pair's order, at most `max` of them. */
export function pickedRuns<T extends { arm: string }>(runs: readonly T[], picked: readonly string[], max: number): T[] {
	return runs.filter((r) => picked.includes(r.arm)).slice(0, max);
}

/** True when every setup of the pair ran in practice mode (scripted agent or model, no model called). */
export function allPractice(runs: readonly Pick<PairRun, "synthetic">[]): boolean {
	return runs.length > 0 && runs.every((r) => r.synthetic);
}

/**
 * One setup picker at the top of the pair view: a checkbox per setup and "Show all". It drives
 * every section below, so the sections need no tab strip of their own.
 */
export function SetupPicker({
	arms,
	picked,
	max,
	onChange,
}: {
	arms: readonly string[];
	picked: readonly string[];
	max: number;
	onChange: (next: string[]) => void;
}) {
	const { t } = useTranslation("uf4b");
	const { t: tb } = useTranslation("bench");
	const id = useId();
	if (arms.length <= 2) return null;
	const toggle = (arm: string) => {
		const on = picked.includes(arm);
		const next = on ? picked.filter((a) => a !== arm) : [...picked, arm];
		// Keep at least one setup, and never more than the view can load.
		if (next.length === 0 || next.length > max) return;
		onChange(arms.filter((a) => next.includes(a)));
	};
	return (
		<fieldset className="flex flex-col gap-2" data-testid="compare-setup-picker">
			<legend className="text-foreground mb-1 text-sm font-medium">
				{t("compare.setupsLegend", {
					shown: picked.length,
					total: arms.length,
				})}
			</legend>
			<div className="flex flex-wrap items-center gap-x-4 gap-y-2">
				{arms.map((arm) => (
					<label key={arm} htmlFor={`${id}-${arm}`} className="flex min-h-6 items-center gap-2 text-sm">
						<input
							id={`${id}-${arm}`}
							type="checkbox"
							className="accent-primary size-4"
							checked={picked.includes(arm)}
							onChange={() => toggle(arm)}
							data-testid={`compare-setup-${arm}`}
						/>
						{tb("runs.arm", { arm })}
					</label>
				))}
				{picked.length < Math.min(arms.length, max) && (
					<Button variant="outline" size="sm" onClick={() => onChange(arms.slice(0, max))} dataTestId="compare-setup-all">
						{arms.length > max ? t("compare.showFirst", { count: max }) : t("compare.showAll", { count: arms.length })}
					</Button>
				)}
			</div>
			{arms.length > max && <p className="text-muted-foreground text-xs">{t("compare.maxSetups", { count: max })}</p>}
		</fieldset>
	);
}