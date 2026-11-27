import { JobRunButton } from "@/components/jobs/jobRunButton";
import { BuiltinReadOnlyNote } from "@/components/suites/builtinReadOnly";
import { AdmissionBadge } from "@/components/suites/suiteBadges";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { benchApi, getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useQuarantineTaskMutation, useRestoreTaskMutation } from "@/lib/store/apis/suiteOpsApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { SuiteSummary, SuiteTask } from "@/lib/types/bench";
import { SUITE_OPS_KINDS, type RepoSource, type TaskFromCommitParams, type TasksMineParams } from "@/lib/types/suiteops";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteops: typeof import("@/locales/en/suiteops.json");
	}
}

type T = ReturnType<typeof useTranslation<"suiteops">>["t"];

/** A job button that cannot run yet, with the reason as visible text under it (p11-7d). */
export function DisabledRun({ label, reason, testId }: { label: string; reason: string; testId: string }) {
	return (
		<div className="flex flex-col items-start gap-1">
			<Button disabled aria-describedby={`${testId}-why`} data-testid={testId}>
				{label}
			</Button>
			<WhyDisabled reason={reason} testId={`${testId}-why`} />
		</div>
	);
}

const splitList = (s: string) =>
	s
		.split(",")
		.map((v) => v.trim())
		.filter(Boolean);

/**
 * Ops part of a suite (web parity lane L11): pausing tasks. Since M6-11 and M6-12 it is a section of
 * the Build tab: the readiness steps live in the ReadinessChecklist (Build section 4 and the Tasks tab),
 * and task from a commit and history mining are the "From a bug fix commit" source of Build "2. Add tasks".
 */
export function OpsTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suiteops");
	const session = useSession();
	const builtin = isBuiltinSuite(suite); // built-in library: read-only for every org
	const writable = canUse(session, "member") && !builtin;
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-ops-tab">
			{builtin && <BuiltinReadOnlyNote testId="suite-ops-builtin" />}
			{!writable && !builtin && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm" data-testid="suite-ops-viewer">
					{t("viewerNote")}
				</p>
			)}
			<QuarantineSection suite={suite} writable={writable} t={t} />
		</div>
	);
}

/** Refreshes the suite list and its tasks once a job that writes suite.lock or tasks ends. */
function useRefreshSuite(suiteId: string) {
	const dispatch = useAppDispatch();
	return useCallback(() => {
		dispatch(
			benchApi.util.invalidateTags([
				{ type: "BenchSuites", id: "LIST" },
				{ type: "BenchSuites", id: `${suiteId}/health` },
			]),
		);
	}, [dispatch, suiteId]);
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
	// M6-28: the reason box opens only after Pause is pressed on this row, not on every row.
	const [asking, setAsking] = useState(false);
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
		else {
			setReason("");
			setAsking(false);
		}
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
							<Button
								size="sm"
								variant="outline"
								disabled={busy}
								onClick={() => void doRestore()}
								data-testid={`suite-ops-restore-${task.id}`}
							>
								{t("health.restore")}
							</Button>
						) : !asking ? (
							<Button size="sm" variant="outline" onClick={() => setAsking(true)} data-testid={`suite-ops-quarantine-${task.id}`}>
								{t("health.quarantine")}
							</Button>
						) : null}
					</span>
				)}
			</div>
			{writable && !task.quarantine && asking && (
				<form
					className="flex flex-wrap items-center gap-2"
					aria-label={t("health.askTitle", { task: task.id })}
					onSubmit={(e) => {
						e.preventDefault();
						void doQuarantine();
					}}
					data-testid={`suite-ops-ask-${task.id}`}
				>
					<Input
						className="h-8 w-full min-w-0 sm:w-72"
						value={reason}
						maxLength={500}
						autoFocus
						placeholder={t("health.reasonPlaceholder")}
						aria-label={t("health.reasonPlaceholder")}
						onChange={(e) => setReason(e.target.value)}
						data-testid={`suite-ops-reason-${task.id}`}
					/>
					<Button size="sm" type="submit" disabled={busy} data-testid={`suite-ops-quarantine-confirm-${task.id}`}>
						{t("health.quarantine")}
					</Button>
					<Button
						size="sm"
						variant="ghost"
						type="button"
						onClick={() => {
							setAsking(false);
							setError(null);
						}}
						data-testid={`suite-ops-quarantine-cancel-${task.id}`}
					>
						{t("health.cancel")}
					</Button>
				</form>
			)}
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
function RepoPicker({
	value,
	onChange,
	hosted,
	t,
	testId,
}: {
	value: RepoSource;
	onChange(v: RepoSource): void;
	hosted: boolean;
	t: T;
	testId: string;
}) {
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
						<Input
							value={value.github ?? ""}
							placeholder={t("source.githubPlaceholder")}
							onChange={(e) => onChange({ ...value, github: e.target.value })}
						/>
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

/**
 * M6-29: Build "2. Add tasks", source "From a bug fix commit": one repository and branch for both
 * ways of cutting tasks (one commit, or a search of the history).
 */
export function CommitSources({ suite, hosted, t }: { suite: SuiteSummary; hosted: boolean; t: T }) {
	const [src, setSrc] = useState<RepoSource>({ github: "" });
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-build-commit-sources">
			<RepoPicker value={src} onChange={setSrc} hosted={hosted} t={t} testId="suite-ops-commit-repo" />
			<FromCommitSection suite={suite} hosted={hosted} t={t} src={src} />
			<MineSection suite={suite} hosted={hosted} t={t} src={src} />
		</div>
	);
}

