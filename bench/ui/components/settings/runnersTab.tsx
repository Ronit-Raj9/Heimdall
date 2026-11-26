import { ForbiddenNote, isRoleRefusal } from "@/components/platform/accessNotes";
import { ConfirmButton, RunnerStatusBadge } from "@/components/platform/badges";
import { CopyBlock, OneTimeSecret } from "@/components/platform/oneTimeSecret";
import { isValidPool, runnerStartCommand } from "@/components/platform/platform.utils";
import { isSharedWorker, SharedWorkerNote } from "@/components/workers/workersTable";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useCreateRunnerTokenMutation,
	useListRunnerTokensQuery,
	useListRunnersQuery,
	useRemoveRunnerMutation,
	useRevokeRunnerTokenMutation,
} from "@/lib/store/apis/platformApi";
import type { OrgSummary, Runner, RunnerTokenCreated } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { BookOpen, Server, ServerCog } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { SettingsSection, TableFrame, When } from "./settingsBits";
import { WorkerPlatformsSection } from "./workerPlatformsSection";
import { ConnectedWorkers } from "@/components/workers/connectedWorkers";
import { WhereRunsGo } from "@/components/workers/whereRunsGo";
import "@/components/workers/hw05I18n";

/** The one file the save, start and install lines use for a runner token (N225; the server's runners.TokenFile). */
export const RUNNER_TOKEN_FILE = "~/.heimdall-runner-token";

/** Where the start command reads the token from: its -token-file, or the default path. */
export function tokenFileOf(command: string): string {
	return /-token-file\s+(\S+)/.exec(command)?.[1] ?? RUNNER_TOKEN_FILE;
}

/** The shell line that saves a runner token where the start command reads it, readable only by its owner. */
export function saveTokenCommand(token: string, file: string): string {
	const dir = file.includes("/") ? file.slice(0, file.lastIndexOf("/")) || "/" : ".";
	return `mkdir -p ${dir} && (umask 077 && printf '%s\\n' '${token}' > ${file})`;
}

/** Runners of the organization and the runner tokens that register them (admin). */
export function RunnersTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const { t: tu } = useTranslation("u11");
	const session = useSession();
	const runners = useListRunnersQuery(undefined, {
		pollingInterval: 15_000,
		skipPollingIfUnfocused: true,
	});
	const [removeRunner, removeState] = useRemoveRunnerMutation();
	const list = runners.data?.runners ?? [];
	return (
		<div className="flex flex-col gap-12" data-testid="settings-runners">
			<WhereRunsGo />
			<WorkerPlatformsSection />
			<ConnectedWorkers />
			<SettingsSection
				title={t("settings.runners.title")}
				description={t("settings.runners.description", { org: org.name })}
				testId="settings-runners-list"
			>
				{runners.isLoading ? (
					<BlockSkeleton rows={3} testId="runners-loading" />
				) : runners.isError || !runners.data ? (
					<LoadError
						title={t("settings.runners.loadError")}
						error={runners.error}
						onRetry={() => void runners.refetch()}
						testId="runners-error"
					/>
				) : list.length === 0 ? (
					<EmptyNote testId="runners-empty">{t("settings.runners.empty")}</EmptyNote>
				) : (
					<div className="grid gap-3 sm:grid-cols-2" data-testid="runners-table">
						{list.map((runner: Runner) => (
							<div
								key={runner.id}
								className="bg-card flex flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md"
								data-testid={`runner-row-${runner.id}`}
							>
								<div className="flex items-start gap-3">
									<span className="bg-muted text-muted-foreground relative flex size-9 shrink-0 items-center justify-center rounded-lg">
										<Server className="size-4" aria-hidden="true" />
										<span
											className={cn(
												"ring-card absolute -top-0.5 -right-0.5 size-2.5 rounded-full ring-2",
												runner.live ? "bg-chart-success animate-pulse" : "bg-muted-foreground/40",
											)}
											title={runner.live ? t("settings.runners.live") : tu("settings.offline")}
											aria-label={runner.live ? t("settings.runners.live") : tu("settings.offline")}
										/>
									</span>
									<div className="flex min-w-0 flex-1 flex-col">
										<span className="text-foreground truncate text-sm font-semibold" title={runner.name}>
											{isSharedWorker(runner.name, runner.pool) ? t("settings.runners.sharedTitle") : runner.name}
										</span>
										<span className="text-muted-foreground truncate text-xs" title={runner.host}>
											{isSharedWorker(runner.name, runner.pool) ? t("settings.runners.sharedHint") : runner.host}
										</span>
									</div>
									<RunnerStatusBadge state={runner.state} />
								</div>
								{isSharedWorker(runner.name, runner.pool) && (
									<SharedWorkerNote
										slots={runner.slots}
										labels={runner.labels}
										setupHref="#runner-token-setup"
										testId={`runner-row-${runner.id}-shared-note`}
									/>
								)}
								<div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
									<Badge variant="outline" className="rounded-full px-2 font-mono">
										{runner.pool}
									</Badge>
									<span className="tabular-nums">
										{t("settings.runners.slotsBusy", {
											busy: runner.running_slots,
											slots: runner.slots,
										})}
									</span>
									<span className="font-mono">{runner.version}</span>
									<span>
										{tu("settings.lastSeen")} <When value={runner.last_seen} />
									</span>
								</div>
								{!isSharedWorker(runner.name, runner.pool) && (
									<div className="flex justify-end border-t pt-3">
										<ConfirmButton
											label={t("settings.runners.remove")}
											question={t("settings.runners.confirmRemove", {
												name: runner.name,
											})}
											confirmLabel={t("settings.runners.remove")}
											cancelLabel={t("settings.runners.cancel")}
											onConfirm={() => void removeRunner(runner.id)}
											isLoading={removeState.isLoading && removeState.originalArgs === runner.id}
											testId={`runner-remove-${runner.id}`}
										/>
									</div>
								)}
							</div>
						))}
					</div>
				)}
				{removeState.error &&
					(isRoleRefusal(session, removeState.error) ? (
						<ForbiddenNote min="admin" error={removeState.error} testId="runner-remove-error" />
					) : (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="runner-remove-error">
							{t("settings.runners.removeError")}: {getBenchErrorMessage(removeState.error)}
						</p>
					))}
			</SettingsSection>
			<SettingsSection title={tu("settings.startCommand")} description={tu("settings.startCommandHint")} testId="runner-start">
				<CopyBlock
					value={runnerStartCommand(typeof window === "undefined" ? "" : window.location.origin, list[0]?.pool ?? "default")}
					label={tu("settings.startCommand")}
					testId="runner-start-command"
				/>
			</SettingsSection>
			<div id="runner-token-setup" className="scroll-mt-6">
				<RunnerTokensSection />
			</div>
		</div>
	);
}

