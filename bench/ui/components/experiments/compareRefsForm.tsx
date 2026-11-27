import { NativeSelect } from "@/components/ui/select";
import { costSentence } from "@/components/platform/confirmDialog";
import { Input } from "@/components/ui/input";
import { paidLabel } from "@/components/ui/paidButton";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { useGetGithubRefsQuery, useGetGithubStatusQuery, useListGithubReposQuery } from "@/lib/store/apis/githubApi";
import { compareRefsParams, compareRefsProblem, REF_TRIALS_MAX, REF_TRIALS_MIN } from "@/lib/types/refCompare";
import "@/app/leaderboard/views/uf4bI18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { FollowUpJob } from "./followUpJob";

/** The server's message for a failed repos call (409: GitHub needs reconnecting), else null. Pure. */
export function githubReposError(error: unknown): string | null {
	const e = error as { data?: { error?: { message?: string } } } | undefined;
	return e?.data?.error?.message || null;
}

/** The benchmark refs "id@version" the suggestions list, sorted, each once. Pure. */
export function suiteChoices(suites: readonly { id: string; version?: string }[] | undefined): string[] {
	return [...new Set((suites ?? []).map((s) => (s.version ? `${s.id}@${s.version}` : s.id)))].sort();
}

/**
 * Compare one agent at two git refs (gap lane G07): the experiment.compare-refs job reads the
 * agent's files at both refs, writes the two-arm experiment and starts it through the launcher.
 * With GitHub connected the refs come from the repository's branches and tags; otherwise they are
 * free text against the project's own git repository.
 */
