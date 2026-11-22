import { JobRunButton } from "@/components/jobs/jobRunButton";
import { BuiltinReadOnlyNote } from "@/components/suites/builtinReadOnly";
import { AdmissionBadge } from "@/components/suites/suiteBadges";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { benchApi, getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useQuarantineTaskMutation, useRestoreTaskMutation } from "@/lib/store/apis/suiteOpsApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { Job, SuiteSummary, SuiteTask } from "@/lib/types/bench";
import { SUITE_OPS_KINDS, type RepoSource, type TaskFromCommitParams, type TasksMineParams } from "@/lib/types/suiteops";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteops: typeof import("@/locales/en/suiteops.json");
	}
}

type T = ReturnType<typeof useTranslation<"suiteops">>["t"];

const splitList = (s: string) =>
	s
		.split(",")
		.map((v) => v.trim())
		.filter(Boolean);

/** Ops tab of a suite (web parity lane L11): checks and admission, quarantine, task from commit, mine. */
export function OpsTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suiteops");
	const session = useSession();
	const builtin = isBuiltinSuite(suite); // built-in library: read-only for every org
	const writable = canUse(session, "member") && !builtin;
	const hosted = session.authMode === "hosted";
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-ops-tab">
			{builtin && <BuiltinReadOnlyNote testId="suite-ops-builtin" />}
			{!writable && !builtin && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm" data-testid="suite-ops-viewer">
					{t("viewerNote")}
				</p>
			)}
			<ChecksSection suite={suite} writable={writable} hosted={hosted && !builtin} t={t} />
			<QuarantineSection suite={suite} writable={writable} t={t} />
			{writable && <FromCommitSection suite={suite} hosted={hosted} t={t} />}
			{writable && <MineSection suite={suite} hosted={hosted} t={t} />}
		</div>
	);
}

/** Refreshes the suite list and its tasks once a job that writes suite.lock or tasks ends. */
function useRefreshSuite(suiteId: string) {
	const dispatch = useAppDispatch();
	return useCallback(() => {
		dispatch(benchApi.util.invalidateTags([{ type: "BenchSuites", id: "LIST" }, { type: "BenchSuites", id: `${suiteId}/health` }]));
	}, [dispatch, suiteId]);
}

function ChecksSection({ suite, writable, hosted, t }: { suite: SuiteSummary; writable: boolean; hosted: boolean; t: T }) {
	const refresh = useRefreshSuite(suite.id);
	const [frozen, setFrozen] = useState(false);
	const lockDone = useCallback(
		(job: Job) => {
			refresh();
			setFrozen(job.status === "succeeded");
		},
		[refresh],
	);
	return (
		<Section title={t("checks.title")} description={t("checks.description")} testId="suite-ops-checks">
			{writable ? (
				<div className="flex flex-wrap items-start gap-2">
					<JobRunButton kind={SUITE_OPS_KINDS.lint} params={{ suite: suite.id }} label={t("checks.lint")} variant="outline" />
					<JobRunButton
						kind={SUITE_OPS_KINDS.lock}
						params={{ suite: suite.id }}
						label={t("checks.lock")}
						variant="outline"
						confirm={t("checks.lockConfirm", { suite: suite.id })}
						onDone={lockDone}
					/>
					<JobRunButton
						kind={SUITE_OPS_KINDS.admit}
						params={{ suite: suite.id }}
						label={t("checks.admit")}
						confirm={hosted ? t("checks.hostedAdmitConfirm", { suite: suite.id }) : t("checks.admitConfirm", { suite: suite.id })}
						onDone={refresh}
					/>
				</div>
			) : null}
			{frozen && (
				<p role="status" className="mt-2 text-sm" data-testid="suite-ops-lock-done">
					{t("checks.lockDone")}
				</p>
			)}
			{hosted && (
				<p className="text-muted-foreground mt-2 text-sm" data-testid="suite-ops-hosted-admit">
					{t("checks.hostedAdmit", { suite: suite.id })}
				</p>
			)}
		</Section>
	);
}

function QuarantineSection({ suite, writable, t }: { suite: SuiteSummary; writable: boolean; t: T }) {
	return (
		<Section
			title={t("health.title")}
			description={t("health.description")}
			testId="suite-ops-quarantine"
			actions={
				writable && suite.tasks.length > 0 ? (
					<JobRunButton
						kind={SUITE_OPS_KINDS.quarantineFlaky}
						params={{ suite: suite.id }}
						label={t("health.flaky")}
						confirm={t("health.flakyConfirm", { suite: suite.id })}
						variant="outline"
					/>
				) : undefined
			}
		>
			{suite.tasks.length === 0 ? (
				<EmptyState message={t("health.empty")} compact testId="suite-ops-no-tasks" />
			) : (
				<ul className="divide-border flex flex-col divide-y rounded-sm border">
					{suite.tasks.map((task) => (
						<QuarantineRow key={task.id} suiteId={suite.id} task={task} writable={writable} t={t} />
					))}
				</ul>
			)}
		</Section>
	);
}

