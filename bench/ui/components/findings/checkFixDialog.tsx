import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { formatUsd } from "@/lib/money";
import { notify } from "@/lib/notify";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useRerunFindingMutation, type Finding } from "@/lib/store/apis/findingsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CAP_MIN, findingExperiment, rerunBody, rerunCap, rerunEstimate } from "./findingPlans";
import { plainJobError } from "./jobModel";
import "./uff1I18n";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Check the fix": the comparison the failure came from is filled in from the finding's history
 * (shown read-only, with Change to pick another), the setup that failed is picked, and the rerun
 * of the finding's tasks with the old agent and the fixed agent starts only after a confirm that
 * names the runs and their estimated cost.
 */
export function CheckFixDialog({ finding, onDone }: { finding: Finding; onDone: () => void }) {
	const { t } = useTranslation("w443");
	const { t: u } = useTranslation("uff1");
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
	// One helper for the label and the confirm, so both name the same rounded number.
	const usd = estimate.usd === null ? null : formatUsd(estimate.usd).replace(/^\$/, "");
	// Every paid start has a cap (lane F1): it opens on the estimate with some room and follows it
	// until the user types one; the server stops the rerun there.
	const [capText, setCapText] = useState<string | null>(null);
	const suggested = rerunCap(estimate.usd);
	const capNum = capText === null ? suggested : Number(capText);
	const cap = Number.isFinite(capNum) && capNum >= CAP_MIN && capNum <= 50 ? capNum : null;
	const capStr = cap === null ? "" : cap.toFixed(2);
	const ask =
		usd === null
			? u("checkFix.askUnknown", { runs: estimate.runs, n, trials, cap: capStr })
			: u("checkFix.ask", { runs: estimate.runs, n, trials, usd, cap: capStr });

	// Paid work: PaidButton always asks first, naming the runs and the estimate (M7-26). The label
	// names the estimate as its amount (owner decision 1: the label always shows a dollar figure).
	const start = async () => {
		const res = await rerun({
			id: finding.id,
			body: rerunBody(expId, armValue, trials, cap ?? suggested),
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
			<label className="flex flex-col gap-1 text-sm">
				{u("checkFix.cap")}
				<input
					type="number"
					min={CAP_MIN}
					max={50}
					step={0.05}
					className={fieldClass}
					value={capText ?? String(suggested)}
					onChange={(e) => setCapText(e.target.value)}
					data-testid="check-fix-cap"
				/>
			</label>
			<p className="text-muted-foreground text-sm" data-testid="check-fix-confirm">
				{t("checkFix.confirm", { n, trials })}
			</p>
			{state.error ? (
				<p className="text-destructive text-sm" role="alert" data-testid="check-fix-error">
					{plainJobError(getBenchErrorMessage(state.error))}
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
					capUsd={cap}
					estimateRuns={estimate.runs}
					estimateUsd={estimate.usd}
					confirm="always"
					kind="form"
					confirmText={ask}
					disabled={!expId || !armValue || cap === null || state.isLoading}
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