export function CompareRefsForm({
	suite: initialSuite = "",
	initialAgent = "",
	testId = "compare-refs-form",
}: {
	suite?: string;
	/** The agent file to start from (agents/<id>.yaml), such as the comparison's setup A; kept only when listed. */
	initialAgent?: string;
	testId?: string;
}) {
	const { t } = useTranslation("refCompare");
	const { t: tu } = useTranslation("uf4b");
	const { t: tp } = useTranslation("paid");
	const uid = useId();
	const agents = useGetAgentsQuery();
	const status = useGetGithubStatusQuery();
	// Round 2 (RG): the status may carry state and hint; needs_reconnect means the stored token cannot be read,
	// so the repos are not asked for (they would answer 409) and the hint says what to do. Read as optional.
	const facts = status.data as (typeof status.data & { state?: string }) | undefined;
	const reconnect = facts?.state === "needs_reconnect";
	const connected = status.data?.connected === true && !reconnect;
	// A hosted project has no git history of its own: only a GitHub repository can be compared (fix wave 2, N67).
	const hosted = status.data?.hosted === true;
	const repos = useListGithubReposQuery(connected ? 1 : skipToken);
	const [picked, setAgent] = useState(initialAgent);
	const [suite, setSuite] = useState(initialSuite);
	const suites = useGetSuitesLightQuery();
	const [repo, setRepo] = useState("");
	const [refA, setRefA] = useState("");
	const [refB, setRefB] = useState("");
	const [tasks, setTasks] = useState("");
	const [trials, setTrials] = useState(3);
	const [budget, setBudget] = useState(5);
	const [owner, name] = repo.split("/");
	const refs = useGetGithubRefsQuery(connected && owner && name ? { owner, repo: name } : skipToken);
	const list = agents.data?.agents ?? [];
	// A start agent no listed agent has leaves the select empty: never a made-up option.
	const agent = list.some((a) => a.path === picked) ? picked : "";
	const refNames = refs.data?.refs.map((r) => r.name) ?? [];
	const needRepo = hosted && !repo;
	const built = compareRefsParams({
		agent,
		suite,
		repo,
		refA,
		refB,
		tasks,
		trials,
		budget,
	});
	const params = needRepo ? null : built;
	const same = refA.trim() !== "" && refA.trim() === refB.trim();
	const problem = compareRefsProblem({ trials, budget });
	const field = "flex flex-col gap-1.5 text-sm";
	const pickRepo = (full: string) => {
		setRepo(full);
		const r = repos.data?.repos.find((x) => x.full_name === full);
		if (r && !refA) setRefA(r.default_branch);
	};
	const reposError = repos.error ? githubReposError(repos.error) : null;
	return (
		<div className="flex flex-col gap-3" data-testid={testId}>
			{(reconnect || reposError) && (
				<p className="text-muted-foreground text-sm" role="note" data-testid={`${testId}-github-note`}>
					{reconnect ? status.data?.hint || reposError : reposError}
				</p>
			)}
			<div className="grid gap-3 sm:grid-cols-2">
				<label className={field} htmlFor={`${uid}-agent`}>
					{t("compare.agent")}
					<NativeSelect
						id={`${uid}-agent`}
						wrapperClassName="w-full"
						value={agent}
						onChange={(e) => setAgent(e.target.value)}
						data-testid={`${testId}-agent`}
					>
						<option value="">{t("compare.agentPlaceholder")}</option>
						{list.map((a) => (
							<option key={a.path} value={a.path}>
								{a.id}
							</option>
						))}
					</NativeSelect>
					{agents.isSuccess && list.length === 0 && (
						<Link to="/agents" className="text-xs underline">
							{t("compare.noAgents")}
						</Link>
					)}
				</label>
				<label className={field} htmlFor={`${uid}-suite`}>
					{t("compare.suite")}
					<Input
						id={`${uid}-suite`}
						value={suite}
						onChange={(e) => setSuite(e.target.value)}
						placeholder="coding-core@1.0.0"
						list={`${uid}-suites`}
						data-testid={`${testId}-suite`}
					/>
					{/* M4-93: the known benchmarks as suggestions; free text still works. */}
					<datalist id={`${uid}-suites`} data-testid={`${testId}-suite-options`}>
						{suiteChoices(suites.data?.suites).map((ref) => (
							<option key={ref} value={ref} />
						))}
					</datalist>
				</label>
				<label className={`${field} sm:col-span-2`} htmlFor={`${uid}-repo`}>
					{t("compare.repo")}
					{connected ? (
						<NativeSelect
							id={`${uid}-repo`}
							wrapperClassName="w-full"
							value={repo}
							onChange={(e) => pickRepo(e.target.value)}
							data-testid={`${testId}-repo`}
						>
							<option value="">
								{repos.isLoading ? t("compare.repoLoading") : hosted ? t("compare.pickRepo") : t("compare.repoProject")}
							</option>
							{(repos.data?.repos ?? []).map((r) => (
								<option key={r.full_name} value={r.full_name}>
									{r.full_name}
								</option>
							))}
						</NativeSelect>
					) : (
						<span className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid={`${testId}-connect`}>
							<span>{hosted ? t("compare.connectHosted") : t("compare.connectHint")}</span>
							<Link to="/settings" search={{ tab: "github" } as never} className="underline">
								{t("compare.connect")}
							</Link>
						</span>
					)}
				</label>
				<label className={field} htmlFor={`${uid}-refa`}>
					{t("compare.refA")}
					<Input
						id={`${uid}-refa`}
						list={`${uid}-refs`}
						value={refA}
						onChange={(e) => setRefA(e.target.value)}
						placeholder={t("compare.refPlaceholder")}
						data-testid={`${testId}-refa`}
					/>
				</label>
				<label className={field} htmlFor={`${uid}-refb`}>
					{t("compare.refB")}
					<Input
						id={`${uid}-refb`}
						list={`${uid}-refs`}
						value={refB}
						onChange={(e) => setRefB(e.target.value)}
						placeholder={t("compare.refPlaceholder")}
						data-testid={`${testId}-refb`}
					/>
				</label>
				<datalist id={`${uid}-refs`} data-testid={`${testId}-refs`}>
					{refNames.map((r) => (
						<option key={r} value={r} />
					))}
				</datalist>
				{repo && refs.isFetching && <span className="text-muted-foreground text-xs sm:col-span-2">{t("compare.refsLoading")}</span>}
				<label className={`${field} sm:col-span-2`} htmlFor={`${uid}-tasks`}>
					{t("compare.tasks")}
					<Input id={`${uid}-tasks`} value={tasks} onChange={(e) => setTasks(e.target.value)} placeholder={t("compare.tasksPlaceholder")} />
				</label>
				<label className={field} htmlFor={`${uid}-trials`}>
					{t("compare.trials")}
					<Input
						id={`${uid}-trials`}
						type="number"
						min={REF_TRIALS_MIN}
						max={REF_TRIALS_MAX}
						value={trials}
						aria-invalid={problem === "trials"}
						onChange={(e) => setTrials(Number(e.target.value))}
						data-testid={`${testId}-trials`}
					/>
					{problem === "trials" && (
						<span className="text-chart-error-ink text-xs" role="alert">
							{tu("refs.trials", { min: REF_TRIALS_MIN, max: REF_TRIALS_MAX })}
						</span>
					)}
				</label>
				<label className={field} htmlFor={`${uid}-budget`}>
					{t("compare.budget")}
					<Input
						id={`${uid}-budget`}
						type="number"
						min={0.01}
						step="0.5"
						value={budget}
						aria-invalid={problem === "budget"}
						onChange={(e) => setBudget(Number(e.target.value))}
						data-testid={`${testId}-budget`}
					/>
					{problem === "budget" && (
						<span className="text-chart-error-ink text-xs" role="alert">
							{tu("refs.budget")}
						</span>
					)}
				</label>
			</div>
			<FollowUpJob
				kind="experiment.compare-refs"
				params={params}
				label={paidLabel(tp as never, t("compare.run"), budget > 0 ? budget : null)}
				confirm={`${t("compare.confirm")} ${costSentence(tp as never, budget > 0 ? budget : null)}`}
				missing={needRepo ? t("compare.pickRepoFirst") : same ? t("compare.same") : t("compare.missing")}
				testId={`${testId}-job`}
			/>
		</div>
	);
}