function RunnerTokensSection() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const { t: th } = useTranslation("hw05");
	const formId = useId();
	const tokens = useListRunnerTokensQuery();
	const [create, createState] = useCreateRunnerTokenMutation();
	const [revoke, revokeState] = useRevokeRunnerTokenMutation();
	const [pool, setPool] = useState("");
	const [name, setName] = useState("");
	const [created, setCreated] = useState<RunnerTokenCreated | null>(null);
	const poolError = pool.trim() && !isValidPool(pool.trim()) ? t("settings.runners.poolInvalid") : undefined;

	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		if (poolError) return;
		try {
			const result = await create({
				...(pool.trim() ? { pool: pool.trim() } : {}),
				...(name.trim() ? { name: name.trim() } : {}),
			}).unwrap();
			setCreated(result);
			setPool("");
			setName("");
		} catch {
			// createState.error renders the failure.
		}
	};

	const list = tokens.data?.tokens ?? [];
	const writeError = (error: unknown, title: string, testId: string) =>
		isRoleRefusal(session, error) ? (
			<ForbiddenNote min="admin" error={error} testId={testId} />
		) : (
			<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
				{title}: {getBenchErrorMessage(error)}
			</p>
		);

	return (
		<SettingsSection
			title={t("settings.runners.tokensTitle")}
			description={t("settings.runners.tokensDescription")}
			testId="settings-runner-tokens"
		>
			{created ? (
				<OneTimeSecret
					title={t("settings.runners.created", { pool: created.info.pool })}
					secret={created.token}
					secretLabel={t("settings.runners.prefix")}
					onDone={() => setCreated(null)}
					testId="runner-token-created"
				>
					<ol className="flex flex-col gap-3 text-xs">
						<li className="flex flex-col gap-1.5">
							<span className="text-muted-foreground">1. {t("settings.runners.step1")}</span>
							<CopyBlock
								value={saveTokenCommand(created.token, tokenFileOf(created.command))}
								label={t("settings.runners.step1")}
								testId="runner-token-save"
							/>
						</li>
						<li className="flex flex-col gap-1.5">
							<span className="text-muted-foreground">2. {t("settings.runners.step2")}</span>
							<CopyBlock
								value={runnerStartCommand(typeof window === "undefined" ? "" : window.location.origin, created.info.pool, created.command)}
								label={t("settings.runners.step2")}
								testId="runner-token-command"
							/>
						</li>
						{created.install && (
							<li className="flex flex-col gap-1.5">
								<span className="text-muted-foreground">{th("runners.installLine")}</span>
								<CopyBlock value={created.install} label={th("runners.installLine")} testId="runner-token-install" />
							</li>
						)}
					</ol>
					<Link
						to="/docs"
						search={{ doc: "runners" } as never}
						hash="installing-a-runner"
						className="text-primary inline-flex items-center gap-1.5 text-xs font-medium underline-offset-4 hover:underline"
						data-testid="runner-install-doc"
					>
						<BookOpen className="size-3.5" aria-hidden="true" />
						{t("settings.runners.installDoc")}
					</Link>
				</OneTimeSecret>
			) : (
				<form
					className="bg-muted/30 grid gap-3 rounded-lg border p-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end"
					onSubmit={(event) => void onSubmit(event)}
					noValidate
					data-testid="runner-token-form"
				>
					<div className="flex min-w-0 flex-col gap-1.5">
						<label htmlFor={`${formId}-pool`} className="text-[13px] font-medium">
							{t("settings.runners.poolLabel")}
						</label>
						<Input
							id={`${formId}-pool`}
							autoComplete="off"
							placeholder={t("settings.runners.poolPlaceholder")}
							value={pool}
							onChange={(event) => setPool(event.target.value)}
							aria-invalid={Boolean(poolError) || undefined}
							className="font-mono"
							data-testid="runner-token-pool"
						/>
						{poolError && <span className="text-chart-error-ink text-xs">{poolError}</span>}
					</div>
					<div className="flex min-w-0 flex-col gap-1.5">
						<label htmlFor={`${formId}-name`} className="text-[13px] font-medium">
							{t("settings.runners.tokenName")}
						</label>
						<Input
							id={`${formId}-name`}
							autoComplete="off"
							placeholder={t("settings.runners.tokenNamePlaceholder")}
							value={name}
							onChange={(event) => setName(event.target.value)}
							data-testid="runner-token-name"
						/>
					</div>
					<Button type="submit" className="h-9 gap-1.5" isLoading={createState.isLoading} dataTestId="runner-token-submit">
						<ServerCog className="size-4" aria-hidden="true" />
						{t("settings.runners.create")}
					</Button>
				</form>
			)}
			{createState.error && !created && writeError(createState.error, t("settings.runners.createError"), "runner-token-create-error")}

			{tokens.isLoading ? (
				<BlockSkeleton rows={2} testId="runner-tokens-loading" />
			) : tokens.isError || !tokens.data ? (
				<LoadError
					title={t("settings.runners.tokensLoadError")}
					error={tokens.error}
					onRetry={() => void tokens.refetch()}
					testId="runner-tokens-error"
				/>
			) : list.length === 0 ? (
				<EmptyNote testId="runner-tokens-empty">{t("settings.runners.tokensEmpty")}</EmptyNote>
			) : (
				<TableFrame testId="runner-tokens-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{t("settings.runners.tokenName")}</TableHead>
								<TableHead>{t("settings.runners.pool")}</TableHead>
								<TableHead>{t("settings.runners.prefix")}</TableHead>
								<TableHead>{t("settings.runners.tokenState")}</TableHead>
								<TableHead>{t("settings.runners.createdAt")}</TableHead>
								<TableHead>{t("settings.runners.lastUsed")}</TableHead>
								<TableHead className="text-right" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{list.map((token) => (
								<TableRow key={token.id} data-testid={`runner-token-row-${token.id}`} data-state={token.state}>
									<TableCell className="text-foreground font-medium">{token.name || "-"}</TableCell>
									<TableCell>
										<Badge variant="outline" className="font-mono">
											{token.pool}
										</Badge>
									</TableCell>
									<TableCell className="text-muted-foreground font-mono text-xs">hmr_{token.prefix}…</TableCell>
									<TableCell>
										<Badge variant={token.state === "active" ? "success" : token.state === "expired" ? "warning" : "outline"}>
											{t(`settings.runners.tokenStates.${token.state}`)}
										</Badge>
									</TableCell>
									<TableCell className="text-muted-foreground">
										<When value={token.created_at} />
									</TableCell>
									<TableCell className="text-muted-foreground">
										<When value={token.last_used_at} />
									</TableCell>
									<TableCell className="text-right">
										{token.state === "active" && (
											<ConfirmButton
												label={t("settings.runners.revoke")}
												question={t("settings.runners.confirmRevoke", {
													pool: token.pool,
												})}
												confirmLabel={t("settings.runners.confirm")}
												cancelLabel={t("settings.runners.cancel")}
												onConfirm={() => void revoke(token.id)}
												isLoading={revokeState.isLoading && revokeState.originalArgs === token.id}
												testId={`runner-token-revoke-${token.id}`}
											/>
										)}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</TableFrame>
			)}
			{revokeState.error && writeError(revokeState.error, t("settings.runners.revokeError"), "runner-token-revoke-error")}
		</SettingsSection>
	);
}