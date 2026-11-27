import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, EmptyNote, InlineError, LoadError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { WhyDisabled, firstReason } from "@/components/ui/whyDisabled";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useDisconnectGithubMutation,
	useGetGithubRefsQuery,
	useGetGithubStatusQuery,
	useInstallGithubCIMutation,
	useListGithubReposQuery,
	useStartGithubConnectMutation,
} from "@/lib/store/apis/githubApi";
import { roleAtLeast } from "@/lib/store/apis/session";
import type { GithubCIMode, GithubRepo } from "@/lib/types/github";
import { ExternalLink, GitPullRequest, Github, Lock } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import "./uf8aI18n";
import { SettingsSection } from "./settingsBits";
import "./w4m8I18n";

/** The base branch choices: the picked base first (the repository's default branch may be missing from the loaded refs), then each branch once. */
export function branchItems(base: string, branches: readonly { name: string }[]): string[] {
	return [...new Set([base, ...branches.map((b) => b.name)].filter(Boolean))];
}

/** The callback outcome the server put in the URL (github=connected or github_error=...), if any. */
export function githubCallbackNotice(search: string): { ok: boolean; message: string } | null {
	const q = new URLSearchParams(search);
	if (q.get("github") === "connected") return { ok: true, message: "" };
	const err = q.get("github_error");
	return err ? { ok: false, message: err } : null;
}

/** Repositories whose full name contains the filter text, case-insensitive. */
export function filterRepos(repos: GithubRepo[], filter: string): GithubRepo[] {
	const f = filter.trim().toLowerCase();
	return f ? repos.filter((r) => r.full_name.toLowerCase().includes(f)) : repos;
}

/** Live mode needs a base agent config path like agents/x.yaml (mirrors the server check). */
export function validBaseAgent(path: string): boolean {
	return /^agents\/[A-Za-z0-9_.-]{1,100}\.ya?ml$/.test(path);
}

/** A server message without "in Settings > GitHub": the user is already there. */
export function withoutHere(message: string): string {
	return message
		.replace(/\s*(?:in|under|from|on)\s+Settings\s*>\s*GitHub/gi, "")
		.replace(/\s+([.,])/g, "$1")
		.trim();
}

/** Settings > GitHub: connect a GitHub account (repo scope), browse repositories, open the CI-install PR. */
export function GithubTab() {
	const { t } = useTranslation("github");
	const { t: tl } = useTranslation("uf8a");
	const session = useSession();
	const isAdmin = roleAtLeast(session.role, "admin");
	const isMember = roleAtLeast(session.role, "member");
	const status = useGetGithubStatusQuery();
	const [start, startState] = useStartGithubConnectMutation();
	const [disconnect, disconnectState] = useDisconnectGithubMutation();
	const [notice, setNotice] = useState<{ ok: boolean; message: string } | null>(null);
	const [picked, setPicked] = useState<GithubRepo | null>(null);

	useEffect(() => {
		if (typeof window === "undefined") return;
		const n = githubCallbackNotice(window.location.search);
		if (!n) return;
		setNotice(n);
		const url = new URL(window.location.href);
		url.searchParams.delete("github");
		url.searchParams.delete("github_error");
		window.history.replaceState(null, "", url.toString());
	}, []);

	const connect = async () => {
		try {
			const res = await start({ return_to: "/settings?tab=github" }).unwrap();
			if (res?.url) window.location.assign(res.url);
		} catch (err) {
			setNotice({ ok: false, message: getBenchErrorMessage(err) });
		}
	};

	const [disconnectError, setDisconnectError] = useState<unknown>(null);
	const onDisconnect = async () => {
		setDisconnectError(null);
		try {
			await disconnect().unwrap();
			notify({ text: tl("github.disconnected") });
		} catch (err) {
			setDisconnectError(err);
		}
	};

	const st = status.data;
	return (
		<div className="flex flex-col gap-10" data-testid="settings-github">
			<SettingsSection
				title={t("title")}
				description={t("description")}
				testId="github-connection"
				actions={
					st?.configured && isAdmin ? (
						<div className="flex gap-2">
							<Button
								type="button"
								size="sm"
								variant={st.connected ? "outline" : "default"}
								isLoading={startState.isLoading}
								onClick={() => void connect()}
								dataTestId="github-connect"
							>
								<Github className="size-3.5" aria-hidden="true" />
								{st.connected ? t("reconnect") : t("connect")}
							</Button>
							{st.connected && (
								<ConfirmButton
									label={t("disconnect")}
									question={t("disconnectConfirm")}
									confirmLabel={t("disconnect")}
									cancelLabel={t("cancel")}
									onConfirm={() => void onDisconnect()}
									isLoading={disconnectState.isLoading}
									testId="github-disconnect"
								/>
							)}
						</div>
					) : undefined
				}
			>
				<InlineError error={disconnectError} title={tl("github.disconnectError")} onRetry={() => void onDisconnect()} testId="github-disconnect-error" />
				{notice && (
					<p
						role="status"
						className={notice.ok ? "text-chart-success-ink text-sm" : "text-destructive text-sm"}
						data-testid={notice.ok ? "github-notice-ok" : "github-notice-error"}
					>
						{notice.ok ? t("connectedOk") : t("connectFailed", { message: notice.message })}
					</p>
				)}
				{status.isLoading ? (
					<BlockSkeleton rows={1} testId="github-loading" />
				) : status.isError || !st ? (
					<LoadError title={t("loadError")} error={status.error} onRetry={() => void status.refetch()} testId="github-error" />
				) : !st.configured ? (
					<EmptyNote testId="github-not-configured">
						{t("notConfigured")} {st.hint}
					</EmptyNote>
				) : st.connected ? (
					<p className="text-foreground flex items-center gap-2 text-sm" data-testid="github-connected">
						<Github className="size-4" aria-hidden="true" />
						{t("connected", { login: st.login })}
					</p>
				) : (
					<EmptyNote testId="github-not-connected">
						{t("notConnected")} {!isAdmin && t("adminOnly")}
					</EmptyNote>
				)}
			</SettingsSection>
			{st?.connected && isMember && (
				<ReposSection
					picked={picked}
					onPick={setPicked}
					onReconnect={isAdmin ? () => void connect() : undefined}
					reconnecting={startState.isLoading}
				/>
			)}
			{st?.connected && isAdmin && <CIInstallSection repo={picked} />}
		</div>
	);
}

