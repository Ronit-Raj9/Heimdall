// Weekly issue sync (W4-48): under From issues, turn the named repository into a benchmark that gets new
// tasks every week from its merged fixes, within a weekly spending cap.
import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { notify } from "@/lib/notify";
import "@/components/suites/uf6bI18n";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	type IssueSyncRepo,
	type IssueSyncRun,
	useCreateIssueSyncMutation,
	useDeleteIssueSyncMutation,
	useListIssueSyncsQuery,
	useRunIssueSyncMutation,
	useUpdateIssueSyncMutation,
} from "@/lib/store/apis/issueSyncApi";
import { useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import { ReactReduxContext } from "react-redux";
import "./w448I18n";

type T = ReturnType<typeof useTranslation<"w448">>["t"];

/** Splits "provider/model" into the model choice; empty or one part is the server's default. */
export function modelChoice(text: string): { provider?: string; model?: string } {
	const s = text.trim();
	const i = s.indexOf("/");
	if (i <= 0 || i === s.length - 1) return {};
	return { provider: s.slice(0, i), model: s.slice(i + 1) };
}

/** One run row: the counts and spend, or the reason when the run did not start. */
export function runLine(r: IssueSyncRun, t: T): string {
	const date = r.at.slice(0, 10);
	if (r.status === "started" || r.status === "nothing_new") {
		if (r.status === "nothing_new" && r.reason) return t("sync.runReason", { date, reason: r.reason });
		return t("sync.runRow", { date, started: r.started, skipped: r.skipped, spent: r.spent_usd.toFixed(2) });
	}
	return t("sync.runReason", { date, reason: r.reason ?? r.status });
}

/** The section; it renders nothing outside a data store (static markup tests of the parent page). */
export function WeeklySync(props: { repo: string; writable: boolean }) {
	return useContext(ReactReduxContext) ? <WeeklySyncSection {...props} /> : null;
}

function WeeklySyncSection({ repo, writable }: { repo: string; writable: boolean }) {
	const { t } = useTranslation("w448");
	const list = useListIssueSyncsQuery();
	const sync = (list.data?.repos ?? []).find((r) => r.repo.toLowerCase() === repo.toLowerCase());
	return (
		<section className="flex max-w-2xl flex-col gap-3 border-t pt-4" data-testid="weekly-sync">
			<h3 className="text-sm font-semibold">{t("sync.heading")}</h3>
			{!repo ? (
				<p className="text-muted-foreground">{t("sync.pickRepo")}</p>
			) : (
				<>
					<p className="text-muted-foreground">{t("sync.intro", { repo })}</p>
					{sync ? <SyncCard sync={sync} writable={writable} t={t} /> : <SyncForm repo={repo} writable={writable} t={t} />}
				</>
			)}
		</section>
	);
}

function SyncForm({ repo, writable, t }: { repo: string; writable: boolean; t: T }) {
	const [day, setDay] = useState(1);
	const [hour, setHour] = useState(3);
	const [max, setMax] = useState(10);
	const [cap, setCap] = useState(10);
	const [model, setModel] = useState("");
	const [error, setError] = useState("");
	const [create, state] = useCreateIssueSyncMutation();
	const days = t("sync.days", { returnObjects: true }) as string[];
	const onCreate = async () => {
		setError("");
		try {
			await create({ repo, weekday: day, hour_utc: hour, max_issues: max, weekly_usd: cap, model: modelChoice(model) }).unwrap();
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};
	const field = "border-input bg-background h-9 rounded-md border px-2";
	return (
		<div className="flex flex-col gap-2">
			<div className="flex flex-wrap items-end gap-2">
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("sync.day")}</span>
					<select className={field} value={day} onChange={(e) => setDay(Number(e.target.value))} data-testid="weekly-sync-day">
						{days.map((d, i) => (
							<option key={d} value={i}>
								{d}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("sync.hour")}</span>
					<input
						className={`${field} w-20`}
						type="number"
						min={0}
						max={23}
						value={hour}
						onChange={(e) => setHour(Number(e.target.value))}
					/>
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("sync.maxIssues")}</span>
					<input className={`${field} w-24`} type="number" min={1} max={30} value={max} onChange={(e) => setMax(Number(e.target.value))} />
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("sync.weeklyCap")}</span>
					<input
						className={`${field} w-24`}
						type="number"
						min={1}
						max={50}
						step={1}
						value={cap}
						onChange={(e) => setCap(Number(e.target.value))}
					/>
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">{t("sync.model")}</span>
					<input
						className={`${field} w-64`}
						value={model}
						placeholder={t("sync.modelPlaceholder")}
						onChange={(e) => setModel(e.target.value)}
					/>
				</label>
			</div>
			<div>
				<Button size="sm" disabled={!writable || state.isLoading} onClick={onCreate} dataTestId="weekly-sync-on">
					{t("sync.turnOn")}
				</Button>
			</div>
			{error && (
				<p className="text-destructive" role="alert">
					{error}
				</p>
			)}
		</div>
	);
}

function SyncCard({ sync, writable, t }: { sync: IssueSyncRepo; writable: boolean; t: T }) {
	const [run, runState] = useRunIssueSyncMutation();
	const [update, updateState] = useUpdateIssueSyncMutation();
	const [remove, removeState] = useDeleteIssueSyncMutation();
	const [error, setError] = useState("");
	const confirm = useConfirm();
	const { t: tu } = useTranslation("uf6b");
	// M6-75: Remove stops the weekly sync for good, so it asks first.
	const onRemove = async () => {
		const opts: DestructiveConfirmOptions = {
			destructive: true,
			title: tu("confirm.syncRemoveTitle", { repo: sync.repo }),
			confirmLabel: tu("confirm.syncRemoveYes"),
		};
		if (!(await confirm(tu("confirm.syncRemove", { repo: sync.repo }), opts))) return;
		await act(
			remove(sync.id)
				.unwrap()
				.then(() => notify({ text: tu("confirm.syncRemoved", { repo: sync.repo }) })),
		);
	};
	const busy = runState.isLoading || updateState.isLoading || removeState.isLoading;
	const act = async (p: Promise<unknown>) => {
		setError("");
		try {
			await p;
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};
	const runs = [...(sync.runs ?? [])].reverse();
	return (
		<div className="flex flex-col gap-2" data-testid="weekly-sync-card">
			{sync.private && <p className="text-muted-foreground text-xs">{t("sync.private")}</p>}
			{sync.paused && (
				<p className="text-muted-foreground" data-testid="weekly-sync-paused">
					{t("sync.paused")}
					{sync.paused_reason ? `: ${sync.paused_reason}` : ""}
				</p>
			)}
			<div className="flex flex-wrap gap-2">
				<Button
					size="sm"
					disabled={!writable || busy || sync.paused}
					onClick={() => act(run(sync.id).unwrap())}
					dataTestId="weekly-sync-run"
				>
					{t("sync.runNow")}
				</Button>
				<Button
					size="sm"
					variant="outline"
					disabled={!writable || busy}
					onClick={() => act(update({ id: sync.id, paused: !sync.paused }).unwrap())}
				>
					{sync.paused ? t("sync.resume") : t("sync.pause")}
				</Button>
				<Button size="sm" variant="outline" disabled={!writable || busy} onClick={() => void onRemove()}>
					{t("sync.remove")}
				</Button>
			</div>
			{error && (
				<p className="text-destructive" role="alert">
					{error}
				</p>
			)}
			<ul className="text-xs" data-testid="weekly-sync-runs">
				{runs.map((r) => (
					<li key={`${r.at}-${r.status}`}>{runLine(r, t)}</li>
				))}
			</ul>
		</div>
	);
}