function QuarantineRow({ suiteId, task, writable, t }: { suiteId: string; task: SuiteTask; writable: boolean; t: T }) {
	const [reason, setReason] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [quarantine, q] = useQuarantineTaskMutation();
	const [restore, r] = useRestoreTaskMutation();
	const busy = q.isLoading || r.isLoading;
	const doQuarantine = async () => {
		if (!reason.trim()) {
			setError(t("health.reasonRequired"));
			return;
		}
		setError(null);
		const res = await quarantine({ suite: suiteId, task: task.id, reason: reason.trim() });
		if ("error" in res && res.error) setError(getBenchErrorMessage(res.error));
		else setReason("");
	};
	const doRestore = async () => {
		setError(null);
		const res = await restore({ suite: suiteId, task: task.id });
		if ("error" in res && res.error) setError(getBenchErrorMessage(res.error));
	};
	return (
		<li className="flex flex-col gap-1 px-3 py-2" data-testid={`suite-ops-task-${task.id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-mono text-sm">{task.id}</span>
				{task.redacted && <span className="text-muted-foreground text-xs">{t("health.redacted")}</span>}
				{task.quarantine ? (
					<span className="text-chart-warning-ink text-xs">{t("health.quarantined", { reason: task.quarantine.reason })}</span>
				) : (
					<>
						<AdmissionBadge task={task} testId={`suite-ops-admission-${task.id}`} />
						<span className="sr-only">{t("health.active")}</span>
					</>
				)}
				{writable && (
					<span className="ml-auto flex items-center gap-2">
						{task.quarantine ? (
							<Button size="sm" variant="outline" disabled={busy} onClick={() => void doRestore()} data-testid={`suite-ops-restore-${task.id}`}>
								{t("health.restore")}
							</Button>
						) : (
							<>
								<Input
									className="h-8 w-56"
									value={reason}
									maxLength={500}
									placeholder={t("health.reasonPlaceholder")}
									aria-label={t("health.reasonPlaceholder")}
									onChange={(e) => setReason(e.target.value)}
									data-testid={`suite-ops-reason-${task.id}`}
								/>
								<Button size="sm" variant="outline" disabled={busy} onClick={() => void doQuarantine()} data-testid={`suite-ops-quarantine-${task.id}`}>
									{t("health.quarantine")}
								</Button>
							</>
						)}
					</span>
				)}
			</div>
			{error && (
				<p role="alert" className="text-destructive text-xs">
					{error}
				</p>
			)}
		</li>
	);
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-sm">
			<span className="text-muted-foreground">{label}</span>
			{children}
		</label>
	);
}

/** Repository picker: GitHub owner/name, or (local mode) a folder on disk. */
function RepoPicker({ value, onChange, hosted, t, testId }: { value: RepoSource; onChange(v: RepoSource): void; hosted: boolean; t: T; testId: string }) {
	const useGitHub = hosted || value.repo === undefined;
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={testId}>
			{!hosted && (
				<div className="flex gap-4 text-sm" role="radiogroup">
					<label className="flex items-center gap-1">
						<input type="radio" checked={useGitHub} onChange={() => onChange({ github: "" })} />
						{t("source.github")}
					</label>
					<label className="flex items-center gap-1">
						<input type="radio" checked={!useGitHub} onChange={() => onChange({ repo: "" })} />
						{t("source.local")}
					</label>
				</div>
			)}
			{useGitHub ? (
				<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2">
					<Field label={t("source.github")}>
						<Input value={value.github ?? ""} placeholder={t("source.githubPlaceholder")} onChange={(e) => onChange({ ...value, github: e.target.value })} />
					</Field>
					<Field label={t("source.branch")}>
						<Input value={value.branch ?? ""} onChange={(e) => onChange({ ...value, branch: e.target.value })} />
					</Field>
					<p className="text-muted-foreground text-xs sm:col-span-2">{t("source.githubHint")}</p>
				</div>
			) : (
				<Field label={t("source.local")}>
					<Input value={value.repo ?? ""} placeholder={t("source.localPlaceholder")} onChange={(e) => onChange({ repo: e.target.value })} />
					<span className="text-muted-foreground text-xs">{t("source.localHint")}</span>
				</Field>
			)}
		</div>
	);
}

/** The repository fields of job params, trimmed; null when no repository is picked. */
export function repoParams(v: RepoSource): RepoSource | null {
	if (v.repo !== undefined) return { repo: v.repo.trim() || "." };
	const github = (v.github ?? "").trim();
	if (!github) return null;
	const branch = (v.branch ?? "").trim();
	return branch ? { github, branch } : { github };
}

export function FromCommitSection({ suite, hosted, t }: { suite: SuiteSummary; hosted: boolean; t: T }) {
	const [src, setSrc] = useState<RepoSource>({ github: "" });
	const [id, setId] = useState("");
	const [base, setBase] = useState("");
	const [reference, setReference] = useState("");
	const [tests, setTests] = useState("");
	const repo = repoParams(src);
	const ready = repo && id.trim() && base.trim() && reference.trim();
	const params: TaskFromCommitParams | null = ready
		? { suite: suite.id, id: id.trim(), base: base.trim(), reference: reference.trim(), ...repo, ...(tests.trim() ? { tests: splitList(tests) } : {}) }
		: null;
	return (
		<Section title={t("fromCommit.title")} description={t("fromCommit.description")} testId="suite-ops-from-commit">
			<div className="flex min-w-0 flex-col gap-3">
				<RepoPicker value={src} onChange={setSrc} hosted={hosted} t={t} testId="suite-ops-from-commit-repo" />
				<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-3">
					<Field label={t("fromCommit.id")}>
						<Input value={id} onChange={(e) => setId(e.target.value)} data-testid="suite-ops-from-commit-id" />
					</Field>
					<Field label={t("fromCommit.base")}>
						<Input value={base} className="font-mono" onChange={(e) => setBase(e.target.value)} />
					</Field>
					<Field label={t("fromCommit.reference")}>
						<Input value={reference} className="font-mono" onChange={(e) => setReference(e.target.value)} />
					</Field>
				</div>
				<Field label={t("fromCommit.tests")}>
					<Input value={tests} placeholder="tests/test_*.py" onChange={(e) => setTests(e.target.value)} />
				</Field>
				<div>
					{params ? (
						<JobRunButton kind={SUITE_OPS_KINDS.fromCommit} params={params} label={t("fromCommit.run")} />
					) : (
						<Button disabled title={t("fromCommit.missing")} data-testid="suite-ops-from-commit-disabled">
							{t("fromCommit.run")}
						</Button>
					)}
				</div>
			</div>
		</Section>
	);
}

function MineSection({ suite, hosted, t }: { suite: SuiteSummary; hosted: boolean; t: T }) {
	const [src, setSrc] = useState<RepoSource>({ github: "" });
	const [since, setSince] = useState("");
	const [limit, setLimit] = useState(5);
	const [dryRun, setDryRun] = useState(true);
	const repo = repoParams(src);
	const refresh = useRefreshSuite(suite.id);
	const preview = dryRun;
	const params: TasksMineParams | null = repo
		? { suite: suite.id, ...repo, limit, dry_run: preview, ...(since.trim() ? { since: since.trim() } : {}) }
		: null;
	const label = preview ? t("mine.preview") : t("mine.run");
	return (
		<Section title={t("mine.title")} description={t("mine.description")} testId="suite-ops-mine">
			<div className="flex min-w-0 flex-col gap-3">
				<RepoPicker value={src} onChange={setSrc} hosted={hosted} t={t} testId="suite-ops-mine-repo" />
				<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2">
					<Field label={t("mine.since")}>
						<Input value={since} onChange={(e) => setSince(e.target.value)} />
					</Field>
					<Field label={t("mine.limit")}>
						<Input type="number" min={1} max={50} value={limit} onChange={(e) => setLimit(Math.min(50, Math.max(1, Number(e.target.value) || 1)))} />
					</Field>
				</div>
				<label className="flex items-center gap-2 text-sm">
					<input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} data-testid="suite-ops-mine-dry-run" />
					{t("mine.dryRun")}
				</label>
				{hosted && !dryRun && (
					<p className="text-muted-foreground text-xs" data-testid="suite-ops-mine-hosted">
						{t("mine.hostedDryRun")}
					</p>
				)}
				<div>
					{params ? (
						<JobRunButton kind={SUITE_OPS_KINDS.mine} params={params} label={label} onDone={preview ? undefined : refresh} />
					) : (
						<Button disabled title={t("mine.missing")} data-testid="suite-ops-mine-disabled">
							{label}
						</Button>
					)}
				</div>
			</div>
		</Section>
	);
}

export default OpsTab;
