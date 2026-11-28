import { defaultJobModel, plainJobError } from "@/components/findings/jobModel";
import { GroupedModelPicker, type PickedModel } from "@/components/models/groupedModelPicker";
import { Input } from "@/components/ui/input";
import { PaidButton } from "@/components/ui/paidButton";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { formatUsd } from "@/lib/money";
import type { InboxItem, InboxSuite } from "@/lib/types/bench";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { BUILD_TASK_DEFAULT_CAP } from "./buildTask";
import { buildTaskBody, useStartBuildTaskMutation } from "./buildTaskApi";
import "./uft3I18n";

/** At most this many failures in one "Build tasks from these failures". */
export const BUILD_TASK_BULK_MAX = 10;

/** The total cap of n builds of cap each, rounded to the cent. */
export function bulkTotal(n: number, cap: number): number {
	return Math.round(n * cap * 100) / 100;
}

/** Round 7 lane T3: one tracetask.build job per picked failure, each with its own cap; always asks first. */
export function BuildTaskBulk({ items, suites }: { items: readonly InboxItem[]; suites: readonly InboxSuite[] }) {
	const { t } = useTranslation("uft3");
	const ids = useId();
	const open = items.filter((i) => i.state === "new" || i.state === "snoozed");
	const models = useGetModelsQuery();
	const fallback = useMemo(() => defaultJobModel(models.data), [models.data]);
	const [picked, setPicked] = useState<PickedModel | null>(null);
	const model = (picked ?? fallback)?.model ?? "";
	const provider = (picked ?? fallback)?.provider ?? "";
	const [suite, setSuite] = useState(suites[0]?.id ?? "");
	const [cap, setCap] = useState(String(BUILD_TASK_DEFAULT_CAP));
	const [chosen, setChosen] = useState<string[]>([]);
	const [start] = useStartBuildTaskMutation();
	const [busy, setBusy] = useState(false);
	const [outcome, setOutcome] = useState<{ ok: number; errors: string[] } | null>(null);
	const capN = Number(cap);
	const total = bulkTotal(chosen.length, capN > 0 ? capN : 0);
	const tooMany = chosen.length > BUILD_TASK_BULK_MAX;
	if (open.length === 0 || suites.length === 0) return null;
	const toggle = (id: string) => setChosen((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]));
	const run = async () => {
		setBusy(true);
		const errors: string[] = [];
		let ok = 0;
		for (const id of chosen) {
			try {
				await start({ item: id, body: buildTaskBody(suite, model, capN, provider) }).unwrap();
				ok++;
			} catch (e) {
				errors.push(t("bulkFailed", { id, error: plainJobError(getBenchErrorMessage(e)) }));
			}
		}
		setOutcome({ ok, errors });
		setBusy(false);
	};
	return (
		<div className="flex flex-col gap-3" data-testid="build-task-bulk">
			<fieldset className="flex max-h-64 flex-col gap-1 overflow-auto">
				<legend className="mb-1 text-xs">{t("bulkPick")}</legend>
				{open.map((i) => (
					<label key={i.id} className="flex min-h-10 items-center gap-2 text-sm">
						<input type="checkbox" checked={chosen.includes(i.id)} onChange={() => toggle(i.id)} data-testid={`build-task-bulk-${i.id}`} />
						<span className="truncate">{i.title || i.id}</span>
					</label>
				))}
			</fieldset>
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-0.5 text-xs" htmlFor={`${ids}-suite`}>
					{t("benchmark")}
					<select
						id={`${ids}-suite`}
						className="border-input bg-background h-8 rounded-md border px-2 text-sm"
						value={suite}
						onChange={(e) => setSuite(e.target.value)}
					>
						{suites.map((s) => (
							<option key={s.id} value={s.id}>
								{s.id}
							</option>
						))}
					</select>
				</label>
				<GroupedModelPicker value={picked ?? fallback} onChange={setPicked} label={t("model")} testId="build-task-bulk-model" />
				<label className="flex flex-col gap-0.5 text-xs" htmlFor={`${ids}-cap`}>
					{t("cap")}
					<Input id={`${ids}-cap`} className="h-8 w-24" type="number" min="0.01" max="5" step="0.01" value={cap} onChange={(e) => setCap(e.target.value)} />
				</label>
				<PaidButton
					size="sm"
					verb={t("bulkVerb", { count: chosen.length })}
					capUsd={total > 0 ? total : null}
					confirm="always"
					confirmText={t("bulkAsk", { count: chosen.length, model, suite, each: formatUsd(capN), total: formatUsd(total) })}
					disabled={busy || chosen.length === 0 || tooMany || !model || !suite || !(capN > 0)}
					onConfirm={run}
					testId="build-task-bulk-start"
				/>
			</div>
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="build-task-bulk-total">
				{t("bulkTotal", { total: formatUsd(total) })}
			</p>
			{tooMany && (
				<p className="text-destructive text-sm" role="alert">
					{t("bulkMax")}
				</p>
			)}
			{outcome && (
				<div className="flex flex-col gap-1 text-sm" role="status" data-testid="build-task-bulk-outcome">
					<p>{t("bulkStarted", { ok: outcome.ok, count: chosen.length })}</p>
					{outcome.errors.map((e) => (
						<p key={e} className="text-destructive">
							{e}
						</p>
					))}
				</div>
			)}
		</div>
	);
}
