import { pageWidthClass } from "@/components/shell/pageWidth";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useConfirm, type ConfirmFn } from "@/components/platform/confirmDialog";
import "@/components/settings/w4m8I18n";
import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { PageHeader, useEmbedded } from "@/components/shell";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	operateApi,
	operateBackupUrl,
	useDeleteOperateBackupMutation,
	useGetOperateBackupsQuery,
	useGetOperateStatusQuery,
} from "@/lib/store/apis/operateApi";
import i18n from "@/lib/i18n";
import { useAppDispatch } from "@/lib/store/hooks";
import type { OperateBackup, OperateStatus } from "@/lib/types/operate";
import { Archive, Camera, Cpu, Download, HardDrive, Info, Server, ShieldCheck, Shuffle, Stethoscope, Trash2, Users } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import "@/components/canaries/uf7bI18n";
import { HealthTile, Panel } from "./uxBits";
import { DoctorPanel } from "@/components/settings/doctorPanel";
import { ModalAppsPanel } from "@/components/settings/modalAppsPanel";
import { RoleGate } from "@/components/platform/accessNotes";
import "@/components/platform/ufintI18n";

/** Drops empty strings so a job gets only the fields the user filled in. */
function compact(o: Record<string, string | boolean | undefined>): Record<string, string | boolean> {
	const out: Record<string, string | boolean> = {};
	for (const [k, v] of Object.entries(o)) {
		if (typeof v === "string" ? v.trim() !== "" : v) out[k] = typeof v === "string" ? v.trim() : (v as boolean);
	}
	return out;
}

function Field({ label, value, onChange, testId }: { label: string; value: string; onChange(v: string): void; testId: string }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			<Input value={value} onChange={(e) => onChange(e.target.value)} data-testid={testId} />
		</label>
	);
}

function Check({ label, checked, onChange, testId }: { label: string; checked: boolean; onChange(v: boolean): void; testId: string }) {
	return (
		<label className="flex items-center gap-2 text-xs">
			<input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} data-testid={testId} />
			<span>{label}</span>
		</label>
	);
}

function Section({
	title,
	description,
	icon,
	children,
	testId,
}: {
	title: string;
	description?: string;
	icon?: LucideIcon;
	children: ReactNode;
	testId: string;
}) {
	return (
		<Panel title={title} description={description} icon={icon} testId={testId}>
			<div className="flex flex-col gap-3">{children}</div>
		</Panel>
	);
}

/** Server, runners, evidence and backup status at a glance; exported for tests. */
export function OperateTiles({ status, backupCount }: { status: OperateStatus; backupCount?: number }) {
	const { t } = useTranslation("operate");
	const hosted = status.hosted;
	return (
		<div className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" data-testid="operate-tiles">
			<HealthTile
				icon={Server}
				title={t("tiles.server")}
				tone="good"
				status={t("tiles.online")}
				line={hosted ? t("tiles.hosted") : t("tiles.local")}
				testId="operate-tile-server"
			/>
			<HealthTile
				icon={Users}
				title={t("tiles.runners")}
				tone={hosted ? "live" : "idle"}
				status={hosted ? t("tiles.yourRunners") : t("tiles.thisMachine")}
				line={
					hosted ? t("tiles.runnersHosted") : status.gpu_mode ? t("health.gpuMode", { mode: status.gpu_mode }) : t("tiles.runnersLocal")
				}
				testId="operate-tile-runners"
			/>
			<HealthTile
				icon={Cpu}
				title={t("tiles.pool")}
				tone={hosted ? "idle" : status.gpu_mode ? "good" : "idle"}
				status={hosted ? t("tiles.viaWorkers") : status.gpu_mode ? t("tiles.gpuReady") : t("tiles.cpuOnly")}
				line={status.driver ? t("health.driver", { driver: status.driver }) : t("tiles.poolLine")}
				testId="operate-tile-pool"
			/>
			<HealthTile
				icon={ShieldCheck}
				title={t("tiles.evidence")}
				tone={status.mirror ? "good" : "warn"}
				status={status.mirror ? t("tiles.mirrored") : t("tiles.noMirror")}
				line={
					backupCount !== undefined
						? t("tiles.backups", { count: backupCount })
						: status.mirror
							? t("tiles.mirrorKind", { kind: status.mirror })
							: t("tiles.noMirrorLine")
				}
				testId="operate-tile-evidence"
			/>
		</div>
	);
}