function ReposSection({
	picked,
	onPick,
	onReconnect,
	reconnecting,
}: {
	picked: GithubRepo | null;
	onPick: (r: GithubRepo) => void;
	/** Admins: a failed listing usually means the grant expired, so the fix is to connect again. */
	onReconnect?: () => void;
	reconnecting?: boolean;
}) {
	const { t } = useTranslation("github");
	const { t: tl } = useTranslation("uf8a");
	const [page, setPage] = useState(1);
	const [filter, setFilter] = useState("");
	const repos = useListGithubReposQuery(page);
	const list = filterRepos(repos.data?.repos ?? [], filter);
	return (
		<SettingsSection title={t("repos.title")} description={t("repos.description")} testId="github-repos">
			<Input
				value={filter}
				onChange={(e) => setFilter(e.target.value)}
				placeholder={t("repos.filter")}
				aria-label={t("repos.filter")}
				className="max-w-sm"
				data-testid="github-repo-filter"
			/>
			{repos.isLoading ? (
				<BlockSkeleton rows={4} testId="github-repos-loading" />
			) : repos.isError && onReconnect ? (
				<div
					role="alert"
					className="flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3.5 text-sm"
					data-testid="github-repos-error"
				>
					<p className="text-foreground min-w-0 flex-1">
						<span className="font-medium">{t("repos.loadError")}</span>: {withoutHere(getBenchErrorMessage(repos.error))}
					</p>
					<Button type="button" size="sm" isLoading={reconnecting} onClick={onReconnect} dataTestId="github-repos-reconnect">
						{tl("github.reconnect")}
					</Button>
				</div>
			) : repos.isError ? (
				<LoadError title={t("repos.loadError")} error={repos.error} onRetry={() => void repos.refetch()} testId="github-repos-error" />
			) : list.length === 0 ? (
				<EmptyNote testId="github-repos-empty">{t("repos.empty")}</EmptyNote>
			) : (
				<ul className="divide-border border-border divide-y rounded-md border" data-testid="github-repo-list">
					{list.map((r) => {
						const isPicked = picked?.full_name === r.full_name;
						return (
							<li
								key={r.full_name}
								className="flex items-center justify-between gap-3 px-3 py-2"
								data-testid={`github-repo-${r.full_name}`}
							>
								<div className="flex min-w-0 flex-col">
									<span className="text-foreground flex items-center gap-1.5 truncate text-sm font-medium">
										{r.full_name}
										{r.private && (
											<Badge variant="outline" className="gap-1 text-xs">
												<Lock className="size-3" aria-hidden="true" />
												{t("repos.private")}
											</Badge>
										)}
									</span>
									{r.description && <span className="text-muted-foreground truncate text-xs">{r.description}</span>}
								</div>
								<Button
									type="button"
									size="sm"
									variant={isPicked ? "default" : "outline"}
									onClick={() => onPick(r)}
									dataTestId={`github-pick-${r.full_name}`}
								>
									{isPicked ? t("repos.picked") : t("repos.pick")}
								</Button>
							</li>
						);
					})}
				</ul>
			)}
			{(page > 1 || repos.data?.more) && (
				<div className="flex gap-2">
					<Button
						type="button"
						size="sm"
						variant="outline"
						disabled={page <= 1}
						onClick={() => setPage(page - 1)}
						dataTestId="github-repos-prev"
					>
						{t("repos.prev")}
					</Button>
					<Button
						type="button"
						size="sm"
						variant="outline"
						disabled={!repos.data?.more}
						onClick={() => setPage(page + 1)}
						dataTestId="github-repos-next"
					>
						{t("repos.more")}
					</Button>
				</div>
			)}
		</SettingsSection>
	);
}

