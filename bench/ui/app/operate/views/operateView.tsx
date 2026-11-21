import { JobRunButton } from "@/components/jobs/jobRunButton";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	operateApi,
	operateBackupUrl,
	useDeleteOperateBackupMutation,
	useGetOperateBackupsQuery,
	useGetOperateStatusQuery,
} from "@/lib/store/apis/operateApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { OperateBackup, OperateStatus } from "@/lib/types/operate";
import { Download, Info, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

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

function Section({ title, description, children, testId }: { title: string; description: string; children: ReactNode; testId: string }) {
	return (
		<Card data-testid={testId}>
			<CardHeader>
				<CardTitle>{title}</CardTitle>
				<CardDescription>{description}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-3">{children}</CardContent>
		</Card>
	);
}

function Note({ children, testId }: { children: ReactNode; testId: string }) {
	return (
		<p role="note" className="bg-muted/50 text-muted-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs" data-testid={testId}>
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

/** The backup zips list; exported for tests. */
export function BackupRows({ backups, onDelete }: { backups: OperateBackup[]; onDelete?(name: string): void }) {
	const { t } = useTranslation("operate");
	if (backups.length === 0) {
		return (
			<p className="text-muted-foreground text-xs" data-testid="operate-backups-empty">
				{t("evidence.backupsEmpty")}
			</p>
		);
	}
	return (
		<ul className="flex flex-col gap-1" data-testid="operate-backups">
			{backups.map((b) => (
				<li key={b.name} className="flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-sm border px-2 py-1 text-xs">
					<span className="min-w-0 truncate font-mono">{b.name}</span>
					<span className="flex items-center gap-2">
						<span className="text-muted-foreground">{formatBytes(b.size)}</span>
						<a
							href={operateBackupUrl(b.name)}
							download={b.name}
							className="text-primary inline-flex items-center gap-1 underline-offset-4 hover:underline"
							data-testid={`operate-backup-download-${b.name}`}
						>
							<Download className="size-3.5" aria-hidden="true" />
							{t("evidence.download")}
						</a>
						{onDelete && (
							<Button size="sm" variant="ghost" onClick={() => onDelete(b.name)} aria-label={`${t("evidence.delete")} ${b.name}`}>
								<Trash2 className="size-3.5" aria-hidden="true" />
							</Button>
						)}
					</span>
				</li>
			))}
		</ul>
	);
}

function BackupsList() {
	const { t } = useTranslation("operate");
	const query = useGetOperateBackupsQuery();
	const [remove] = useDeleteOperateBackupMutation();
	return (
		<div className="flex flex-col gap-2" data-testid="operate-backups-section">
			<p className="text-sm font-medium">{t("evidence.backupsTitle")}</p>
			{query.isError ? (
				<p className="text-muted-foreground flex items-center gap-2 text-xs">
					{t("evidence.backupsError")}
					<Button size="sm" variant="outline" onClick={() => void query.refetch()}>
						{t("evidence.retry")}
					</Button>
				</p>
			) : (
				<BackupRows backups={query.data?.backups ?? []} onDelete={(name) => void remove(name)} />
			)}
		</div>
	);
}

/** The operate panels for a server status; exported for tests. */
export function OperatePanels({ status, backups, onBackupDone }: { status: OperateStatus; backups?: ReactNode; onBackupDone?(): void }) {
	const { t } = useTranslation("operate");
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
		<div className="grid min-w-0 gap-4 lg:grid-cols-2">
			<Section title={t("health.title")} description={hosted ? t("health.hostedDescription") : t("health.description")} testId="operate-health">
				{(status.gpu_mode || status.driver) && (
					<p className="text-muted-foreground text-xs">
						{status.gpu_mode && t("health.gpuMode", { mode: status.gpu_mode })} {status.driver && t("health.driver", { driver: status.driver })}
					</p>
				)}
				<div className="flex flex-wrap gap-2">
					{has("operate.doctor") && <JobRunButton kind="operate.doctor" label={t("health.doctor")} />}
					{has("k8s.status") && <JobRunButton kind="k8s.status" label={t("health.k8s")} variant="outline" />}
					{hosted && (
						<a href="/workers" className="text-primary self-center text-sm font-medium underline-offset-4 hover:underline" data-testid="operate-workers-link">
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
			</Section>

			{showEvidence && (
				<Section
					title={t("evidence.title")}
					description={hosted ? t("evidence.hostedDescription") : t("evidence.description")}
					testId="operate-evidence"
				>
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
					<Field label={t("evidence.exp")} value={exp} onChange={setExp} testId="operate-evidence-exp" />
					<div className="flex flex-wrap gap-4">
						{status.mirror && <Check label={t("evidence.deep")} checked={deep} onChange={setDeep} testId="operate-evidence-deep" />}
						{status.mirror && <Check label={t("evidence.dryRun")} checked={dryRun} onChange={setDryRun} testId="operate-evidence-dry-run" />}
					</div>
					{hosted && status.mirror && !exp.trim() && (
						<Field label={t("evidence.days")} value={days} onChange={setDays} testId="operate-evidence-days" />
					)}
					{(status.mirror || hosted) && (
						<div className="flex flex-wrap gap-2">
							{has("evidence.verify") && (
								<JobRunButton kind="evidence.verify" params={compact({ exp, deep: status.mirror ? deep : false })} label={t("evidence.verify")} />
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
				<Section title={t("suite.title")} description={t("suite.description")} testId="operate-suite">
					<div className="grid gap-2 sm:grid-cols-2">
						<Field label={t("suite.suite")} value={suite} onChange={setSuite} testId="operate-suite-id" />
						<Field label={t("suite.task")} value={task} onChange={setTask} testId="operate-task-id" />
					</div>
					<Field label={t("suite.image")} value={image} onChange={setImage} testId="operate-image" />
					<div className="flex flex-wrap gap-2">
						{has("voice.check") && suite.trim() && <JobRunButton kind="voice.check" params={{ suite: suite.trim() }} label={t("suite.voice")} />}
						{has("visual.compare") && suite.trim() && task.trim() && image.trim() && (
							<JobRunButton kind="visual.compare" params={compact({ suite, task, image })} label={t("suite.compare")} variant="outline" />
						)}
					</div>
				</Section>
			)}

			{has("visual.capture") && (
				<Section title={t("suite.captureTitle")} description={t("suite.captureDescription")} testId="operate-capture">
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
				<Section title={t("simulator.title")} description={t("simulator.description")} testId="operate-simulator">
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
	);
}

/** The /operate page: server health, GPU, voice and visual checks, evidence jobs (hosted: the org's own evidence and backups). Each button runs a background job with a live log. */
export default function OperateView() {
	const { t } = useTranslation("operate");
	const query = useGetOperateStatusQuery();
	const dispatch = useAppDispatch();
	const status = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-6 p-6" data-testid="operate-screen">
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
						onBackupDone={() => dispatch(operateApi.util.invalidateTags(["OperateBackups"]))}
					/>
				</>
			)}
		</section>
	);
}
