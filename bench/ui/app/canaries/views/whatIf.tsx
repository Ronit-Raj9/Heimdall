import "@/components/settings/ufl8I18n";

import { BlockSkeleton, InlineError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useSimulateCanaryMutation, type CanarySimResult, type CanarySimulateBody } from "@/lib/store/apis/canarySimulateApi";
import type { CanaryDefinition } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** The study body for this monitor: its tasks, trials and sensitivity, the drop as a fraction. null when out of range. Exported for tests. */
export function whatIfBody(
	def: Pick<CanaryDefinition, "tasks" | "trials" | "sensitivity">,
	tasksText: string,
	dropPct: string,
): CanarySimulateBody | null {
	const tasks = Array.isArray(def.tasks) ? def.tasks.length : Number(tasksText);
	const drop = Number(dropPct);
	if (!Number.isInteger(tasks) || tasks < 1 || tasks > 50 || !(drop >= 0 && drop <= 100)) return null;
	const body: CanarySimulateBody = { tasks, trials: Math.min(5, Math.max(1, def.trials || 1)) };
	if (def.sensitivity) body.sensitivity = def.sensitivity;
	if (drop > 0) body.drop = drop / 100;
	return body;
}

const pct = (v: number) => `${Math.round(v * 1000) / 10}%`;

/** The answer in two lines. Exported for tests. */
export function WhatIfResult({ r, drop }: { r: CanarySimResult; drop: boolean }) {
	const { t } = useTranslation("ufl8");
	return (
		<p className="text-xs" data-testid="what-if-result">
			{drop
				? t("whatIf.detect", { first: pct(r.detect_first_trigger), by3: pct(r.detect_by_3) })
				: t("whatIf.falseAlarm", { month: pct(r.any_alarm_30) })}
		</p>
	);
}

/** "What if": the alarm rule of this monitor, simulated (no stored data, nothing runs, free). */
export function WhatIf({ def }: { def: CanaryDefinition }) {
	const { t } = useTranslation("ufl8");
	const [tasks, setTasks] = useState("8");
	const [drop, setDrop] = useState("10");
	const [run, state] = useSimulateCanaryMutation();
	const body = whatIfBody(def, tasks, drop);
	return (
		<details className="bg-card min-w-0 rounded-xl border p-3" data-testid="what-if">
			<summary className="cursor-pointer text-sm font-medium">{t("whatIf.title")}</summary>
			<div className="mt-3 flex min-w-0 flex-col gap-3">
				<div className="flex flex-wrap items-end gap-3">
					{!Array.isArray(def.tasks) && (
						<label className="flex flex-col gap-1 text-xs">
							<span className="text-muted-foreground">{t("whatIf.tasks")}</span>
							<Input
								type="number"
								min={1}
								max={50}
								value={tasks}
								onChange={(e) => setTasks(e.target.value)}
								className="w-24"
								data-testid="what-if-tasks"
							/>
						</label>
					)}
					<label className="flex flex-col gap-1 text-xs">
						<span className="text-muted-foreground">{t("whatIf.drop")}</span>
						<Input
							type="number"
							min={0}
							max={100}
							value={drop}
							onChange={(e) => setDrop(e.target.value)}
							className="w-24"
							data-testid="what-if-drop"
						/>
					</label>
					<Button
						size="sm"
						variant="outline"
						disabled={!body || state.isLoading}
						onClick={() => body && void run(body)}
						data-testid="what-if-run"
					>
						{t("whatIf.run")}
					</Button>
				</div>
				<p className="text-muted-foreground text-xs">{t("whatIf.hint")}</p>
				{state.isLoading ? (
					<BlockSkeleton rows={1} testId="what-if-loading" />
				) : state.error ? (
					<InlineError error={state.error} testId="what-if-error" />
				) : state.data ? (
					<WhatIfResult r={state.data} drop={Boolean(state.originalArgs?.drop)} />
				) : null}
			</div>
		</details>
	);
}