export function FromCommitSection({
	suite,
	hosted,
	t,
	src: shared,
}: {
	suite: SuiteSummary;
	hosted: boolean;
	t: T;
	/** The repository picked once above (CommitSources); without it the section has its own picker. */
	src?: RepoSource;
}) {
	const [own, setOwn] = useState<RepoSource>({ github: "" });
	const src = shared ?? own;
	const [id, setId] = useState("");
	const [base, setBase] = useState("");
	const [reference, setReference] = useState("");
	const [tests, setTests] = useState("");
	const repo = repoParams(src);
	const ready = repo && id.trim() && base.trim() && reference.trim();
	const params: TaskFromCommitParams | null = ready
		? {
				suite: suite.id,
				id: id.trim(),
				base: base.trim(),
				reference: reference.trim(),
				...repo,
				...(tests.trim() ? { tests: splitList(tests) } : {}),
			}
		: null;
	return (
		<Section title={t("fromCommit.title")} description={t("fromCommit.description")} testId="suite-ops-from-commit">
			<div className="flex min-w-0 flex-col gap-3">
				{!shared && <RepoPicker value={src} onChange={setOwn} hosted={hosted} t={t} testId="suite-ops-from-commit-repo" />}
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
						<DisabledRun label={t("fromCommit.run")} reason={t("fromCommit.missing")} testId="suite-ops-from-commit-disabled" />
					)}
				</div>
			</div>
		</Section>
	);
}

function MineSection({ suite, hosted, t, src: shared }: { suite: SuiteSummary; hosted: boolean; t: T; src?: RepoSource }) {
	const [own, setOwn] = useState<RepoSource>({ github: "" });
	const src = shared ?? own;
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
				{!shared && <RepoPicker value={src} onChange={setOwn} hosted={hosted} t={t} testId="suite-ops-mine-repo" />}
				<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2">
					<Field label={t("mine.since")}>
						<Input value={since} onChange={(e) => setSince(e.target.value)} />
					</Field>
					<Field label={t("mine.limit")}>
						<Input
							type="number"
							min={1}
							max={50}
							value={limit}
							onChange={(e) => setLimit(Math.min(50, Math.max(1, Number(e.target.value) || 1)))}
						/>
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
						<DisabledRun label={label} reason={t("mine.missing")} testId="suite-ops-mine-disabled" />
					)}
				</div>
			</div>
		</Section>
	);
}

export default OpsTab;