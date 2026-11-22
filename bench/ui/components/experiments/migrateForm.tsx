import { Input } from "@/components/ui/input";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { migrateParams } from "@/lib/types/experimentActions";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { FollowUpJob } from "./followUpJob";

/**
 * Compare models: one agent on its current model and on candidates over the same suite, through
 * the experiment.migrate job. Used by the experiment actions tab and the compare page.
 */
export function MigrateForm({
	suite: initialSuite = "",
	initialAgent = "",
	initialFrom = "",
	testId = "migrate-form",
}: {
	suite?: string;
	/** The agent file to start from (agents/<id>.yaml), such as the comparison's setup A; kept only when listed. */
	initialAgent?: string;
	/** The current model to start from. */
	initialFrom?: string;
	testId?: string;
}) {
	const { t } = useTranslation("experimentActions");
	const uid = useId();
	const agents = useGetAgentsQuery();
	const [picked, setAgent] = useState(initialAgent);
	const [suite, setSuite] = useState(initialSuite);
	const [from, setFrom] = useState(initialFrom);
	const [to, setTo] = useState("");
	const [trials, setTrials] = useState(3);
	const [budget, setBudget] = useState(0);
	const list = agents.data?.agents ?? [];
	// A start agent no listed agent has leaves the select empty: never a made-up option.
	const agent = list.some((a) => a.path === picked) ? picked : "";
	const pickAgent = (path: string) => {
		setAgent(path);
		const a = list.find((x) => x.path === path);
		if (a?.model && !from) setFrom(a.model);
	};
	const params = migrateParams({ agent, suite, from, to, trials, budget });
	const field = "flex flex-col gap-1 text-sm";
	return (
		<div className="flex flex-col gap-3" data-testid={testId}>
			<div className="grid gap-3 sm:grid-cols-2">
				<label className={field} htmlFor={`${uid}-agent`}>
					{t("migrate.agent")}
					<select
						id={`${uid}-agent`}
						className="border-input bg-background h-9 rounded-md border px-2 text-sm"
						value={agent}
						onChange={(e) => pickAgent(e.target.value)}
						data-testid={`${testId}-agent`}
					>
						<option value="">{t("migrate.agentPlaceholder")}</option>
						{list.map((a) => (
							<option key={a.path} value={a.path}>
								{a.id}
							</option>
						))}
					</select>
					{agents.isSuccess && list.length === 0 && <span className="text-muted-foreground text-xs">{t("migrate.noAgents")}</span>}
				</label>
				<label className={field} htmlFor={`${uid}-suite`}>
					{t("migrate.suite")}
					<Input
						id={`${uid}-suite`}
						value={suite}
						onChange={(e) => setSuite(e.target.value)}
						placeholder="coding-core@1.0.0"
						data-testid={`${testId}-suite`}
					/>
				</label>
				<label className={field} htmlFor={`${uid}-from`}>
					{t("migrate.from")}
					<Input id={`${uid}-from`} value={from} onChange={(e) => setFrom(e.target.value)} data-testid={`${testId}-from`} />
				</label>
				<label className={field} htmlFor={`${uid}-to`}>
					{t("migrate.to")}
					<Input id={`${uid}-to`} value={to} onChange={(e) => setTo(e.target.value)} data-testid={`${testId}-to`} />
				</label>
				<label className={field} htmlFor={`${uid}-trials`}>
					{t("migrate.trials")}
					<Input id={`${uid}-trials`} type="number" min={1} max={50} value={trials} onChange={(e) => setTrials(Number(e.target.value))} />
				</label>
				<label className={field} htmlFor={`${uid}-budget`}>
					{t("migrate.budget")}
					<Input id={`${uid}-budget`} type="number" min={0} step="0.5" value={budget} onChange={(e) => setBudget(Number(e.target.value))} />
				</label>
			</div>
			<FollowUpJob
				kind="experiment.migrate"
				params={params}
				label={t("migrate.run")}
				confirm={t("migrate.confirm")}
				missing={t("migrate.missing")}
				testId={`${testId}-job`}
			/>
		</div>
	);
}