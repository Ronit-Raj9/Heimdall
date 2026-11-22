import { Input } from "@/components/ui/input";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetGithubRefsQuery, useGetGithubStatusQuery, useListGithubReposQuery } from "@/lib/store/apis/githubApi";
import { compareRefsParams } from "@/lib/types/refCompare";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { FollowUpJob } from "./followUpJob";

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
	const uid = useId();
	const agents = useGetAgentsQuery();
	const status = useGetGithubStatusQuery();
	const connected = status.data?.connected === true;
	// A hosted project has no git history of its own: only a GitHub repository can be compared (fix wave 2, N67).
	const hosted = status.data?.hosted === true;
	const repos = useListGithubReposQuery(connected ? 1 : skipToken);
	const [picked, setAgent] = useState(initialAgent);
	const [suite, setSuite] = useState(initialSuite);
	const [repo, setRepo] = useState("");
	const [refA, setRefA] = useState("");
	const [refB, setRefB] = useState("");
	const [tasks, setTasks] = useState("");
	const [trials, setTrials] = useState(3);
	const [budget, setBudget] = useState(0);
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
	const field = "flex flex-col gap-1 text-sm";
	const select = "border-input bg-background h-9 rounded-md border px-2 text-sm";
	const pickRepo = (full: string) => {
		setRepo(full);
		const r = repos.data?.repos.find((x) => x.full_name === full);
		if (r && !refA) setRefA(r.default_branch);
	};
	return (
		<div className="flex flex-col gap-3" data-testid={testId}>
			<div className="grid gap-3 sm:grid-cols-2">
				<label className={field} htmlFor={`${uid}-agent`}>
					{t("compare.agent")}
					<select
						id={`${uid}-agent`}
						className={select}
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
					</select>
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
						data-testid={`${testId}-suite`}
					/>
				</label>
				<label className={`${field} sm:col-span-2`} htmlFor={`${uid}-repo`}>
					{t("compare.repo")}
					{connected ? (
						<select
							id={`${uid}-repo`}
							className={select}
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
						</select>
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
					<Input id={`${uid}-trials`} type="number" min={1} max={50} value={trials} onChange={(e) => setTrials(Number(e.target.value))} />
				</label>
				<label className={field} htmlFor={`${uid}-budget`}>
					{t("compare.budget")}
					<Input id={`${uid}-budget`} type="number" min={0} step="0.5" value={budget} onChange={(e) => setBudget(Number(e.target.value))} />
				</label>
			</div>
			<FollowUpJob
				kind="experiment.compare-refs"
				params={params}
				label={t("compare.run")}
				confirm={t("compare.confirm")}
				missing={needRepo ? t("compare.pickRepoFirst") : same ? t("compare.same") : t("compare.missing")}
				testId={`${testId}-job`}
			/>
		</div>
	);
}