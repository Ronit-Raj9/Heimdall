import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { getBenchErrorMessage, useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import { useCheckFindingMutation, useFindingReasonMutation, type Finding } from "@/lib/store/apis/findingsApi";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CheckFixDialog } from "./checkFixDialog";
import { FindingStateBadge } from "./findingList";

/** The newest history event that reopened the finding, shown at the top of a reopened one. */
export function reopenReason(f: Finding): string | null {
	if (f.state !== "reopened") return null;
	for (let i = f.history.length - 1; i >= 0; i--) {
		if (f.history[i].to === "reopened") return f.history[i].reason;
	}
	return null;
}

/**
 * One finding: what we saw (verified runs and tasks), the possible cause (model text, labelled as a
 * hypothesis), the checked quotes, the history, and the actions.
 */
export function FindingDetail({ finding, onClose }: { finding: Finding; onClose: () => void }) {
	const { t } = useTranslation("w443");
	const [mode, setMode] = useState<"none" | "check" | "expected" | "reopen">("none");
	const [reason, setReason] = useState("");
	const [act, actState] = useFindingReasonMutation();
	const [check, checkState] = useCheckFindingMutation();
	const reopened = reopenReason(finding);
	const fix = finding.fix;
	const error = actState.error ?? checkState.error;
	// The newest rerun is read once it shows finished (POST /findings/{id}/check).
	const lastRerun = finding.reruns?.[finding.reruns.length - 1];
	const rerunState = useGetExperimentQuery(lastRerun ?? "", { skip: !lastRerun }).data?.state;
	const checked = useRef<string | null>(null);
	const judged = finding.history.some((e) => e.experiment === lastRerun && e.by === "Heimdall");
	useEffect(() => {
		if (!lastRerun || judged || rerunState !== "done" || checked.current === lastRerun) return;
		checked.current = lastRerun;
		void check(finding.id);
	}, [lastRerun, judged, rerunState, check, finding.id]);

	const save = async () => {
		if (mode !== "expected" && mode !== "reopen") return;
		const res = await act({ id: finding.id, action: mode, reason });
		if ("data" in res) {
			setMode("none");
			setReason("");
		}
	};

	return (
		<section className="border-border flex min-w-0 flex-col gap-4 rounded-lg border p-4" data-testid="finding-detail">
			<header className="flex flex-wrap items-center gap-3">
				<h3 className="text-base font-semibold">{finding.title}</h3>
				<FindingStateBadge state={finding.state} />
				<Button size="sm" variant="ghost" className="ml-auto" onClick={onClose}>
					{t("detail.close")}
				</Button>
			</header>
			{reopened ? (
				<p className="text-chart-warning-ink text-sm" role="status" data-testid="finding-reopened">
					{reopened}
				</p>
			) : null}
			{finding.state === "fixed" && fix ? (
				<p className="text-chart-success-ink text-sm" data-testid="finding-fixed">
					{t("detail.fixed", { k: fix.fixed_passed, n: fix.fixed_runs, a: fix.old_passed, b: fix.old_runs })}
				</p>
			) : null}
			<div className="flex flex-col gap-2">
				<h4 className="text-sm font-medium">{t("detail.saw")}</h4>
				<p className="text-sm">
					{t("detail.tasks")}: {finding.tasks.map((x) => `${x.suite}/${x.task}`).join(", ")}
				</p>
				{finding.runs.length > 0 ? (
					<p className="flex flex-wrap gap-2 text-sm">
						{t("detail.runs")}:
						{finding.runs.map((r) => (
							<Link key={r} to="/runs/$runId" params={{ runId: r }} className="text-primary hover:underline">
								{r}
							</Link>
						))}
					</p>
				) : null}
			</div>
			{finding.origin === "model" && finding.hypothesis ? (
				<div className="flex flex-col gap-1" data-testid="finding-cause">
					<h4 className="text-sm font-medium">{t("detail.cause")}</h4>
					<p className="text-muted-foreground text-xs">{t("detail.hypothesisLabel")}</p>
					<p className="text-sm">{finding.hypothesis}</p>
				</div>
			) : null}
			{finding.quotes && finding.quotes.length > 0 ? (
				<div className="flex flex-col gap-1" data-testid="finding-quotes">
					<h4 className="text-sm font-medium">{t("detail.quotes")}</h4>
					<ul className="flex flex-col gap-1 text-sm">
						{finding.quotes.map((q) => (
							<li key={`${q.run_id}-${q.seq}`}>
								<Link to="/runs/$runId" params={{ runId: q.run_id }} hash={`step-${q.seq}`} className="text-primary hover:underline">
									{t("detail.step", { seq: q.seq })}
								</Link>{" "}
								<q>{q.text}</q>
							</li>
						))}
					</ul>
				</div>
			) : null}
			<div className="flex flex-col gap-1">
				<h4 className="text-sm font-medium">{t("detail.history")}</h4>
				<ul className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="finding-history">
					{finding.history.map((e, i) => (
						<li key={i}>
							{t("detail.event", {
								date: new Date(e.at).toLocaleDateString(),
								from: e.from ? t(`states.${e.from}` as "states.open") : "",
								to: t(`states.${e.to}` as "states.open"),
								reason: e.reason,
							})}
						</li>
					))}
				</ul>
			</div>
			{error ? (
				<p className="text-destructive text-sm" role="alert">
					{getBenchErrorMessage(error)}
				</p>
			) : null}
			{mode === "check" ? <CheckFixDialog finding={finding} onDone={() => setMode("none")} /> : null}
			{mode === "expected" || mode === "reopen" ? (
				<div className="flex flex-col gap-2" data-testid="finding-reason">
					<label className="flex flex-col gap-1 text-sm">
						{t("actions.reason")}
						<textarea
							className="border-border bg-background rounded-md border px-2 py-1.5 text-sm"
							maxLength={500}
							value={reason}
							onChange={(e) => setReason(e.target.value)}
						/>
					</label>
					<div className="flex gap-2">
						<Button size="sm" disabled={reason.trim().length < 3 || actState.isLoading} onClick={() => void save()}>
							{t("actions.save")}
						</Button>
						<Button size="sm" variant="outline" onClick={() => setMode("none")}>
							{t("actions.cancel")}
						</Button>
					</div>
				</div>
			) : null}
			{mode === "none" ? (
				<div className="flex flex-wrap gap-2">
					<Button size="sm" onClick={() => setMode("check")} data-testid="finding-check-fix">
						{t("actions.checkFix")}
					</Button>
					{finding.reruns && finding.reruns.length > 0 ? (
						<Button
							size="sm"
							variant="outline"
							disabled={checkState.isLoading}
							onClick={() => void check(finding.id)}
							data-testid="finding-judge"
						>
							{t("actions.judge")}
						</Button>
					) : null}
					{finding.state !== "expected" ? (
						<Button size="sm" variant="outline" onClick={() => setMode("expected")} data-testid="finding-expected">
							{t("actions.expected")}
						</Button>
					) : null}
					{finding.state === "fixed" || finding.state === "expected" ? (
						<Button size="sm" variant="outline" onClick={() => setMode("reopen")} data-testid="finding-reopen">
							{t("actions.reopen")}
						</Button>
					) : null}
				</div>
			) : null}
		</section>
	);
}