function Note({ children, testId }: { children: ReactNode; testId: string }) {
	return (
		<p
			role="note"
			className="bg-muted/50 text-muted-foreground flex items-start gap-2 rounded-lg border px-3 py-2 text-xs"
			data-testid={testId}
		>
			<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
			<span>{children}</span>
		</p>
	);
}

function formatBytes(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
	if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MiB`;
	return `${(n / 1024 / 1024 / 1024).toFixed(2)} GiB`;
}

/** "Saved results, 21 Nov 2026, 2.0 KiB" for one backup copy (M8-29). Exported for tests. */
export function backupLabel(b: OperateBackup, t: (k: string, o?: Record<string, unknown>) => string): string {
	const d = new Date(b.mod_time);
	const date = Number.isNaN(d.getTime()) ? "-" : d.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
	return t("operate.backupLabel", { date, size: formatBytes(b.size) });
}

/** The backup zips list; exported for tests. */
export function BackupRows({ backups, onDelete }: { backups: OperateBackup[]; onDelete?(name: string): void }) {
	const { t: m } = useTranslation("uf7b");
	const { t } = useTranslation("operate");
	const { t: tm } = useTranslation("w4m8");
	if (backups.length === 0) {
		return (
			<p
				className="text-muted-foreground rounded-lg border border-dashed px-3 py-4 text-center text-sm"
				data-testid="operate-backups-empty"
			>
				{t("evidence.backupsEmpty")}
			</p>
		);
	}
	return (
		<ul className="divide-border flex flex-col divide-y rounded-lg border" data-testid="operate-backups">
			{backups.map((b) => (
				<li
					key={b.name}
					className="hover:bg-accent/40 flex min-w-0 flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm transition-colors"
				>
					<span className="flex min-w-0 items-center gap-2">
						<Archive className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
						{/* M8-29: a readable line; the file name (with the org id) stays in the hover text. */}
						<span className="min-w-0 truncate text-xs" title={b.name} data-testid={`operate-backup-label-${b.name}`}>
							{backupLabel(b, m as never)}
						</span>
					</span>
					<span className="flex items-center gap-2">
						<span className="text-muted-foreground text-xs tabular-nums">{formatBytes(b.size)}</span>
						<a
							href={operateBackupUrl(b.name)}
							download={b.name}
							className="text-primary hover:bg-primary/10 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium"
							data-testid={`operate-backup-download-${b.name}`}
						>
							<Download className="size-3.5" aria-hidden="true" />
							{t("evidence.download")}
						</a>
						{onDelete && (
							<Button
								size="sm"
								variant="ghost"
								className="gap-1"
								onClick={() => onDelete(b.name)}
								aria-label={`${t("evidence.delete")} ${b.name}`}
								dataTestId={`operate-backup-delete-${b.name}`}
							>
								<Trash2 className="size-3.5" aria-hidden="true" />
								{tm("operate.delete")}
							</Button>
						)}
					</span>
				</li>
			))}
		</ul>
	);
}

/**
 * Deletes a backup copy only after the person confirms; a copy cannot be brought back. Waits for
 * the server, so a failed delete throws to the caller (M1-44). True when it was deleted.
 */
export async function deleteBackup(confirm: ConfirmFn, remove: (name: string) => unknown, name: string): Promise<boolean> {
	const ok = await confirm(i18n.t("operate.deleteConfirm", { ns: "w4m8", name }), {
		destructive: true,
		title: i18n.t("confirm.deleteBackup", { ns: "ufint", name }),
		confirmLabel: i18n.t("operate.delete", { ns: "w4m8" }),
	});
	if (!ok) return false;
	await remove(name);
	return true;
}

function BackupsList() {
	const { t } = useTranslation("operate");
	const query = useGetOperateBackupsQuery();
	const [remove] = useDeleteOperateBackupMutation();
	const confirm = useConfirm();
	const [err, setErr] = useState<unknown>(null);
	const onDelete = async (name: string) => {
		setErr(null);
		try {
			if (await deleteBackup(confirm, (n) => remove(n).unwrap(), name)) notify({ text: i18n.t("deleted", { ns: "toast", name }) });
		} catch (e) {
			setErr(e);
		}
	};
	return (
		<div className="flex flex-col gap-2" data-testid="operate-backups-section">
			<p className="flex items-center gap-2 text-sm font-medium">
				<HardDrive className="text-muted-foreground size-4" aria-hidden="true" />
				{t("evidence.backupsTitle")}
			</p>
			{query.isError ? (
				<p className="text-muted-foreground flex items-center gap-2 text-xs">
					{t("evidence.backupsError")}
					<Button size="sm" variant="outline" onClick={() => void query.refetch()}>
						{t("evidence.retry")}
					</Button>
				</p>
			) : (
				<BackupRows backups={query.data?.backups ?? []} onDelete={(name) => void onDelete(name)} />
			)}
			<InlineError error={err} testId="operate-backup-error" />
		</div>
	);
}

/** The operate panels for a server status; exported for tests. */
export function OperatePanels({
	status,
	backups,
	doctor,
	backupCount,
	onBackupDone,
	comparisons = [],
}: {
	status: OperateStatus;
	backups?: ReactNode;
	/** The "Check this server" panel (round 5 L8); a slot so tests render the panels without a store. */
	doctor?: ReactNode;
	backupCount?: number;
	onBackupDone?(): void;
	/** Finished comparisons the evidence check can be limited to (M7-72); empty: only "All". */
	comparisons?: readonly { id: string; label: string }[];
}) {
	const { t } = useTranslation("operate");
	const { t: m } = useTranslation("uf7b");
	const hosted = status.hosted;
	const [days, setDays] = useState("");
	const has = (kind: string) => status.kinds.includes(kind);
	const [gpuSuite, setGpuSuite] = useState("");
	const [exp, setExp] = useState("");
	const [deep, setDeep] = useState(false);
	const [dryRun, setDryRun] = useState(true);
	const [suite, setSuite] = useState("");
	const [task, setTask] = useState("");
	const [image, setImage] = useState("");
	const [browser, setBrowser] = useState("");
	const [desktop, setDesktop] = useState("");
	const [url, setUrl] = useState("");
	const [out, setOut] = useState("capture.png");
	const [simA, setSimA] = useState("");
	const [simB, setSimB] = useState("");
	const [condition, setCondition] = useState("");
	const evidenceKinds = ["evidence.verify", "evidence.mirror", "evidence.archive", "evidence.backup", "evidence.restore"];
	const showEvidence = evidenceKinds.some(has);

	return (
		<div className="flex min-w-0 flex-col gap-6">
			<OperateTiles status={status} backupCount={backupCount} />
			<div className="grid min-w-0 gap-6 lg:grid-cols-2 2xl:grid-cols-3">
				<Section title={t("health.title")} icon={Stethoscope} testId="operate-health">
					{(status.gpu_mode || status.driver) && (
						<p className="text-muted-foreground text-xs">
							{status.gpu_mode && t("health.gpuMode", { mode: status.gpu_mode })}{" "}
							{status.driver && t("health.driver", { driver: status.driver })}
						</p>
					)}
					<div className="flex flex-wrap gap-2">
						{has("operate.doctor") && <JobRunButton kind="operate.doctor" label={t("health.doctor")} />}
						{has("k8s.status") && <JobRunButton kind="k8s.status" label={t("health.k8s")} variant="outline" />}
						{hosted && (
							<a
								href="/workers"
								className="text-primary hover:bg-primary/10 self-center rounded-md px-3 py-1.5 text-sm font-medium"
								data-testid="operate-workers-link"
							>
								{t("health.runnersLink")}
							</a>
						)}
					</div>
					{has("gpu.check") && (
						<div className="flex flex-wrap items-end gap-2">
							<Field label={t("health.gpuSuite")} value={gpuSuite} onChange={setGpuSuite} testId="operate-gpu-suite" />
							<JobRunButton kind="gpu.check" params={compact({ suite: gpuSuite })} label={t("health.gpu")} variant="outline" />
						</div>
					)}
					{doctor}
				</Section>

				{showEvidence && (
					<Section title={t("evidence.title")} icon={ShieldCheck} testId="operate-evidence">
						{hosted ? (
							status.mirror ? (
								<p className="text-muted-foreground text-xs" data-testid="operate-mirror">
									{t("evidence.hostedMirror")}
								</p>
							) : (
								<Note testId="operate-no-mirror">{t("evidence.hostedNoMirror")}</Note>
							)
						) : status.mirror ? (
							<p className="text-muted-foreground font-mono text-xs" data-testid="operate-mirror">
								{t("evidence.mirrorAt", { location: status.mirror_location ?? status.mirror })}
							</p>
						) : (
							<Note testId="operate-no-mirror">{t("evidence.noMirror")}</Note>
						)}
						{/* M7-72: pick a comparison by name (type to search); empty is all of them. */}
						<label className="flex min-w-0 flex-col gap-1 text-xs">
							<span className="text-muted-foreground">{m("operate.which")}</span>
							<Input
								value={exp}
								onChange={(e) => setExp(e.target.value)}
								list="operate-evidence-exps"
								placeholder={m("operate.all")}
								data-testid="operate-evidence-exp"
							/>
							<datalist id="operate-evidence-exps">
								{comparisons.map((c) => (
									<option key={c.id} value={c.id} label={c.label} />
								))}
							</datalist>
						</label>
						<div className="flex flex-wrap gap-4">
							{status.mirror && <Check label={t("evidence.deep")} checked={deep} onChange={setDeep} testId="operate-evidence-deep" />}
							{status.mirror && (
								<Check label={t("evidence.dryRun")} checked={dryRun} onChange={setDryRun} testId="operate-evidence-dry-run" />
							)}
						</div>
						{hosted && status.mirror && !exp.trim() && (
							<Field label={t("evidence.days")} value={days} onChange={setDays} testId="operate-evidence-days" />
						)}
						{(status.mirror || hosted) && (
							<div className="flex flex-wrap gap-2">
								{has("evidence.verify") && (
									<JobRunButton
										kind="evidence.verify"
										params={compact({ exp, deep: status.mirror ? deep : false })}
										label={t("evidence.verify")}
									/>
								)}
								{has("evidence.mirror") && (
									<JobRunButton kind="evidence.mirror" params={compact({ exp })} label={t("evidence.mirror")} variant="outline" />
								)}
								{has("evidence.archive") && status.mirror && (
									<JobRunButton
										kind="evidence.archive"
										params={{
											...compact({ exp, dry_run: exp.trim() ? false : dryRun }),
											...(hosted && !exp.trim() && Number(days) > 0 ? { days: Math.floor(Number(days)) } : {}),
										}}
										label={t("evidence.archive")}
										variant="outline"
									/>
								)}
								{has("evidence.backup") && (
									<JobRunButton
										kind="evidence.backup"
										params={hosted ? compact({ exp }) : undefined}
										label={t("evidence.backup")}
										confirm={hosted ? t("evidence.hostedBackupConfirm") : t("evidence.backupConfirm")}
										variant="outline"
										onDone={() => onBackupDone?.()}
									/>
								)}
								{has("evidence.restore") &&
									status.mirror &&
									(exp.trim() ? (
										<JobRunButton
											kind="evidence.restore"
											params={{ exp: exp.trim() }}
											label={t("evidence.restore")}
											confirm={hosted ? t("evidence.hostedRestoreConfirm") : t("evidence.restoreConfirm")}
											variant="outline"
										/>
									) : (
										<span className="text-muted-foreground self-center text-xs">{t("evidence.restoreNeedsExp")}</span>
									))}
							</div>
						)}
						{hosted && backups}
					</Section>
				)}

				{(has("voice.check") || has("visual.compare")) && (
					<Section title={t("suite.title")} icon={ShieldCheck} testId="operate-suite">
						<div className="grid gap-2 sm:grid-cols-2">
							<Field label={t("suite.suite")} value={suite} onChange={setSuite} testId="operate-suite-id" />
							<Field label={t("suite.task")} value={task} onChange={setTask} testId="operate-task-id" />
						</div>
						<Field label={t("suite.image")} value={image} onChange={setImage} testId="operate-image" />
						<div className="flex flex-wrap gap-2">
							{has("voice.check") && suite.trim() && (
								<JobRunButton kind="voice.check" params={{ suite: suite.trim() }} label={t("suite.voice")} />
							)}
							{has("visual.compare") && suite.trim() && task.trim() && image.trim() && (
								<JobRunButton kind="visual.compare" params={compact({ suite, task, image })} label={t("suite.compare")} variant="outline" />
							)}
						</div>
					</Section>
				)}

				{has("visual.capture") && (
					<Section title={t("suite.captureTitle")} description={t("suite.captureDescription")} icon={Camera} testId="operate-capture">
						<div className="grid gap-2 sm:grid-cols-2">
							<Field label={t("suite.browser")} value={browser} onChange={setBrowser} testId="operate-capture-browser" />
							<Field label={t("suite.desktop")} value={desktop} onChange={setDesktop} testId="operate-capture-desktop" />
							<Field label={t("suite.url")} value={url} onChange={setUrl} testId="operate-capture-url" />
							<Field label={t("suite.out")} value={out} onChange={setOut} testId="operate-capture-out" />
						</div>
						{(browser.trim() !== "") !== (desktop.trim() !== "") && (
							<div>
								<JobRunButton
									kind="visual.capture"
									params={compact({ browser, desktop, url: desktop.trim() ? "" : url, out })}
									label={t("suite.capture")}
								/>
							</div>
						)}
					</Section>
				)}

				{has("simulator.spread") && (
					<Section title={t("simulator.title")} description={t("simulator.description")} icon={Shuffle} testId="operate-simulator">
						<div className="grid gap-2 sm:grid-cols-2">
							<Field label={t("simulator.a")} value={simA} onChange={setSimA} testId="operate-sim-a" />
							<Field label={t("simulator.b")} value={simB} onChange={setSimB} testId="operate-sim-b" />
							<Field label={t("simulator.condition")} value={condition} onChange={setCondition} testId="operate-sim-condition" />
						</div>
						{simA.trim() && simB.trim() && (
							<div>
								<JobRunButton kind="simulator.spread" params={compact({ a: simA, b: simB, condition })} label={t("simulator.run")} />
							</div>
						)}
					</Section>
				)}
			</div>
		</div>
	);
}

/** The /operate page: server health, GPU, voice and visual checks, evidence jobs (hosted: the org's own evidence and backups). Each button runs a background job with a live log. */
export default function OperateView() {
	const { t } = useTranslation("operate");
	const query = useGetOperateStatusQuery();
	const dispatch = useAppDispatch();
	const status = query.data;
	const backupsQuery = useGetOperateBackupsQuery(undefined, { skip: !status?.hosted });
	const experiments = useListExperimentsQuery();
	// Q-54: inside Settings the frame already pads the page; a second padding shifted the heading 16 px.
	const embedded = useEmbedded();
	return (
		<section
			className={cn(pageWidthClass(embedded ? "wide" : "dash"), "flex flex-col gap-6", !embedded && "p-4 sm:p-6")}
			data-testid="operate-screen"
		>
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="operate-loading" />
			) : query.isError || !status ? (
				<LoadError title={t("loadError")} error={query.error} onRetry={() => void query.refetch()} testId="operate-error" />
			) : (
				<>
					{status.hosted && <Note testId="operate-hosted-note">{t("hostedNote")}</Note>}
					<OperatePanels
						status={status}
						backups={status.hosted ? <BackupsList /> : undefined}
						doctor={
							<>
								<DoctorPanel hosted={status.hosted} />
								<RoleGate min="admin" fallback={null}>
									<ModalAppsPanel />
								</RoleGate>
							</>
						}
						backupCount={status.hosted ? backupsQuery.data?.backups.length : undefined}
						onBackupDone={() => dispatch(operateApi.util.invalidateTags(["OperateBackups"]))}
						comparisons={(experiments.data?.experiments ?? [])
							.filter((e) => e.state === "done")
							.map((e) => ({ id: e.exp_id, label: e.name || e.exp_id }))}
					/>
				</>
			)}
		</section>
	);
}