function CIInstallSection({ repo }: { repo: GithubRepo | null }) {
	const { t } = useTranslation("github");
	const { t: tm } = useTranslation("w4m8");
	const refs = useGetGithubRefsQuery(repo ? { owner: repo.owner, repo: repo.name } : { owner: "", repo: "" }, { skip: !repo });
	const [install, installState] = useInstallGithubCIMutation();
	const [base, setBase] = useState("");
	const [mode, setMode] = useState<GithubCIMode>("recorded");
	const [baseAgent, setBaseAgent] = useState("");
	const [force, setForce] = useState(false);
	const [error, setError] = useState("");

	useEffect(() => {
		setBase(repo?.default_branch ?? "");
		setError("");
	}, [repo?.full_name, repo?.default_branch]);

	const branches = (refs.data?.refs ?? []).filter((r) => r.kind === "branch");
	const result = installState.data;
	const canSubmit = !!repo && !!base && (mode === "recorded" || validBaseAgent(baseAgent));
	const why = repo
		? firstReason(!base && tm("github.whyBase"), mode === "live" && !validBaseAgent(baseAgent) && tm("github.whyFile"))
		: null;

	const submit = async (e: FormEvent) => {
		e.preventDefault();
		if (!repo) return;
		setError("");
		try {
			await install({
				owner: repo.owner,
				repo: repo.name,
				base,
				mode,
				base_agent: mode === "live" ? baseAgent : undefined,
				force,
			}).unwrap();
		} catch (err) {
			setError(getBenchErrorMessage(err));
		}
	};

	return (
		<SettingsSection
			title={t("ci.title")}
			description={repo ? t("ci.description", { repo: repo.full_name }) : undefined}
			testId="github-ci-install"
		>
			{!repo ? (
				<EmptyNote testId="github-ci-pick-first">{t("ci.pickFirst")}</EmptyNote>
			) : (
				<form className="flex max-w-xl flex-col gap-3" onSubmit={(e) => void submit(e)} data-testid="github-ci-form">
					{!repo.can_push && <p className="text-sm text-amber-600">{t("ci.noPush")}</p>}
					<label className="flex flex-col gap-1 text-sm">
						{t("ci.base")}
						<Select value={base} onValueChange={setBase} disabled={refs.isLoading}>
							<SelectTrigger size="sm" aria-label={t("ci.base")} data-testid="github-ci-base">
								<SelectValue placeholder={refs.isLoading ? t("ci.refsLoading") : base} />
							</SelectTrigger>
							<SelectContent position="popper">
								{branchItems(base, branches).map((name) => (
									<SelectItem key={name} value={name}>
										{name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("ci.mode")}
						<Select value={mode} onValueChange={(v) => setMode(v as GithubCIMode)}>
							<SelectTrigger size="sm" aria-label={t("ci.mode")} data-testid="github-ci-mode">
								<SelectValue />
							</SelectTrigger>
							<SelectContent position="popper">
								<SelectItem value="recorded">{t("ci.modeRecorded")}</SelectItem>
								<SelectItem value="live">{t("ci.modeLive")}</SelectItem>
							</SelectContent>
						</Select>
					</label>
					{mode === "live" && (
						<Input
							value={baseAgent}
							onChange={(e) => setBaseAgent(e.target.value)}
							placeholder={t("ci.baseAgent")}
							aria-label={t("ci.baseAgent")}
							data-testid="github-ci-base-agent"
						/>
					)}
					<label className="flex items-center gap-2 text-sm">
						<input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} data-testid="github-ci-force" />
						{t("ci.force")}
					</label>
					<div>
						<Button
							type="submit"
							size="sm"
							disabled={!canSubmit}
							isLoading={installState.isLoading}
							aria-describedby={why ? "github-ci-why" : undefined}
							dataTestId="github-ci-submit"
						>
							<GitPullRequest className="size-3.5" aria-hidden="true" />
							{t("ci.submit")}
						</Button>
						<WhyDisabled reason={installState.isLoading ? null : why} testId="github-ci-why" className="mt-1" />
					</div>
					{error && (
						<p role="alert" className="text-destructive text-sm" data-testid="github-ci-error">
							{error}
						</p>
					)}
					{result && (
						<p className="flex items-center gap-2 text-sm text-emerald-600" data-testid="github-ci-result">
							{t("ci.opened", { number: result.number })}
							<a href={result.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 underline">
								{t("ci.view")}
								<ExternalLink className="size-3" aria-hidden="true" />
							</a>
						</p>
					)}
				</form>
			)}
		</SettingsSection>
	);
}

export default GithubTab;