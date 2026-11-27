import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useRerunFindingMutation, type Finding } from "@/lib/store/apis/findingsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { findingExperiment, rerunEstimate } from "./findingPlans";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Check the fix": the comparison the failure came from is filled in from the finding's history
 * (shown read-only, with Change to pick another), the setup that failed is picked, and the rerun
 * of the finding's tasks with the old agent and the fixed agent starts only after a confirm that
 * names the runs and their estimated cost.
 */
export function CheckFixDialog({ finding, onDone }: { finding: Finding; onDone: () => void }) {
	const { t } = useTranslation("w443");
	const experiments = useListExperimentsQuery().data?.experiments ?? [];
	const fromHistory = findingExperiment(finding);
	const [expId, setExpId] = useState(fromHistory);
	const [editing, setEditing] = useState(!fromHistory);
	const [arm, setArm] = useState(finding.arm ?? "");
	const [trials, setTrials] = useState(3);
	const [rerun, state] = useRerunFindingMutation();
	const [note, setNote] = useState<string | null>(null);
	const source = experiments.find((e) => e.exp_id === expId);
	const arms = source?.arms ?? [];
	// The finding names its arm once the server sends it; with one arm only, it is the one.
	const armValue = arm || (arms.length === 1 ? arms[0] : "");
	const n = finding.tasks.length;
	const estimate = rerunEstimate(n, trials, source);
	const usd = estimate.usd === null ? null : estimate.usd.toFixed(2);
	const ask =
		usd === null
			? t("checkFix.askUnknown", { runs: estimate.runs, n, trials })
			: t("checkFix.ask", { runs: estimate.runs, n, trials, usd });

	// Paid work: PaidButton asks first (auto: a rerun has no cap, so it always asks), naming the runs and the estimate (M7-26). A rerun has
	// no server cap, so the label says it uses the model key.
	const start = async () => {
		const res = await rerun({
			id: finding.id,
			body: { exp_id: expId, arm: armValue, trials, yes: true },
		});
		if ("data" in res && res.data) {
			setNote(res.data.note ?? t("checkFix.started"));
			notify({ text: t("started", { ns: "toast", name: t("checkFix.verb") }) });
		}
	};

	return (
		<div className="border-border flex flex-col gap-3 rounded-lg border p-3" data-testid="check-fix-dialog">
			{editing ? (
				<label className="flex flex-col gap-1 text-sm">
					{t("checkFix.experiment")}
					<select
						className={fieldClass}
						value={expId}
						onChange={(e) => {
							setExpId(e.target.value);
							setArm("");
						}}
						data-testid="check-fix-experiment"
					>
						<option value="" />
						{experiments.map((e) => (
							<option key={e.exp_id} value={e.exp_id}>
								{e.name}
							</option>
						))}
					</select>
				</label>
			) : (
				<p className="flex flex-wrap items-center gap-2 text-sm" data-testid="check-fix-from">
					<span>{t("checkFix.from", { name: source?.name ?? expId })}</span>
					<Button size="sm" variant="ghost" onClick={() => setEditing(true)} data-testid="check-fix-change">
						{t("checkFix.change")}
					</Button>
				</p>
			)}
			{arms.length !== 1 && !(finding.arm && arm === finding.arm && !editing) && (
				<label className="flex flex-col gap-1 text-sm">
					{t("checkFix.arm")}
					<select className={fieldClass} value={arm} onChange={(e) => setArm(e.target.value)} data-testid="check-fix-arm">
						<option value="" />
						{arms.map((a) => (
							<option key={a} value={a}>
								{a}
							</option>
						))}
					</select>
				</label>
			)}
			<label className="flex flex-col gap-1 text-sm">
				{t("checkFix.trials")}
				<input
					type="number"
					min={1}
					max={5}
					className={fieldClass}
					value={trials}
					onChange={(e) => setTrials(Math.min(5, Math.max(1, Number(e.target.value) || 1)))}
					data-testid="check-fix-trials"
				/>
			</label>
			<p className="text-muted-foreground text-sm" data-testid="check-fix-confirm">
				{t("checkFix.confirm", { n, trials })}
			</p>
			{state.error ? (
				<p className="text-destructive text-sm" role="alert" data-testid="check-fix-error">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
			{note ? (
				<p className="text-sm" data-testid="check-fix-note">
					{note}
				</p>
			) : null}
			<div className="flex flex-wrap gap-2">
				<PaidButton
					size="sm"
					verb={t("checkFix.verb")}
					capUsd={null}
					estimateRuns={estimate.runs}
					estimateUsd={estimate.usd}
					confirm="auto"
					kind="form"
					confirmText={ask}
					disabled={!expId || !armValue || state.isLoading}
					onConfirm={start}
					testId="check-fix-start"
				/>
				<Button size="sm" variant="outline" onClick={onDone}>
					{t("actions.cancel")}
				</Button>
			</div>
		</div>
	);
}