import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { useConfirm } from "@/components/platform/confirmDialog";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { StatusPill } from "@/components/ui/statusPill";
import {
	useGetImprovementStudiesQuery,
	useGetImprovementStudyQuery,
	useStopImprovementStudyMutation,
} from "@/lib/store/apis/improveStudyApi";
import type { ImprovementStatus } from "@/lib/types/improve";
import type { StudyView } from "@/lib/types/improvementStudy";
import { Link } from "@tanstack/react-router";
import { Layers } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { points, statusTone, usd } from "./improve.utils";
import "./ufl7I18n";
import i18n from "@/lib/i18n";

/** True while a study can still change (poll then). */
export function isLiveStudy(s: Pick<StudyView, "status" | "running">): boolean {
	return s.running || s.status === "planned" || s.status === "running";
}

/** The study's result line, or undefined before every loop is done. */
export function studyVerdictLine(t: (k: string, o?: Record<string, unknown>) => string, s: StudyView): string | undefined {
	const f = s.final;
	if (!f) return undefined;
	return `${f.headline} ${t("studies.gain", { gain: points(f.gain), low: points(f.ci95[0]), high: points(f.ci95[1]), better: f.better, loops: f.loops })}`;
}

/** The Studies tab of Auto-improve: every study, open one, stop one (admins). */
export function StudiesTab({ admin, onCreate }: { admin: boolean; onCreate?: () => void }) {
	const { t } = useTranslation("ufl7");
	const [poll, setPoll] = useState(true);
	const q = useGetImprovementStudiesQuery(undefined, { pollingInterval: poll ? 10_000 : 0, skipPollingIfUnfocused: true });
	const [open, setOpen] = useState<string | null>(null);
	const rows = q.data?.studies ?? [];
	const live = rows.some(isLiveStudy);
	useEffect(() => {
		if (q.data) setPoll(live);
	}, [q.data, live]);
	if (q.isLoading) return <BlockSkeleton rows={3} testId="studies-loading" />;
	if (q.error || !q.data)
		return <LoadError title={t("studies.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="studies-error" />;
	if (rows.length === 0) {
		return (
			<EmptyState
				icon={Layers}
				title={t("studies.emptyTitle")}
				body={t("studies.emptyBody")}
				action={admin && onCreate ? { label: t("studies.emptyAction"), onClick: onCreate } : undefined}
				testId="studies-empty"
			/>
		);
	}
	return (
		<>
			<div className="overflow-x-auto rounded-lg border">
				<table className="w-full text-left text-sm" data-testid="studies-table">
					<thead className="text-muted-foreground border-b text-xs">
						<tr>
							<th className="px-3 py-2 font-medium">{t("studies.name")}</th>
							<th className="px-3 py-2 font-medium">{t("studies.agent")}</th>
							<th className="px-3 py-2 font-medium">{t("studies.status")}</th>
							<th className="px-3 py-2 font-medium">{t("studies.loops")}</th>
							<th className="px-3 py-2 text-right font-medium">{t("studies.spend")}</th>
						</tr>
					</thead>
					<tbody>
						{rows.map((s) => (
							<tr key={s.id} className="hover:bg-muted/40 border-b last:border-0">
								<td className="px-3 py-2">
									<button
										type="button"
										className="text-foreground font-medium hover:underline"
										onClick={() => setOpen(s.id)}
										data-testid={`study-open-${s.id}`}
									>
										{s.name}
									</button>
								</td>
								<td className="text-muted-foreground px-3 py-2 font-mono text-xs">{s.agent}</td>
								<td className="px-3 py-2">
									<StudyStatus s={s} />
								</td>
								<td className="px-3 py-2 tabular-nums">{t("studies.loopsValue", { done: s.loops.length, total: s.loops_max })}</td>
								<td className="px-3 py-2 text-right tabular-nums">{usd(s.cost_usd)}</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			{open && <StudyDialog id={open} admin={admin} onClose={() => setOpen(null)} />}
		</>
	);
}

function StudyStatus({ s }: { s: StudyView }) {
	const { t } = useTranslation("improve");
	return (
		<StatusPill tone={statusTone(s.status as ImprovementStatus)} pulse={s.running}>
			{t(`status.${s.status}`, { defaultValue: s.status })}
		</StatusPill>
	);
}

/** One study: state, limit, held-out size, each loop's run, the result, and Stop for admins. */
export function StudyDialog({ id, admin, onClose }: { id: string; admin: boolean; onClose(): void }) {
	const { t } = useTranslation("ufl7");
	const confirm = useConfirm();
	const q = useGetImprovementStudyQuery(id, { pollingInterval: 10_000, skipPollingIfUnfocused: true });
	const [stop, stopState] = useStopImprovementStudyMutation();
	const [stopError, setStopError] = useState<unknown>(null);
	const s = q.data;
	const onStop = async () => {
		if (!s || !(await confirm(t("studies.stopConfirm", { name: s.name })))) return;
		setStopError(null);
		try {
			await stop(id).unwrap();
		} catch (e) {
			setStopError(e);
		}
	};
	const tl = (k: string, o?: Record<string, unknown>): string => String(i18n.t(k as never, { ns: "ufl7", ...o } as never));
	const verdict = s ? studyVerdictLine(tl, s) : undefined;
	return (
		<Dialog
			open
			onOpenChange={(next) => !next && onClose()}
			title={s?.name ?? id}
			closeLabel={t("studies.close")}
			side="right"
			testId="study-dialog"
			footer={
				admin && s && isLiveStudy(s) ? (
					<Button variant="outline" size="sm" disabled={stopState.isLoading} onClick={() => void onStop()} data-testid="study-stop">
						{t("studies.stop")}
					</Button>
				) : undefined
			}
		>
			{q.isLoading ? (
				<BlockSkeleton rows={3} testId="study-loading" />
			) : q.error || !s ? (
				<LoadError title={t("studies.detailError")} error={q.error} onRetry={() => void q.refetch()} testId="study-error" />
			) : (
				<div className="flex flex-col gap-4 text-sm" data-testid="study-detail">
					<InlineError error={stopError} title={t("studies.stopError")} testId="study-stop-error" />
					<dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
						<dt className="text-muted-foreground">{t("studies.status")}</dt>
						<dd>
							<StudyStatus s={s} />
						</dd>
						<dt className="text-muted-foreground">{t("studies.reason")}</dt>
						<dd>{s.reason}</dd>
						<dt className="text-muted-foreground">{t("studies.agent")}</dt>
						<dd className="font-mono text-xs break-all">{s.agent}</dd>
						<dt className="text-muted-foreground">{t("studies.heldOut")}</dt>
						<dd className="tabular-nums">{s.held_out}</dd>
						{s.limit && (
							<>
								<dt className="text-muted-foreground">{t("studies.limit")}</dt>
								<dd data-testid="study-limit">{s.limit}</dd>
							</>
						)}
						<dt className="text-muted-foreground">{t("studies.spend")}</dt>
						<dd className="tabular-nums">{usd(s.cost_usd)}</dd>
					</dl>
					<div className="flex flex-col gap-1">
						<span className="text-foreground font-medium">{t("studies.verdict")}</span>
						<p data-testid="study-verdict">{verdict ?? t("studies.noVerdict")}</p>
					</div>
					<div className="flex flex-col gap-1">
						<span className="text-foreground font-medium">{t("studies.loopRuns")}</span>
						{s.loops.length === 0 ? (
							<span className="text-muted-foreground">{t("studies.noLoops")}</span>
						) : (
							<ul className="flex flex-col gap-1" data-testid="study-loops">
								{s.loops.map((run) => (
									<li key={run}>
										<Link to="/improvements/$improvementId" params={{ improvementId: run }} className="font-mono text-xs hover:underline">
											{run}
										</Link>
									</li>
								))}
							</ul>
						)}
					</div>
				</div>
			)}
		</Dialog>
	);
}